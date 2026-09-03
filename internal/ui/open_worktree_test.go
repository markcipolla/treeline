package ui

import (
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/gitx"
)

// newTestModel builds a model sized to the requested width with two
// worktrees already listed.
func newTestModel(t *testing.T, width int) Model {
	t.Helper()
	startTerm = func(dir string, cols, rows int, persist bool) (*agentSession, error) {
		return nil, errors.New("agent sessions disabled in tests")
	}
	startShell = func(dir string, cols, rows int, persist bool, kind string) (*agentSession, error) {
		return startTerm(dir, cols, rows, persist)
	}

	root := t.TempDir()
	m := New(&config.Config{BranchTypes: []string{"feature"}, SlugMaxLen: 48}, root)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	model := mm.(Model)
	model.loadingWT, model.loadingIssues = false, false
	model.wts = []gitx.Worktree{
		{Root: root, Path: root + "/.worktrees/first", Branch: "feature/lab-1-first"},
		{Root: root, Path: root + "/.worktrees/second", Branch: "feature/lab-2-second"},
	}
	model.refreshRows()
	return model
}

// TestOpenWorktreePanelLayout: in the panel layout opening a worktree works it
// in place — it selects the row and focuses the agent pane instead of exiting.
func TestOpenWorktreePanelLayout(t *testing.T) {
	m := newTestModel(t, 200)
	if !m.threePane() {
		t.Fatal("want panel layout at 200 cols")
	}
	target := m.wts[1].Path

	mm, _ := m.openWorktree(target)
	got := mm.(Model)

	if got.JumpPath() != "" {
		t.Errorf("opened in the panel but still set a jump path: %q", got.JumpPath())
	}
	if got.screen != scrMain {
		t.Errorf("screen = %v, want scrMain", got.screen)
	}
	if got.pane != paneAgent {
		t.Errorf("pane = %d, want paneAgent (%d)", got.pane, paneAgent)
	}
	if p := got.agentDir(); p != target {
		t.Errorf("agentDir = %q, want %q", p, target)
	}
}

// TestOpenWorktreeNarrowLayout: without room for the panels, opening a
// worktree still exits so the shell wrapper can cd into it.
func TestOpenWorktreeNarrowLayout(t *testing.T) {
	m := newTestModel(t, 80)
	if m.threePane() {
		t.Fatal("want narrow layout at 80 cols")
	}
	target := m.wts[1].Path

	mm, _ := m.openWorktree(target)
	got := mm.(Model)

	if got.JumpPath() != target {
		t.Errorf("JumpPath = %q, want %q", got.JumpPath(), target)
	}
}

// TestOpenWorktreeBeforeListRefresh covers "jump in" straight off the created
// screen, while loadWorktrees is still in flight: the panes must not latch
// onto the wrong worktree, and the selection must land once the list arrives.
func TestOpenWorktreeBeforeListRefresh(t *testing.T) {
	m := newTestModel(t, 200)
	fresh := gitx.Worktree{Root: m.root, Path: m.root + "/.worktrees/third", Branch: "feature/lab-3-third"}

	mm, _ := m.openWorktree(fresh.Path)
	pending := mm.(Model)

	if pending.pendSelect != fresh.Path {
		t.Fatalf("pendSelect = %q, want %q", pending.pendSelect, fresh.Path)
	}
	if got := pending.agentDir(); got == m.wts[0].Path || got == m.wts[1].Path {
		t.Errorf("agentDir latched onto an unrelated worktree: %q", got)
	}

	// the reload that createdMsg kicked off finally lands
	mm, _ = pending.Update(worktreesMsg{wts: append(pending.wts, fresh)})
	got := mm.(Model)

	if got.pendSelect != "" {
		t.Errorf("pendSelect = %q, want cleared", got.pendSelect)
	}
	if p := got.agentDir(); p != fresh.Path {
		t.Errorf("agentDir = %q, want %q", p, fresh.Path)
	}
	if got.pane != paneAgent {
		t.Errorf("pane = %d, want paneAgent (%d)", got.pane, paneAgent)
	}
	if got.JumpPath() != "" {
		t.Errorf("unexpected jump path %q", got.JumpPath())
	}
}

// TestCreatedScreenEntryPoints drives the created screen the way a user does —
// the enter key and a click on "jump in" — rather than calling openWorktree
// directly, so a call site reverting to jumpTo (exiting the app instead of
// opening the worktree in the panel) is caught.
func TestCreatedScreenEntryPoints(t *testing.T) {
	setup := func(t *testing.T) Model {
		m := newTestModel(t, 200)
		m.screen = scrCreated
		m.createdPath = m.wts[1].Path
		m.createdBranch = "feature/lab-2-second"
		return m
	}

	t.Run("enter", func(t *testing.T) {
		m := setup(t)
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := mm.(Model)
		if got.JumpPath() != "" {
			t.Errorf("enter exited the app with jump path %q", got.JumpPath())
		}
		if got.screen != scrMain || got.pane != paneAgent {
			t.Errorf("screen=%v pane=%d, want scrMain and paneAgent", got.screen, got.pane)
		}
		if p := got.agentDir(); p != m.createdPath {
			t.Errorf("agentDir = %q, want %q", p, m.createdPath)
		}
	})

	t.Run("jump in button", func(t *testing.T) {
		m := setup(t)
		z := awaitZone(t, m, "btn:jump")
		// handleClick only runs on release, the way a real click arrives
		click := tea.MouseMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
		if !m.clicked(click, "btn:jump") {
			t.Fatalf("synthesized click at %d,%d misses btn:jump", click.X, click.Y)
		}
		mm, _ := m.Update(click)
		got := mm.(Model)
		if got.JumpPath() != "" {
			t.Errorf("click exited the app with jump path %q", got.JumpPath())
		}
		if got.screen != scrMain || got.pane != paneAgent {
			t.Errorf("screen=%v pane=%d, want scrMain and paneAgent", got.screen, got.pane)
		}
	})

	t.Run("esc goes back without opening", func(t *testing.T) {
		m := setup(t)
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		got := mm.(Model)
		if got.screen != scrMain {
			t.Errorf("screen = %v, want scrMain", got.screen)
		}
		if got.JumpPath() != "" || got.pendSelect != "" {
			t.Errorf("esc opened a worktree: jump=%q pend=%q", got.JumpPath(), got.pendSelect)
		}
	})
}

// awaitZone renders the model and waits for the button's bounds. Scan hands
// zones to a background goroutine, so Get races with the render it follows —
// the running app never notices because View is called every frame.
func awaitZone(t *testing.T, m Model, id string) *zone.ZoneInfo {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if v := m.View(); v == "" {
			t.Fatal("empty view")
		}
		if z := m.zones.Get(id); z != nil && !z.IsZero() {
			return z
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never got bounds", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// awaitZoneMoved waits until a zone's bounds have left a stale column. Right
// after a layout change awaitZone can answer with where the previous frame put
// the zone — clicking there would miss — so a test that moved a zone waits for
// the zone manager to catch up with the move.
func awaitZoneMoved(t *testing.T, m Model, id string, staleX int) *zone.ZoneInfo {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if v := m.View(); v == "" {
			t.Fatal("empty view")
		}
		if z := m.zones.Get(id); z != nil && !z.IsZero() && z.StartX != staleX {
			return z
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never moved off column %d", id, staleX)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
