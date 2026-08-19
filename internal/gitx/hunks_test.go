package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestHunkStagingRoundtrip stages one of two hunks, checks the file shows in
// both sections, then reverse-applies it back out of the index.
func TestHunkStagingRoundtrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		if _, err := run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustRun("init", "-q")
	mustRun("config", "user.email", "t@t")
	mustRun("config", "user.name", "t")
	content := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\np\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun("add", "f.txt")
	mustRun("commit", "-qm", "init")
	edited := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\nO\np\n"
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	header, hunks, err := FileHunks(dir, "f.txt", false)
	if err != nil || len(hunks) != 2 {
		t.Fatalf("FileHunks: %d hunks, err %v", len(hunks), err)
	}
	if err := ApplyToIndex(dir, header+"\n"+hunks[0]+"\n", false); err != nil {
		t.Fatal("stage hunk:", err)
	}
	st, err := Status(dir)
	if err != nil || len(st) != 1 {
		t.Fatalf("Status: %+v err %v", st, err)
	}
	if st[0].Staged != 'M' || st[0].Unstaged != 'M' {
		t.Fatalf("want partially staged MM, got %c%c", st[0].Staged, st[0].Unstaged)
	}
	if err := ApplyToIndex(dir, header+"\n"+hunks[0]+"\n", true); err != nil {
		t.Fatal("unstage hunk:", err)
	}
	st, _ = Status(dir)
	if len(st) != 1 || st[0].Staged != ' ' || st[0].Unstaged != 'M' {
		t.Fatalf("want unstaged-only M, got %+v", st)
	}

	commits, err := Log(dir, 10)
	if err != nil || len(commits) != 1 || commits[0].Subject != "init" {
		t.Fatalf("Log: %+v err %v", commits, err)
	}
}
