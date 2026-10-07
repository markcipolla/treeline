package update

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.17.6", "0.17.5", true},
		{"v0.18.0", "0.17.5", true},
		{"v1.0.0", "0.99.9", true},
		{"v0.17.5", "0.17.5", false},
		{"v0.17.5", "0.17.6", false},
		{"v0.9.0", "0.10.0", false}, // numeric, not lexical
		{"", "0.17.5", false},       // nothing cached yet
		{"v0.17.5", "dev", false},
		{"v0.17.5-rc1", "0.17.5", false}, // pre-release tail ignored
	}
	for _, c := range cases {
		if got := newer(c.latest, c.current); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
