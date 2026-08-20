package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/markcipolla/treeline/internal/tmux"
)

// waitFor polls until the session's screen contains want.
func waitFor(t *testing.T, s *claudeSession, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.render(false), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never saw %q on screen; last frame:\n%s", want, s.render(false))
}

// TestPersistedSessionSurvivesClose is the whole point of the tmux backing:
// closing a pane must leave the program running, and starting it again must
// land back in the same session rather than a fresh one.
func TestPersistedSessionSurvivesClose(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
	restore := tmux.Socket
	tmux.Socket = "treeline-test" // never touch the user's real sessions
	t.Cleanup(func() { _ = tmux.KillAll(); tmux.Socket = restore })

	dir := t.TempDir()
	name := tmux.Name("persisttest", dir)

	first, err := startProgramSession(dir, 40, 10, true, "persisttest", "sh", "-c", "echo MARKER; sleep 60")
	if err != nil {
		t.Fatal(err)
	}
	if first.tmuxName != name {
		t.Fatalf("session name = %q, want %q", first.tmuxName, name)
	}
	waitFor(t, first, "MARKER")

	live, err := tmux.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].Name != name {
		t.Fatalf("List() = %+v, want just %q", live, name)
	}
	created := live[0].Created

	first.close() // as treeline does on quit

	// the program is still there, now detached
	after, err := tmux.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("session did not survive close: %+v", after)
	}
	if after[0].Attached {
		t.Error("session should be detached after close")
	}

	// starting the pane again reattaches: same session, and MARKER — printed
	// before the first close — is still on the screen tmux redraws for us.
	second, err := startProgramSession(dir, 40, 10, true, "persisttest", "sh", "-c", "echo NOT-REACHED; sleep 60")
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	waitFor(t, second, "MARKER")

	back, err := tmux.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || !back[0].Created.Equal(created) {
		t.Fatalf("reattach created a new session: %+v, want the one from %v", back, created)
	}
	if strings.Contains(second.render(false), "NOT-REACHED") {
		t.Error("reattaching should not have run the command again")
	}
}

// TestUnpersistedSessionDiesOnClose keeps the fallback path honest: without
// persistence the program is killed with the pane.
func TestUnpersistedSessionDiesOnClose(t *testing.T) {
	restore := tmux.Socket
	tmux.Socket = "treeline-test"
	t.Cleanup(func() { _ = tmux.KillAll(); tmux.Socket = restore })

	s, err := startProgramSession(t.TempDir(), 40, 10, false, "persisttest", "sh", "-c", "echo MARKER; sleep 60")
	if err != nil {
		t.Fatal(err)
	}
	if s.tmuxName != "" {
		t.Fatalf("tmuxName = %q, want empty when persistence is off", s.tmuxName)
	}
	waitFor(t, s, "MARKER")
	pid := s.cmd.Process.Pid
	s.close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !s.exited.Load() {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return // process reaped
	}
	t.Fatalf("process %d outlived its pane", pid)
}
