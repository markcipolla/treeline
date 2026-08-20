package ui

import (
	"strings"
	"testing"
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

	topH, _ := m.panelHeights()
	got := len(strings.Split(m.issuesPane("issues & worktrees"), "\n"))
	if got != topH {
		t.Errorf("issues pane is %d lines, want %d", got, topH)
	}
}
