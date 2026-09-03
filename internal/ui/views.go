package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/markcipolla/treeline/internal/branch"
	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/ide"
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
	case scrRepoPick:
		body = m.viewRepoPick()
	case scrSettings:
		body = m.viewSettings()
	case scrRepoEdit:
		body = m.viewRepoEdit()
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
	// no repo name here: which repo a branch lives in is per-worktree, and
	// the table's REPO column says it per row
	left := titleStyle.Render("🌲 treeline")
	right := ""
	if who := m.viewer.Email; m.authed || who != "" || m.viewer.Name != "" {
		if who == "" {
			who = m.viewer.Name
		}
		label := "linear"
		if who != "" {
			label = "linear: " + who
		}
		// fetch state: spinner while refreshing, green when good, red when
		// the last refresh failed
		dot := okStyle.Render("●")
		switch {
		case m.linearBusy:
			dot = m.spinner.View()
		case m.linearFail:
			dot = errStyle.Render("●")
		}
		right = dot + " " + metaStyle.Render(label)
	}
	w := m.width - 4 // docStyle pads 2 each side
	if w < 20 {
		w = 20
	}
	// the counts and buttons sit beside the logo, settings top-right past the
	// linear status. Only on the main screen: the settings screen is where
	// the gear leads, and the modal screens have their own buttons.
	if m.screen == scrMain {
		if right != "" {
			right += "  "
		}
		gear := right + m.button("btn:settings", "⚙ settings", false)
		tightGear := right + m.button("btn:settings", "⚙", false)
		// widest cluster that fits; the counts go before the button labels do
		cands := []struct{ mid, right string }{
			{m.summaryCluster(true, true), gear},
			{m.summaryCluster(false, true), gear},
			{m.summaryCluster(false, false), tightGear},
			{"", tightGear},
		}
		pick := cands[len(cands)-1]
		for _, c := range cands {
			if lipgloss.Width(left)+lipgloss.Width(c.mid)+lipgloss.Width(c.right)+2 <= w {
				pick = c
				break
			}
		}
		left += pick.mid
		right = pick.right
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	return left + strings.Repeat(" ", gap) + right + "\n" +
		metaStyle.Render(strings.Repeat("═", w))
}

// summaryCluster is the main-screen header's middle: the issue and worktree
// counts, the new/search buttons, a spinner while anything refreshes, and a
// connect button while Linear isn't. counts and longLabels shed detail as the
// header tightens.
func (m Model) summaryCluster(counts, longLabels bool) string {
	s := ""
	if counts {
		s += "  " + metaStyle.Render(fmt.Sprintf("%d issues · %d worktrees", len(m.issues), len(m.wts)))
	}
	newL, searchL := "+ new", "⌕ search"
	if !longLabels {
		newL, searchL = "+", "⌕"
	}
	s += "  " + m.button("btn:new", newL, false) + " " + m.button("btn:search", searchL, false)
	if m.loadingWT || m.loadingIssues || m.loadingCI {
		s += "  " + m.spinner.View()
		if counts {
			s += dimStyle.Render(" refreshing…")
		}
	}
	if !m.authed {
		label := "connect linear"
		if !longLabels {
			label = "connect"
		}
		s += "  " + m.button("btn:connect", label, true)
		if counts {
			note := " to see your issues here"
			if m.cfg.Linear.App().ClientID == "" {
				note = " (needs an OAuth app — see README)"
			}
			s += dimStyle.Render(note)
		}
	}
	return s
}

