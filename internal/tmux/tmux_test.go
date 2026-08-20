package tmux

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNameIsStableAndAddressable(t *testing.T) {
	dir := "/repo/.worktrees/LMAP-142-fix-login"
	got := Name("claude", dir)
	if got != Name("claude", dir) {
		t.Fatalf("Name is not stable: %q", got)
	}
	if strings.ContainsAny(got, ".: \t") {
		t.Errorf("name %q contains characters tmux reserves for addressing", got)
	}
	if !strings.HasPrefix(got, "claude-LMAP-142-fix-login-") {
		t.Errorf("name %q should stay readable", got)
	}
	if Name("shell", dir) == got {
		t.Error("the claude and shell panes must not share a session")
	}
}

func TestNameSeparatesSameNamedWorktrees(t *testing.T) {
	a := Name("claude", "/one/.worktrees/LMAP-142")
	b := Name("claude", "/two/.worktrees/LMAP-142")
	if a == b {
		t.Errorf("same-named worktrees in different repos collided: %q", a)
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"feature/LMAP-1.2":      "feature-LMAP-1-2",
		"..":                    "dir",
		"":                      "dir",
		"plain_name-9":          "plain_name-9",
		strings.Repeat("x", 60): strings.Repeat("x", 40),
	} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandAttachesOrCreates(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	cmd := Command("claude-x", "/tmp", "claude")
	args := strings.Join(cmd.Args[1:], " ")
	for _, want := range []string{"-L " + Socket, "new-session -A", "-s claude-x", "-c /tmp", "claude"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	if cmd.Dir != "/tmp" {
		t.Errorf("cmd.Dir = %q, want /tmp", cmd.Dir)
	}
	if filepath.Base(cmd.Path) != "tmux" {
		t.Errorf("cmd.Path = %q, want the tmux binary", cmd.Path)
	}
}

func TestCommandQuotesTheProgram(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	cmd := Command("shell-x", "/tmp", "/opt/my shell/zsh", "-l")
	last := cmd.Args[len(cmd.Args)-1]
	if last != `'/opt/my shell/zsh' -l` {
		t.Errorf("shell-command = %q, want the path quoted as one word", last)
	}
}

func TestEnvDropsTheOuterTmux(t *testing.T) {
	got := Env([]string{"PATH=/bin", "TMUX=/private/tmp/x,1,0", "TMUX_PANE=%3", "TERM=xterm"})
	want := []string{"PATH=/bin", "TERM=xterm"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("Env = %v, want %v", got, want)
	}
}

func TestListWithNoServer(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	restore := Socket
	Socket = "treeline-test-absent"
	defer func() { Socket = restore }()

	got, err := List()
	if err != nil {
		t.Fatalf("List with no server should be empty, not an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List = %+v, want empty", got)
	}
	if err := KillAll(); err != nil {
		t.Errorf("KillAll with no server should be a no-op: %v", err)
	}
}
