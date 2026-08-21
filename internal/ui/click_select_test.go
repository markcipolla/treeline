package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/gitx"
)

// refIndexOf finds the table row showing the given worktree path.
func refIndexOf(t *testing.T, m Model, path string) int {
	t.Helper()
	for i, ref := range m.refs {
		if ref.wt != nil && ref.wt.Path == path {
			return i
		}
	}
	t.Fatalf("no row for %s", path)
	return -1
}

// TestClickSelectsCardPanelLayout: in the panel layout a click on a card
// selects it — the cursor lands on the row under the pointer, not wherever it
// was parked — and the issues pane takes focus.
func TestClickSelectsCardPanelLayout(t *testing.T) {
	m := newTestModel(t, 200)
	if !m.threePane() {
		t.Fatal("want panel layout at 200 cols")
	}
	target := m.wts[1].Path
	idx := refIndexOf(t, m, target)
	z := awaitZone(t, m, "pane:issues")

	// the pane's title, its rule, the header and the header's rule sit
	// above the cards
	click := tea.MouseMsg{X: z.StartX + 2, Y: z.StartY + 4 + idx - m.gridTop(),
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	if got := m.rowUnderMouse(click); got != idx {
		t.Fatalf("rowUnderMouse = %d, want %d", got, idx)
	}

	mm, _ := m.Update(click)
	got := mm.(Model)
	if ref := got.selectedRef(); ref.wt == nil || ref.wt.Path != target {
		t.Errorf("selected %+v, want worktree %s", ref, target)
	}
	if got.pane != paneIssues {
		t.Errorf("pane = %d, want paneIssues", got.pane)
	}
}

// TestClickSelectsCardNarrowLayout: the boxed table on a narrow terminal is
// clickable too — only the frame's top edge sits above the header there.
func TestClickSelectsCardNarrowLayout(t *testing.T) {
	m := newTestModel(t, 80)
	if m.threePane() {
		t.Fatal("want narrow layout at 80 cols")
	}
	target := m.wts[1].Path
	idx := refIndexOf(t, m, target)
	z := awaitZone(t, m, "pane:issues")

	click := tea.MouseMsg{X: z.StartX + 2, Y: z.StartY + 3 + idx - m.gridTop(),
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	mm, _ := m.Update(click)
	got := mm.(Model)
	if ref := got.selectedRef(); ref.wt == nil || ref.wt.Path != target {
		t.Errorf("selected %+v, want worktree %s", ref, target)
	}
}

// TestClickOnHeaderKeepsSelection: group headers aren't cards; a click on one
// focuses the pane but leaves the cursor where it was.
func TestClickOnHeaderKeepsSelection(t *testing.T) {
	m := newTestModel(t, 200)
	if m.refs[0].kind != rowHeader {
		t.Fatal("want a group header as the first row")
	}
	m.table.SetCursor(2)
	z := awaitZone(t, m, "pane:issues")

	click := tea.MouseMsg{X: z.StartX + 2, Y: z.StartY + 4,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	if got := m.rowUnderMouse(click); got != -1 {
		t.Fatalf("rowUnderMouse = %d, want -1 for a header", got)
	}
	mm, _ := m.Update(click)
	got := mm.(Model)
	if got.table.Cursor() != 2 {
		t.Errorf("cursor = %d, want 2 (unchanged)", got.table.Cursor())
	}
}

// TestClickScrolledWindow: with more cards than the grid can show, the click
// mapping follows the same window the render used (gridTop), so a click deep
// in a scrolled list still lands on the card under the pointer.
func TestClickScrolledWindow(t *testing.T) {
	m := newTestModel(t, 200)
	for i := 3; i <= 40; i++ {
		m.wts = append(m.wts, gitx.Worktree{
			Root: m.root, Path: fmt.Sprintf("%s/.worktrees/wt%02d", m.root, i),
			Branch: fmt.Sprintf("feature/lab-%d-more", i),
		})
	}
	m.refreshRows()
	if len(m.refs) <= m.gridBody {
		t.Fatalf("list (%d rows) fits the grid (%d): the window never slides", len(m.refs), m.gridBody)
	}
	m.table.SetCursor(len(m.refs) - 1) // deep enough that gridTop > 0
	if m.gridTop() == 0 {
		t.Fatal("want a scrolled window")
	}
	target := m.wts[len(m.wts)-3].Path
	idx := refIndexOf(t, m, target)
	z := awaitZone(t, m, "pane:issues")

	click := tea.MouseMsg{X: z.StartX + 2, Y: z.StartY + 4 + idx - m.gridTop(),
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	mm, _ := m.Update(click)
	got := mm.(Model)
	if ref := got.selectedRef(); ref.wt == nil || ref.wt.Path != target {
		t.Errorf("selected %+v, want worktree %s", ref, target)
	}
}
