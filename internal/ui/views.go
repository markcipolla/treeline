package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/markcipolla/treeline/internal/branch"
	"github.com/markcipolla/treeline/internal/linear"
)

var errMissingCreds = errors.New("a client ID is required")

func typeZoneID(i int) string { return "type:" + strconv.Itoa(i) }

func (m Model) View() string {
	var body string
	switch m.screen {
	case scrMain:
		body = m.viewMain()
	case scrDetail:
		body = m.viewDetail()
	case scrManual:
		body = m.viewManual()
	case scrTypePick:
		body = m.viewTypePick()
	case scrEditBranch:
		body = m.viewEditBranch()
	case scrCreating:
		body = m.spinner.View() + " creating worktree…"
	case scrCreated:
		body = m.viewCreated()
	case scrDeleteConfirm:
		body = m.viewDeleteConfirm()
	case scrAuth:
		body = m.viewAuth()
	case scrAuthWait:
		body = boxStyle.Render(
			m.spinner.View() + " Waiting for authorization in your browser…\n\n" +
				m.help.ShortHelpView([]key.Binding{keyCancel}))
	case scrGitHub:
		body = m.viewGitHub()
	case scrSearch:
		body = m.viewSearch()
	}
	return m.zones.Scan(docStyle.Render(m.viewHeader() + "\n\n" + body))
}

func (m Model) viewGitHub() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Connect GitHub") + "\n\n")
	if m.deviceCode == nil {
		b.WriteString(m.spinner.View() + " requesting a device code…\n\n")
		b.WriteString(m.statusOrHelp([]key.Binding{keyCancel}))
		return boxStyle.Render(b.String())
	}
	b.WriteString("Enter this code at " + okStyle.Render(m.deviceCode.VerificationURI) + "\n\n")
	b.WriteString("    " + titleStyle.Render(m.deviceCode.UserCode) + "\n\n")
	b.WriteString(m.spinner.View() + " waiting for approval…\n\n")
	b.WriteString(m.buttonRow(
		m.button("btn:open", "open browser", true),
		m.button("btn:cancel", "cancel", false),
	) + "\n\n")
	b.WriteString(m.statusOrHelp([]key.Binding{keyOpenURL, keyCancel}))
	return boxStyle.Render(b.String())
}

func (m Model) viewHeader() string {
	left := titleStyle.Render("🌲 treeline")
	if name := m.repoName(); name != "treeline" {
		left += metaStyle.Render("  " + name)
	}
	right := ""
	if who := m.viewer.Email; who != "" || m.viewer.Name != "" {
		if who == "" {
			who = m.viewer.Name
		}
		right = metaStyle.Render("linear: " + who)
	}
	w := m.width - 4 // docStyle pads 2 each side
	if w < 20 {
		w = 20
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	return left + strings.Repeat(" ", gap) + right + "\n" +
		metaStyle.Render(strings.Repeat("═", w))
}

func (m Model) statusOrHelp(bindings []key.Binding) string {
	if m.err != nil {
		return errStyle.Render("✗ " + m.err.Error())
	}
	return m.help.ShortHelpView(bindings)
}

func (m Model) button(id, label string, primary bool) string {
	st := btnStyle
	if primary {
		st = btnPrimaryStyle
	}
	return m.zones.Mark(id, st.Render(label))
}

func (m Model) buttonRow(buttons ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Center, strings.Join(buttons, " "))
}

