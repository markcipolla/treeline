package branch

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"Fix login redirect", 48, "fix-login-redirect"},
		{"  Weird -- punctuation!! (here) ", 48, "weird-punctuation-here"},
		{"UPPER Case & Symbols/Paths", 48, "upper-case-symbols-paths"},
		{"a very long title that should be cut at a word boundary somewhere", 30, "a-very-long-title-that-should"},
		{"", 48, ""},
		{"---", 48, ""},
	}
	for _, c := range cases {
		if got := Slugify(c.in, c.max); got != c.want {
			t.Errorf("Slugify(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestParseIssueKey(t *testing.T) {
	if got := ParseIssueKey("lmap-142"); got != "LMAP-142" {
		t.Errorf("got %q", got)
	}
	if got := ParseIssueKey("feature/x"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := ParseIssueKey("LMAP-"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestNameAndDirFor(t *testing.T) {
	n := Name("feature", "LMAP-142", "fix-login")
	if n != "feature/LMAP-142/fix-login" {
		t.Fatalf("Name = %q", n)
	}
	if d := DirFor(n); d != "LMAP-142-fix-login" {
		t.Errorf("DirFor = %q", d)
	}
	if d := DirFor("bug/LMAP-9"); d != "LMAP-9" {
		t.Errorf("DirFor = %q", d)
	}
	if d := DirFor("random/branch-name"); d != "random-branch-name" {
		t.Errorf("DirFor = %q", d)
	}
}

func TestValidateRef(t *testing.T) {
	if err := ValidateRef("feature/LMAP-1/ok"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	for _, bad := range []string{"", "/x", "x/", "a..b", "a b", "a:b", "x.lock", "-x", "a//b"} {
		if err := ValidateRef(bad); err == nil {
			t.Errorf("ValidateRef(%q) should fail", bad)
		}
	}
}
