package ui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The capture/apply mechanics live with the pane (internal/ide); these tests
// cover treeline's side: the snapshot riding the ui state file and a fresh
// launch feeding it back through the worktree sync.

// stateTestModel is ideTestModel worked into a mid-session shape: a folder
// unfolded, a dirty buffer, a second tab, the ide pane focused.
func stateTestModel(t *testing.T) Model {
	t.Helper()
	m := ideTestModel(t, 200)
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // row 0 is sub/: unfold it
	_ = m.ideReady().OpenFile("main.go")
	m = keyIDE(t, m, runes("e"))
	m = typeIDE(t, m, "zz")
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // dirty now
	_ = m.ideReady().OpenFile(filepath.Join("sub", "util.go"))
	mm, _ := m.focusPane(paneIDE)
	return mm.(Model)
}

// relaunch builds a fresh model over the same repo — as a new process would —
// and feeds it the snapshot plus the first worktree list.
func relaunch(t *testing.T, m Model, s uiState) Model {
	t.Helper()
	m2 := New(m.cfg, m.root)
	mm, _ := m2.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m2 = mm.(Model)
	m2.restore = &s
	mm, _ = m2.Update(worktreesMsg{wts: m.wts})
	return mm.(Model)
}

// TestUIStateSaveLoad: the snapshot survives the disk round trip.
func TestUIStateSaveLoad(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := stateTestModel(t)
	m.saveUIState()
	got := loadUIState(m.root)
	if got == nil {
		t.Fatal("no state came back")
	}
	if got.Selected != m.wts[0].Path || got.Pane != paneIDE {
		t.Errorf("selected %q pane %d, want %q %d", got.Selected, got.Pane, m.wts[0].Path, paneIDE)
	}
	if len(got.IDE.Bufs) != 2 || !got.IDE.Bufs[0].Dirty || got.IDE.Bufs[1].Dirty {
		t.Fatalf("bufs = %+v, want main.go dirty and util.go clean", got.IDE.Bufs)
	}
	if !strings.HasPrefix(got.IDE.Bufs[0].Val, "zz") {
		t.Errorf("the dirty text should be persisted, got %q", got.IDE.Bufs[0].Val)
	}
}

// TestUIStateRestoresPlace: a fresh launch lands where the last one was —
// row, pane, unfolded folders, tabs, and the unsaved edit still unsaved.
func TestUIStateRestoresPlace(t *testing.T) {
	m := stateTestModel(t)
	m2 := relaunch(t, m, m.captureUIState())

	if ref := m2.selectedRef(); ref.wt == nil || ref.wt.Path != m.wts[0].Path {
		t.Fatalf("selected %+v, want %s", ref, m.wts[0].Path)
	}
	if m2.pane != paneIDE {
		t.Errorf("pane = %d, want paneIDE", m2.pane)
	}
	got := m2.ide.Capture()
	if !slices.Contains(got.Expanded, "sub") {
		t.Error("sub/ should come back unfolded")
	}
	if tabs := m2.ide.Tabs(); len(tabs) != 2 || tabs[0] != "main.go" ||
		tabs[1] != filepath.Join("sub", "util.go") {
		t.Fatalf("tabs = %v, want main.go and sub/util.go", tabs)
	}
	if got.Cur != 1 {
		t.Errorf("active tab = %d, want 1 (util.go)", got.Cur)
	}
	if !got.Bufs[0].Dirty || !strings.HasPrefix(got.Bufs[0].Val, "zz") {
		t.Errorf("main.go should be dirty with its edits: dirty=%v val=%q",
			got.Bufs[0].Dirty, got.Bufs[0].Val)
	}
}

// TestUIStateStale: disk moved on under the persisted edits — the buffer
// comes back dirty, its edits intact. (The stale marking itself is the pane's
// business, tested in internal/ide.)
func TestUIStateStale(t *testing.T) {
	m := stateTestModel(t)
	s := m.captureUIState()
	writeIDEFile(t, m.wts[0].Path, "main.go", "package main // rewritten\n")

	m2 := relaunch(t, m, s)
	got := m2.ide.Capture()
	if len(got.Bufs) == 0 || !got.Bufs[0].Dirty || !strings.HasPrefix(got.Bufs[0].Val, "zz") {
		t.Errorf("bufs = %+v, want main.go still dirty with its edits", got.Bufs)
	}
}

// TestUIStateGoneWorktree: state naming a worktree that no longer exists
// restores nothing and breaks nothing.
func TestUIStateGoneWorktree(t *testing.T) {
	m := stateTestModel(t)
	s := m.captureUIState()
	s.Selected = m.root + "/.worktrees/vanished"
	s.IDE.For = s.Selected

	m2 := relaunch(t, m, s)
	if tabs := m2.ide.Tabs(); len(tabs) != 0 {
		t.Errorf("no tabs should be restored for a vanished worktree, got %d", len(tabs))
	}
}
