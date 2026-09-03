package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/markcipolla/treeline/internal/ide"
)

// The ui state file is where the user was when treeline last exited: written
// on the way out, restored on the next launch of the same repo — so a restart
// (a hot-reload one included) lands back in place instead of at the top.
// Dirty buffers carry their text; everything else reloads from disk.

type uiState struct {
	Selected string    `json:"selected,omitempty"` // worktree path under the cursor
	Pane     int       `json:"pane"`
	IDE      ide.State `json:"ide"`
}

// stateFile is ~/.config/treeline/state/<repo>-<hash>.json (XDG respected):
// one file per repo, named readably, keyed on the full path.
func stateFile(root string) (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	sum := sha256.Sum256([]byte(root))
	name := filepath.Base(root) + "-" + hex.EncodeToString(sum[:4]) + ".json"
	return filepath.Join(base, "treeline", "state", name), nil
}

// captureUIState snapshots the user's place for the next launch.
func (m Model) captureUIState() uiState {
	s := uiState{Pane: m.pane, IDE: m.ide.Capture()}
	if ref := m.selectedRef(); ref.wt != nil {
		s.Selected = ref.wt.Path
	}
	return s
}

// saveUIState writes the snapshot, best effort — a failed save must never
// block the exit.
func (m Model) saveUIState() {
	p, err := stateFile(m.root)
	if err != nil {
		return
	}
	data, err := json.MarshalIndent(m.captureUIState(), "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0o600) // 0600: dirty buffers may hold anything
}

func loadUIState(root string) *uiState {
	p, err := stateFile(root)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var s uiState
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	return &s
}
