package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/markcipolla/treeline/internal/gitx"
)

var errUntrackedHunks = errors.New("untracked file — space stages it whole")

func maxWidthStyle(w int) lipgloss.Style {
	return lipgloss.NewStyle().MaxWidth(w)
}

// The right panel is the git pane: a two-column file picker over unstaged
// and staged changes with per-file diffs (default), hunk-level staging, the
// commit log, and the whole branch-vs-base diff.
const (
	gitModeFiles = iota
	gitModeHunks
	gitModeLog
	gitModeBranch
	gitModeCommit
)

func gitZoneID(staged bool, i int) string {
	side := "u"
	if staged {
		side = "s"
	}
	return "git:" + side + ":" + strconv.Itoa(i)
}

type gitStatusMsg struct {
	dir   string
	files []gitx.FileStatus
	err   error
}

type gitLogMsg struct {
	dir     string
	commits []gitx.Commit
	err     error
}

type gitFileDiffMsg struct {
	dir    string
	path   string
	staged bool
	diff   string
	err    error
}

type gitCommitMsg struct {
	dir string
	err error
}

func commitStagedCmd(dir, subject, body string) tea.Cmd {
	return func() tea.Msg {
		return gitCommitMsg{dir: dir, err: gitx.CommitStaged(dir, subject, body)}
	}
}

type genCommitMsg struct {
	dir     string
	subject string
	body    string
	err     error
}

// generateCommitMsgCmd asks the claude CLI to draft a commit message from
// the staged diff.
func generateCommitMsgCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		diff, err := gitx.StagedDiff(dir)
		if err != nil {
			return genCommitMsg{dir: dir, err: err}
		}
		if strings.TrimSpace(diff) == "" {
			return genCommitMsg{dir: dir, err: errors.New("nothing staged to describe")}
		}
		if len(diff) > 60000 {
			diff = diff[:60000] + "\n… (diff truncated)"
		}
		prompt := "Write a git commit message for the staged diff below. " +
			"First line: imperative subject under 65 characters. Then a blank line, " +
			"then a short body (2-5 sentences) explaining what changed and why. " +
			"Output only the commit message — no markdown, no code fences, no commentary.\n\n" + diff
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			return genCommitMsg{dir: dir, err: fmt.Errorf("claude: %v", err)}
		}
		subject, body := splitCommitMessage(string(out))
		if subject == "" {
			return genCommitMsg{dir: dir, err: errors.New("claude returned an empty message")}
		}
		return genCommitMsg{dir: dir, subject: subject, body: body}
	}
}

// splitCommitMessage separates a generated message into subject and body,
// tolerating stray code fences.
func splitCommitMessage(out string) (subject, body string) {
	out = strings.TrimSpace(out)
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		return "", ""
	}
	subject = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		body = strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}
	return subject, body
}

// openCommitForm switches the pane to the commit form.
func (m *Model) openCommitForm() {
	if len(m.gitStaged) == 0 {
		m.err = errors.New("nothing staged — stage files or hunks first")
		return
	}
	m.gitMode = gitModeCommit
	m.commitFocus = 0
	m.commitSubject.Focus()
	m.commitBody.Blur()
}

func (m *Model) closeCommitForm() {
	m.commitSubject.Blur()
	m.commitBody.Blur()
	m.gitMode = gitModeFiles
}

func (m *Model) setCommitFocus(f int) tea.Cmd {
	m.commitFocus = f
	if f == 0 {
		m.commitBody.Blur()
		return m.commitSubject.Focus()
	}
	m.commitSubject.Blur()
	return m.commitBody.Focus()
}

func (m *Model) submitCommit() tea.Cmd {
	subject := strings.TrimSpace(m.commitSubject.Value())
	if subject == "" {
		m.err = errors.New("a commit subject is required")
		return nil
	}
	return commitStagedCmd(m.gitFor, subject, m.commitBody.Value())
}

func loadGitStatusCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		files, err := gitx.Status(dir)
		return gitStatusMsg{dir: dir, files: files, err: err}
	}
}

func loadGitLogCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		commits, err := gitx.Log(dir, 100)
		return gitLogMsg{dir: dir, commits: commits, err: err}
	}
}

func loadFileDiffCmd(dir string, fs gitx.FileStatus, staged bool) tea.Cmd {
	return func() tea.Msg {
		diff, err := gitx.DiffFile(dir, fs.Path, staged, fs.Untracked && !staged)
		return gitFileDiffMsg{dir: dir, path: fs.Path, staged: staged, diff: diff, err: err}
	}
}

