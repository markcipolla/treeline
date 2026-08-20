package ui

import (
	"path/filepath"

	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
)

// columnIndex locates a column in the current set.
func columnIndex(m Model, title string) int {
	for i, c := range m.table.Columns() {
		if c.Title == title {
			return i
		}
	}
	return -1
}

// withSecondRepo registers another repo and gives it a worktree, the way a
// second entry in the settings registry does.
func withSecondRepo(t *testing.T, m Model) Model {
	t.Helper()
	other := t.TempDir()
	m.repos = append(m.repos, repoEntry{name: "sidecar", path: other, base: "main"})
	m.wts = append(m.wts, gitx.Worktree{
		Root: other, Path: other + "/.worktrees/lab-6", Branch: "feature/LAB-6/sidecar-work",
	})
	m.resize() // the column set depends on how many repos are in play
	m.refreshRows()
	return m
}

// TestRepoColumnOnlyWithSeveralRepos: one repo means every row would say the
// same thing, so the column stays out of the set entirely.
func TestRepoColumnOnlyWithSeveralRepos(t *testing.T) {
	m := withIssues(newTestModel(t, 200))
	if m.multiRepo() {
		t.Fatal("a fresh model should hold a single repo")
	}
	if i := columnIndex(m, "REPO"); i >= 0 {
		t.Errorf("REPO column present at index %d with one repo", i)
	}
	if strings.Contains(m.View(), "REPO") {
		t.Error("rendered view shows a REPO header with one repo")
	}

	m = withSecondRepo(t, m)
	if !m.multiRepo() {
		t.Fatal("two repos registered but multiRepo() is false")
	}
	i := columnIndex(m, "REPO")
	if i < 0 {
		t.Fatal("no REPO column with two repos")
	}
	if w := m.table.Columns()[i].Width; w <= 0 {
		t.Fatalf("REPO hidden at 200 cols (width %d)", w)
	}
	if !strings.Contains(m.View(), "REPO") {
		t.Error("rendered view is missing the REPO header")
	}
}

// TestRepoColumnCells: worktree rows name their repo, cards inherit it from
// the worktree they are linked to, and cards with no worktree stay blank.
func TestRepoColumnCells(t *testing.T) {
	m := newTestModel(t, 200)
	m.authed = true
	m.issues = []linear.Issue{
		{Identifier: "LAB-6", Title: "Sidecar work", State: "In Progress", StateType: "started"},
		{Identifier: "LAB-7", Title: "Nothing checked out", State: "Todo", StateType: "unstarted"},
	}
	m = withSecondRepo(t, m)

	repo, wtCol := columnIndex(m, "REPO"), columnIndex(m, "WORKTREE")
	if repo < 0 || wtCol < 0 {
		t.Fatal("missing REPO or WORKTREE column")
	}
	primary := filepath.Base(m.root)

	cell := func(key string) []string {
		for i, ref := range m.refs {
			if (ref.issue != nil && ref.issue.Identifier == key) ||
				(ref.kind == rowWorktree && ref.wt != nil && strings.Contains(ref.wt.Branch, key)) {
				return m.table.Rows()[i]
			}
		}
		t.Fatalf("no row for %s", key)
		return nil
	}

	// the card linked to the sidecar repo's worktree
	if got := cell("LAB-6")[repo]; got != "sidecar" {
		t.Errorf("LAB-6 repo cell = %q, want %q", got, "sidecar")
	}
	// its worktree path no longer needs the repo prefix the column now carries
	if got := cell("LAB-6")[wtCol]; got != ".worktrees/lab-6" {
		t.Errorf("LAB-6 worktree cell = %q, want %q", got, ".worktrees/lab-6")
	}
	// a card with no worktree has no repo yet
	if got := cell("LAB-7")[repo]; got != "" {
		t.Errorf("LAB-7 repo cell = %q, want empty", got)
	}
	// spare worktrees of the launched repo name it too
	if got := cell("lab-1")[repo]; got != primary {
		t.Errorf("spare worktree repo cell = %q, want %q", got, primary)
	}
}

// TestRepoColumnDropsWhenNarrow: which repo a branch lives in matters more
// than who it is assigned to, so ASSIGNEE goes first and REPO only after it.
// The row cells must keep matching the column set at every width, and a
// hidden REPO hands the repo name back to the worktree cell.
func TestRepoColumnDropsWhenNarrow(t *testing.T) {
	for _, width := range []int{60, 80, 91, 103, 104, 120, 200} {
		m := withSecondRepo(t, withIssues(newTestModel(t, width)))
		asg, repo := columnIndex(m, "ASSIGNEE"), columnIndex(m, "REPO")
		if asg < 0 || repo < 0 {
			t.Fatalf("%d cols: column set is missing ASSIGNEE or REPO", width)
		}
		aw, rw := m.table.Columns()[asg].Width, m.table.Columns()[repo].Width
		if aw > 0 && rw == 0 {
			t.Errorf("%d cols: ASSIGNEE kept (%d) while REPO was dropped", width, aw)
		}
		// the sidecar worktree must stay identifiable either way
		wtCell := ""
		for i, ref := range m.refs {
			if ref.wt != nil && strings.Contains(ref.wt.Branch, "LAB-6") {
				wtCell = m.table.Rows()[i][columnIndex(m, "WORKTREE")]
			}
		}
		if wtCell == "" {
			t.Fatalf("%d cols: no row for the sidecar worktree", width)
		}
		if named := strings.HasPrefix(wtCell, "sidecar:"); named == (rw > 0) {
			t.Errorf("%d cols: worktree cell %q with REPO width %d", width, wtCell, rw)
		}
		cols := len(m.table.Columns())
		for i, row := range m.table.Rows() {
			if len(row) != cols {
				t.Fatalf("%d cols: row %d has %d cells, want %d", width, i, len(row), cols)
			}
		}
		if width < 80 {
			continue // minimum column widths overflow toy terminals by design
		}
		frame := 1
		for _, c := range m.table.Columns() {
			if c.Width > 0 {
				frame += c.Width + 3
			}
		}
		if frame > width {
			t.Errorf("%d cols: table frame is %d wide", width, frame)
		}
		for n, line := range strings.Split(m.View(), "\n") {
			if lw := lipgloss.Width(line); lw > width {
				t.Errorf("%d cols: line %d overflows at %d", width, n, lw)
			}
		}
	}
}
