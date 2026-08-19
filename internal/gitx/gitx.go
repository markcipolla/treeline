// Package gitx wraps the git CLI for worktree management.
package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type Worktree struct {
	Path        string
	Branch      string // short name; empty when detached
	Detached    bool
	Bare        bool
	IsPrimary   bool // the main checkout
	Prunable    bool // directory is gone; git would prune it
	Dirty       bool
	Ahead       int
	Behind      int
	HasUpstream bool
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// RepoRoot returns the top-level directory of the repository containing dir.
func RepoRoot(dir string) (string, error) {
	return run(dir, "rev-parse", "--show-toplevel")
}

// List returns all worktrees of the repository at root, primary first.
// Statuses (dirty/ahead/behind) are filled concurrently.
func List(root string) ([]Worktree, error) {
	out, err := run(root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	var cur Worktree
	flush := func() {
		if cur.Path != "" {
			wts = append(wts, cur)
		}
		cur = Worktree{}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Detached = true
		case line == "bare":
			cur.Bare = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.Prunable = true
		}
	}
	flush()
	if len(wts) > 0 {
		wts[0].IsPrimary = true
	}

	var wg sync.WaitGroup
	for i := range wts {
		if wts[i].Bare || wts[i].Prunable {
			continue
		}
		wg.Add(1)
		go func(w *Worktree) {
			defer wg.Done()
			fillStatus(w)
		}(&wts[i])
	}
	wg.Wait()
	return wts, nil
}

func fillStatus(w *Worktree) {
	if out, err := run(w.Path, "status", "--porcelain"); err == nil {
		w.Dirty = out != ""
	}
	if out, err := run(w.Path, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
		if _, err := fmt.Sscanf(out, "%d %d", &w.Ahead, &w.Behind); err == nil {
			w.HasUpstream = true
		}
	}
}

// DefaultBase returns the ref new branches should start from: the remote's
// default branch when known (e.g. "origin/main"), otherwise "HEAD".
func DefaultBase(root string) string {
	if out, err := run(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && out != "" {
		return out
	}
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, err := run(root, "rev-parse", "--verify", "--quiet", ref); err == nil {
			return ref
		}
	}
	return "HEAD"
}

// HasCommits reports whether the repository has any commit — an unborn HEAD
// cannot be used as a worktree base.
func HasCommits(root string) bool {
	_, err := run(root, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// BranchExists reports whether a local branch with this name exists.
// Branches lists local and remote-tracking branch names (short form,
// e.g. "feature/LAB-1/slug" and "origin/feature/LAB-1/slug").
func Branches(root string) (local, remote []string) {
	if out, err := run(root, "for-each-ref", "--format=%(refname:short)", "refs/heads"); err == nil && out != "" {
		local = strings.Split(out, "\n")
	}
	if out, err := run(root, "for-each-ref", "--format=%(refname:short)", "refs/remotes"); err == nil && out != "" {
		for _, b := range strings.Split(out, "\n") {
			if strings.HasSuffix(b, "/HEAD") {
				continue
			}
			remote = append(remote, b)
		}
	}
	return local, remote
}

// Diff returns the worktree's changes against base — everything committed on
// the branch plus uncommitted edits — colored for terminal display.
func Diff(path, base string) (string, error) {
	target := "HEAD"
	if base != "" {
		if mb, err := run(path, "merge-base", base, "HEAD"); err == nil && mb != "" {
			target = mb
		}
	}
	return run(path, "-c", "color.diff=always", "diff", target)
}

func BranchExists(root, name string) bool {
	_, err := run(root, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// Add creates a worktree at dir (relative to root). When createBranch is
// true a new branch is created from base; otherwise the existing branch is
// checked out.
func Add(root, dir, branchName, base string, createBranch bool) error {
	args := []string{"worktree", "add"}
	if createBranch {
		args = append(args, "-b", branchName, dir, base)
	} else {
		args = append(args, dir, branchName)
	}
	_, err := run(root, args...)
	return err
}

// Prune drops worktree registrations whose directories no longer exist.
func Prune(root string) error {
	_, err := run(root, "worktree", "prune")
	return err
}

// Remove deletes the worktree at path. force discards uncommitted changes.
func Remove(root, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := run(root, args...)
	return err
}

// DeleteBranch force-deletes a local branch.
func DeleteBranch(root, name string) error {
	_, err := run(root, "branch", "-D", name)
	return err
}

// EnsureExcluded adds ".worktrees/" to .git/info/exclude so worktrees never
// show up as untracked files, without touching the committed .gitignore.
func EnsureExcluded(root string) error {
	common, err := run(root, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	exclude := filepath.Join(common, "info", "exclude")
	data, _ := os.ReadFile(exclude)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == ".worktrees/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	entry := ".worktrees/\n"
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		entry = "\n" + entry
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}