// splitStatus divides status entries over the two columns. A file with both
// staged and unstaged changes appears in each.
func splitStatus(files []gitx.FileStatus) (unstaged, staged []gitx.FileStatus) {
	for _, f := range files {
		if f.Untracked || f.Unstaged != ' ' {
			unstaged = append(unstaged, f)
		}
		if !f.Untracked && f.Staged != ' ' {
			staged = append(staged, f)
		}
	}
	return unstaged, staged
}

// gitFilesLayout is the files-mode geometry: the height of the file picker
// (its first line is the column header) and the width of the left column.
// The renderer and the mouse handler both need these numbers, so that a
// wheel event can tell which of the three regions it lands in.
func (m Model) gitFilesLayout(w, h int) (listH, lw int) {
	listH = h / 2
	if listH < 4 {
		listH = 4
	}
	maxRows := len(m.gitUnstaged)
	if len(m.gitStaged) > maxRows {
		maxRows = len(m.gitStaged)
	}
	if listH > maxRows+1 {
		listH = maxRows + 1
	}
	return listH, (w - 3) / 2
}

// clampScroll keeps an offset inside a scrollable range.
func clampScroll(off, n, rows int) int {
	if max := n - rows; off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	return off
}

// scrollGitDiff moves the files-mode preview under the picker, leaving the
// selected file where it is.
func (m *Model) scrollGitDiff(delta int) {
	w, h := m.gitPaneSize()
	listH, _ := m.gitFilesLayout(w, h)
	m.gitDiffScroll = clampScroll(m.gitDiffScroll+delta,
		len(strings.Split(m.gitDiff, "\n")), h-listH-1)
}

// revealGitSel scrolls the active column just far enough to show the selected
// row: moving the cursor with the keyboard pulls the view along, while the
// wheel is free to scroll away from it.
func (m *Model) revealGitSel() {
	w, h := m.gitPaneSize()
	listH, _ := m.gitFilesLayout(w, h)
	rows := listH - 1
	if rows < 1 {
		return
	}
	sel := m.gitSelU
	if m.gitCol == 1 {
		sel = m.gitSelS
	}
	off := &m.gitScroll[m.gitCol]
	if sel < *off {
		*off = sel
	}
	if sel >= *off+rows {
		*off = sel - rows + 1
	}
	*off = clampScroll(*off, len(m.gitList(m.gitCol)), rows)
}

// gitList returns the column's entries: 0 unstaged, 1 staged.
func (m Model) gitList(col int) []gitx.FileStatus {
	if col == 1 {
		return m.gitStaged
	}
	return m.gitUnstaged
}

func (m *Model) clampGitSel() {
	clamp := func(sel int, n int) int {
		if sel >= n {
			sel = n - 1
		}
		if sel < 0 {
			sel = 0
		}
		return sel
	}
	m.gitSelU = clamp(m.gitSelU, len(m.gitUnstaged))
	m.gitSelS = clamp(m.gitSelS, len(m.gitStaged))
	m.gitScroll[0] = clampScroll(m.gitScroll[0], len(m.gitUnstaged), 1)
	m.gitScroll[1] = clampScroll(m.gitScroll[1], len(m.gitStaged), 1)
	// don't rest on an empty column when the other has entries
	if len(m.gitList(m.gitCol)) == 0 && len(m.gitList(1-m.gitCol)) > 0 {
		m.gitCol = 1 - m.gitCol
	}
}

// selectedGitFile is the file under the cursor in the active column.
func (m Model) selectedGitFile() (fs gitx.FileStatus, staged, ok bool) {
	list := m.gitList(m.gitCol)
	sel := m.gitSelU
	if m.gitCol == 1 {
		sel = m.gitSelS
	}
	if sel < 0 || sel >= len(list) {
		return fs, m.gitCol == 1, false
	}
	return list[sel], m.gitCol == 1, true
}

// loadSelectedFileDiff refreshes the preview under the file columns.
func (m *Model) loadSelectedFileDiff() tea.Cmd {
	fs, staged, ok := m.selectedGitFile()
	if !ok {
		m.gitDiff = ""
		return nil
	}
	return loadFileDiffCmd(m.gitFor, fs, staged)
}

func (m *Model) moveGitSel(delta int) tea.Cmd {
	sel := &m.gitSelU
	if m.gitCol == 1 {
		sel = &m.gitSelS
	}
	n := len(m.gitList(m.gitCol))
	i := *sel + delta
	if i < 0 || i >= n {
		return nil
	}
	*sel = i
	m.gitDiffScroll = 0
	m.revealGitSel()
	return m.loadSelectedFileDiff()
}

