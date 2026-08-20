package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// plainRows renders a view and strips it to plain, untrimmed lines.
func plainRows(v string) []string {
	rows := strings.Split(v, "\n")
	for i, r := range rows {
		rows[i] = ansiRE.ReplaceAllString(r, "")
	}
	return rows
}

// panelRows is the rows of the panel itself, from its top border to its
// bottom one, with the doc padding trimmed off each end.
func panelRows(t *testing.T, m Model) []string {
	t.Helper()
	var out []string
	for _, r := range plainRows(m.View()) {
		r = strings.TrimSpace(r)
		if len(out) == 0 && !strings.HasPrefix(r, "╭") {
			continue
		}
		out = append(out, r)
		if len(out) > 1 && strings.HasPrefix(r, "╰") {
			break
		}
	}
	if len(out) == 0 {
		t.Fatalf("no panel in:\n%s", m.View())
	}
	return out
}

// TestFrameSharesBorders: neighbouring panes share the line between them. Two
// boxes pushed together read as a doubled seam, which is what the frame exists
// to avoid — at four columns that would be three seams of wasted width.
func TestFrameSharesBorders(t *testing.T) {
	for _, width := range []int{110, 160, 200, 260, 320} {
		m := withIssues(newTestModel(t, width))
		m.height = 34
		m.resize()
		if !m.selectWorktree(m.wts[1].Path) {
			t.Fatal("no row for the second worktree")
		}
		for _, pane := range []int{paneIssues, paneClaude, paneDiff, paneTerm} {
			mm, _ := m.focusPane(pane)
			for n, row := range panelRows(t, mm.(Model)) {
				if strings.Contains(row, "││") {
					t.Errorf("%d cols pane %d: doubled seam on row %d: %q",
						width, pane, n, row)
				}
			}
		}
	}
}

// TestFrameJunctions: where lines meet, the glyph has to say so. A ╮ left in
// the middle of the top border (or a ╡ mid-rule) is the tell-tale of boxes
// merely placed side by side.
func TestFrameJunctions(t *testing.T) {
	m := withIssues(newTestModel(t, 260))
	m.height = 34
	m.resize()
	if !m.selectWorktree(m.wts[1].Path) {
		t.Fatal("no row for the second worktree")
	}
	rows := panelRows(t, m)
	top, bottom := rows[0], rows[len(rows)-1]

	// four columns, so three seams meeting the top and bottom borders
	if got := strings.Count(top, "┬"); got != 3 {
		t.Errorf("top border has %d ┬ joins, want 3: %q", got, top)
	}
	if strings.Contains(top[1:len(top)-len("╮")], "╮") {
		t.Errorf("a pane corner is stranded in the top border: %q", top)
	}
	if got := strings.Count(bottom, "┴"); got < 3 {
		t.Errorf("bottom border has %d ┴ joins, want at least 3: %q", got, bottom)
	}

	// the title rules of neighbouring panes meet with ╪, and the frame's own
	// edges close them off with ╞ and ╡
	rule := rows[2]
	if got := strings.Count(rule, "╪"); got != 3 {
		t.Errorf("title rule has %d ╪ joins, want 3: %q", got, rule)
	}
	if !strings.HasPrefix(rule, "╞") || !strings.HasSuffix(rule, "╡") {
		t.Errorf("title rule does not meet the frame's edges: %q", rule)
	}
}

// TestFrameStackedSeam: in the stacked layout the issues strip's bottom border
// is also the top of the panes below, so the grid's column joins (┴) and the
// seam starting below it (┬) share one row.
func TestFrameStackedSeam(t *testing.T) {
	m := withIssues(newTestModel(t, 160))
	m.height = 34
	m.resize()
	rows := panelRows(t, m)

	seam := ""
	for _, r := range rows {
		if strings.HasPrefix(r, "├") && strings.Contains(r, "┬") && strings.Contains(r, "┴") {
			seam = r
			break
		}
	}
	if seam == "" {
		t.Fatalf("no band seam carrying both ┴ and ┬ in:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.HasSuffix(seam, "┤") {
		t.Errorf("the band seam does not close on the frame's edge: %q", seam)
	}
	if strings.Contains(seam, "╰") || strings.Contains(seam, "╭") {
		t.Errorf("pane corners stranded in the band seam: %q", seam)
	}
}

// TestFrameRowsAreRectangular: every row of the frame is exactly as wide as
// its borders, whatever the panes put inside. A pane that renders something
// too wide must be cut, not left to shear the columns below it.
func TestFrameRowsAreRectangular(t *testing.T) {
	for _, width := range []int{110, 180, 240, 300} {
		m := withIssues(newTestModel(t, width))
		m.height = 30
		m.resize()
		if !m.selectWorktree(m.wts[1].Path) {
			t.Fatal("no row for the second worktree")
		}
		// a body far too wide and too tall for any pane
		m.terms[m.claudeDir()] = fakeSession(m.claudeDir(), width*2, 200)

		rows := panelRows(t, m)
		want := lipgloss.Width(rows[0])
		for n, row := range rows {
			if got := lipgloss.Width(row); got != want {
				t.Fatalf("%d cols: row %d is %d wide, want %d: %q",
					width, n, got, want, row)
			}
		}
	}
}
