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

// The log's rails come back as box-drawing, decorations ride along, and a
// divider row marks the merge-base with the default base — but only once the
// checked-out branch has actually forked from it.
func TestLogGraphAndBranchDivider(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := tempRepo(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-m", "second")

	// on the base branch itself there is no fork to mark
	g, err := Log(root, 50)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if g.BaseRef != "" {
		t.Errorf("BaseRef = %q, want no divider on the base branch itself", g.BaseRef)
	}

	mustGit(t, root, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, root, "add", ".")
	mustGit(t, root, "commit", "-m", "feat work")

	g, err = Log(root, 50)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(g.Commits) != 3 {
		t.Fatalf("got %d commits, want 3: %+v", len(g.Commits), g.Commits)
	}
	if g.Commits[0].Subject != "feat work" || !strings.Contains(g.Commits[0].Refs, "feature") {
		t.Errorf("newest commit should be the decorated branch tip, got %+v", g.Commits[0])
	}
	if !strings.Contains(g.Rows[0].Graph, "●") {
		t.Errorf("rails should be box-drawing, got %q", g.Rows[0].Graph)
	}
	if g.BaseRef != "main" {
		t.Errorf("BaseRef = %q, want main", g.BaseRef)
	}
	div := -1
	for i, row := range g.Rows {
		if row.Divider {
			div = i
		}
	}
	if div < 0 {
		t.Fatal("no divider row in a forked branch's log")
	}
	if next := g.Rows[div+1]; next.Commit < 0 || g.Commits[next.Commit].Subject != "second" {
		t.Errorf("the divider should sit directly above the merge-base, got row %+v", g.Rows[div+1])
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

// TestGrepGroupsAndParses: Grep finds a case-insensitive substring across
// tracked and untracked files, groups the hits by file, and reports 1-based
// line numbers with the matching text.
func TestGrepGroupsAndParses(t *testing.T) {
	dir := tempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package a\n\nfunc Needle() {}\nvar x = 1\nfunc needle2() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "a.go")
	mustGit(t, dir, "commit", "-m", "a")
	// untracked, so --untracked is what brings it in
	if err := os.WriteFile(filepath.Join(dir, "b.go"),
		[]byte("// NEEDLE in a comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, more, err := Grep(dir, "needle", 100, 100)
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if more {
		t.Error("nothing should have been capped")
	}
	byPath := map[string][]GrepMatch{}
	for _, f := range files {
		byPath[f.Path] = f.Matches
	}
	if len(byPath) != 2 {
		t.Fatalf("want hits in 2 files, got %d: %+v", len(byPath), files)
	}
	a := byPath["a.go"]
	if len(a) != 2 {
		t.Fatalf("a.go: want 2 matches, got %+v", a)
	}
	if a[0].Line != 3 || !strings.Contains(a[0].Text, "func Needle()") {
		t.Errorf("a.go first match = %+v, want line 3 with the func", a[0])
	}
	if a[1].Line != 5 {
		t.Errorf("a.go second match on line %d, want 5", a[1].Line)
	}
	if b := byPath["b.go"]; len(b) != 1 || b[0].Line != 1 {
		t.Errorf("b.go matches = %+v, want one on line 1", b)
	}
}

// TestGrepNoMatchesIsNotAnError: git grep exits 1 when nothing matches, which
// must read as an empty result rather than a failure.
func TestGrepNoMatchesIsNotAnError(t *testing.T) {
	dir := tempRepo(t)
	files, more, err := Grep(dir, "zzz-nothing-holds-this-zzz", 100, 100)
	if err != nil {
		t.Fatalf("no matches should not error, got %v", err)
	}
	if len(files) != 0 || more {
		t.Errorf("want no hits, got %+v more=%v", files, more)
	}
}

// TestGrepFixedStringAndLeadingDash: the query is a literal, so regex
// metacharacters match themselves and a leading dash is not read as a flag.
func TestGrepFixedStringAndLeadingDash(t *testing.T) {
	dir := tempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "c.txt"),
		[]byte("a.b\naxb\n--flaglike\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _, err := Grep(dir, "a.b", 100, 100)
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(files) != 1 || len(files[0].Matches) != 1 || files[0].Matches[0].Line != 1 {
		t.Errorf("a.b should match only the literal, got %+v", files)
	}
	files, _, err = Grep(dir, "--flaglike", 100, 100)
	if err != nil {
		t.Fatalf("leading-dash query: %v", err)
	}
	if len(files) != 1 || files[0].Matches[0].Line != 3 {
		t.Errorf("--flaglike should match line 3, got %+v", files)
	}
}

// TestGrepCaps: the caps bound both the files and the total matches, and say
// so, so a one-letter query cannot return the whole repo.
func TestGrepCaps(t *testing.T) {
	dir := tempRepo(t)
	for _, n := range []string{"f1.txt", "f2.txt", "f3.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n),
			[]byte("hit\nhit\nhit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, more, err := Grep(dir, "hit", 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !more {
		t.Errorf("maxFiles=2: got %d files more=%v", len(files), more)
	}
	files, more, err = Grep(dir, "hit", 100, 4)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, f := range files {
		total += len(f.Matches)
	}
	if total != 4 || !more {
		t.Errorf("maxMatches=4: got %d matches more=%v", total, more)
	}
}

// TestGrepEmptyQuery: a blank query is a no-op, not a git invocation that
// would match every line.
func TestGrepEmptyQuery(t *testing.T) {
	dir := tempRepo(t)
	files, more, err := Grep(dir, "   ", 100, 100)
	if err != nil || len(files) != 0 || more {
		t.Errorf("blank query = %+v more=%v err=%v, want an empty no-op", files, more, err)
	}
}

// TestParseGrepRecordNULDelimited: NUL delimiters mean a path holding a colon
// still parses, and a malformed record is skipped rather than guessed at.
func TestParseGrepRecordNULDelimited(t *testing.T) {
	path, line, text, ok := parseGrepRecord("we:ird/pa th.go\x0042\x00\tcode := 1")
	if !ok || path != "we:ird/pa th.go" || line != 42 || text != "\tcode := 1" {
		t.Errorf("got %q %d %q ok=%v", path, line, text, ok)
	}
	if _, _, _, ok := parseGrepRecord("no-nuls-at-all"); ok {
		t.Error("a record with no delimiters should not parse")
	}
	if _, _, _, ok := parseGrepRecord("path\x00notanumber\x00text"); ok {
		t.Error("a non-numeric line number should not parse")
	}
}
