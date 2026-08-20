package github

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestStatusFromRuns pins the folding rules: in-flight beats everything, a
// single bad conclusion fails the commit, and no runs means no verdict.
func TestStatusFromRuns(t *testing.T) {
	ok := checkRun{Status: "completed", Conclusion: "success"}
	for _, tc := range []struct {
		name string
		runs []checkRun
		want Status
	}{
		{"no runs", nil, StatusNone},
		{"all green", []checkRun{ok, ok}, StatusOK},
		{"neutral and skipped still pass", []checkRun{
			{Status: "completed", Conclusion: "neutral"},
			{Status: "completed", Conclusion: "skipped"},
		}, StatusOK},
		{"one failure", []checkRun{ok, {Status: "completed", Conclusion: "failure"}}, StatusFail},
		{"timed out", []checkRun{{Status: "completed", Conclusion: "timed_out"}}, StatusFail},
		{"cancelled", []checkRun{{Status: "completed", Conclusion: "cancelled"}}, StatusFail},
		{"action required", []checkRun{{Status: "completed", Conclusion: "action_required"}}, StatusFail},
		{"queued beats green", []checkRun{ok, {Status: "queued"}}, StatusRunning},
		// still running takes precedence even when a sibling has failed:
		// the commit's verdict isn't in yet
		{"in progress beats failure", []checkRun{
			{Status: "completed", Conclusion: "failure"},
			{Status: "in_progress"},
		}, StatusRunning},
	} {
		if got := statusFromRuns(tc.runs); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func gitRepoWithOrigin(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"remote", "add", "origin", url},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v — %s", args, err, out)
		}
	}
	return dir
}

// TestRepoFromRemote covers the remote URL shapes git hands back, including
// hosts that aren't GitHub at all.
func TestRepoFromRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, tc := range []struct {
		url         string
		owner, repo string
		ok          bool
	}{
		{"git@github.com:markcipolla/treeline.git", "markcipolla", "treeline", true},
		{"git@github.com:markcipolla/treeline", "markcipolla", "treeline", true},
		{"https://github.com/markcipolla/treeline.git", "markcipolla", "treeline", true},
		{"https://github.com/markcipolla/treeline", "markcipolla", "treeline", true},
		{"ssh://git@github.com/markcipolla/treeline.git", "markcipolla", "treeline", true},
		{"https://user@github.com/labflow/labmaster.git", "labflow", "labmaster", true},
		{"git@gitlab.com:markcipolla/treeline.git", "", "", false},
		{"https://bitbucket.org/markcipolla/treeline.git", "", "", false},
		{"/srv/git/bare.git", "", "", false},
	} {
		dir := gitRepoWithOrigin(t, tc.url)
		owner, repo, ok := RepoFromRemote(dir)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo {
			t.Errorf("%s -> (%q, %q, %v), want (%q, %q, %v)",
				tc.url, owner, repo, ok, tc.owner, tc.repo, tc.ok)
		}
	}
}

// TestRepoFromRemoteNoOrigin: a repo with no origin, or no repo at all, is
// simply not a GitHub repo rather than an error.
func TestRepoFromRemoteNoOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v — %s", err, out)
	}
	if _, _, ok := RepoFromRemote(dir); ok {
		t.Error("repo with no origin reported a GitHub remote")
	}

	plain := filepath.Join(t.TempDir(), "not-a-repo")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := RepoFromRemote(plain); ok {
		t.Error("plain directory reported a GitHub remote")
	}
}

// TestTokenPrefersConfigured: the configured token wins outright, so the gh
// CLI is never consulted (and the test never depends on it).
func TestTokenPrefersConfigured(t *testing.T) {
	if got := Token("cfg-token"); got != "cfg-token" {
		t.Errorf("Token() = %q, want the configured one", got)
	}
}
