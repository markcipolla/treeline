package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func tempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "init")
	// macOS TempDir lives under /var -> /private/var; normalize like git does.
	root, err := RepoRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAddListRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempRepo(t)

	dir := filepath.Join(".worktrees", "LMAP-1-test-thing")
	if err := Add(root, dir, "feature/LMAP-1/test-thing", "HEAD", true); err != nil {
		t.Fatalf("Add: %v", err)
	}

	wts, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(wts))
	}
	if !wts[0].IsPrimary || wts[0].Branch != "main" {
		t.Errorf("primary worktree wrong: %+v", wts[0])
	}
	if wts[1].Branch != "feature/LMAP-1/test-thing" {
		t.Errorf("branch = %q", wts[1].Branch)
	}
	if wts[1].Dirty {
		t.Errorf("fresh worktree should be clean")
	}

	if !BranchExists(root, "feature/LMAP-1/test-thing") {
		t.Error("BranchExists should be true")
	}

	// dirty detection
	if err := os.WriteFile(filepath.Join(wts[1].Path, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wts, _ = List(root)
	if !wts[1].Dirty {
		t.Error("worktree should be dirty")
	}

	if err := Remove(root, wts[1].Path, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := DeleteBranch(root, "feature/LMAP-1/test-thing"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	wts, _ = List(root)
	if len(wts) != 1 {
		t.Errorf("expected 1 worktree after remove, got %d", len(wts))
	}
}

func TestEnsureExcluded(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempRepo(t)
	if err := EnsureExcluded(root); err != nil {
		t.Fatalf("EnsureExcluded: %v", err)
	}
	// idempotent
	if err := EnsureExcluded(root); err != nil {
		t.Fatalf("EnsureExcluded (2nd): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), ".worktrees/") != 1 {
		t.Errorf("exclude should contain exactly one entry:\n%s", data)
	}
}

func TestDefaultBaseWithoutRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempRepo(t)
	// with no origin, a local main/master branch is the next best base;
	// only when neither exists does it fall back to HEAD
	base := DefaultBase(root)
	if base != "main" && base != "master" && base != "HEAD" {
		t.Errorf("DefaultBase = %q, want main, master, or HEAD", base)
	}
	current, err := run(root, "branch", "--show-current")
	if err == nil && (current == "main" || current == "master") && base != current {
		t.Errorf("DefaultBase = %q, want the local %q branch", base, current)
	}
}