func (m *Model) switchGitCol() tea.Cmd {
	if len(m.gitList(1-m.gitCol)) == 0 {
		return nil
	}
	m.gitCol = 1 - m.gitCol
	m.gitDiffScroll = 0
	m.revealGitSel()
	return m.loadSelectedFileDiff()
}

// clickGitFile selects a clicked row, focusing the git pane.
func (m Model) clickGitFile(staged bool, i int) (tea.Model, tea.Cmd) {
	m.gitMode = gitModeFiles
	if staged {
		m.gitCol, m.gitSelS = 1, i
	} else {
		m.gitCol, m.gitSelU = 0, i
	}
	m.gitDiffScroll = 0
	m.revealGitSel()
	mm, cmd := m.focusPane(paneDiff)
	model := mm.(Model)
	return model, tea.Batch(cmd, model.loadSelectedFileDiff())
}

// reloadGit refreshes the pane's status and log for the current directory.
func (m *Model) reloadGit() tea.Cmd {
	if m.gitFor == "" {
		return nil
	}
	return tea.Batch(loadGitStatusCmd(m.gitFor), loadGitLogCmd(m.gitFor))
}

// openHunks enters hunk mode for the selected file.
func (m *Model) openHunks() {
	fs, staged, ok := m.selectedGitFile()
	if !ok {
		return
	}
	if fs.Untracked && !staged {
		m.err = errUntrackedHunks
		return
	}
	header, hunks, err := gitx.FileHunks(m.gitFor, fs.Path, staged)
	if err != nil {
		m.err = err
		return
	}
	if len(hunks) == 0 {
		return
	}
	m.hunkPath, m.hunkStaged = fs.Path, staged
	m.hunkHeader, m.hunks, m.hunkSel = header, hunks, 0
	m.gitMode = gitModeHunks
}

// stageSelectedHunk applies (or reverse-applies) the selected hunk to the
// index and refreshes.
func (m *Model) stageSelectedHunk() tea.Cmd {
	if m.hunkSel < 0 || m.hunkSel >= len(m.hunks) {
		return nil
	}
	patch := m.hunkHeader + "\n" + m.hunks[m.hunkSel] + "\n"
	if err := gitx.ApplyToIndex(m.gitFor, patch, m.hunkStaged); err != nil {
		m.err = err
		return nil
	}
	// re-split: hunk offsets shift after applying
	header, hunks, err := gitx.FileHunks(m.gitFor, m.hunkPath, m.hunkStaged)
	if err != nil || len(hunks) == 0 {
		m.gitMode = gitModeFiles
	} else {
		m.hunkHeader, m.hunks = header, hunks
		if m.hunkSel >= len(hunks) {
			m.hunkSel = len(hunks) - 1
		}
	}
	return tea.Batch(m.reloadGit(), m.loadSelectedFileDiff())
}

// toggleStageFile stages or unstages the selected file whole.
func (m *Model) toggleStageFile() tea.Cmd {
	fs, staged, ok := m.selectedGitFile()
	if !ok {
		return nil
	}
	var err error
	if staged {
		err = gitx.UnstageFile(m.gitFor, fs.Path)
	} else {
		err = gitx.StageFile(m.gitFor, fs.Path)
	}
	if err != nil {
		m.err = err
		return nil
	}
	return m.reloadGit()
}