func (m Model) viewMain() string {
	summary := metaStyle.Render(fmt.Sprintf("%d issues · %d worktrees", len(m.issues), len(m.wts))) +
		"  " + m.button("btn:new", "+ new", false) +
		"  " + m.button("btn:search", "⌕ search", false)
	if m.loadingWT || m.loadingIssues || m.loadingCI {
		summary += "  " + m.spinner.View() + dimStyle.Render(" refreshing…")
	}
	if !m.authed {
		summary += "  " + m.button("btn:connect", "connect linear", true)
		note := " to see your issues here"
		if m.cfg.Linear.App().ClientID == "" {
			note = " (needs an OAuth app — see README)"
		}
		summary += dimStyle.Render(note)
	}

	filterLine := ""
	if m.filtering || m.filterInput.Value() != "" {
		filterLine = m.filterInput.View() + "\n"
	}

	bindings := []key.Binding{keyOpen, keyJump, keyView, keyNew, keySearchL, keyDelete, keyRefresh, keyFilter}
	if !m.authed {
		bindings = append(bindings, keyAuth)
	}
	if m.ghOK && m.ghToken == "" {
		bindings = append(bindings, keyGitHub)
	}
	bindings = append(bindings, keyQuit)
	if m.threePane() {
		bindings = append([]key.Binding{keyPane}, bindings...)
		switch m.pane {
		case paneClaude:
			bindings = []key.Binding{keyTermEsc}
		case paneDiff:
			bindings = []key.Binding{keyChoose, keyStage, keyHunks, keyCommitC, keyLog, keyBranchD, keyBack}
			if m.gitMode == gitModeCommit {
				bindings = []key.Binding{keyField, keyCommitGo, keyGenMsg, keyCancel}
			}
		}
	}
	if m.filtering {
		bindings = []key.Binding{keyApply, keyClear, keyChoose}
	}

	content := m.renderTable()
	if m.threePane() {
		content = m.viewPanels()
	}
	return summary + "\n\n" + filterLine + content + "\n" + m.statusOrHelp(bindings)
}

// viewPanels lays out the wide-terminal main screen: a full-width issues
// strip on top (one line when unfocused, half the screen when focused), with
// the claude chat and the selected worktree's diff side by side below.
func (m Model) viewPanels() string {
	w := m.width - docStyle.GetHorizontalFrameSize()
	topH, bottomH := m.panelHeights()

	pane := func(w, h int, focused bool, title, body string) string {
		st, ts, rs := paneStyle, paneTitleStyle, metaStyle
		if focused {
			st, ts, rs = paneFocusStyle, paneTitleFocus, okStyle
		}
		rule := rs.Render(strings.Repeat("═", w-2))
		return st.Width(w - 2).Height(h - 2).Render(
			ts.Render(truncate(title, w-4)) + "\n" + rule + "\n" + body)
	}

	var top string
	if m.pane == paneIssues {
		top = pane(w, topH, true, "issues & worktrees", m.renderTable())
	} else {
		top = paneStyle.Width(w - 2).Height(topH - 2).Render(m.collapsedIssueLine(w - 4))
	}
	top = m.zones.Mark("pane:issues", top)

	lw := w / 2
	rw := w - lw

	dir := m.claudeDir()
	claudeTitle := "claude — " + filepath.Base(dir)
	var claudeBody string
	switch s := m.terms[dir]; {
	case s == nil:
		claudeBody = dimStyle.Render("press enter on a card (or tab here) to launch claude in its worktree\n\nctrl+q cycles panes from anywhere")
	case s.exited.Load():
		claudeTitle += " · exited"
		claudeBody = s.render(false) // frozen last frame
	default:
		claudeBody = s.render(m.pane == paneClaude)
	}
	if m.pane == paneClaude {
		if s := m.terms[dir]; s != nil && s.exited.Load() {
			claudeTitle += " — enter restarts"
		} else {
			claudeTitle += " · ctrl+q next pane"
		}
	}
	center := m.zones.Mark("pane:claude",
		pane(lw, bottomH, m.pane == paneClaude, claudeTitle, claudeBody))

	gitTitle, gitBody := m.gitPaneContent(rw-4, bottomH-4)
	if wt := m.selectedRef().wt; wt != nil && wt.Prunable {
		gitBody = warnStyle.Render("worktree directory is gone — press d to clean it up")
	}
	right := m.zones.Mark("pane:diff", pane(rw, bottomH, m.pane == paneDiff, gitTitle, gitBody))

	return lipgloss.JoinVertical(lipgloss.Left,
		top,
		lipgloss.JoinHorizontal(lipgloss.Top, center, right))
}

// collapsedIssueLine is the one-line summary shown in the issues strip when
// another pane has focus: the selected card/worktree at a glance.
func (m Model) collapsedIssueLine(w int) string {
	prefix := paneTitleStyle.Render("issues ▸ ")
	var s string
	ref := m.selectedRef()
	switch {
	case ref.issue != nil:
		s = ref.issue.Identifier + " — " + ref.issue.Title + "  · " + ref.issue.State
		if ref.wt != nil {
			s += " · " + ref.wt.Branch
		}
	case ref.wt != nil:
		s = ref.wt.Branch + " · " + relPath(m.root, ref.wt.Path) + " · " + wtStatus(*ref.wt)
	default:
		s = fmt.Sprintf("%d issues · %d worktrees", len(m.issues), len(m.wts))
	}
	return prefix + truncate(s, w-lipgloss.Width(prefix))
}

