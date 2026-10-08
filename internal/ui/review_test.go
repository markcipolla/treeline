package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/github"
)

// stubTerm records the command the agent pane would have run.
func stubTerm(got *string) {
	startTerm = func(dir string, cols, rows int, persist bool, agent string) (*claudeSession, error) {
		*got = agent
		return nil, errors.New("claude sessions disabled in tests")
	}
}

// TestReviewFlow: ctrl+r on the new-worktree screen looks a pull request up,
// its worktree is created from the PR's head branch, and the agent pane opens
// with the review already asked.
func TestReviewFlow(t *testing.T) {
	m := newTestModel(t, 200)
	var gotAgent string
	stubTerm(&gotAgent)

	mm, _ := m.openManual()
	mm = keys(mm, "#123")
	mm, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := mm.(Model)
	if !got.fetchingPR || cmd == nil {
		t.Fatalf("ctrl+r did not start a PR lookup: fetching=%v cmd=%v", got.fetchingPR, cmd)
	}

	pr := github.PR{Number: 123, Title: "a title", HeadRefName: "pr/not-checked-out", URL: "https://example.com/pr/123"}
	mm, _ = got.Update(prFoundMsg{pr: &pr})
	got = mm.(Model)
	if got.fetchingPR || got.pendPR == nil || got.screen != scrCreating {
		t.Fatalf("after the lookup: fetching=%v pendPR=%v screen=%v", got.fetchingPR, got.pendPR, got.screen)
	}

	path := got.wts[1].Path // stands in for the worktree reviewWorktreeCmd makes
	mm, _ = got.Update(createdMsg{root: got.root, path: path, branchName: pr.HeadRefName})
	got = mm.(Model)
	if got.screen != scrMain || got.pane != paneClaude {
		t.Fatalf("a review lands elsewhere than the agent pane: screen=%v pane=%d", got.screen, got.pane)
	}
	if !strings.Contains(gotAgent, "claude --dangerously-skip-permissions '") ||
		!strings.Contains(gotAgent, "#123") || !strings.Contains(gotAgent, pr.URL) {
		t.Fatalf("agent command = %q", gotAgent)
	}
	if got.agentPrompt[path] != "" {
		t.Errorf("prompt still queued after the session started: %q", got.agentPrompt[path])
	}
}

// TestReviewExistingWorktree: a PR whose branch is already checked out is
// opened where it is instead of failing on the existing path.
func TestReviewExistingWorktree(t *testing.T) {
	m := newTestModel(t, 200)
	var gotAgent string
	stubTerm(&gotAgent)

	pr := github.PR{Number: 9, Title: "second", HeadRefName: m.wts[1].Branch, URL: "https://example.com/pr/9"}
	m.fetchingPR = true
	mm, _ := m.Update(prFoundMsg{pr: &pr})
	got := mm.(Model)
	if got.pendPR != nil || got.screen != scrMain || got.pane != paneClaude {
		t.Fatalf("pendPR=%v screen=%v pane=%d", got.pendPR, got.screen, got.pane)
	}
	if !strings.Contains(gotAgent, "#9") {
		t.Fatalf("agent command = %q", gotAgent)
	}
}

func TestShQuote(t *testing.T) {
	if got := shQuote("it's fine"); got != `'it'\''s fine'` {
		t.Errorf("shQuote = %s", got)
	}
}
