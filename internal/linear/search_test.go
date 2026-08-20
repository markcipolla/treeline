package linear

import (
	"testing"
	"time"
)

// TestParseSearchQuery covers the token forms the search box accepts, which
// full-text search can't express on its own.
func TestParseSearchQuery(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		want  SearchQuery
		notes string
	}{
		{raw: "", want: SearchQuery{}},
		{raw: "api timeout", want: SearchQuery{Term: "api timeout"}},
		{raw: "state:review", want: SearchQuery{State: "review"}},
		{raw: "@sam", want: SearchQuery{Assignee: "sam"}},
		{raw: "assignee:sam", want: SearchQuery{Assignee: "sam"}},
		{
			raw:   "state:review @sam api timeout",
			want:  SearchQuery{Term: "api timeout", State: "review", Assignee: "sam"},
			notes: "tokens in any position, everything else is the term",
		},
		{
			raw:   "api state:in-progress timeout @mark.cipolla",
			want:  SearchQuery{Term: "api timeout", State: "in-progress", Assignee: "mark.cipolla"},
			notes: "tokens interleaved with the term",
		},
		{
			raw:   "STATE:Review @Sam",
			want:  SearchQuery{State: "Review", Assignee: "Sam"},
			notes: "prefix match is case-insensitive but the value keeps its case",
		},
		{
			raw:   "@sam @jo",
			want:  SearchQuery{Assignee: "jo"},
			notes: "last assignee wins",
		},
		{
			raw:   "a@b.com",
			want:  SearchQuery{Term: "a@b.com"},
			notes: "@ mid-word is not an assignee token",
		},
		{
			raw:   "@",
			want:  SearchQuery{Term: "@"},
			notes: "a bare @ is not a token, so it stays searchable text",
		},
		{
			raw:   "  spaced   out  ",
			want:  SearchQuery{Term: "spaced out"},
			notes: "runs of whitespace collapse",
		},
	} {
		got := ParseSearchQuery(tc.raw)
		if got != tc.want {
			t.Errorf("ParseSearchQuery(%q) = %+v, want %+v (%s)", tc.raw, got, tc.want, tc.notes)
		}
	}
}

func TestPriorityName(t *testing.T) {
	for p, want := range map[int]string{0: "", 1: "urgent", 2: "high", 3: "medium", 4: "low", 5: "", -1: ""} {
		if got := PriorityName(p); got != want {
			t.Errorf("PriorityName(%d) = %q, want %q", p, got, want)
		}
	}
}

// TestTokenFreshness: Fresh gates whether a request can go out as-is, Usable
// whether authorizing again can be skipped. An access token about to expire
// is unusable as-is but still refreshable.
func TestTokenFreshness(t *testing.T) {
	for _, tc := range []struct {
		name          string
		tok           Token
		fresh, usable bool
	}{
		{"empty", Token{}, false, false},
		{"no expiry", Token{AccessToken: "a"}, true, true},
		{"well in date", Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)}, true, true},
		{"expiring within the grace window", Token{AccessToken: "a", ExpiresAt: time.Now().Add(30 * time.Second)}, false, true},
		{"expired", Token{AccessToken: "a", ExpiresAt: time.Now().Add(-time.Hour)}, false, true},
		{"refresh only", Token{RefreshToken: "r"}, false, true},
	} {
		if got := tc.tok.Fresh(); got != tc.fresh {
			t.Errorf("%s: Fresh() = %v, want %v", tc.name, got, tc.fresh)
		}
		if got := tc.tok.Usable(); got != tc.usable {
			t.Errorf("%s: Usable() = %v, want %v", tc.name, got, tc.usable)
		}
	}
}
