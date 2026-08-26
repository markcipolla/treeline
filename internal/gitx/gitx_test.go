package gitx

import (
	"errors"
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

	if err := Remove(root, wts[1].Path, true, false); err != nil {
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

// DiffFile on an untracked file runs `diff --no-index`, which exits 1 simply
// because the files differ. The diff must still come back — otherwise the git
// pane shows "✗ git diff: exit status 1" for every new file.
func TestDiffFileUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempRepo(t)
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := DiffFile(root, "new.txt", false, true)
	if err != nil {
		t.Fatalf("DiffFile: %v", err)
	}
	if !strings.Contains(diff, "fresh") {
		t.Errorf("diff should show the new content, got:\n%s", diff)
	}
}

// A tracked file's diff still fails loudly when git really fails.
func TestDiffFileMissingPathErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempRepo(t)
	if _, err := DiffFile(root, "../outside-the-repo", false, false); err == nil {
		t.Error("expected an error for a path outside the repository")
	}
}

// A locked worktree — claude locks the one it is working in — can only be
// removed with a second --force. Remove reports the refusal as ErrLocked,
// carrying the reason so the caller can say what holds it.
func TestRemoveLockedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempRepo(t)
	dir := filepath.Join(".worktrees", "LMAP-9-locked")
	if err := Add(root, dir, "feature/LMAP-9/locked", "HEAD", true); err != nil {
		t.Fatal(err)
	}
	const reason = "claude session encapsulated-shimmying-sparrow (pid 65825)"
	mustGit(t, root, "worktree", "lock", "--reason", reason, dir)

	wts, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	var wt *Worktree
	for i := range wts {
		if strings.HasSuffix(wts[i].Path, "LMAP-9-locked") {
			wt = &wts[i]
		}
	}
	if wt == nil {
		t.Fatal("worktree not listed")
	}
	if !wt.Locked || wt.LockReason != reason {
		t.Errorf("Locked=%v LockReason=%q, want true and %q", wt.Locked, wt.LockReason, reason)
	}

	err = Remove(root, wt.Path, true, false) // --force alone is not enough
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Remove without unlock: %v, want ErrLocked", err)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("error should name what holds the lock, got %v", err)
	}

	if err := Remove(root, wt.Path, true, true); err != nil {
		t.Fatalf("Remove with unlock: %v", err)
	}
	after, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range after {
		if strings.HasSuffix(w.Path, "LMAP-9-locked") {
			t.Error("worktree survived a forced removal")
		}
	}
}

// The log's per-commit view needs the patch a single commit introduced, not
// the whole branch: only the second commit's change may appear.
func TestCommitDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempRepo(t)
	if err := os.WriteFile(filepath.Join(root, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-m", "add second")

	diff, err := CommitDiff(root, "HEAD")
	if err != nil {
		t.Fatalf("CommitDiff: %v", err)
	}
	// the patch is colored, so escape codes sit between the marker and the
	// text: match the pieces, not "+second"
	if !strings.Contains(diff, "second.txt") || !strings.Contains(diff, "@@") {
		t.Errorf("HEAD's patch should show second.txt, got:\n%s", diff)
	}
	if strings.Contains(diff, "README.md") {
		t.Errorf("HEAD's patch should not include the first commit, got:\n%s", diff)
	}

	// the root commit has no parent, and still has a patch
	first := mustGit(t, root, "rev-list", "--max-parents=0", "HEAD")
	diff, err = CommitDiff(root, first)
	if err != nil {
		t.Fatalf("CommitDiff(root commit): %v", err)
	}
	if !strings.Contains(diff, "README.md") {
		t.Errorf("the root commit's patch should show README.md, got:\n%s", diff)
	}
}

func TestChangedLines(t *testing.T) {
	dir := tempRepo(t)

	// commit a file, then rewrite one line and append two
	base := "one\ntwo\nthree\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "commit", "-m", "f")

	edited := "one\nTWO\nthree\nfour\nfive\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	marks, err := ChangedLines(dir, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]rune{1: '~', 3: '+', 4: '+'}
	if len(marks) != len(want) {
		t.Fatalf("marks = %v, want %v", marks, want)
	}
	for line, mark := range want {
		if marks[line] != mark {
			t.Errorf("line %d marked %q, want %q (all: %v)", line, marks[line], mark, marks)
		}
	}

	// an untracked file is new throughout
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marks, err = ChangedLines(dir, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 2 || marks[0] != '+' || marks[1] != '+' {
		t.Errorf("untracked marks = %v, want both lines '+'", marks)
	}
}
