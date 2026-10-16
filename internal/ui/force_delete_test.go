package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/gitx"
)

func deleteModel(t *testing.T, wt gitx.Worktree) Model {
	t.Helper()
	m := New(&config.Config{BranchTypes: []string{"feature"}, SlugMaxLen: 48}, t.TempDir())
	m.width, m.height = 120, 40
	m.delTarget = &wt
	m.screen = scrDeleteConfirm
	m.removing = true
	return m
}

// A lock is not a dead end: the modal stays up and asks again, this time
// offering to break the lock.
func TestLockedRemovalOffersForce(t *testing.T) {
	wt := gitx.Worktree{Path: "/repo/.worktrees/LMAP-9", Branch: "feature/LMAP-9/x"}
	m := deleteModel(t, wt)

	err := fmt.Errorf("%w — claude session encapsulated-shimmying-sparrow (pid 65825)", gitx.ErrLocked)
	out, _ := m.Update(removedMsg{err: err})
	m = out.(Model)

	if m.screen != scrDeleteConfirm {
		t.Fatalf("screen = %v, want the remove modal to stay open", m.screen)
	}
	if !m.delForce {
		t.Error("the modal should escalate to a force confirmation")
	}
	if m.delTarget == nil {
		t.Fatal("the target must survive for the second attempt")
	}
	if m.delFocus != 2 {
		t.Errorf("delFocus = %d, want cancel (2) to be the default on a force prompt", m.delFocus)
	}
	if m.removing {
		t.Error("removing should have finished")
	}

	view := m.viewDeleteConfirm()
	for _, want := range []string{"Force remove locked worktree?", "force remove", "encapsulated-shimmying-sparrow"} {
		if !strings.Contains(view, want) {
			t.Errorf("modal should mention %q, got:\n%s", want, view)
		}
	}
}

// Any other failure is a plain error, not something to force past.
func TestOtherRemovalErrorDoesNotOfferForce(t *testing.T) {
	m := deleteModel(t, gitx.Worktree{Path: "/repo/.worktrees/x", Branch: "b"})
	out, _ := m.Update(removedMsg{err: errors.New("git worktree: fatal: something else")})
	m = out.(Model)
	if m.delForce {
		t.Error("only a lock should offer a force removal")
	}
}

// Once the force attempt is under way a second refusal must not loop back into
// the same prompt.
func TestForceRefusalIsReported(t *testing.T) {
	m := deleteModel(t, gitx.Worktree{Path: "/repo/.worktrees/x", Branch: "b"})
	m.delForce = true
	out, _ := m.Update(removedMsg{err: fmt.Errorf("%w — still locked", gitx.ErrLocked)})
	m = out.(Model)
	if m.err == nil {
		t.Error("a refusal after forcing should surface as an error")
	}
}

// Cancelling forgets the escalation, so the next delete starts from the plain
// confirmation rather than offering to force straight away.
func TestCancelClearsTheEscalation(t *testing.T) {
	m := deleteModel(t, gitx.Worktree{Path: "/repo/.worktrees/x", Branch: "b"})
	m.delForce, m.removing = true, false
	m.cancelDelete()
	if m.delForce || m.delTarget != nil || m.screen != scrMain {
		t.Errorf("cancel left state behind: force=%v target=%v screen=%v", m.delForce, m.delTarget, m.screen)
	}
}

// A worktree already known to be locked says so before the first attempt.
func TestModalNamesAKnownLock(t *testing.T) {
	wt := gitx.Worktree{Path: "/repo/.worktrees/x", Branch: "b", Locked: true, LockReason: "claude session sparrow"}
	m := deleteModel(t, wt)
	m.removing = false
	if view := m.viewDeleteConfirm(); !strings.Contains(view, "claude session sparrow") {
		t.Errorf("modal should name the lock up front, got:\n%s", view)
	}
}

// Confirming the removal closes the modal: the worktree goes while the main
// screen is back up, rather than behind a spinner in the modal.
func TestConfirmingRemovalReturnsToTheMainScreen(t *testing.T) {
	m := newTestModel(t, 200)
	if !m.selectWorktree(m.wts[1].Path) {
		t.Fatal("could not select the second worktree")
	}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	asked := out.(Model)
	if asked.screen != scrDeleteConfirm {
		t.Fatalf("screen = %v, want the removal to be confirmed first", asked.screen)
	}

	out, cmd := asked.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	got := out.(Model)
	if got.screen != scrMain {
		t.Errorf("screen = %v, want the modal closed on confirmation", got.screen)
	}
	if !got.removing || cmd == nil {
		t.Errorf("removing=%v cmd=%v, want the removal under way", got.removing, cmd != nil)
	}
	if view := got.View(); !strings.Contains(view, "removing the worktree…") {
		t.Error("the main screen should say a removal is in flight")
	}
}

// A failed removal is what brings the modal up.
func TestRemovalFailureOpensTheModal(t *testing.T) {
	m := deleteModel(t, gitx.Worktree{Path: "/repo/.worktrees/x", Branch: "b"})
	m.screen = scrMain
	out, _ := m.Update(removedMsg{err: errors.New("git worktree: fatal: something else")})
	got := out.(Model)
	if got.screen != scrDeleteConfirm {
		t.Fatalf("screen = %v, want the modal to report the failure", got.screen)
	}
	if view := got.viewDeleteConfirm(); !strings.Contains(view, "Removing failed") {
		t.Errorf("modal should say the removal failed, got:\n%s", view)
	}
}
