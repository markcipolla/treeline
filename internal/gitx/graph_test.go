package gitx

import (
	"strings"
	"testing"
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
