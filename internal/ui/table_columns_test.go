package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
)

// withIssues seeds cards with assignees and rebuilds the table.
func withIssues(m Model) Model {
	m.authed = true
	m.loadingWT, m.loadingIssues = false, false
	m.issues = []linear.Issue{
		{Identifier: "LAB-1", Title: "Add a column for the assignee", State: "In Progress", StateType: "started", Assignee: "Mark Cipolla", AssigneeIsMe: true, Priority: 2},
		{Identifier: "LAB-2", Title: "A card assigned to someone else", State: "Backlog", StateType: "backlog", Assignee: "Alexandra Kowalczyk"},
		{Identifier: "LAB-3", Title: "An unassigned card", State: "Backlog", StateType: "backlog"},
	}
	// LAB-2 is someone else's, so it only stays in the column because a
	// worktree holds it (see showIssue). The branch has to carry the key in
	// its own segment, the way branch.Name writes it.
	m.wts = append(m.wts, gitx.Worktree{
		Root:   m.root,
		Path:   m.root + "/.worktrees/lab-2",
		Branch: "feature/LAB-2/a-card-assigned-to-someone-else",
	})
	m.refreshRows()
	return m
}

func columnWidth(m Model, title string) (int, bool) {
	for _, c := range m.table.Columns() {
		if c.Title == title {
			return c.Width, true
		}
	}
	return 0, false
}

// TestAssigneeColumn: wide terminals carry an ASSIGNEE column showing the
// card's assignee, and unassigned cards leave it blank.
func TestAssigneeColumn(t *testing.T) {
	m := withIssues(newTestModel(t, 160))

	w, ok := columnWidth(m, "ASSIGNEE")
	if !ok {
		t.Fatal("no ASSIGNEE column")
	}
	if w <= 0 {
		t.Fatalf("ASSIGNEE hidden at 160 cols (width %d)", w)
	}

	var idx = -1
	for i, c := range m.table.Columns() {
		if c.Title == "ASSIGNEE" {
			idx = i
		}
	}
	want := map[string]string{"LAB-1": "Mark Cipolla", "LAB-2": "Alexandra Kowalczyk", "LAB-3": ""}
	seen := 0
	for _, row := range m.table.Rows() {
		if w, ok := want[row[0]]; ok {
			seen++
			if row[idx] != w {
				t.Errorf("%s assignee cell = %q, want %q", row[0], row[idx], w)
			}
		}
	}
	if seen != len(want) {
		t.Errorf("matched %d card rows, want %d", seen, len(want))
	}

	if v := m.View(); !strings.Contains(v, "ASSIGNEE") || !strings.Contains(v, "Mark Cipolla") {
		t.Error("rendered view is missing the assignee column")
	}
}

// TestAssigneeColumnDropsWhenNarrow: ASSIGNEE is the first column sacrificed
// on a narrow terminal, and hiding it must not push the frame past the edge.
func TestAssigneeColumnDropsWhenNarrow(t *testing.T) {
	for _, tc := range []struct {
		width   int
		visible bool
		// 179 is as wide as the stacked layout goes: past it the issues
		// list becomes a column of its own and sheds cells again, which
		// TestColumnLayoutFitsItsColumns covers
	}{{60, false}, {80, false}, {90, false}, {91, true}, {120, true}, {179, true}} {
		m := withIssues(newTestModel(t, tc.width))
		w, ok := columnWidth(m, "ASSIGNEE")
		if !ok {
			t.Fatalf("%d cols: no ASSIGNEE column", tc.width)
		}
		if (w > 0) != tc.visible {
			t.Errorf("%d cols: ASSIGNEE width %d, want visible=%v", tc.width, w, tc.visible)
		}
		// the widget and the frame drawing both skip zero-width columns, so
		// hiding one has to buy real space back. Minimum column widths
		// overflow toy terminals by design, same as TestViewSmoke assumes.
		if tc.width < 80 {
			continue
		}
		frame := 1
		for _, c := range m.table.Columns() {
			if c.Width > 0 {
				frame += c.Width + 3
			}
		}
		if frame > tc.width {
			t.Errorf("%d cols: table frame is %d wide", tc.width, frame)
		}
	}
}

// TestTableRowsMatchColumns guards renderRow, which indexes the column set
// per row cell and panics if a row carries more cells than there are columns.
func TestTableRowsMatchColumns(t *testing.T) {
	for _, width := range []int{60, 80, 91, 120, 179, 200, 260} {
		m := withIssues(newTestModel(t, width))
		// a spare worktree row and group headers, not just card rows
		m.refreshRows()
		cols := len(m.table.Columns())
		for i, row := range m.table.Rows() {
			if len(row) != cols {
				t.Fatalf("%d cols: row %d has %d cells, want %d", width, i, len(row), cols)
			}
		}
		mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		if got := len(mm.(Model).table.Columns()); got != cols {
			t.Fatalf("%d cols: column count changed to %d after resize", width, got)
		}
	}
}
