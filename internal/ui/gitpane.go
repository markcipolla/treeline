package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/markcipolla/treeline/internal/gitx"
)

var errUntrackedHunks = errors.New("untracked file — space stages it whole")

func maxWidthStyle(w int) lipgloss.Style {
	return lipgloss.NewStyle().MaxWidth(w)
}

// The right panel is the git pane: a file picker over unstaged/staged
// changes with per-file diffs (default), hunk-level staging, the commit
// log, and the whole branch-vs-base diff.
const (
	gitModeFiles = iota
	gitModeHunks
	gitModeLog
	gitModeBranch
)

// gitRow is one line of the files view: a section header or a file, tagged
// with which section (unstaged vs staged) it appears in.
type gitRow struct {
	header bool
	label  string
	fs     gitx.FileStatus
	staged bool
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

// buildGitRows splits status entries into the two sections. A file with both
// staged and unstaged changes appears in each.
func buildGitRows(files []gitx.FileStatus) []gitRow {
	var unstaged, staged []gitRow
	for _, f := range files {
		if f.Untracked || f.Unstaged != ' ' {
			unstaged = append(unstaged, gitRow{fs: f})
		}
		if !f.Untracked && f.Staged != ' ' {
			staged = append(staged, gitRow{fs: f, staged: true})
		}
	}
	var rows []gitRow
	if len(unstaged) > 0 {
		rows = append(rows, gitRow{header: true, label: "UNSTAGED"})
		rows = append(rows, unstaged...)
	}
	if len(staged) > 0 {
		rows = append(rows, gitRow{header: true, label: "STAGED", staged: true})
		rows = append(rows, staged...)
	}
	return rows
}

func (m *Model) clampGitSel() {
	if m.gitSel >= len(m.gitRows) {
		m.gitSel = len(m.gitRows) - 1
	}
	if m.gitSel < 0 {
		m.gitSel = 0
	}
	// never rest on a header
	for m.gitSel < len(m.gitRows) && m.gitRows[m.gitSel].header {
		m.gitSel++
	}
	if m.gitSel >= len(m.gitRows) {
		m.gitSel = 0
	}
}

func (m Model) selectedGitRow() *gitRow {
	if m.gitSel < 0 || m.gitSel >= len(m.gitRows) || m.gitRows[m.gitSel].header {
		return nil
	}
	return &m.gitRows[m.gitSel]
}

// loadSelectedFileDiff refreshes the preview under the file list.
func (m *Model) loadSelectedFileDiff() tea.Cmd {
	row := m.selectedGitRow()
	if row == nil {
		m.gitDiff = ""
		return nil
	}
	return loadFileDiffCmd(m.gitFor, row.fs, row.staged)
}

func (m *Model) moveGitSel(delta int) tea.Cmd {
	i := m.gitSel
	for {
		i += delta
		if i < 0 || i >= len(m.gitRows) {
			return nil
		}
		if !m.gitRows[i].header {
			break
		}
	}
	m.gitSel = i
	return m.loadSelectedFileDiff()
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
	row := m.selectedGitRow()
	if row == nil {
		return
	}
	if row.fs.Untracked && !row.staged {
		m.err = errUntrackedHunks
		return
	}
	header, hunks, err := gitx.FileHunks(m.gitFor, row.fs.Path, row.staged)
	if err != nil {
		m.err = err
		return
	}
	if len(hunks) == 0 {
		return
	}
	m.hunkPath, m.hunkStaged = row.fs.Path, row.staged
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
	row := m.selectedGitRow()
	if row == nil {
		return nil
	}
	var err error
	if row.staged {
		err = gitx.UnstageFile(m.gitFor, row.fs.Path)
	} else {
		err = gitx.StageFile(m.gitFor, row.fs.Path)
	}
	if err != nil {
		m.err = err
		return nil
	}
	return m.reloadGit()
}

func (m Model) keyGit(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	// files mode
	switch k.String() {
	case "esc":
		return m.focusPane(paneIssues)
	case "up", "k":
		return m, m.moveGitSel(-1)
	case "down", "j":
		return m, m.moveGitSel(1)
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

func statusLetter(r gitRow) string {
	b := r.fs.Unstaged
	if r.staged {
		b = r.fs.Staged
	}
	if r.fs.Untracked {
		b = '?'
	}
	return string(b)
}

// gitPaneContent renders the right panel's title and body for the current
// mode, fitted to the pane's inner width and height.
func (m Model) gitPaneContent(w, h int) (string, string) {
	switch m.gitMode {
	case gitModeHunks:
		title := fmt.Sprintf("git — %s · hunk %d/%d · space stages", m.hunkPath, m.hunkSel+1, len(m.hunks))
		if m.hunkStaged {
			title = fmt.Sprintf("git — %s · hunk %d/%d · space unstages", m.hunkPath, m.hunkSel+1, len(m.hunks))
		}
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

	// files mode
	title := "git — files · space stage · enter hunks · l log · b diff"
	if len(m.gitRows) == 0 {
		return title, dimStyle.Render("working tree clean")
	}
	listH := h / 2
	if listH < 4 {
		listH = 4
	}
	if listH > len(m.gitRows) {
		listH = len(m.gitRows)
	}
	start := 0
	if m.gitSel >= listH {
		start = m.gitSel - listH + 1
	}
	var b strings.Builder
	for i := start; i < len(m.gitRows) && i < start+listH; i++ {
		r := m.gitRows[i]
		if r.header {
			b.WriteString(groupTitleStyle.Render(r.label) + "\n")
			continue
		}
		line := statusLetter(r) + " " + r.fs.Path
		if r.fs.Orig != "" {
			line += " ← " + r.fs.Orig
		}
		if i == m.gitSel {
			b.WriteString(cursorStyle.Render("❯ ") + okStyle.Render(truncate(line, w-2)) + "\n")
		} else {
			b.WriteString("  " + truncate(line, w-2) + "\n")
		}
	}
	b.WriteString(metaStyle.Render(strings.Repeat("─", w)) + "\n")
	if m.gitDiff == "" {
		b.WriteString(dimStyle.Render("(no diff)"))
	} else {
		b.WriteString(m.gitDiff)
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
