package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
)

// fakeSession is a claudeSession with a virtual terminal but no process, so
// the pane plumbing can be exercised without spawning claude.
func fakeSession(dir string, cols, rows int) *claudeSession {
	s := &claudeSession{dir: dir, em: vt.NewEmulator(cols, rows), cols: cols, rows: rows}
	for i := 0; i < rows*3; i++ {
		s.em.Write([]byte("a line of output\r\n"))
	}
	return s
}

// wheel builds a wheel event over the middle of a pane's zone.
func wheel(t *testing.T, m Model, id string, up bool) tea.MouseMsg {
	t.Helper()
	z := awaitZone(t, m, id)
	btn := tea.MouseButtonWheelDown
	if up {
		btn = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{
		X: z.StartX + 2, Y: z.StartY + 3,
		Button: btn, Action: tea.MouseActionPress,
	}
}

// TestWheelScrollsPaneUnderPointer: the wheel works whichever pane the
// pointer is over. Needing to focus the claude pane first (ctrl+q) before its
// scrollback would move made the panel feel dead under the mouse.
func TestWheelScrollsPaneUnderPointer(t *testing.T) {
	m := withIssues(newTestModel(t, 160))
	m.height = 44
	m.resize()
	if !m.selectWorktree(m.wts[1].Path) {
		t.Fatal("no row for the second worktree")
	}
	dir := m.claudeDir()
	claude, shell := fakeSession(dir, 40, 10), fakeSession(dir, 40, 10)
	m.terms[dir], m.shells[dir] = claude, shell

	// focus stays on the issues list the whole time
	if m.pane != paneIssues {
		t.Fatalf("pane = %d, want paneIssues", m.pane)
	}
	mm, _ := m.Update(wheel(t, m, "pane:claude", true))
	got := mm.(Model)
	if claude.scrolled() == 0 {
		t.Error("wheel over the claude pane did not scroll its scrollback")
	}
	if got.pane != paneIssues {
		t.Errorf("the wheel stole focus: pane = %d", got.pane)
	}
	if c := got.table.Cursor(); c != m.table.Cursor() {
		t.Errorf("the wheel moved the card cursor to %d", c)
	}

	// and back down to live
	mm, _ = got.Update(wheel(t, got, "pane:claude", false))
	if claude.scrolled() != 0 {
		t.Errorf("wheel down left the pane %d lines back", claude.scrolled())
	}

	// the shell pane below the git pane scrolls the same way
	mm, _ = got.Update(wheel(t, got, "pane:term", true))
	if shell.scrolled() == 0 {
		t.Error("wheel over the shell pane did not scroll it")
	}
	_ = mm

	// over the issues list it still steps the cards (the cursor is on the
	// last row, so up is the direction with somewhere to go)
	before := got.table.Cursor()
	mm, _ = got.Update(wheel(t, got, "pane:issues", true))
	if after := mm.(Model).table.Cursor(); after == before {
		t.Error("wheel over the issues list did not move the cursor")
	}
}

// TestWheelFallsBackToFocusedPane: outside the panels (or with no zone hit)
// the wheel still works the focused pane, as it always did.
func TestWheelFallsBackToFocusedPane(t *testing.T) {
	m := withIssues(newTestModel(t, 160))
	m.height = 44
	m.resize()
	if !m.selectWorktree(m.wts[1].Path) {
		t.Fatal("no row for the second worktree")
	}
	dir := m.claudeDir()
	claude := fakeSession(dir, 40, 10)
	m.terms[dir] = claude
	mm, _ := m.focusPane(paneClaude)
	m = mm.(Model)

	// a wheel event nowhere near a pane
	m.Update(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if claude.scrolled() == 0 {
		t.Error("wheel outside the panels did not reach the focused claude pane")
	}
}
