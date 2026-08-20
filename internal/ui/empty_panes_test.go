package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
)

// selectIssue moves the cursor to the row for a card, failing if it has none.
func selectIssue(t *testing.T, m *Model, key string) {
	t.Helper()
	for i, ref := range m.refs {
		if ref.issue != nil && ref.issue.Identifier == key {
			m.table.SetCursor(i)
			return
		}
	}
	t.Fatalf("no row for %s", key)
}

// TestPanesEmptyWithoutWorktree: a card with no worktree must not borrow the
// repo root — claude, git and the shell have nothing to work in, so they stay
// empty and unfocusable rather than showing the main checkout's diff.
func TestPanesEmptyWithoutWorktree(t *testing.T) {
	m := newTestModel(t, 200)
	m.authed = true
	m.issues = []linear.Issue{
		{Identifier: "LAB-1", Title: "Has a worktree", State: "In Progress", StateType: "started"},
		{Identifier: "LAB-9", Title: "No worktree", State: "QA Ready", StateType: "started"},
	}
	m.refreshRows()
	selectIssue(t, &m, "LAB-9")

	if dir := m.claudeDir(); dir != "" {
		t.Fatalf("claudeDir = %q, want empty (no worktree for LAB-9)", dir)
	}

	// no git status or log is loaded for the parked-on directory
	if cmd := m.syncPanes(); cmd != nil {
		t.Error("syncPanes issued commands for a card with no worktree")
	}
	if m.gitFor != "" {
		t.Errorf("gitFor = %q, want empty", m.gitFor)
	}

	for _, p := range []int{paneClaude, paneDiff, paneTerm} {
		if m.paneEnabled(p) {
			t.Errorf("pane %d enabled without a worktree", p)
		}
	}
	// ctrl+q has nowhere to go: focus stays on the issues list
	mm, _ := m.keyMain(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if got := mm.(Model); got.pane != paneIssues {
		t.Errorf("ctrl+q moved focus to pane %d, want paneIssues", got.pane)
	}
	// ...and even a direct focus request (a click, say) bounces back
	mm, _ = m.focusPane(paneClaude)
	if got := mm.(Model); got.pane != paneIssues {
		t.Errorf("focusPane(paneClaude) = %d, want paneIssues", got.pane)
	}
	if m.terms[""] != nil || m.shells[""] != nil {
		t.Error("started a session with no worktree")
	}

	v := m.View()
	if !strings.Contains(v, "no worktree for LAB-9") {
		t.Errorf("view is missing the empty-pane hint:\n%s", v)
	}

	// the same cursor on a real worktree row wakes the panes back up
	m.wts = append(m.wts, gitx.Worktree{Root: m.root, Path: m.root + "/.worktrees/nine", Branch: "feature/lab-9-nine"})
	m.refreshRows()
	if !m.selectWorktree(m.root + "/.worktrees/nine") {
		t.Fatal("no row for the new worktree")
	}
	if dir := m.claudeDir(); dir != m.root+"/.worktrees/nine" {
		t.Errorf("claudeDir = %q, want the worktree path", dir)
	}
	if !m.paneEnabled(paneClaude) {
		t.Error("claude pane still disabled with a worktree selected")
	}
}

// TestFocusFallsBackWhenWorktreeVanishes: a worktree removed underneath a
// focused pane hands focus back to the issues list instead of leaving the
// panes pointed at nothing.
func TestFocusFallsBackWhenWorktreeVanishes(t *testing.T) {
	m := newTestModel(t, 200)
	target := m.wts[1].Path
	if !m.selectWorktree(target) {
		t.Fatal("no row for the second worktree")
	}
	mm, _ := m.focusPane(paneClaude)
	m = mm.(Model)
	if m.pane != paneClaude {
		t.Fatalf("pane = %d, want paneClaude", m.pane)
	}

	mm, _ = m.Update(worktreesMsg{wts: nil}) // both worktrees removed
	got := mm.(Model)
	if got.pane != paneIssues {
		t.Errorf("pane = %d after the worktree went away, want paneIssues", got.pane)
	}
}

// TestCardsLoadingKeepsPanesInStep: worktrees list first (they are local and
// fast), then the Linear cards land and rebuild every row. The cursor must
// follow what it was on, and the panes must follow the cursor — the bug was a
// card ending up selected while the git pane still showed main's diff.
func TestCardsLoadingKeepsPanesInStep(t *testing.T) {
	m := newTestModel(t, 200)
	target := m.wts[1].Path
	if !m.selectWorktree(target) {
		t.Fatal("no row for the second worktree")
	}
	m.syncPanes()
	if m.gitFor != target {
		t.Fatalf("gitFor = %q, want %q", m.gitFor, target)
	}

	// the cards arrive: rows are regrouped by status and every index shifts
	mm, _ := m.Update(issuesMsg{issues: []linear.Issue{
		{Identifier: "LAB-7", Title: "Unrelated card", State: "Todo", StateType: "unstarted"},
		{Identifier: "LAB-8", Title: "Another card", State: "Todo", StateType: "unstarted"},
	}})
	got := mm.(Model)

	if ref := got.selectedRef(); ref.wt == nil || ref.wt.Path != target {
		t.Errorf("cursor drifted off the worktree row: %+v", ref)
	}
	if got.gitFor != target {
		t.Errorf("gitFor = %q, want %q", got.gitFor, target)
	}

	// and when the cursor does land on a card with no worktree, the git pane
	// lets go of the old directory instead of showing it under the card
	selectIssue(t, &got, "LAB-7")
	mm, _ = got.Update(ciMsg{})
	got = mm.(Model)
	if got.gitFor != "" {
		t.Errorf("gitFor = %q, want empty under a card with no worktree", got.gitFor)
	}
	if got.diffFor != "" || got.diffRaw != "" {
		t.Errorf("branch diff left over: diffFor=%q raw=%d bytes", got.diffFor, len(got.diffRaw))
	}
}

// TestCursorFollowsWorktreeIntoItsCard: at startup a worktree is listed on its
// own; once its card loads the two merge into one row. The cursor has to
// follow that row, not the index it used to sit at.
func TestCursorFollowsWorktreeIntoItsCard(t *testing.T) {
	m := newTestModel(t, 200)
	target := m.root + "/.worktrees/third"
	m.wts = append(m.wts, gitx.Worktree{Root: m.root, Path: target, Branch: "feature/LAB-3/third"})
	m.refreshRows()
	if !m.selectWorktree(target) {
		t.Fatal("no row for the third worktree")
	}

	mm, _ := m.Update(issuesMsg{issues: []linear.Issue{
		{Identifier: "LAB-5", Title: "Noise above", State: "In Progress", StateType: "started"},
		{Identifier: "LAB-3", Title: "The third worktree's card", State: "Todo", StateType: "unstarted"},
	}})
	got := mm.(Model)

	ref := got.selectedRef()
	if ref.wt == nil || ref.wt.Path != target {
		t.Fatalf("cursor left the worktree behind: %+v", ref)
	}
	if ref.issue == nil || ref.issue.Identifier != "LAB-3" {
		t.Errorf("cursor is not on the merged card row: kind=%d issue=%v", ref.kind, ref.issue)
	}
	if got.gitFor != target {
		t.Errorf("gitFor = %q, want %q", got.gitFor, target)
	}
}
