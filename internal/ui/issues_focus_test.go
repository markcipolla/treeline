package ui

import "testing"

// TestIssuesColumnGrowsOnFocus: in a column layout the issues list is a slice
// of the terminal and sheds cells to fit. Focusing it grows the column until
// the whole grid shows, and moving focus away shrinks it back.
func TestIssuesColumnGrowsOnFocus(t *testing.T) {
	for _, width := range []int{180, 200, 240, 300} {
		m := withIssues(newTestModel(t, width))
		if !m.columnLayout() {
			t.Fatalf("%d cols: want a column layout, got mode %d", width, m.layoutMode())
		}

		m.pane = paneClaude
		m.resize()
		away := m.layout().issues.w
		shed := 0
		for _, c := range m.table.Columns() {
			if c.Width == 0 {
				shed++
			}
		}

		m.pane = paneIssues
		m.resize()
		l := m.layout()
		if shed > 0 && l.issues.w <= away {
			t.Errorf("%d cols: issues column stayed at %d with focus while %d columns were shed",
				width, l.issues.w, shed)
		}
		// once the terminal can afford the whole grid alongside usable work
		// panes, focusing the list has to show every column. Below that it
		// grows as far as the cap allows and still sheds the rest.
		if l.issues.w >= m.gridFullWidth() {
			for _, c := range m.table.Columns() {
				if c.Width <= 0 {
					t.Errorf("%d cols: %s still hidden at a full-width column (%d wide)",
						width, c.Title, l.issues.w)
				}
			}
		} else if width >= 240 {
			t.Errorf("%d cols: focused column only reached %d of the %d the grid wants",
				width, l.issues.w, m.gridFullWidth())
		}
		// growing must not squeeze the work panes out of usefulness
		for _, p := range []struct {
			name string
			b    box
		}{{"claude", l.claude}, {"git", l.git}, {"term", l.term}} {
			if l.mode == layCols && p.name == "term" {
				continue // shares the git column's width, checked there
			}
			if p.b.w < minWorkCol {
				t.Errorf("%d cols: %s pane squeezed to %d, below minWorkCol %d",
					width, p.name, p.b.w, minWorkCol)
			}
		}
	}
}

// TestIssuesColumnFitsNarrowTerminal: a terminal with nothing to lend must not
// grow the list past what the work panes can spare.
func TestIssuesColumnFitsNarrowTerminal(t *testing.T) {
	m := withIssues(newTestModel(t, 180))
	m.pane = paneIssues
	m.resize()
	l := m.layout()
	total := l.issues.w + l.claude.w + l.git.w
	if l.issues.w >= total {
		t.Errorf("issues column took the whole band: %d of %d", l.issues.w, total)
	}
}
