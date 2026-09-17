package gitx

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/markcipolla/treeline/internal/tui"
)

// rails runs a parentage through the lane layout and returns the drawing,
// one row per line. Parents are given newest-first, as git lists them.
func rails(t *testing.T, history [][]string) string {
	t.Helper()
	return railsFrom(t, "", history)
}

// railsFrom is rails with head checked out, so the spine is drawn double.
func railsFrom(t *testing.T, head string, history [][]string) string {
	t.Helper()
	b := railBuilder{trunk: head}
	for _, entry := range history {
		b.add(Commit{Hash: entry[0], Short: entry[0]}, entry[1:])
	}
	var lines []string
	for _, row := range b.rows {
		lines = append(lines, strings.TrimRight(row.Graph, " "))
	}
	return strings.Join(lines, "\n")
}

func TestRailsLinearHistory(t *testing.T) {
	got := rails(t, [][]string{{"c", "b"}, {"b", "a"}, {"a"}})
	want := "●\n●\n●"
	if got != want {
		t.Errorf("rails:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsForkAndMergeJoinUp(t *testing.T) {
	// m merges a and b, which both sit on top of root p
	got := rails(t, [][]string{
		{"m", "a", "b"},
		{"a", "p"},
		{"b", "p"},
		{"p"},
	})
	want := strings.Join([]string{
		"●",
		"├─╮",
		"● │",
		"│ ●",
		"├─╯",
		"●",
	}, "\n")
	if got != want {
		t.Errorf("rails:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsBranchTipWithoutMerge(t *testing.T) {
	// two tips over one base: the second tip opens its own lane
	got := rails(t, [][]string{
		{"x", "p"},
		{"y", "p"},
		{"p"},
	})
	want := strings.Join([]string{
		"●",
		"│ ●",
		"├─╯",
		"●",
	}, "\n")
	if got != want {
		t.Errorf("rails:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsCrossingLaneIsATee(t *testing.T) {
	// m opens a lane for its second parent past a lane that keeps running
	got := rails(t, [][]string{
		{"t1", "m"},
		{"t2", "q"},
		{"m", "p", "r"},
	})
	want := strings.Join([]string{
		"●",
		"│ ●",
		"● │",
		"├─┼─╮",
	}, "\n")
	if got != want {
		t.Errorf("rails:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsOctopusMerge(t *testing.T) {
	got := rails(t, [][]string{{"m", "a", "b", "c"}})
	want := strings.Join([]string{
		"●",
		"├─┬─╮",
	}, "\n")
	if got != want {
		t.Errorf("rails:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsRootCommitEndsItsLane(t *testing.T) {
	// two unrelated roots: the first lane must close so the second reuses it
	got := rails(t, [][]string{{"a"}, {"b"}})
	want := "●\n●"
	if got != want {
		t.Errorf("rails:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsDrawTheCheckedOutSpineDouble(t *testing.T) {
	got := railsFrom(t, "m", [][]string{
		{"m", "a", "b"},
		{"a", "p"},
		{"b", "p"},
		{"p"},
	})
	want := strings.Join([]string{
		"●",
		"╟─╮", // the branch leaves the spine
		"● │",
		"║ ●", // the spine runs on past the branch
		"╟─╯", // and the branch folds back into it
		"●",
	}, "\n")
	if got != want {
		t.Errorf("rails:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsSpineIsOnlyHeadsFirstParents(t *testing.T) {
	// b is merged into the spine but is not on it, so it stays light
	got := railsFrom(t, "m", [][]string{
		{"m", "a", "b"},
		{"b", "x"},
		{"a", "p"},
	})
	want := strings.Join([]string{
		"●",
		"╟─╮",
		"║ ●", // b
		"● │", // a, back on the spine
	}, "\n")
	if got != want {
		t.Errorf("rails:\n%s\nwant:\n%s", got, want)
	}
}

// railColours runs a parentage through the lane layout and returns the
// colour of every drawn cell, one row per line, with a dot where the cell
// carries no branch. Colours come out as their index in the cycle.
func railColours(t *testing.T, head string, history [][]string) string {
	t.Helper()
	b := railBuilder{trunk: head}
	for _, entry := range history {
		b.add(Commit{Hash: entry[0], Short: entry[0]}, entry[1:])
	}
	var lines []string
	for _, row := range b.rows {
		var sb strings.Builder
		for i, r := range []rune(row.Graph) {
			switch {
			case r == ' ':
				sb.WriteByte(' ')
			case row.Colours[i] < 0:
				sb.WriteByte('.')
			default:
				sb.WriteString(strconv.Itoa(int(row.Colours[i])))
			}
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}
	return strings.Join(lines, "\n")
}

func TestRailsColourFollowsOneLineOfDescent(t *testing.T) {
	// m merges a and b; each of the three lanes keeps its own colour the
	// whole way down, and the runs that fold a and b back in take theirs
	got := railColours(t, "", [][]string{
		{"m", "a", "b"},
		{"a", "p"},
		{"b", "p"},
		{"p"},
	})
	want := strings.Join([]string{
		"0",   // ●     m opens the first lane
		"011", // ├─╮   the run leaving m belongs to b's new lane
		"0 1", // ● │   a stays on m's lane, b waits on its own
		"0 1", // │ ●
		"011", // ├─╯   and b folds back in, still in its colour
		"0",   // ●     p, back on the first lane
	}, "\n")
	if got != want {
		t.Errorf("colours:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsCrossingKeepsTheLaneItPassesBehind(t *testing.T) {
	// the run opening m's second parent crosses t2's lane: the cell they
	// share is t2's colour, so the vertical reads as passing behind
	got := railColours(t, "", [][]string{
		{"t1", "m"},
		{"t2", "q"},
		{"m", "p", "r"},
	})
	want := strings.Join([]string{
		"0",
		"0 1",
		"0 1",
		"02122", // ├─┼─╮ — cell 2 stays lane 1's, the rest is the new lane
	}, "\n")
	if got != want {
		t.Errorf("colours:\n%s\nwant:\n%s", got, want)
	}
}

func TestRailsColourCycleWraps(t *testing.T) {
	// more lines of descent than colours: the thirteenth is back to the
	// first, and no lane is ever left without one
	var history [][]string
	for i := 0; i < len(tui.GraphColours)+1; i++ {
		history = append(history, []string{fmt.Sprintf("r%d", i)})
	}
	b := railBuilder{}
	for _, entry := range history {
		b.add(Commit{Hash: entry[0]}, nil)
	}
	if n := len(b.rows); n != len(history) {
		t.Fatalf("got %d rows, want %d", n, len(history))
	}
	first, last := b.rows[0].Colours[0], b.rows[len(b.rows)-1].Colours[0]
	if first != 0 || last != 0 {
		t.Errorf("colours %d…%d, want the cycle to wrap back to 0", first, last)
	}
}

func TestSquareOffAlignsTheCommitTextPastEveryLane(t *testing.T) {
	// left ragged, the one-lane row's text would start in the column the
	// three-lane row runs a rail down
	g := GraphLog{Rows: []LogRow{
		{Graph: "●", Colours: []int8{0}, Commit: 0},
		{Graph: "│ │ ●", Colours: []int8{0, -1, 1, -1, 2}, Commit: 1},
	}}
	g.squareOff()
	const want = 5 + 2 // the widest row, plus the gap before the text
	for i, row := range g.Rows {
		if n := len([]rune(row.Graph)); n != want {
			t.Errorf("row %d is %d wide, want %d: %q", i, n, want, row.Graph)
		}
		if n := len(row.Colours); n != want {
			t.Errorf("row %d has %d colours, want %d", i, n, want)
		}
	}
	if g.Rows[0].Graph != "●      " {
		t.Errorf("padded %q, want the rails then blanks", g.Rows[0].Graph)
	}
	if c := g.Rows[0].Colours[4]; c != noColour {
		t.Errorf("padding took colour %d, want none", c)
	}
}
