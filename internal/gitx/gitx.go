// Package gitx wraps the git CLI for worktree management.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Worktree struct {
	Root        string // primary checkout this worktree belongs to
	Path        string
	Branch      string // short name; empty when detached
	Detached    bool
	Bare        bool
	IsPrimary   bool // the main checkout
	Prunable    bool // directory is gone; git would prune it
	Locked      bool // git refuses to remove it without a second --force
	LockReason  string
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
		// stdout comes back with the error: some commands exit non-zero as a
		// matter of course (diff --no-index reports "the files differ" that
		// way), and their caller decides whether that is a failure.
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %s", args[0], msg)
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
		case line == "locked" || strings.HasPrefix(line, "locked "):
			// agent and other tools lock a worktree while they work in it,
			// leaving the reason for the lock here
			cur.Locked = true
			cur.LockReason = strings.TrimSpace(strings.TrimPrefix(line, "locked"))
		}
	}
	flush()
	if len(wts) > 0 {
		wts[0].IsPrimary = true
	}
	for i := range wts {
		wts[i].Root = root
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

// FileStatus is one entry of git status: a path with its index (staged) and
// worktree (unstaged) state letters.
type FileStatus struct {
	Path      string
	Orig      string // previous path for renames
	Staged    byte   // index status letter, ' ' when clean
	Unstaged  byte   // worktree status letter, ' ' when clean
	Untracked bool
}

// Status lists changed files (porcelain v1, NUL-separated). Entries can
// start with a significant space (unstaged-only files), so the output must
// not be trimmed.
func Status(dir string) ([]FileStatus, error) {
	cmd := exec.Command("git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	cmd.Dir = dir
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git status: %s", msg)
	}
	out := outb.String()
	var files []FileStatus
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		fs := FileStatus{Staged: f[0], Unstaged: f[1], Path: f[3:]}
		if fs.Staged == '?' {
			fs.Untracked = true
		}
		if fs.Staged == 'R' || fs.Staged == 'C' {
			i++ // rename/copy: next field is the original path
			if i < len(fields) {
				fs.Orig = fields[i]
			}
		}
		files = append(files, fs)
	}
	return files, nil
}

// StageFile stages the whole file (git add).
func StageFile(dir, path string) error {
	_, err := run(dir, "add", "--", path)
	return err
}

// UnstageFile removes the file's staged changes from the index.
func UnstageFile(dir, path string) error {
	if _, err := run(dir, "restore", "--staged", "--", path); err == nil {
		return nil
	}
	// repos with no commits yet have no HEAD to restore from
	_, err := run(dir, "rm", "--cached", "--quiet", "--", path)
	return err
}

// DiffFile returns the colored diff for one file: the staged (index) side or
// the unstaged (worktree) side. Untracked files diff against /dev/null.
func DiffFile(dir, path string, staged, untracked bool) (string, error) {
	if untracked {
		// --no-index exits 1 whenever the files differ; that's not an error
		out, err := run(dir, "diff", "--color=always", "--no-index", "--", "/dev/null", path)
		if out != "" {
			return out, nil
		}
		return out, err
	}
	args := []string{"diff", "--color=always"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	return run(dir, args...)
}

// FileHunks splits a file's plain diff into its header and hunks so single
// hunks can be applied to the index.
func FileHunks(dir, path string, staged bool) (header string, hunks []string, err error) {
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	out, err := run(dir, args...)
	if err != nil || out == "" {
		return "", nil, err
	}
	lines := strings.Split(out, "\n")
	i := 0
	for ; i < len(lines) && !strings.HasPrefix(lines[i], "@@"); i++ {
	}
	header = strings.Join(lines[:i], "\n")
	for i < len(lines) {
		j := i + 1
		for ; j < len(lines) && !strings.HasPrefix(lines[j], "@@"); j++ {
		}
		hunks = append(hunks, strings.Join(lines[i:j], "\n"))
		i = j
	}
	return header, hunks, nil
}

// ApplyToIndex applies a patch to the index only — staging a hunk, or with
// reverse, unstaging one.
func ApplyToIndex(dir, patch string, reverse bool) error {
	args := []string{"apply", "--cached", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "-R")
	}
	args = append(args, "-")
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(patch)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git apply: %s", msg)
	}
	return nil
}

// StagedDiff is the plain (uncolored) diff of everything in the index.
func StagedDiff(dir string) (string, error) {
	return run(dir, "diff", "--cached", "--no-color")
}

// CommitStaged commits what's in the index with a subject and optional body.
func CommitStaged(dir, subject, body string) error {
	args := []string{"commit", "-m", subject}
	if strings.TrimSpace(body) != "" {
		args = append(args, "-m", body)
	}
	_, err := run(dir, args...)
	return err
}

