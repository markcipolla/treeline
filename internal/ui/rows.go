package ui

import (
	"fmt"
	"path/filepath"
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

// issueKeyFromBranch extracts an issue key from a branch name following the
// type/KEY/slug convention (any segment that parses as a key counts).
func issueKeyFromBranch(name string) string {
	for _, seg := range strings.Split(name, "/") {
		if k := branch.ParseIssueKey(seg); k != "" {
			return k
		}
	}
	return ""
}

// worktreeForKey returns the worktree whose branch carries the issue key.
func (m Model) worktreeForKey(key string) *gitx.Worktree {
	for i := range m.wts {
		wt := &m.wts[i]
		if !wt.IsPrimary && !wt.Prunable && issueKeyFromBranch(wt.Branch) == key {
			return wt
		}
	}
	return nil
}

// branchForKey finds an existing branch already carrying the issue key —
// local first, then remote-tracking (e.g. origin/feature/LAB-1/slug).
func (m Model) branchForKey(key string) (local, remote string) {
	locals, remotes := gitx.Branches(m.root)
	for _, b := range locals {
		if issueKeyFromBranch(b) == key {
			return b, ""
		}
	}
	for _, b := range remotes {
		if issueKeyFromBranch(b) == key {
			return "", b
		}
	}
	return "", ""
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