func (m Model) keyGit(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.gitSel.clear() // typing moves the text the highlight was drawn over
	switch m.gitMode {
	case gitModeHunks:
		switch k.String() {
		case "esc", "q", "f":
			m.gitMode = gitModeFiles
			return m, nil
		case "up", "k":
			if m.hunkSel > 0 {
				m.hunkSel--
			}
			return m, nil
		case "down", "j":
			if m.hunkSel < len(m.hunks)-1 {
				m.hunkSel++
			}
			return m, nil
		case " ", "space", "enter":
			return m, m.stageSelectedHunk()
		}
		return m, nil

	case gitModeLog:
		switch k.String() {
		case "esc", "q", "f":
			m.gitMode = gitModeFiles
			return m, nil
		case "up", "k":
			if m.commitSel > 0 {
				m.commitSel--
			}
			return m, nil
		case "down", "j":
			if m.commitSel < len(m.commits)-1 {
				m.commitSel++
			}
			return m, nil
		case "b":
			m.gitMode = gitModeBranch
			return m, nil
		}
		return m, nil

	case gitModeBranch:
		switch k.String() {
		case "esc", "f":
			m.gitMode = gitModeFiles
			return m, nil
		case "l":
			m.gitMode = gitModeLog
			return m, nil
		}
		var cmd tea.Cmd
		m.diffVP, cmd = m.diffVP.Update(k)
		return m, cmd
	}

	if m.gitMode == gitModeCommit {
		switch k.String() {
		case "esc":
			m.closeCommitForm()
			return m, nil
		case "tab", "shift+tab":
			return m, m.setCommitFocus(1 - m.commitFocus)
		case "ctrl+s":
			return m, m.submitCommit()
		case "ctrl+g":
			if !m.generating {
				m.generating = true
				return m, generateCommitMsgCmd(m.gitFor)
			}
			return m, nil
		case "enter":
			if m.commitFocus == 0 {
				return m, m.setCommitFocus(1)
			}
		}
		var cmd tea.Cmd
		if m.commitFocus == 0 {
			m.commitSubject, cmd = m.commitSubject.Update(k)
		} else {
			m.commitBody, cmd = m.commitBody.Update(k)
		}
		return m, cmd
	}

	// files mode
	switch k.String() {
	case "esc":
		return m.focusPane(paneIssues)
	case "up", "k":
		return m, m.moveGitSel(-1)
	case "down", "j":
		return m, m.moveGitSel(1)
	case "pgup", "shift+up", "K":
		m.scrollGitDiff(-5) // the preview scrolls without moving the cursor
		return m, nil
	case "pgdown", "shift+down", "J":
		m.scrollGitDiff(5)
		return m, nil
	case "left", "right", "h", "tab":
		return m, m.switchGitCol()
	case " ", "space":
		return m, m.toggleStageFile()
	case "enter":
		m.openHunks()
		return m, nil
	case "l":
		m.gitMode = gitModeLog
		return m, nil
	case "b":
		m.gitMode = gitModeBranch
		return m, nil
	case "c":
		m.openCommitForm()
		return m, nil
	case "r":
		return m, m.reloadGit()
	}
	return m, nil
}

// colorizeDiff tints plain diff lines: additions green, removals red, hunk
// headers dim.
func colorizeDiff(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "+"):
			lines[i] = okStyle.Render(ln)
		case strings.HasPrefix(ln, "-"):
			lines[i] = errStyle.Render(ln)
		case strings.HasPrefix(ln, "@@"):
			lines[i] = dimStyle.Render(ln)
		}
	}
	return strings.Join(lines, "\n")
}

func statusLetter(fs gitx.FileStatus, staged bool) string {
	b := fs.Unstaged
	if staged {
		b = fs.Staged
	}
	if fs.Untracked {
		b = '?'
	}
	return string(b)
}