// Commit is one entry of the log.
type Commit struct {
	Short   string
	Author  string
	When    string // relative, e.g. "2 hours ago"
	Refs    string // decorations: branches and tags pointing here
	Subject string
	Body    string
}

// LogRow is one display line of the graph log: the rail drawing, and for
// rows that carry a commit, an index into GraphLog.Commits. A Divider row
// marks where the checked-out branch forked from the base ref.
type LogRow struct {
	Graph   string
	Commit  int // index into Commits; -1 for rail-only and divider rows
	Divider bool
}

// GraphLog is git log --all --graph with the rails redrawn in the
// box-drawing set panel borders use.
type GraphLog struct {
	Rows    []LogRow
	Commits []Commit // in display order, newest first
	BaseRef string   // ref the divider marks; "" when there is no divider
}

// prettyRail redraws git's ASCII graph rails with box-drawing characters.
func prettyRail(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '*':
			return '●'
		case '|':
			return '│'
		case '/':
			return '╱'
		case '\\':
			return '╲'
		case '_', '-':
			return '─'
		case '.':
			return '·'
		}
		return r
	}, s)
}

// Log returns up to n commits across all refs, newest first, each with its
// slice of the graph. The graph pass yields only rails and hashes — one line
// per commit — and a second pass fills in the details, so multi-line bodies
// never tangle with the rail drawing.
func Log(dir string, n int) (GraphLog, error) {
	var g GraphLog
	max := fmt.Sprintf("--max-count=%d", n)
	graph, err := run(dir, "log", "--all", "--graph", "--no-color", max, "--format=%x1f%h")
	if err != nil {
		return g, err
	}
	if graph == "" {
		return g, nil
	}
	details, err := run(dir, "log", "--all", max,
		"--format=%h%x1f%an%x1f%ar%x1f%D%x1f%s%x1f%b%x1e")
	if err != nil {
		return g, err
	}
	byHash := map[string]Commit{}
	for _, rec := range strings.Split(details, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 6)
		if len(f) < 5 {
			continue
		}
		c := Commit{Short: f[0], Author: f[1], When: f[2], Refs: f[3], Subject: f[4]}
		if len(f) == 6 {
			c.Body = strings.TrimSpace(f[5])
		}
		byHash[c.Short] = c
	}
	for _, line := range strings.Split(graph, "\n") {
		rail, hash, isCommit := strings.Cut(line, "\x1f")
		row := LogRow{Graph: prettyRail(rail), Commit: -1}
		if c, ok := byHash[hash]; isCommit && ok {
			g.Commits = append(g.Commits, c)
			row.Commit = len(g.Commits) - 1
		}
		g.Rows = append(g.Rows, row)
	}
	g.markBranchStart(dir)
	return g, nil
}

// markBranchStart inserts a divider row above the merge-base with the
// default base ref: everything the branch added since forking sits above it.
// On the base branch itself there is no fork, so no divider.
func (g *GraphLog) markBranchStart(dir string) {
	base := DefaultBase(dir)
	if base == "HEAD" {
		return
	}
	mb, err := run(dir, "merge-base", base, "HEAD")
	if err != nil || mb == "" {
		return
	}
	if head, err := run(dir, "rev-parse", "HEAD"); err != nil || head == mb {
		return
	}
	for i, row := range g.Rows {
		if row.Commit >= 0 && strings.HasPrefix(mb, g.Commits[row.Commit].Short) {
			rows := append([]LogRow{}, g.Rows[:i]...)
			rows = append(rows, LogRow{Divider: true, Commit: -1})
			g.Rows = append(rows, g.Rows[i:]...)
			g.BaseRef = base
			return
		}
	}
}

// CommitDiff returns the colored patch one commit introduced, with a
// diffstat ahead of it. A merge is shown against its first parent, which is
// the only reading of "what this commit changed" that makes sense.
func CommitDiff(dir, rev string) (string, error) {
	return run(dir, "show", "--color=always", "--format=", "--stat", "--patch",
		"-m", "--first-parent", rev)
}

// Prune drops worktree registrations whose directories no longer exist.
func Prune(root string) error {
	_, err := run(root, "worktree", "prune")
	return err
}

// Remove deletes the worktree at path. force discards uncommitted changes.
// ErrLocked reports a removal git refused because the worktree is locked.
// Callers can offer to override it — that is what Remove's unlock does.
var ErrLocked = errors.New("worktree is locked")