func (m Model) statusOrHelp(bindings []key.Binding) string {
	w := m.width - docStyle.GetHorizontalFrameSize()
	if w < 20 {
		w = 20
	}
	if m.err != nil {
		return maxWidthStyle(w).Render(errStyle.Render("✗ " + m.err.Error()))
	}
	return maxWidthStyle(w).Render(m.help.ShortHelpView(bindings))
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
	// the counts, the new/search buttons and the auth state all live in the
	// header (see summaryCluster) — the panes get the height they occupied
	filterLine := ""
	if m.filtering || m.filterInput.Value() != "" {
		filterLine = m.filterInput.View() + "\n"
	}

	bindings := []key.Binding{keyOpen, keyJump, keyView, keyNew, keySearchL, keyDelete, keyRefresh, keyFilter, keySettingsK}
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
		case paneTerm:
			bindings = []key.Binding{keyTermTabs, keyTermEsc}
		case paneIDE:
			switch {
			case m.ide.Editing():
				bindings = []key.Binding{keyIDESave, keyIDESelect, keyIDEIndent, keyIDEMulti, keyIDEView, keyTermEsc}
			case m.ide.InputActive():
				bindings = []key.Binding{keyApply, keyCancel}
			case m.ide.FileFocused():
				bindings = []key.Binding{keyIDEEdit, keyIDESave, keyIDEFind, keyIDETabs, keyIDEClose, keyIDETree}
			default:
				bindings = []key.Binding{keyIDEOpen, keyIDEFilter, keyIDENew, keyIDERename, keyIDEDel}
			}
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
	return filterLine + content + "\n" + m.statusOrHelp(bindings)
}

// viewPanels lays out the wide-terminal main screen. The arrangement follows
// the width: the issues list is a strip above the work panes on a merely wide
// terminal, a column beside them on a wider one, and on a very wide terminal
// the shell steps out from under the git pane into a column of its own. The
// panes are drawn as one border grid, sharing the lines between them.
func (m Model) viewPanels() string {
	l := m.layout()

	// the issues list: full height in the column layouts, and in the stacked
	// one a strip that collapses to a single line when another pane has focus
	var issues panePart
	switch w, h := l.issues.w-2, l.issues.h-2; {
	case m.columnLayout() || m.pane == paneIssues:
		issues = m.issuesPart(m.issuesTitle(l.issues.w), m.pane == paneIssues)
	default:
		issues = panePart{w: w, rows: fitRows(m.collapsedIssueLine(w), w, h),
			edges: make([]edgeKind, h)}
	}
	issues = m.mark("pane:issues", issues)

	dir := m.claudeDir()
	noWT := dir == "" // nothing checked out: the work panes stay empty
	claudeTitle := "balance"
	if !noWT {
		claudeTitle += " — " + m.paneLabel(dir)
	}
	var claudeBody string
	switch s := m.terms[dir]; {
	case noWT:
		claudeBody = dimStyle.Render(m.noWorktreeHint())
	case s == nil:
		claudeBody = dimStyle.Render("press enter on a card (or tab here) to launch balance in its worktree\n\nctrl+q cycles panes from anywhere")
	case s.exited.Load():
		claudeTitle += " · exited"
		claudeBody = s.render(false) // frozen last frame
	default:
		claudeBody = s.render(m.pane == paneClaude)
		if s.tmuxName != "" {
			claudeTitle += dimStyle.Render(" · tmux") // survives quitting treeline
		}
		if n := s.scrolled(); n > 0 {
			claudeTitle += fmt.Sprintf(" · ↑%d — wheel down for live", n)
		}
	}
	if m.pane == paneClaude && !noWT {
		if s := m.terms[dir]; s != nil && s.exited.Load() {
			claudeTitle += " — enter restarts"
		} else {
			claudeTitle += " · ctrl+q next pane"
		}
	}
	if time.Now().Before(m.copiedUntil) {
		claudeTitle += okStyle.Render(" · copied ✓")
	}
	claude := m.mark("pane:claude",
		m.workPart(l.claude, m.pane == paneClaude, claudeTitle, claudeBody))

	var ide panePart
	if noWT && !m.ide.AnyDirty() {
		ide = m.workPart(l.ide, m.pane == paneIDE, "ide", dimStyle.Render(m.noWorktreeHint()))
	} else {
		ide = m.idePart(l.ide, m.pane == paneIDE)
	}
	ide = m.mark("pane:ide", ide)

	gitTitle, gitBody := m.gitPaneContent(l.git.w-4, l.git.h-4)
	if wt := m.selectedRef().wt; wt != nil && wt.Prunable {
		gitTitle, gitBody = "git", warnStyle.Render("worktree directory is gone — press d to clean it up")
	} else if noWT {
		gitTitle, gitBody = "git", dimStyle.Render(m.noWorktreeHint())
	}
	if a, b, ok := m.gitSel.bounds(); ok {
		gitBody = strings.Join(highlightSel(strings.Split(gitBody, "\n"), a, b), "\n")
	}
	if m.copiedFrom == paneDiff && time.Now().Before(m.copiedUntil) {
		gitTitle += okStyle.Render(" · copied ✓")
	}
	git := m.mark("pane:diff",
		m.workPart(l.git, m.pane == paneDiff, gitTitle, gitBody))

	var termTitle, termBody string
	if noWT {
		termTitle = "shell"
		termBody = dimStyle.Render(m.noWorktreeHint())
	} else {
		tabs := m.termTabsFor(dir)
		sel := m.termSel[dir]
		if sel < 0 || sel >= len(tabs) {
			sel = 0
		}
		termTitle = "shell"
		var body string
		switch t := tabs[sel]; {
		case t.sess == nil:
			if t.kind == "setup" {
				body = dimStyle.Render("the setup script runs here when a worktree is created — press enter to run it now")
			} else {
				body = dimStyle.Render("tab here (or click) to open a shell in this worktree\n\nctrl+t opens another shell tab")
			}
		case t.sess.exited.Load():
			termTitle += " · exited"
			if m.pane == paneTerm {
				switch {
				case t.kind == "setup":
					termTitle += " — enter reruns"
				case t.kind != "shell":
					termTitle += " — enter restarts, ✕ closes"
				default:
					termTitle += " — enter restarts"
				}
			}
			body = t.sess.render(false)
		default:
			body = t.sess.render(m.pane == paneTerm)
			if t.sess.tmuxName != "" {
				termTitle += dimStyle.Render(" · tmux")
			}
			if n := t.sess.scrolled(); n > 0 {
				termTitle += fmt.Sprintf(" · ↑%d", n)
			}
		}
		bar := m.termTabBar(tabs, sel, l.term.w-3, m.pane == paneTerm)
		termBody = strings.Join(append(bar, strings.Split(body, "\n")...), "\n")
	}
	term := m.mark("pane:term",
		m.workPart(l.term, m.pane == paneTerm, termTitle, termBody))

	switch l.mode {
	case layFour:
		return frame(band{{issues}, {claude}, {ide}, {git}, {term}})
	case layCols:
		return frame(band{{issues}, {claude}, {ide}, {git, term}})
	}
	return frame(band{{issues}}, band{{claude}, {ide}, {git, term}})
}

