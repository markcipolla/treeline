package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/gitx"
)

func runeKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// logModel is a model parked in the git log with a few commits.
func logModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t, 200)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 80}) // room for a patch
	m = mm.(Model)
	m.gitFor = t.TempDir()
	m.commits = []gitx.Commit{
		{Short: "aaa1111", Author: "Test", When: "an hour ago", Subject: "newest", Body: "why it changed"},
		{Short: "bbb2222", Author: "Test", When: "a day ago", Subject: "middle"},
		{Short: "ccc3333", Author: "Test", When: "a week ago", Subject: "oldest"},
	}
	return m
}

// Opening the log asks for the selected commit's patch, and the pane says so
// until the patch lands.
func TestLogLoadsSelectedCommitDiff(t *testing.T) {
	m := logModel(t)
	cmd := m.openGitLog()
	if m.gitMode != gitModeLog {
		t.Fatalf("gitMode = %d, want gitModeLog", m.gitMode)
	}
	if cmd == nil {
		t.Fatal("opening the log issued no diff load")
	}
	msg, ok := cmd().(gitCommitDiffMsg)
	if !ok {
		t.Fatalf("expected a gitCommitDiffMsg, got %T", cmd())
	}
	if msg.rev != "aaa1111" {
		t.Errorf("loaded %q, want the selected commit aaa1111", msg.rev)
	}

	w, h := m.gitPaneSize()
	_, body := m.gitPaneContent(w, h)
	if !strings.Contains(body, "loading diff") {
		t.Errorf("body should say the patch is loading, got:\n%s", body)
	}

	mm, _ := m.Update(gitCommitDiffMsg{dir: m.gitFor, rev: "aaa1111", diff: "@@ -1 +1 @@\n+hello"})
	m = mm.(Model)
	_, body = m.gitPaneContent(w, h)
	for _, want := range []string{"aaa1111", "newest", "why it changed", "+hello"} {
		if !strings.Contains(body, want) {
			t.Errorf("log body missing %q, got:\n%s", want, body)
		}
	}
}

// Walking the log swaps the patch below it, and a patch that arrives for a
// commit the cursor has left is dropped.
func TestLogSelectionSwapsDiff(t *testing.T) {
	m := logModel(t)
	m.openGitLog()
	m.commitDiff, m.commitDiffFor = "old patch", "aaa1111"

	mm, cmd := m.keyGit(runeKey("j"))
	m = mm.(Model)
	if m.commitSel != 1 {
		t.Fatalf("commitSel = %d, want 1", m.commitSel)
	}
	if m.commitDiff != "" {
		t.Error("the previous commit's patch is still on screen")
	}
	if cmd == nil {
		t.Fatal("moving the cursor issued no diff load")
	}
	if msg := cmd().(gitCommitDiffMsg); msg.rev != "bbb2222" {
		t.Errorf("loaded %q, want bbb2222", msg.rev)
	}

	// the patch for the commit we moved off never lands
	mm, _ = m.Update(gitCommitDiffMsg{dir: m.gitFor, rev: "aaa1111", diff: "stale"})
	if got := mm.(Model); got.commitDiff != "" {
		t.Errorf("commitDiff = %q, want the stale patch dropped", got.commitDiff)
	}
	mm, _ = m.Update(gitCommitDiffMsg{dir: m.gitFor, rev: "bbb2222", diff: "fresh"})
	if got := mm.(Model); got.commitDiff != "fresh" {
		t.Errorf("commitDiff = %q, want \"fresh\"", got.commitDiff)
	}
}

// J and K scroll the patch under the log without moving the cursor.
func TestLogScrollsDiffWithoutMovingCursor(t *testing.T) {
	m := logModel(t)
	m.openGitLog()
	m.commitDiffFor = "aaa1111"
	m.commitDiff = strings.TrimSuffix(strings.Repeat("a line of patch\n", 200), "\n")

	mm, _ := m.keyGit(runeKey("J"))
	m = mm.(Model)
	if m.commitSel != 0 {
		t.Errorf("commitSel = %d, want the cursor to stay on 0", m.commitSel)
	}
	if m.commitDiffScroll != 5 {
		t.Errorf("commitDiffScroll = %d, want 5", m.commitDiffScroll)
	}
	w, h := m.gitPaneSize()
	if title, _ := m.gitPaneContent(w, h); !strings.Contains(title, "↓5") {
		t.Errorf("title should show the scroll offset, got %q", title)
	}

	mm, _ = m.keyGit(runeKey("K"))
	if got := mm.(Model); got.commitDiffScroll != 0 {
		t.Errorf("commitDiffScroll = %d, want 0 after scrolling back", got.commitDiffScroll)
	}
}

// A reloaded log that starts with a new commit pulls a fresh patch in.
func TestLogReloadRefreshesDiff(t *testing.T) {
	m := logModel(t)
	m.openGitLog()
	m.commitDiff, m.commitDiffFor = "old patch", "aaa1111"

	commits := append([]gitx.Commit{{Short: "ddd4444", Subject: "brand new"}}, m.commits...)
	mm, cmd := m.Update(gitLogMsg{dir: m.gitFor, commits: commits})
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("a reloaded log issued no diff load")
	}
	if msg := cmd().(gitCommitDiffMsg); msg.rev != "ddd4444" {
		t.Errorf("loaded %q, want ddd4444", msg.rev)
	}
	if m.commitDiff != "" {
		t.Errorf("commitDiff = %q, want the old patch cleared", m.commitDiff)
	}
}
