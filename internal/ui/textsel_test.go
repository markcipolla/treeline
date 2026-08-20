package ui

import (
	"strings"
	"testing"
)

// rendered lines the way the git pane produces them: colours around the text.
var diffLines = []string{
	okStyle.Render("+added line"),
	errStyle.Render("-removed line"),
	"  context line",
}

func TestSelectedTextSpansLines(t *testing.T) {
	// from column 1 of line 0 to column 8 of line 2
	got := selectedText(diffLines, selPoint{line: 0, col: 1}, selPoint{line: 2, col: 8})
	want := "added line\n-removed line\n  context"
	if got != want {
		t.Errorf("selectedText = %q, want %q", got, want)
	}
}

func TestSelectedTextWithinOneLine(t *testing.T) {
	got := selectedText(diffLines, selPoint{line: 1, col: 1}, selPoint{line: 1, col: 7})
	if got != "removed" {
		t.Errorf("selectedText = %q, want %q", got, "removed")
	}
}

func TestSelectedTextIgnoresLinesOutsideTheRange(t *testing.T) {
	got := selectedText(diffLines, selPoint{line: 2, col: 0}, selPoint{line: 2, col: 20})
	if got != "  context line" {
		t.Errorf("selectedText = %q, want the third line only", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Error("clipboard text must be free of escape sequences")
	}
}

func TestHighlightSelKeepsTheVisibleText(t *testing.T) {
	out := highlightSel(diffLines, selPoint{line: 0, col: 0}, selPoint{line: 1, col: 5})
	for i, ln := range out {
		if plainWidth(ln) != plainWidth(diffLines[i]) {
			t.Errorf("line %d changed width: %q", i, ansiRE.ReplaceAllString(ln, ""))
		}
	}
	if !strings.Contains(out[0], "\x1b[7m") {
		t.Error("selected line should be reversed")
	}
	if out[2] != diffLines[2] {
		t.Error("unselected line should be untouched")
	}
}

// A line built from several styles has resets in the middle of it, and a reset
// clears the reverse attribute too — so the highlight has to be re-asserted or
// it stops partway through the selection.
func TestHighlightSpanSurvivesInnerResets(t *testing.T) {
	// written out rather than styled: lipgloss emits no colour under `go test`
	line := "\x1b[36m❯ \x1b[0m\x1b[32mM README.md\x1b[0m"
	out := highlightSpan(line, 0, plainWidth(line))
	tail := out[strings.LastIndex(out, "\x1b[0m"):]
	if !strings.HasPrefix(tail, "\x1b[0m\x1b[7m") {
		t.Errorf("highlight not re-asserted after a reset: %q", out)
	}
}

// File rows are wrapped in bubblezone markers so they can be clicked. They
// occupy no columns on screen, so they must not shift the selection or reach
// the clipboard.
func TestSelectionIgnoresZoneMarkers(t *testing.T) {
	const zoneStart, zoneEnd = "\x1b[1008z", "\x1b[1008z"
	lines := []string{zoneStart + "  M README.md" + zoneEnd}

	if got := plainWidth(lines[0]); got != len("  M README.md") {
		t.Errorf("plainWidth = %d, want %d", got, len("  M README.md"))
	}
	got := selectedText(lines, selPoint{line: 0, col: 2}, selPoint{line: 0, col: 12})
	if got != "M README.md" {
		t.Errorf("selectedText = %q, want %q", got, "M README.md")
	}
	out := highlightSel(lines, selPoint{line: 0, col: 0}, selPoint{line: 0, col: 12})
	if !strings.Contains(out[0], zoneStart) {
		t.Error("the zone marker must survive highlighting, or the row stops being clickable")
	}
	if plainWidth(out[0]) != plainWidth(lines[0]) {
		t.Error("highlighting changed the line's width")
	}
}

func TestIsReset(t *testing.T) {
	for seq, want := range map[string]bool{
		"\x1b[0m":    true,
		"\x1b[m":     true,
		"\x1b[1;0m":  true,
		"\x1b[32m":   false,
		"\x1b[1;32m": false,
		"\x1b[1008z": false, // a zone marker clears nothing
	} {
		if got := isReset(seq); got != want {
			t.Errorf("isReset(%q) = %v, want %v", seq, got, want)
		}
	}
}

func TestSelectionNormalisesBackwardsDrags(t *testing.T) {
	var s textSel
	s.press(9, 4) // drag up and to the left
	s.drag(2, 1)
	if !s.release() {
		t.Fatal("a drag that moved is a selection")
	}
	a, b, ok := s.bounds()
	if !ok || a != (selPoint{line: 1, col: 2}) || b != (selPoint{line: 4, col: 9}) {
		t.Errorf("bounds = %v..%v (ok=%v), want reading order", a, b, ok)
	}
}

func TestClickIsNotASelection(t *testing.T) {
	var s textSel
	s.press(3, 3)
	if s.release() {
		t.Error("a press with no movement must fall through as a click")
	}
	if _, _, ok := s.bounds(); ok {
		t.Error("a click should leave nothing highlighted")
	}
}

func TestClampScroll(t *testing.T) {
	cases := []struct{ off, n, rows, want int }{
		{5, 3, 10, 0},    // content shorter than the view
		{5, 20, 10, 5},   // in range
		{15, 20, 10, 10}, // past the end
		{-2, 20, 10, 0},  // before the start
	}
	for _, c := range cases {
		if got := clampScroll(c.off, c.n, c.rows); got != c.want {
			t.Errorf("clampScroll(%d, %d, %d) = %d, want %d", c.off, c.n, c.rows, got, c.want)
		}
	}
}