// idePart is the ide pane as a panePart: workPart's chrome with the
// explorer/editor divider carried into it — ╤ where it meets the title rule
// and ┴ into the bottom border — so the divider runs the pane's full height
// instead of hanging between them.
func (m Model) idePart(b box, focused bool) panePart {
	m.ide.SetFocused(focused)
	m.ide.SetSize(b.w-4, b.h-4)
	title, body := m.ide.Content(b.w-4, b.h-4)
	p := m.workPart(b, focused, title, body)
	dx := ide.TreeWidth(b.w-4) + 2 // the │ in " │ ", past the body's pad column
	if len(p.rows) > 1 && dx > 0 && dx < p.w-1 {
		rs := metaStyle
		if focused {
			rs = okStyle
		}
		p.rows[1] = rs.Render(strings.Repeat("═", dx) + "╤" + strings.Repeat("═", p.w-dx-1))
		p.botJoin = []int{dx}
	}
	return p
}

// workPart builds one of the work panes: its title, the ═ rule under it, and
// a body cut to the box the layout gave it.
func (m Model) workPart(b box, focused bool, title, body string) panePart {
	w, h := b.w-2, b.h-2 // the box less its borders
	ts, rs := paneTitleStyle, metaStyle
	if focused {
		ts, rs = paneTitleFocus, okStyle
	}
	// a box too short for all of its chrome gives up the rule, then the
	// title: whatever it keeps, it stays exactly as tall as the layout said
	var rows []string
	if h > 0 {
		rows = append(rows, padTo(ts.Render(" "+title), w))
	}
	if h > 1 {
		rows = append(rows, rs.Render(strings.Repeat("═", w)))
	}
	// the body sits a column off the border, like the title above it
	for _, r := range fitRows(body, w-1, h-2) {
		rows = append(rows, " "+r)
	}
	edges := make([]edgeKind, len(rows))
	if len(edges) > 1 {
		edges[1] = edgeRule
	}
	return panePart{w: w, rows: rows, edges: edges, focused: focused}
}

