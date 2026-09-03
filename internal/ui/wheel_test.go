package ui

import (
	"io"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
)

// fakeSession is a agentSession with a virtual terminal but no process, so
// the pane plumbing can be exercised without spawning agent.
func fakeSession(dir string, cols, rows int) *agentSession {
	s := &agentSession{dir: dir, em: vt.NewEmulator(cols, rows), cols: cols, rows: rows}
	s.trackMouseModes()
	// drain emulator replies (mouse reports, DA responses) the way the real
	// session pumps them into the pty; unread they block on the pipe
	go func() { _, _ = io.Copy(io.Discard, s.em) }()
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
// pointer is over. Needing to focus the agent pane first (ctrl+q) before its
// scrollback would move made the panel feel dead under the mouse.
func TestWheelScrollsPaneUnderPointer(t *testing.T) {
	m := withIssues(newTestModel(t, 160))
	m.height = 44
	m.resize()
	if !m.selectWorktree(m.wts[1].Path) {
		t.Fatal("no row for the second worktree")
	}
	dir := m.agentDir()
	agent, shell := fakeSession(dir, 40, 10), fakeSession(dir, 40, 10)
	m.terms[dir] = agent
	m.termTabs[dir] = []*termTab{{kind: "shell", sess: shell}}

	// focus stays on the issues list the whole time
	if m.pane != paneIssues {
		t.Fatalf("pane = %d, want paneIssues", m.pane)
	}
	mm, _ := m.Update(wheel(t, m, "pane:agent", true))
	got := mm.(Model)
	if agent.scrolled() == 0 {
		t.Error("wheel over the agent pane did not scroll its scrollback")
	}
	if got.pane != paneIssues {
		t.Errorf("the wheel stole focus: pane = %d", got.pane)
	}
	if c := got.table.Cursor(); c != m.table.Cursor() {
		t.Errorf("the wheel moved the card cursor to %d", c)
	}

	// and back down to live
	mm, _ = got.Update(wheel(t, got, "pane:agent", false))
	if agent.scrolled() != 0 {
		t.Errorf("wheel down left the pane %d lines back", agent.scrolled())
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
	dir := m.agentDir()
	agent := fakeSession(dir, 40, 10)
	m.terms[dir] = agent
	mm, _ := m.focusPane(paneAgent)
	m = mm.(Model)

	// a wheel event nowhere near a pane
	m.Update(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if agent.scrolled() == 0 {
		t.Error("wheel outside the panels did not reach the focused agent pane")
	}
}

// TestWheelDeclinedWithoutMouseMode: full-screen alone is not enough — a
// program that never asked for mouse events (less, a plain vim) can't use
// the wheel, so sendWheel must decline rather than eat the event, leaving
// the caller free to fall back to the pane's own scrollback.
func TestWheelDeclinedWithoutMouseMode(t *testing.T) {
	s := fakeSession(t.TempDir(), 40, 10)
	s.em.Write([]byte("\x1b[?1049h"))
	if s.sendWheel(true, 5, 3) {
		t.Error("sendWheel claimed a wheel event the program never asked for")
	}
	s.em.Write([]byte("\x1b[?1000h"))
	if !s.sendWheel(true, 5, 3) {
		t.Error("sendWheel declined after the program enabled mouse reporting")
	}
	s.em.Write([]byte("\x1b[?1000l"))
	if s.sendWheel(true, 5, 3) {
		t.Error("sendWheel kept claiming after mouse reporting was disabled")
	}
}