func (m Model) viewDetail() string {
	if m.detailIssue == nil {
		return ""
	}
	footer := m.buttonRow(
		m.button("btn:create", "create worktree", true),
		m.button("btn:back", "back", false),
	)
	return m.viewport.View() + "\n" + footer + "  " +
		m.statusOrHelp([]key.Binding{keyCreate, keyBack})
}

func renderIssueDetail(is linear.Issue, width int) string {
	wrap := lipgloss.NewStyle().Width(width - 2)
	var b strings.Builder
	b.WriteString(titleStyle.Render(is.Identifier) + "  " + wrap.Render(is.Title) + "\n\n")
	b.WriteString(labelStyle.Render("state") + " " + is.State + "\n")
	if p := linear.PriorityName(is.Priority); p != "" {
		b.WriteString(labelStyle.Render("priority") + " " + p + "\n")
	}
	if len(is.Labels) > 0 {
		b.WriteString(labelStyle.Render("labels") + " " + strings.Join(is.Labels, ", ") + "\n")
	}
	if is.URL != "" {
		b.WriteString(labelStyle.Render("url") + " " + is.URL + "\n")
	}
	b.WriteString("\n")
	if strings.TrimSpace(is.Description) == "" {
		b.WriteString(dimStyle.Render("(no description)"))
	} else {
		b.WriteString(wrap.Render(is.Description))
	}
	return b.String()
}

func (m Model) viewManual() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("New worktree") + "\n\n")
	b.WriteString("Enter a Linear issue key or a full branch name:\n\n")
	b.WriteString(m.manualInput.View() + "\n\n")
	if m.fetchingIssue {
		b.WriteString(m.spinner.View() + " fetching " + m.pendKey + " from Linear…\n\n")
	}
	b.WriteString(m.buttonRow(
		m.button("btn:continue", "continue", true),
		m.button("btn:back", "back", false),
	) + "\n\n")
	b.WriteString(m.statusOrHelp([]key.Binding{keyConfirm, keyBack}))
	return b.String()
}

func (m Model) viewTypePick() string {
	var b strings.Builder
	title := "New worktree"
	if m.pendKey != "" {
		title += " for " + m.pendKey
		if m.pendTitle != "" {
			title += " — " + m.pendTitle
		}
	}
	b.WriteString(titleStyle.Render(title) + "\n\n")
	b.WriteString("Branch type:\n\n")
	for i, t := range m.cfg.BranchTypes {
		preview := m.branchPreview(t)
		var line string
		if i == m.typeIdx {
			line = cursorStyle.Render("❯ "+padRight(t, 9)) + okStyle.Render(preview)
		} else {
			line = "  " + padRight(t, 9) + dimStyle.Render(preview)
		}
		b.WriteString(m.zones.Mark(typeZoneID(i), line) + "\n")
	}
	b.WriteString("\n" + m.buttonRow(m.button("btn:back", "back", false)) + "\n\n")
	b.WriteString(m.statusOrHelp([]key.Binding{keyChoose, keyConfirm, keyBack}))
	return b.String()
}

func (m Model) viewEditBranch() string {
	name := strings.TrimSpace(m.branchInput.Value())
	dir := filepath.Join(".worktrees", branch.DirFor(name))

	var b strings.Builder
	b.WriteString(titleStyle.Render("Branch name") + "\n\n")
	b.WriteString(m.branchInput.View() + "\n\n")
	b.WriteString(labelStyle.Render("worktree") + " " + dir + "\n")
	b.WriteString(labelStyle.Render("base") + " " + m.base + dimStyle.Render("  (existing branches are checked out as-is)") + "\n\n")
	b.WriteString(m.buttonRow(
		m.button("btn:create", "create", true),
		m.button("btn:back", "back", false),
	) + "\n\n")
	b.WriteString(m.statusOrHelp([]key.Binding{keyDoIt, keyBack}))
	return b.String()
}

