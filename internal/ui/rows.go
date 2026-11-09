package ui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/markcipolla/treeline/internal/branch"
	"github.com/markcipolla/treeline/internal/github"
	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
)

// rowKind describes what a table row stands for. Header rows are group
// separators the cursor skips over.
type rowKind int

const (
	rowHeader rowKind = iota
	rowIssue
	rowWorktree
)

type rowRef struct {
	kind  rowKind
	issue *linear.Issue
	wt    *gitx.Worktree // for rowIssue: the linked worktree, if any
}

// keys identifies what a row points at — a card, a worktree, or both when the
// card has one checked out — so the cursor can follow the same thing when the
// table is rebuilt underneath it. Cards arriving from Linear shift every row
// index, and a worktree listed on its own merges into its card's row.
func (r rowRef) keys() []string {
	var ks []string
	if r.issue != nil {
		ks = append(ks, "issue:"+r.issue.Identifier)
	}
	if r.wt != nil {
		ks = append(ks, "wt:"+r.wt.Path)
	}
	return ks
}

// sameRow reports whether two rows stand for the same card or worktree.
func sameRow(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// stateRank orders status groups: active work first, then queued, then the
// noise. Unknown types sort last.
func stateRank(stateType string) int {
	switch stateType {
	case "started":
		return 0
	case "unstarted":
		return 1
	case "triage":
		return 2
	case "backlog":
		return 3
	}
	return 4
}

// linearSegRe matches Linear's own branch style, user/lab-123-slug, where the
// key leads a segment instead of being one.
var linearSegRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*-\d+)(?:-|$)`)

// titleKeyRe finds a key in a PR title ("LAB-12: fix", "[LAB-12] fix").
// Uppercase only: lowercase words-with-numbers in prose aren't keys.
var titleKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-\d+\b`)

// issueKeyFromBranch extracts an issue key from a branch name following the
// type/KEY/slug convention (any segment that parses as a key counts), or
// Linear's user/key-slug one.
func issueKeyFromBranch(name string) string {
	segs := strings.Split(name, "/")
	for _, seg := range segs {
		if k := branch.ParseIssueKey(seg); k != "" {
			return k
		}
	}
	for _, seg := range segs {
		if sm := linearSegRe.FindStringSubmatch(seg); sm != nil {
			return strings.ToUpper(sm[1])
		}
	}
	return ""
}

// wtKey is the card a worktree is working on: the key its branch carries,
// else one its open PR's title names — a review of someone else's branch
// rarely follows our naming.
func (m Model) wtKey(wt gitx.Worktree) string {
	if k := issueKeyFromBranch(wt.Branch); k != "" {
		return k
	}
	if pr, ok := m.prs[wt.Branch]; ok {
		return titleKeyRe.FindString(pr.Title)
	}
	return ""
}

// prCell is the PR column: the open pull request for the worktree's branch.
func (m Model) prCell(wt *gitx.Worktree) string {
	if pr, ok := m.prs[wt.Branch]; ok && wt.Branch != "" {
		return fmt.Sprintf("#%d", pr.Number)
	}
	return ""
}

// worktreeForKey returns the worktree whose branch carries the issue key.
func (m Model) worktreeForKey(key string) *gitx.Worktree {
	for i := range m.wts {
		wt := &m.wts[i]
		if !wt.IsPrimary && !wt.Prunable && m.wtKey(*wt) == key {
			return wt
		}
	}
	return nil
}

// worktreeForBranch finds the worktree a branch is checked out in.
func (m Model) worktreeForBranch(name string) *gitx.Worktree {
	for i := range m.wts {
		wt := &m.wts[i]
		if !wt.IsPrimary && !wt.Prunable && wt.Branch == name {
			return wt
		}
	}
	return nil
}

// branchForKey finds an existing branch already carrying the issue key in
// any operating repo — local first, then remote-tracking.
func (m Model) branchForKey(key string) (root, local, remote string) {
	for _, r := range m.repos {
		locals, remotes := gitx.Branches(r.path)
		for _, b := range locals {
			if issueKeyFromBranch(b) == key {
				return r.path, b, ""
			}
		}
		for _, b := range remotes {
			if issueKeyFromBranch(b) == key {
				return r.path, "", b
			}
		}
	}
	return "", "", ""
}

// wtLabel names a worktree's location, relative to the repo it belongs to.
// Foreign worktrees carry their repo name here when the REPO column is not
// showing it — with one repo registered, or on a terminal too narrow for it.
func (m Model) wtLabel(wt *gitx.Worktree) string {
	if wt.Root == m.root || wt.Root == "" || m.repoColumnVisible() {
		return relPath(m.repoRoot(wt), wt.Path)
	}
	return m.repoFor(wt.Root).name + ":" + relPath(wt.Root, wt.Path)
}

// repoColumnVisible reports whether the table is currently showing the REPO
// column: it is out of the set with a single repo, and squeezed to zero width
// on a narrow terminal.
func (m Model) repoColumnVisible() bool {
	for _, c := range m.table.Columns() {
		if c.Title == "REPO" {
			return c.Width > 0
		}
	}
	return false
}

// repoRoot is the primary checkout a worktree hangs off, defaulting to the
// repo treeline was launched in.
func (m Model) repoRoot(wt *gitx.Worktree) string {
	if wt == nil || wt.Root == "" {
		return m.root
	}
	return wt.Root
}

// wtRepoName names the repo a worktree belongs to, for the REPO column. Cards
// with no worktree leave the cell blank: nothing ties them to a repo yet.
func (m Model) wtRepoName(wt *gitx.Worktree) string {
	if wt == nil {
		return ""
	}
	return m.repoFor(m.repoRoot(wt)).name
}

// paneLabel names a working directory for pane titles: the linked Linear
// card's key when the worktree branch carries one, else the dir basename.
func (m Model) paneLabel(dir string) string {
	for i := range m.wts {
		wt := &m.wts[i]
		if wt.Path == dir {
			if k := m.wtKey(*wt); k != "" {
				return k + " · " + filepath.Base(dir)
			}
			break
		}
	}
	return filepath.Base(dir)
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func wtStatus(wt gitx.Worktree) string {
	if wt.Prunable {
		return "⚠ missing"
	}
	var parts []string
	if wt.Dirty {
		parts = append(parts, "✚ dirty")
	}
	if wt.HasUpstream && (wt.Ahead > 0 || wt.Behind > 0) {
		parts = append(parts, fmt.Sprintf("↑%d ↓%d", wt.Ahead, wt.Behind))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, " ")
}

func ciSymbol(s github.Status) string {
	switch s {
	case github.StatusOK:
		return "✓"
	case github.StatusFail:
		return "✗"
	case github.StatusRunning:
		return "●"
	}
	return ""
}

func issueHaystack(is linear.Issue, wt *gitx.Worktree, root string) string {
	hay := is.Identifier + " " + is.Title + " " + is.State + " " + strings.Join(is.Labels, " ")
	if wt != nil {
		hay += " " + wt.Branch + " " + relPath(root, wt.Path)
	}
	return strings.ToLower(hay)
}

func wtHaystack(wt gitx.Worktree, root string) string {
	return strings.ToLower(wt.Branch + " " + relPath(root, wt.Path))
}