func termTabZoneID(i int) string { return "termtab:" + strconv.Itoa(i) }

// termTabBar is the shell pane's tab row, drawn like the ide's: one bordered
// tab per shell (the setup script's leads when the repo shows it here), the
// active one open at the bottom, a ✕ on an extra shell — clicking it closes
// the tab, killing what runs there — and a trailing + that opens another
// shell. ctrl+←/→ move between them from the keyboard.
func (m Model) termTabBar(tabs []*termTab, sel, w int, focused bool) []string {
	items := make([]tabItem, 0, len(tabs)+1)
	for i, t := range tabs {
		// every shell tab reads "shell" — the numbers in the kind only keep
		// their tmux sessions apart
		label := t.kind
		if strings.HasPrefix(label, "shell") {
			label = "shell"
		}
		it := tabItem{zone: termTabZoneID(i), label: label, active: i == sel}
		if t.kind != "shell" && t.kind != "setup" {
			it.closeZone = "termtab:x"
		}
		items = append(items, it)
	}
	items = append(items, tabItem{zone: "termtab:new", label: "+"})
	return m.tabBar(w, focused, items)
}

// mark tags a pane's rows for the mouse. The zone covers the pane's inside,
// which is what the click and drag handlers measure their offsets from.
func (m Model) mark(id string, p panePart) panePart {
	p.rows = strings.Split(m.zones.Mark(id, strings.Join(p.rows, "\n")), "\n")
	return p
}

// issuesTitle names the issues pane, dropping the "& worktrees" half when the
// column is too narrow to spell it out.
func (m Model) issuesTitle(w int) string {
	const full = "issues & worktrees"
	if w-4 < len(full) {
		return "issues"
	}
	return full
}

// noWorktreeHint explains why the claude, git and shell panes are empty: the
// selected row has no worktree for them to work in.
func (m Model) noWorktreeHint() string {
	ref := m.selectedRef()
	switch {
	case ref.wt != nil && ref.wt.Prunable:
		return "worktree directory is gone — press d to clean it up"
	case ref.issue != nil:
		return "no worktree for " + ref.issue.Identifier + " yet — press enter on the card to create one"
	}
	return "select a card or worktree to work in"
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
	b.WriteString(labelStyle.Render("repo") + " " + m.pendRepo.name + "\n")
	b.WriteString(labelStyle.Render("worktree") + " " + dir + "\n")
	b.WriteString(labelStyle.Render("base") + " " + m.pendRepo.base + dimStyle.Render("  (existing branches are checked out as-is)") + "\n\n")
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
	setupLine := ""
	if m.setupBusy {
		setupLine = m.spinner.View() + dimStyle.Render(" running setup script…") + "\n"
	}
	content := okStyle.Render("✓ Worktree created") + "\n" + setupLine + "\n" +
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
	title := "Remove worktree?"
	if m.delForce {
		title = "Force remove locked worktree?"
	}
	b.WriteString(warnStyle.Render(title) + "\n\n")
	b.WriteString(labelStyle.Render("branch") + " " + wt.Branch + "\n")
	b.WriteString(labelStyle.Render("path") + " " + relPath(m.root, wt.Path) + "\n")
	if reason := lockReason(wt, m.delErr); reason != "" {
		b.WriteString(labelStyle.Render("locked by") + " " + reason + "\n")
	}
	if wt.Dirty {
		b.WriteString("\n" + warnStyle.Render("⚠ has uncommitted changes — removing will discard them") + "\n")
	}
	if m.delForce {
		// wrapped: the modal is sized to its content, and an unwrapped
		// sentence this long would run past the edge of the terminal
		b.WriteString("\n" + errStyle.Width(66).Render("⚠ git refused: the worktree is locked. Forcing pulls "+
			"the directory out from under whatever holds it — a claude session running "+
			"there loses the files it is working on.") + "\n")
	}
	b.WriteString("\n")
	if m.removing {
		b.WriteString(m.spinner.View() + " removing…")
	} else {
		removeLabel, branchLabel := "remove", "remove + delete branch"
		if m.delForce {
			removeLabel, branchLabel = "force remove", "force remove + delete branch"
		}
		b.WriteString(m.buttonRow(
			m.button("btn:remove", removeLabel, m.delFocus == 0),
			m.button("btn:remove-branch", branchLabel, m.delFocus == 1),
			m.button("btn:cancel", "cancel", m.delFocus == 2),
		) + "\n\n")
		remove, removeB := keyRemove, keyRemoveB
		if m.delForce {
			remove, removeB = keyForce, keyForceB
		}
		b.WriteString(m.statusOrHelp([]key.Binding{keyChoose2, keyConfirm2, remove, removeB, keyCancel}))
	}
	return boxStyle.Render(b.String())
}