func (m Model) viewCreated() string {
	rel, err := filepath.Rel(m.root, m.createdPath)
	if err != nil {
		rel = m.createdPath
	}
	content := okStyle.Render("✓ Worktree created") + "\n\n" +
		labelStyle.Render("branch") + " " + m.createdBranch + "\n" +
		labelStyle.Render("path") + " " + rel + "\n\n" +
		m.buttonRow(
			m.button("btn:jump", "jump in", true),
			m.button("btn:back", "back", false),
		)
	return boxStyle.Render(content)
}

func (m Model) viewDeleteConfirm() string {
	if m.delTarget == nil {
		return ""
	}
	wt := *m.delTarget
	var b strings.Builder
	b.WriteString(warnStyle.Render("Remove worktree?") + "\n\n")
	b.WriteString(labelStyle.Render("branch") + " " + wt.Branch + "\n")
	b.WriteString(labelStyle.Render("path") + " " + relPath(m.root, wt.Path) + "\n")
	if wt.Dirty {
		b.WriteString("\n" + warnStyle.Render("⚠ has uncommitted changes — removing will discard them") + "\n")
	}
	b.WriteString("\n")
	if m.removing {
		b.WriteString(m.spinner.View() + " removing…")
	} else {
		b.WriteString(m.buttonRow(
			m.button("btn:remove", "remove", m.delFocus == 0),
			m.button("btn:remove-branch", "remove + delete branch", m.delFocus == 1),
			m.button("btn:cancel", "cancel", m.delFocus == 2),
		) + "\n\n")
		b.WriteString(m.statusOrHelp([]key.Binding{keyChoose2, keyConfirm2, keyRemove, keyRemoveB, keyCancel}))
	}
	return boxStyle.Render(b.String())
}

func (m Model) viewAuth() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Connect Linear") + "\n\n")
	b.WriteString(dimStyle.Render("Create an OAuth app at linear.app → Settings → API → OAuth applications.\nCallback URL: "+linear.RedirectURI) + "\n\n")
	b.WriteString(labelStyle.Render("client id") + "\n" + m.zones.Mark("auth:0", m.authInputs[0].View()) + "\n\n")
	b.WriteString(labelStyle.Render("secret") + dimStyle.Render(" (leave empty for public/PKCE apps)") + "\n" + m.zones.Mark("auth:1", m.authInputs[1].View()) + "\n\n")
	b.WriteString(m.buttonRow(
		m.button("btn:connect", "connect", true),
		m.button("btn:cancel", "cancel", false),
	) + "\n\n")
	b.WriteString(m.statusOrHelp([]key.Binding{keyField, keyConfirm, keyCancel}))
	return b.String()
}

func searchZoneID(i int) string { return "search:" + strconv.Itoa(i) }

func (m Model) viewSearch() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Search Linear") + "\n")
	b.WriteString(dimStyle.Render("anyone's issues, live as you type — state:review or @name narrows · tab completes, ↑/↓ select, enter opens") + "\n\n")
	b.WriteString(m.searchInput.View() + "\n\n")

	switch {
	case m.searching:
		b.WriteString(m.spinner.View() + " searching…\n")
	case m.searchedFor != "" && len(m.searchResults) == 0:
		b.WriteString(dimStyle.Render("no issues matched \u201c"+m.searchedFor+"\u201d") + "\n")
	case len(m.searchResults) > 0:
		b.WriteString(m.renderSearchResults())
	}
	b.WriteString("\n" + m.statusOrHelp([]key.Binding{keyChoose, keyConfirm, keyBack}))
	return b.String()
}

func (m Model) renderSearchResults() string {
	keyW, stateW, whoW := 10, 14, 16
	w := m.width - 4
	titleW := w - keyW - stateW - whoW - 6
	if titleW < 16 {
		titleW = 16
	}
	visible := m.height - 12
	if visible < 5 {
		visible = 5
	}
	start := 0
	if m.searchSel >= visible {
		start = m.searchSel - visible + 1
	}

	var b strings.Builder
	b.WriteString(dimStyle.Render("  "+padRight("KEY", keyW)+padRight("TITLE", titleW)+padRight("STATE", stateW)+"ASSIGNEE") + "\n")
	for i := start; i < len(m.searchResults) && i < start+visible; i++ {
		is := m.searchResults[i]
		title := is.Title
		if m.worktreeForKey(is.Identifier) != nil {
			title += " ⌂" // a worktree already exists; enter jumps into it
		}
		line := padRight(is.Identifier, keyW) + padRight(truncate(title, titleW-1), titleW) +
			padRight(truncate(is.State, stateW-1), stateW) + truncate(is.Assignee, whoW)
		if i == m.searchSel {
			line = cursorStyle.Render("❯ ") + okStyle.Render(line)
		} else {
			line = "  " + line
		}
		b.WriteString(m.zones.Mark(searchZoneID(i), line) + "\n")
	}
	if more := len(m.searchResults) - start - visible; more > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  … %d more", more)) + "\n")
	}
	return b.String()
}

