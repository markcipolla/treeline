package ui

import (
	"testing"

	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
)

// hasCard reports whether the issues column drew a row for an issue key.
func hasCard(m Model, key string) bool {
	for _, row := range m.table.Rows() {
		if row[0] == key {
			return true
		}
	}
	return false
}

// wtFor builds a worktree whose branch carries the key the way branch.Name
// writes it — its own path segment.
func wtFor(root, key string) gitx.Worktree {
	return gitx.Worktree{Root: root, Path: root + "/.worktrees/" + key, Branch: "feature/" + key + "/work"}
}

// assigneeModel seeds one card per ownership case, with no worktrees.
func assigneeModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t, 160)
	m.authed = true
	m.loadingWT, m.loadingIssues = false, false
	m.wts = nil
	m.issues = []linear.Issue{
		{Identifier: "LAB-1", Title: "Mine", State: "Backlog", StateType: "backlog",
			Assignee: "Mark Cipolla", AssigneeIsMe: true},
		{Identifier: "LAB-2", Title: "Someone else's", State: "Backlog", StateType: "backlog",
			Assignee: "Alexandra Kowalczyk"},
		{Identifier: "LAB-3", Title: "Unassigned", State: "Backlog", StateType: "backlog"},
	}
	m.refreshRows()
	return m
}

// TestOtherPeoplesCardsDropWithoutAWorktree: the column is yours — someone
// else's card is not in it unless a worktree says you are working on it.
func TestOtherPeoplesCardsDropWithoutAWorktree(t *testing.T) {
	m := assigneeModel(t)

	if !hasCard(m, "LAB-1") {
		t.Error("your own card should stay")
	}
	if hasCard(m, "LAB-2") {
		t.Error("someone else's card with no worktree should have dropped")
	}
	if !hasCard(m, "LAB-3") {
		t.Error("an unassigned card is nobody else's, so it should stay")
	}
}

// TestOtherPeoplesCardStaysWithAWorktree: a card you have a worktree for is
// one you are working on, whoever it is assigned to.
func TestOtherPeoplesCardStaysWithAWorktree(t *testing.T) {
	m := assigneeModel(t)
	m.wts = append(m.wts, wtFor(m.root, "LAB-2"))
	m.refreshRows()

	if !hasCard(m, "LAB-2") {
		t.Fatal("someone else's card with a worktree should stay")
	}

	// and the moment that worktree goes, so does the card — the symptom that
	// started this: removing a worktree left the card behind
	m.wts = nil
	m.refreshRows()
	if hasCard(m, "LAB-2") {
		t.Error("the card should have gone with its worktree")
	}
}

// TestExtraIssueDropsWhenItsWorktreeGoes: cards fetched only because a
// worktree referenced them (reviews, hand-offs) are held in extraIssues,
// which is not rebuilt until the next fetch. The column must not show a
// stale one.
func TestExtraIssueDropsWhenItsWorktreeGoes(t *testing.T) {
	m := assigneeModel(t)
	m.issues = m.issues[:1] // only your own card is assigned to you
	m.extraIssues = []linear.Issue{
		{Identifier: "LAB-9", Title: "A review", State: "In Review", StateType: "started",
			Assignee: "Alexandra Kowalczyk"},
	}
	m.wts = append(m.wts, wtFor(m.root, "LAB-9"))
	m.refreshRows()
	if !hasCard(m, "LAB-9") {
		t.Fatal("a worktree-referenced card should show while its worktree exists")
	}

	// the worktree is removed but extraIssues still holds the card
	m.wts = nil
	m.refreshRows()
	if hasCard(m, "LAB-9") {
		t.Error("a stale extra card outlived its worktree")
	}
}

// TestPrunableWorktreeDoesNotHoldACard: a stale registration is not work in
// progress, so it does not keep someone else's card in the column.
func TestPrunableWorktreeDoesNotHoldACard(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*gitx.Worktree)
	}{
		{"prunable", func(w *gitx.Worktree) { w.Prunable = true }},
		{"primary", func(w *gitx.Worktree) { w.IsPrimary = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := assigneeModel(t)
			wt := wtFor(m.root, "LAB-2")
			tc.mut(&wt)
			m.wts = append(m.wts, wt)
			m.refreshRows()
			if hasCard(m, "LAB-2") {
				t.Errorf("a %s worktree should not hold someone else's card", tc.name)
			}
		})
	}
}

// TestShowIssueRule states the rule on its own, away from the table.
func TestShowIssueRule(t *testing.T) {
	mine := linear.Issue{Assignee: "Mark Cipolla", AssigneeIsMe: true}
	theirs := linear.Issue{Assignee: "Alexandra Kowalczyk"}
	nobodys := linear.Issue{}
	wt := &gitx.Worktree{Branch: "feature/LAB-2/work"}

	for _, tc := range []struct {
		name string
		is   linear.Issue
		wt   *gitx.Worktree
		want bool
	}{
		{"mine, no worktree", mine, nil, true},
		{"mine, worktree", mine, wt, true},
		{"theirs, no worktree", theirs, nil, false},
		{"theirs, worktree", theirs, wt, true},
		{"unassigned, no worktree", nobodys, nil, true},
	} {
		if got := showIssue(tc.is, tc.wt); got != tc.want {
			t.Errorf("%s: showIssue = %v, want %v", tc.name, got, tc.want)
		}
	}
}