// lockReason names whatever holds the worktree: the lock recorded in git's
// worktree list, or — when the list is older than the lock — the reason git
// gave when it refused to remove it.
func lockReason(wt gitx.Worktree, err error) string {
	if wt.LockReason != "" {
		return wt.LockReason
	}
	if wt.Locked {
		return "no reason given"
	}
	if err != nil && errors.Is(err, gitx.ErrLocked) {
		if _, reason, ok := strings.Cut(err.Error(), "— "); ok {
			return reason
		}
	}
	return ""
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

// ansiRE strips SGR color sequences so rendered lines can be matched on text.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// tableFrame builds the table's top/bottom border with joins matched to the
// visible column widths: left, per-column dashes, join, …, right.
func (m Model) tableFrame(left, join, right rune) string {
	return metaStyle.Render(string(left) + m.tableFill(join, "─") + string(right))
}

// tableFill is the grid's columns as one horizontal line with no outer edges,
// so the frame can supply its own: each visible column's width of fill, with
// a join between. The pane's "═" title rule is this over a double line, which
// is what carries the column joins into the frame.
func (m Model) tableFill(join rune, fill string) string {
	var b strings.Builder
	first := true
	for _, c := range m.table.Columns() {
		if c.Width <= 0 {
			continue // hidden column (narrow layouts)
		}
		if !first {
			b.WriteRune(join)
		}
		first = false
		b.WriteString(strings.Repeat(fill, c.Width+2))
	}
	return b.String()
}

// tableJoins is where the grid's column dividers sit, as offsets into a row.
// The frame turns them into ┬ and ┴ on the border rows above and below, so
// the columns read as one grid rather than stopping at the pane's edge.
func (m Model) tableJoins() []int {
	var js []int
	x, first := 0, true
	for _, c := range m.table.Columns() {
		if c.Width <= 0 {
			continue
		}
		if !first {
			js = append(js, x)
			x++
		}
		first = false
		x += c.Width + 2
	}
	return js
}

// tableWidth is the grid's rendered width, borders included.
func (m Model) tableWidth() int {
	w := 1
	for _, c := range m.table.Columns() {
		if c.Width > 0 {
			w += c.Width + 3
		}
	}
	return w
}

// tableGrid is the table's rows — the header, its rule, the group dividers and
// the cards — with the outer edges left off for the frame to draw, and what
// each row presents to them.
func (m Model) tableGrid() ([]string, []edgeKind) {
	lines := strings.Split(m.table.View(), "\n")
	// the widget renders every row (fitTableHeight); show gridTop's window,
	// padded back out to the grid's height with filler
	if len(lines) > 2 {
		body := lines[2:]
		if top := m.gridTop(); top < len(body) {
			body = body[top:]
		} else {
			body = nil
		}
		if len(body) > m.gridBody {
			body = body[:m.gridBody]
		}
		for len(body) < m.gridBody {
			body = append(body, "")
		}
		lines = append(lines[:2:2], body...)
	}
	edges := make([]edgeKind, len(lines))
	chrome := m.gridChrome()
	tintedDivider := chrome.Render("│")
	for i, line := range lines {
		plain := ansiRE.ReplaceAllString(line, "")
		if idx := strings.Index(plain, "▸ "); idx >= 0 && strings.Trim(plain[:idx], "│ ") == "" {
			// group headers keep every cell before the marker empty
			title := plain[idx+len("▸ "):]
			if j := strings.IndexRune(title, '│'); j >= 0 {
				title = title[:j]
			}
			lines[i], edges[i] = m.groupDividerLine(strings.TrimSpace(title)), edgeCross
			continue
		}
		if i == 1 { // the header's rule, its joins already in place
			lines[i], edges[i] = dropEdge(line), edgeCross
			continue
		}
		if strings.TrimSpace(plain) == "" { // viewport filler below the rows
			var fb strings.Builder
			first := true
			for _, c := range m.table.Columns() {
				if c.Width <= 0 {
					continue
				}
				if !first {
					fb.WriteString("│")
				}
				first = false
				fb.WriteString(strings.Repeat(" ", c.Width+2))
			}
			lines[i] = chrome.Render(fb.String())
			continue
		}
		// Cell dividers are rendered uncolored so the Selected row style
		// isn't cut off by an embedded ANSI reset; tint them subtle on the
		// remaining (unstyled) rows. The selected row keeps accent dividers.
		line = dropEdge(line)
		if i > 1 && !strings.Contains(line, "\x1b") {
			line = strings.ReplaceAll(line, "│", tintedDivider)
		}
		lines[i] = line
	}
	return lines, edges
}

// dropEdge removes a row's leading border character. The table widget draws
// one for the first column's cell, where the frame draws its own.
func dropEdge(s string) string {
	for i, r := range s {
		switch r {
		case '│', '┼', '├':
			return s[:i] + s[i+len(string(r)):]
		}
	}
	return s
}

// renderTable boxes the grid on its own, for the narrow single-column layout.
func (m Model) renderTable() string {
	rows, edges := m.tableGrid()
	var b strings.Builder
	b.WriteString(m.tableFrame('┌', '┬', '┐') + "\n")
	for i, row := range rows {
		left, right := "│", "│"
		if edges[i] == edgeCross {
			left, right = "├", "┤"
		}
		b.WriteString(metaStyle.Render(left) + row + metaStyle.Render(right) + "\n")
	}
	b.WriteString(m.tableFrame('└', '┴', '┘'))
	// marked for the mouse like the panel layout's pane, so a click can
	// land on a card here too
	return m.zones.Mark("pane:issues", b.String())
}

// issuesPart is the issues list as a pane: the grid is the pane, its own
// column dividers standing in for any inner border, and its columns carrying
// joins out into the frame. Nesting a boxed table inside a boxed pane would
// double every edge and cost two lines.
func (m Model) issuesPart(title string, focused bool) panePart {
	ts, fs := paneTitleStyle, metaStyle
	if focused {
		ts, fs = paneTitleFocus, okStyle
	}
	w := m.tableWidth() - 2
	rows := []string{
		padTo(ts.Render(" "+truncate(title, w-1)), w),
		fs.Render(m.tableFill('╤', "═")),
	}
	edges := []edgeKind{edgeBody, edgeRule}
	grid, gridEdges := m.tableGrid()
	return panePart{
		w:       w,
		rows:    append(rows, grid...),
		edges:   append(edges, gridEdges...),
		botJoin: m.tableJoins(),
		focused: focused,
	}
}

// groupDividerLine draws a rule across all columns, intersections aligned
// with the cell dividers, with the group title inset after the left edge.
func (m Model) groupDividerLine(title string) string {
	var rule []rune
	first := true
	for _, c := range m.table.Columns() {
		if c.Width <= 0 {
			continue // hidden column (narrow layouts)
		}
		if !first {
			rule = append(rule, '┼')
		}
		first = false
		for j := 0; j < c.Width+2; j++ {
			rule = append(rule, '─')
		}
	}
	text := []rune(" " + title + " ")
	if max := len(rule) - 3; len(text) > max && max > 0 {
		text = text[:max]
	}
	chrome, name := m.gridChrome(), groupTitleStyle
	if m.gridFocused() {
		name = groupTitleFocus
	}
	return chrome.Render(string(rule[:1])) +
		name.Render(string(text)) +
		chrome.Render(string(rule[1+len(text):]))
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s + " "
	}
	return s + strings.Repeat(" ", w-len(s))
}