// truncate shortens s to at most w runes, ending with an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

// ansiRE strips SGR color sequences so rendered lines can be matched on text.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// renderTable post-processes the table view: the header rule gets a proper
// "├" left edge, and group-header rows (marked "▸ ") become full-width rules
// with the title inset, e.g. ├─ IN PROGRESS ──────┼──────────┼────
// tableFrame builds the table's top/bottom border with joins matched to the
// visible column widths: left, per-column dashes, join, …, right.
func (m Model) tableFrame(left, join, right rune) string {
	var b strings.Builder
	b.WriteRune(left)
	first := true
	for _, c := range m.table.Columns() {
		if c.Width <= 0 {
			continue // hidden column (three-pane layout)
		}
		if !first {
			b.WriteRune(join)
		}
		first = false
		b.WriteString(strings.Repeat("─", c.Width+2))
	}
	b.WriteRune(right)
	return metaStyle.Render(b.String())
}

func (m Model) renderTable() string {
	totalW := 0
	for _, c := range m.table.Columns() {
		if c.Width > 0 {
			totalW += c.Width + 3
		}
	}
	lines := strings.Split(m.table.View(), "\n")
	if len(lines) > 1 {
		lines[1] = strings.Replace(lines[1], "┼", "├", 1)
	}
	tintedDivider := metaStyle.Render("│")
	rightEdge := tintedDivider
	for i, line := range lines {
		plain := ansiRE.ReplaceAllString(line, "")
		if idx := strings.Index(plain, "▸ "); idx >= 0 && strings.Trim(plain[:idx], "│ ") == "" {
			// group headers keep every cell before the marker empty
			title := plain[idx+len("▸ "):]
			if j := strings.IndexRune(title, '│'); j >= 0 {
				title = title[:j]
			}
			lines[i] = m.groupDividerLine(strings.TrimSpace(title)) + metaStyle.Render("┤")
			continue
		}
		if i == 1 { // header rule meets the right edge with a join
			lines[i] = line + metaStyle.Render("┤")
			continue
		}
		if strings.TrimSpace(plain) == "" { // viewport filler below the rows
			pad := totalW - 1
			if pad < 0 {
				pad = 0
			}
			lines[i] = tintedDivider + strings.Repeat(" ", pad) + rightEdge
			continue
		}
		// Cell dividers are rendered uncolored so the Selected row style
		// isn't cut off by an embedded ANSI reset; tint them subtle on the
		// remaining (unstyled) rows. The selected row keeps accent dividers.
		if i > 1 && !strings.Contains(line, "\x1b") {
			line = strings.ReplaceAll(line, "│", tintedDivider)
		}
		lines[i] = line + rightEdge
	}
	return m.tableFrame('┌', '┬', '┐') + "\n" +
		strings.Join(lines, "\n") + "\n" +
		m.tableFrame('└', '┴', '┘')
}

// groupDividerLine draws a rule across all columns, intersections aligned
// with the cell dividers, with the group title inset after the left edge.
func (m Model) groupDividerLine(title string) string {
	var rule []rune
	for i, c := range m.table.Columns() {
		if c.Width <= 0 {
			continue // hidden column (three-pane layout)
		}
		edge := '┼'
		if i == 0 {
			edge = '├'
		}
		rule = append(rule, edge)
		for j := 0; j < c.Width+2; j++ {
			rule = append(rule, '─')
		}
	}
	text := []rune(" " + title + " ")
	if max := len(rule) - 4; len(text) > max {
		text = text[:max]
	}
	return metaStyle.Render(string(rule[:2])) +
		groupTitleStyle.Render(string(text)) +
		metaStyle.Render(string(rule[2+len(text):]))
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s + " "
	}
	return s + strings.Repeat(" ", w-len(s))
}