// fileColumn renders one side of the split picker, marking rows as click
// zones.
func (m Model) fileColumn(col, w, h int) []string {
	list := m.gitList(col)
	sel := m.gitSelU
	if col == 1 {
		sel = m.gitSelS
	}
	title := "UNSTAGED"
	if col == 1 {
		title = "STAGED"
	}
	head := groupTitleStyle.Render(title)
	if col == m.gitCol {
		head = paneTitleFocus.Render(title)
	}
	lines := []string{head}
	if len(list) == 0 {
		lines = append(lines, dimStyle.Render("  (none)"))
	}
	rows := h - 1 // the header takes the first line
	start := clampScroll(m.gitScroll[col], len(list), rows)
	for i := start; i < len(list) && i < start+rows; i++ {
		fs := list[i]
		line := statusLetter(fs, col == 1) + " " + fs.Path
		if fs.Orig != "" {
			line += " ← " + fs.Orig
		}
		switch {
		case i == sel && col == m.gitCol:
			line = cursorStyle.Render("❯ ") + okStyle.Render(truncate(line, w-2))
		case i == sel:
			line = dimStyle.Render("❯ ") + truncate(line, w-2)
		default:
			line = "  " + truncate(line, w-2)
		}
		lines = append(lines, m.zones.Mark(gitZoneID(col == 1, i), line))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines
}

// gitPaneContent renders the right panel's title and body for the current
// mode, fitted to the pane's inner width and height.
func (m Model) gitPaneContent(w, h int) (string, string) {
	switch m.gitMode {
	case gitModeHunks:
		verb := "stages"
		if m.hunkStaged {
			verb = "unstages"
		}
		title := fmt.Sprintf("git — %s · hunk %d/%d · space %s", m.hunkPath, m.hunkSel+1, len(m.hunks), verb)
		var b strings.Builder
		for i := m.hunkSel; i < len(m.hunks); i++ {
			b.WriteString(colorizeDiff(m.hunks[i]))
			b.WriteString("\n")
		}
		return title, clipLines(b.String(), w, h)

	case gitModeLog:
		title := "git — log"
		if len(m.commits) == 0 {
			return title, dimStyle.Render("no commits yet")
		}
		listH := h / 2
		if listH < 3 {
			listH = 3
		}
		start := 0
		if m.commitSel >= listH {
			start = m.commitSel - listH + 1
		}
		var b strings.Builder
		for i := start; i < len(m.commits) && i < start+listH; i++ {
			c := m.commits[i]
			line := c.Short + " " + c.Subject
			if i == m.commitSel {
				b.WriteString(cursorStyle.Render("❯ ") + okStyle.Render(truncate(line, w-2)) + "\n")
			} else {
				b.WriteString("  " + truncate(line, w-2) + "\n")
			}
		}
		b.WriteString(metaStyle.Render(strings.Repeat("─", w)) + "\n")
		c := m.commits[m.commitSel]
		b.WriteString(titleStyle.Render(c.Short) + " " + c.Subject + "\n")
		b.WriteString(dimStyle.Render(c.Author+" · "+c.When) + "\n")
		if c.Body != "" {
			b.WriteString("\n" + c.Body + "\n")
		}
		return title, clipLines(b.String(), w, h)

	case gitModeBranch:
		title := "git — branch diff vs " + m.base
		switch {
		case m.diffFor == "":
			return title, dimStyle.Render("select a worktree to diff against " + m.base)
		case m.loadingDiff:
			return title, m.spinner.View() + dimStyle.Render(" diffing…")
		case strings.TrimSpace(m.diffRaw) == "":
			return title, dimStyle.Render("no changes against " + m.base)
		}
		return title, m.diffVP.View()
	}

	if m.gitMode == gitModeCommit {
		title := fmt.Sprintf("git — commit %d staged file(s)", len(m.gitStaged))
		var b strings.Builder
		b.WriteString(labelStyle.Render("subject") + "\n" + m.commitSubject.View() + "\n\n")
		b.WriteString(labelStyle.Render("body") + "\n" + m.commitBody.View() + "\n\n")
		genLabel := "✨ generate message"
		if m.generating {
			genLabel = m.spinner.View() + " generating…"
		}
		b.WriteString(m.buttonRow(
			m.button("btn:commit", "commit (ctrl+s)", true),
			m.button("btn:gen", genLabel, false),
			m.button("btn:commit-cancel", "cancel", false),
		))
		return title, clipLines(b.String(), w, h)
	}

	// files mode: unstaged | staged side by side, preview below
	title := "git — files · space stage · enter hunks · l log · b diff"
	if len(m.gitUnstaged) == 0 && len(m.gitStaged) == 0 {
		return title, dimStyle.Render("working tree clean")
	}
	listH := h / 2
	if listH < 4 {
		listH = 4
	}
	maxRows := len(m.gitUnstaged)
	if len(m.gitStaged) > maxRows {
		maxRows = len(m.gitStaged)
	}
	if listH > maxRows+1 {
		listH = maxRows + 1
	}
	lw := (w - 3) / 2
	rw := w - 3 - lw
	left := m.fileColumn(0, lw, listH)
	right := m.fileColumn(1, rw, listH)
	sep := metaStyle.Render(" │ ")
	lwStyle := lipgloss.NewStyle().Width(lw).MaxWidth(lw).Inline(true)
	var b strings.Builder
	for i := 0; i < listH; i++ {
		b.WriteString(lwStyle.Render(left[i]) + sep + right[i] + "\n")
	}
	// the column separator joins the rule under the picker
	rule := strings.Repeat("─", lw+1) + "┴" + strings.Repeat("─", w-lw-2)
	b.WriteString(metaStyle.Render(rule) + "\n")
	if m.gitDiff == "" {
		b.WriteString(dimStyle.Render("(no diff)"))
	} else {
		lines := strings.Split(m.gitDiff, "\n")
		rows := h - listH - 1 // what is left under the picker and its rule
		off := clampScroll(m.gitDiffScroll, len(lines), rows)
		b.WriteString(strings.Join(lines[off:], "\n"))
		if off > 0 {
			title += fmt.Sprintf(" · ↓%d", off)
		}
	}
	return title, clipLines(b.String(), w, h)
}

// clipLines fits text into a w×h box: truncates each line (ANSI-aware) and
// drops lines past h.
func clipLines(s string, w, h int) string {
	trunc := maxWidthStyle(w)
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, ln := range lines {
		lines[i] = trunc.Render(ln)
	}
	return strings.Join(lines, "\n")
}
