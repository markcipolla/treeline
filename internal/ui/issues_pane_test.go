package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestIssuesPaneMergedWithTable: the panel layout draws the issues list as one
// grid with its pane — no box inside a box, no line spent on a second frame.
func TestIssuesPaneMergedWithTable(t *testing.T) {
	m := withIssues(newTestModel(t, 160))
	m.height = 44
	m.resize()
	m.refreshRows()
	if !m.threePane() {
		t.Fatal("want the panel layout at 160 cols")
	}

	var lines []string
	for _, l := range strings.Split(m.View(), "\n") {
		lines = append(lines, strings.TrimSpace(ansiRE.ReplaceAllString(l, "")))
	}

	// the pane title rule carries the grid's column joins
	rule := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "╞") && strings.Contains(l, "╤") {
			rule = i
			break
		}
	}
	if rule < 0 {
		t.Fatalf("no merged title rule in:\n%s", strings.Join(lines, "\n"))
	}
	// the column header sits directly under it, no nested frame between
	if got := lines[rule+1]; !strings.HasPrefix(got, "│ KEY") {
		t.Errorf("line under the title rule = %q, want the column header", got)
	}
	// and the pane's bottom border closes the columns off
	bottom := -1
	for i := rule; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "╰") && strings.Contains(lines[i], "┴") {
			bottom = i
			break
		}
	}
	if bottom < 0 {
		t.Fatal("the pane's bottom border does not close the grid's columns")
	}
	// nothing between the two draws a second frame (the claude|git seam below
	// the pane is a real doubled border, so only the pane's own lines count)
	for i := rule; i <= bottom; i++ {
		if strings.Contains(lines[i], "││") {
			t.Errorf("line %d has a doubled border: %q", i, lines[i])
		}
	}
}

// TestIssuesPaneFillsItsHeight: merging the frames must not change how much
// vertical space the pane claims, or the panes below it shift.
func TestIssuesPaneFillsItsHeight(t *testing.T) {
	m := withIssues(newTestModel(t, 160))
	m.height = 44
	m.resize()
	m.refreshRows()

	want := m.layout().issues.h
	framed := frame(band{{m.issuesPart("issues & worktrees", true)}})
	if got := len(strings.Split(framed, "\n")); got != want {
		t.Errorf("issues pane is %d lines, want %d", got, want)
	}
}

// codeBefore is the ANSI sequence a rendered line applies immediately before
// the given text — the style the terminal draws that text in.
func codeBefore(line, text string) string {
	i := strings.Index(line, text)
	if i < 0 {
		return ""
	}
	all := ansiRE.FindAllString(line[:i], -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// TestGridChromeFollowsFocus: the grid's own chrome — the column headers, and
// with them the rules and dividers — takes the accent along with the pane
// border when the issues list has focus. A grey table inside a green box did
// not read as the focused panel.
func TestGridChromeFollowsFocus(t *testing.T) {
	t.Cleanup(func() { lipgloss.SetColorProfile(lipgloss.ColorProfile()) })
	lipgloss.SetColorProfile(termenv.ANSI256)

	m := withIssues(newTestModel(t, 200))
	m.height = 30
	m.resize()
	if !m.selectWorktree(m.wts[1].Path) {
		t.Fatal("no row for the second worktree")
	}

	header := func(m Model) string {
		t.Helper()
		for _, line := range strings.Split(m.View(), "\n") {
			if strings.Contains(ansiRE.ReplaceAllString(line, ""), "KEY") {
				return line
			}
		}
		t.Fatal("no column header row in the view")
		return ""
	}

	for _, tc := range []struct {
		pane int
		want string
	}{
		{paneIssues, ansiRE.FindString(paneTitleFocus.Render("KEY"))},
		{paneClaude, ansiRE.FindString(paneTitleStyle.Render("KEY"))},
	} {
		mm, _ := m.focusPane(tc.pane)
		if mm.(Model).pane != tc.pane {
			t.Fatalf("pane %d did not take focus", tc.pane)
		}
		if got := codeBefore(header(mm.(Model)), "KEY"); got != tc.want {
			t.Errorf("pane %d focused: KEY is drawn %q, want %q", tc.pane, got, tc.want)
		}
	}
}
