package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const sampleDiff = "diff --git a/x.go b/x.go\n" +
	"--- a/x.go\n+++ b/x.go\n" +
	"@@ -1,3 +1,3 @@\n" +
	" \tkept\n" +
	"-\tgone one\n" +
	"-\tgone two\n" +
	"+\tnew one\n" +
	" \ttail\n"

// TestDiffRowsWrapsNarrow: a line longer than the box comes back as several
// rows, none wider than the box, instead of being cut off.
func TestDiffRowsWrapsNarrow(t *testing.T) {
	long := "+" + strings.Repeat("abcdefgh", 10)
	rows := diffRows("@@ -1 +1 @@\n"+long+"\n", 40)
	joined := ""
	for _, r := range rows {
		if got := ansi.StringWidth(r); got > 40 {
			t.Fatalf("row wider than the box: %d > 40 (%q)", got, r)
		}
		joined += ansi.Strip(r)
	}
	if !strings.Contains(joined, ansi.Strip(long)) {
		t.Fatalf("wrapped rows lost content: %q", joined)
	}
}

// TestDiffRowsSplitsWide: given room, a removal and the addition replacing it
// share a row, and the divider sits at the same column on every row.
func TestDiffRowsSplitsWide(t *testing.T) {
	const w = 100
	rows := diffRows(sampleDiff, w)
	var paired string
	for _, r := range rows {
		if got := ansi.StringWidth(r); got > w {
			t.Fatalf("row wider than the box: %d > %d (%q)", got, w, r)
		}
		plain := ansi.Strip(r)
		if strings.Contains(plain, "gone one") {
			paired = plain
		}
		if i := strings.Index(plain, "│"); i >= 0 && ansi.StringWidth(plain[:i]) != (w-3)/2+1 {
			t.Fatalf("divider off the column at %d: %q", i, plain)
		}
	}
	if paired == "" || !strings.Contains(paired, "new one") {
		t.Fatalf("removal and addition not paired on one row: %q", paired)
	}
	if strings.Contains(paired, "\t") {
		t.Fatalf("tab left in a column, alignment will drift: %q", paired)
	}
}

// TestDiffRowsPairsUnevenRuns: three removals against one addition leaves the
// spare removals with an empty right-hand cell rather than dropping them.
func TestDiffRowsPairsUnevenRuns(t *testing.T) {
	rows := diffRows("@@ -1,3 +1,1 @@\n-a\n-b\n-c\n+z\n", 100)
	for _, want := range []string{"-a", "-b", "-c", "+z"} {
		found := false
		for _, r := range rows {
			if strings.Contains(ansi.Strip(r), want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q missing from the split view", want)
		}
	}
}

// TestCellLinesReopensColour: every segment of a wrapped cell carries the
// line's own colour, so a continuation can't inherit its neighbour's.
func TestCellLinesReopensColour(t *testing.T) {
	segs := cellLines("\x1b[31m-"+strings.Repeat("x", 50)+"\x1b[m", 20)
	if len(segs) < 2 {
		t.Fatalf("expected the line to wrap, got %d segment(s)", len(segs))
	}
	for i, s := range segs {
		if !strings.HasPrefix(s, "\x1b[31m") {
			t.Errorf("segment %d doesn't open its colour: %q", i, s)
		}
		if !strings.HasSuffix(s, "\x1b[m") {
			t.Errorf("segment %d doesn't close its colour: %q", i, s)
		}
	}
	if plain := strings.Join([]string{ansi.Strip(segs[0]), ansi.Strip(segs[1])}, ""); !strings.HasPrefix(plain, "-xxxx") {
		t.Errorf("wrapped content mangled: %q", plain)
	}
}
