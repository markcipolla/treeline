package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// stateTestModel is ideTestModel worked into a mid-session shape: a folder
// unfolded, a dirty buffer, a second tab, the ide pane focused.
func stateTestModel(t *testing.T) Model {
	t.Helper()
	m := ideTestModel(t, 200)
	m.ideExpanded["sub"] = true
	m.refreshIDETree()
	_ = m.openIDEFile("main.go")
	m = keyIDE(t, m, runes("e"))
	m = typeIDE(t, m, "zz")
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // dirty now
	_ = m.openIDEFile(filepath.Join("sub", "util.go"))
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
	if !m2.ideExpanded["sub"] {
		t.Error("sub/ should come back unfolded")
	}
	if len(m2.ideBufs) != 2 || m2.ideBufs[0].rel != "main.go" ||
		m2.ideBufs[1].rel != filepath.Join("sub", "util.go") {
		t.Fatalf("tabs = %+v, want main.go and sub/util.go", m2.ideBufs)
	}
	if m2.ideCur != 1 {
		t.Errorf("active tab = %d, want 1 (util.go)", m2.ideCur)
	}
	b := m2.ideBufs[0]
	if !b.dirty || !strings.HasPrefix(b.val, "zz") || b.stale {
		t.Errorf("main.go should be dirty with its edits, not stale: dirty=%v stale=%v val=%q",
			b.dirty, b.stale, b.val)
	}
	if !strings.HasPrefix(b.hl[0], "\x1b") && !strings.Contains(b.hl[0], "zz") {
		t.Errorf("the restored buffer should be highlighted, got %q", b.hl[0])
	}
}

// TestUIStateStale: disk moved on under the persisted edits — the buffer
// comes back dirty and marked stale, the way an in-session disk change would.
func TestUIStateStale(t *testing.T) {
	m := stateTestModel(t)
	s := m.captureUIState()
	writeIDEFile(t, m.wts[0].Path, "main.go", "package main // rewritten\n")

	m2 := relaunch(t, m, s)
	b := m2.ideBufs[0]
	if !b.dirty || !b.stale {
		t.Errorf("dirty=%v stale=%v, want both after the disk changed", b.dirty, b.stale)
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
	if len(m2.ideBufs) != 0 {
		t.Errorf("no tabs should be restored for a vanished worktree, got %d", len(m2.ideBufs))
	}
}
