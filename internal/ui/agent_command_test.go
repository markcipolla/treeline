package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keys(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// TestAgentCommandField: the settings screen edits the agent command on its
// top row and writes it to config.json, leaving the repo rows below it.
func TestAgentCommandField(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := newTestModel(t, 200)
	if got := m.cfg.Agent(); got != "claude --dangerously-skip-permissions" {
		t.Fatalf("default agent = %q", got)
	}

	mm, _ := m.openSettings()
	if v := mm.(Model).View(); !strings.Contains(v, "agent command") {
		t.Fatalf("settings screen has no agent field:\n%s", v)
	}
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !mm.(Model).agentEdit {
		t.Fatal("enter on the agent row did not start editing")
	}
	mm = keys(mm, "codex")
	mm, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})

	got := mm.(Model)
	if got.agentEdit || got.cfg.AgentCommand != "codex" {
		t.Fatalf("after enter: editing=%v command=%q", got.agentEdit, got.cfg.AgentCommand)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "treeline", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		AgentCommand string `json:"agent_command"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.AgentCommand != "codex" {
		t.Errorf("config.json agent_command = %q", saved.AgentCommand)
	}
}
