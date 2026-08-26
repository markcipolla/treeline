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

// panelRows is the rows of the panel itself, from its first top border to its
// last bottom one, with the doc padding trimmed off each end.
func panelRows(t *testing.T, m Model) []string {
	t.Helper()
	var out []string
	for _, r := range plainRows(m.View()) {
		r = strings.TrimSpace(r)
		if len(out) == 0 && !strings.HasPrefix(r, "╭") {
			continue
		}
		// the panel ends where the rows stop starting with a border glyph
		if len(out) > 0 && !strings.ContainsRune("╭│├╞╰", []rune(r + " ")[0]) {
			break
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		t.Fatalf("no panel in:\n%s", m.View())
	}
	return out
}

// TestFrameBoxesAreClosed: every pane is its own box. Panes sit flush, so the
// seam between two of them is two lines thick — what must not happen is one
// pane's border merging into the next, which is how a rule inside the issues
// grid ends up T-joining the pane beside it.
func TestFrameBoxesAreClosed(t *testing.T) {
	for _, width := range []int{110, 160, 200, 260, 320} {
		m := withIssues(newTestModel(t, width))
		m.height = 34
		m.resize()
		if !m.selectWorktree(m.wts[1].Path) {
			t.Fatal("no row for the second worktree")
		}
		for _, pane := range []int{paneIssues, paneClaude, paneIDE, paneDiff, paneTerm} {
			mm, _ := m.focusPane(pane)
			rows := panelRows(t, mm.(Model))

			// ╪ only appears where two panes' title rules run into each
			// other, which is exactly what closed boxes prevent
			for n, row := range rows {
				if strings.Contains(row, "╪") {
					t.Errorf("%d cols pane %d: shared rule join on row %d: %q",
						width, pane, n, row)
				}
			}
			// each top border row opens and closes as many boxes as it draws
			top := rows[0]
			if o, c := strings.Count(top, "╭"), strings.Count(top, "╮"); o != c || o == 0 {
				t.Errorf("%d cols pane %d: top border opens %d boxes and closes %d: %q",
					width, pane, o, c, top)
			}
			// ...and no pane's rule reaches past its own box
			for n, row := range rows {
				o, c := strings.Count(row, "╞"), strings.Count(row, "╡")
				if o != c {
					t.Errorf("%d cols pane %d: row %d has %d ╞ and %d ╡: %q",
						width, pane, n, o, c, row)
				}
			}
		}
	}
}

// TestFrameSeamsAreTwoLines: neighbouring panes each keep their own edge, so a
// content row of the column layouts crosses a "││" seam per boundary.
func TestFrameSeamsAreTwoLines(t *testing.T) {
	for _, tc := range []struct{ width, seams int }{
		{200, 3}, // issues │ claude │ ide │ git-over-shell
		{300, 4}, // issues │ claude │ ide │ git │ shell
	} {
		m := withIssues(newTestModel(t, tc.width))
		m.height = 34
		m.resize()
		if !m.selectWorktree(m.wts[1].Path) {
			t.Fatal("no row for the second worktree")
		}
		rows := panelRows(t, m)
		// the title row: every pane has one, so every seam shows on it
		if got := strings.Count(rows[1], "││"); got != tc.seams {
			t.Errorf("%d cols: title row has %d seams, want %d: %q",
				tc.width, got, tc.seams, rows[1])
		}
	}
}

// TestFrameGridJoinsItsOwnBox: the issues grid's columns still meet the pane's
// bottom border with ┴, and its group dividers close on the pane's own edge —
// the grid is that pane, and only that pane.
func TestFrameGridJoinsItsOwnBox(t *testing.T) {
	m := withIssues(newTestModel(t, 160))
	m.height = 34
	m.resize()
	rows := panelRows(t, m)

	var divider, bottom string
	for _, r := range rows {
		if strings.HasPrefix(r, "├─ ") && divider == "" {
			divider = r
		}
		if strings.HasPrefix(r, "╰") && strings.Contains(r, "┴") {
			bottom = r
			break
		}
	}
	if divider == "" {
		t.Fatalf("no group divider in:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.HasSuffix(divider, "┤") {
		t.Errorf("a group divider does not close on the pane's edge: %q", divider)
	}
	if bottom == "" {
		t.Fatalf("the issues pane's bottom border carries no column joins:\n%s",
			strings.Join(rows, "\n"))
	}
	if !strings.HasSuffix(bottom, "╯") {
		t.Errorf("the issues pane's bottom border is not closed: %q", bottom)
	}
}

// TestFrameRowsAreRectangular: every row of the frame is exactly as wide as
// the panel, whatever the panes put inside. A pane that renders something too
// wide must be cut, not left to shear the columns below it.
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
