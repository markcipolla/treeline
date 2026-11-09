package ui

import (
	"testing"

	"github.com/markcipolla/treeline/internal/github"
	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
)

// TestReviewLinksCardAndPR: a review worktree whose branch carries no key in
// treeline's style still lands on its card's row — via Linear's branch style
// or the PR title — and the row shows the PR number.
func TestReviewLinksCardAndPR(t *testing.T) {
	m := newTestModel(t, 300)
	m.authed = true
	m.loadingWT, m.loadingIssues = false, false
	m.issues = []linear.Issue{
		{Identifier: "LAB-7", Title: "linear style", State: "In Review", StateType: "started"},
		{Identifier: "LAB-8", Title: "named in the title", State: "In Review", StateType: "started"},
	}
	m.wts = append(m.wts,
		gitx.Worktree{Root: m.root, Path: m.root + "/.worktrees/a", Branch: "alex/lab-7-linear-style"},
		gitx.Worktree{Root: m.root, Path: m.root + "/.worktrees/b", Branch: "fix-the-thing"},
	)
	m.prs = map[string]github.PR{
		"alex/lab-7-linear-style": {Number: 41},
		"fix-the-thing":           {Number: 42, Title: "[LAB-8] fix the thing"},
	}
	m.pane = paneIssues // focused, the grid grows to show every column
	m.resize()
	m.refreshRows()

	cols := m.table.Columns()
	prIdx := -1
	for i, c := range cols {
		if c.Title == "PR" {
			prIdx = i
		}
	}
	if prIdx < 0 || cols[prIdx].Width == 0 {
		t.Fatal("no visible PR column at 300 cols")
	}
	want := map[string]string{"LAB-7": "#41", "LAB-8": "#42"}
	for i, ref := range m.refs {
		if ref.kind != rowIssue || want[ref.issue.Identifier] == "" {
			continue
		}
		if ref.wt == nil {
			t.Errorf("%s has no worktree linked", ref.issue.Identifier)
			continue
		}
		if got := m.table.Rows()[i][prIdx]; got != want[ref.issue.Identifier] {
			t.Errorf("%s PR cell = %q, want %q", ref.issue.Identifier, got, want[ref.issue.Identifier])
		}
		delete(want, ref.issue.Identifier)
	}
	if len(want) > 0 {
		t.Errorf("cards missing from the table: %v", want)
	}
}

// TestReviewingGroup: worktrees opened from the review flow leave their
// status group (or WORKTREES) for a REVIEWING group at the bottom; others,
// PR or not, stay where they were.
func TestReviewingGroup(t *testing.T) {
	m := newTestModel(t, 300)
	m.authed = true
	m.loadingWT, m.loadingIssues = false, false
	m.issues = []linear.Issue{
		{Identifier: "LAB-7", Title: "reviewed", State: "In Review", StateType: "started"},
		{Identifier: "LAB-8", Title: "mine", State: "In Review", StateType: "started"},
	}
	m.wts = append(m.wts,
		gitx.Worktree{Root: m.root, Path: m.root + "/.worktrees/a", Branch: "alex/lab-7-reviewed", Review: true},
		gitx.Worktree{Root: m.root, Path: m.root + "/.worktrees/b", Branch: "feature/LAB-8/mine"},
		gitx.Worktree{Root: m.root, Path: m.root + "/.worktrees/c", Branch: "sam-cleanup", Review: true},
	)
	m.prs = map[string]github.PR{"feature/LAB-8/mine": {Number: 2}}
	m.refreshRows()

	var headers []string
	section := map[string]string{} // row → the header it sits under
	cur := ""
	for i, ref := range m.refs {
		title := m.table.Rows()[i][1]
		switch {
		case ref.kind == rowHeader:
			cur = title
			headers = append(headers, title)
		case ref.issue != nil:
			section[ref.issue.Identifier] = cur
		case ref.wt != nil:
			section[ref.wt.Branch] = cur
		}
	}
	if len(headers) < 2 || headers[len(headers)-1] != "▸ REVIEWING" || headers[len(headers)-2] != "▸ WORKTREES" {
		t.Fatalf("headers = %v, want ... WORKTREES, REVIEWING", headers)
	}
	for k, want := range map[string]string{"LAB-7": "▸ REVIEWING", "sam-cleanup": "▸ REVIEWING", "LAB-8": "▸ IN REVIEW"} {
		if section[k] != want {
			t.Errorf("%s under %q, want %q", k, section[k], want)
		}
	}
}