// Remove deletes a worktree. force discards uncommitted changes (git's -f),
// and unlock breaks a lock as well (git wants a second -f for that, and
// refuses outright without it).
func Remove(root, path string, force, unlock bool) error {
	args := []string{"worktree", "remove"}
	if force || unlock {
		args = append(args, "--force")
	}
	if unlock {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := run(root, args...); err != nil {
		if strings.Contains(err.Error(), "locked working tree") {
			return fmt.Errorf("%w — %s", ErrLocked, lockReasonFrom(err.Error()))
		}
		return err
	}
	return nil
}

// lockReasonFrom pulls the reason out of git's refusal, which reads
// "fatal: cannot remove a locked working tree, lock reason: <reason>".
func lockReasonFrom(msg string) string {
	if _, reason, ok := strings.Cut(msg, "lock reason: "); ok {
		if line, _, _ := strings.Cut(reason, "\n"); strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return "no reason given"
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

// ChangedLines reports which lines of one file the working tree changed
// against HEAD, keyed by 0-based line number: '+' for added lines, '~' for
// lines a hunk rewrote. An untracked file is new throughout.
func ChangedLines(dir, path string) (map[int]rune, error) {
	if st, err := run(dir, "status", "--porcelain", "--", path); err == nil && strings.HasPrefix(st, "??") {
		data, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			return nil, err
		}
		marks := map[int]rune{}
		for i := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			marks[i] = '+'
		}
		return marks, nil
	}
	out, err := run(dir, "diff", "-U0", "HEAD", "--", path)
	if err != nil {
		return nil, err // no HEAD yet, or not a repo: no gutter to show
	}
	return parseChangedLines(out), nil
}

// parseChangedLines pulls the new-side line ranges out of a -U0 diff's hunk
// headers: @@ -a[,b] +c[,d] @@ marks d lines from c, added when b is 0.
func parseChangedLines(diff string) map[int]rune {
	marks := map[int]rune{}
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "@@") {
			continue
		}
		fs := strings.Fields(line)
		if len(fs) < 3 {
			continue
		}
		_, oldN := hunkRange(fs[1])
		start, newN := hunkRange(fs[2])
		mark := '~'
		if oldN == 0 {
			mark = '+'
		}
		for i := 0; i < newN; i++ {
			marks[start-1+i] = mark
		}
	}
	return marks
}

func hunkRange(s string) (start, count int) {
	s = strings.TrimLeft(s, "+-")
	count = 1
	if a, c, ok := strings.Cut(s, ","); ok {
		start, _ = strconv.Atoi(a)
		count, _ = strconv.Atoi(c)
	} else {
		start, _ = strconv.Atoi(s)
	}
	return start, count
}

// GrepMatch is one matching line.
type GrepMatch struct {
	Line int // 1-based, as git reports it
	Text string
}

// GrepFile collects one file's matches, in the order git found them.
type GrepFile struct {
	Path    string
	Matches []GrepMatch
}

// Grep searches dir for a fixed, case-insensitive substring — tracked files
// and untracked ones alike, binaries and ignored paths left out — and returns
// the hits grouped by file. more reports that a cap cut the results short.
//
// git grep exits 1 with an empty stdout when nothing matches; that is an
// answer, not a failure, so only a non-empty stderr counts as an error.
func Grep(dir, query string, maxFiles, maxMatches int) (files []GrepFile, more bool, err error) {
	if strings.TrimSpace(query) == "" || dir == "" {
		return nil, false, nil
	}
	// -e keeps a query starting with a dash from being read as a flag
	out, runErr := run(dir, "grep", "-z", "-n", "-I", "-i", "-F",
		"--untracked", "--no-color", "-e", query)
	if out == "" {
		if runErr != nil && !strings.HasSuffix(runErr.Error(), "exit status 1") {
			return nil, false, runErr
		}
		return nil, false, nil
	}
	at := map[string]int{}
	total := 0
	for _, rec := range strings.Split(out, "\n") {
		path, line, text, ok := parseGrepRecord(rec)
		if !ok {
			continue
		}
		i, seen := at[path]
		if !seen {
			if len(files) >= maxFiles {
				more = true
				continue
			}
			files = append(files, GrepFile{Path: path})
			i = len(files) - 1
			at[path] = i
		}
		if total >= maxMatches {
			more = true
			break
		}
		files[i].Matches = append(files[i].Matches, GrepMatch{Line: line, Text: text})
		total++
	}
	return files, more, nil
}

// parseGrepRecord splits one `git grep -z -n` record: the path and the line
// number are NUL-terminated, the matching text runs to the end. NUL delimiters
// mean a path holding a colon or a space still parses.
func parseGrepRecord(rec string) (path string, line int, text string, ok bool) {
	p := strings.IndexByte(rec, 0)
	if p < 0 {
		return "", 0, "", false
	}
	rest := rec[p+1:]
	q := strings.IndexByte(rest, 0)
	if q < 0 {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(rest[:q])
	if err != nil {
		return "", 0, "", false
	}
	return rec[:p], n, rest[q+1:], true
}
