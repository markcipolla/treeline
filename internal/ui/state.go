package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
)

// The ui state file is where the user was when treeline last exited: written
// on the way out, restored on the next launch of the same repo — so a restart
// (a hot-reload one included) lands back in place instead of at the top.
// Dirty buffers carry their text; everything else reloads from disk.

type uiState struct {
	Selected string   `json:"selected,omitempty"` // worktree path under the cursor
	Pane     int      `json:"pane"`
	IDE      ideState `json:"ide"`
}

type ideState struct {
	For      string     `json:"for,omitempty"` // worktree the pane showed
	Expanded []string   `json:"expanded,omitempty"`
	Sel      int        `json:"sel"`
	Scroll   int        `json:"scroll"`
	Focus    int        `json:"focus"`
	Cur      int        `json:"cur"`
	Bufs     []bufState `json:"bufs,omitempty"`
}

type bufState struct {
	Rel     string `json:"rel"`
	Cursor  int    `json:"cursor"`
	ScrollY int    `json:"scroll_y"`
	Dirty   bool   `json:"dirty,omitempty"`
	Val     string `json:"val,omitempty"`   // unsaved text, dirty buffers only
	Saved   string `json:"saved,omitempty"` // hash of the disk text the edits sit on
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
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
	m.stashIDEBuf() // fold live editor text into the active buffer first
	s := uiState{Pane: m.pane}
	if ref := m.selectedRef(); ref.wt != nil {
		s.Selected = ref.wt.Path
	}
	ide := ideState{For: m.ideFor, Sel: m.ideSel, Scroll: m.ideScroll,
		Focus: m.ideFocus, Cur: m.ideCur}
	for rel, on := range m.ideExpanded {
		if on {
			ide.Expanded = append(ide.Expanded, rel)
		}
	}
	sort.Strings(ide.Expanded)
	for _, b := range m.ideBufs {
		bs := bufState{Rel: b.rel, Cursor: b.cursor, ScrollY: b.scrollY}
		if b.dirty {
			bs.Dirty = true
			bs.Val = b.val
			bs.Saved = hashText(b.savedVal)
		}
		ide.Bufs = append(ide.Bufs, bs)
	}
	s.IDE = ide
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

// applyIDEState reopens the pane the way it was left: folders unfolded, the
// same tabs in the same order, dirty text laid back over what disk holds now —
// marked stale when disk moved on underneath. Runs after resetIDE pointed the
// pane at the restored worktree.
func (m *Model) applyIDEState(s ideState) tea.Cmd {
	for _, rel := range s.Expanded {
		m.ideExpanded[rel] = true
	}
	m.refreshIDETree()
	var cmds []tea.Cmd
	for _, bs := range s.Bufs {
		if _, err := os.Stat(filepath.Join(m.ideFor, bs.Rel)); err != nil {
			continue // the file is gone; its tab goes with it
		}
		before := len(m.ideBufs)
		cmds = append(cmds, m.openIDEFile(bs.Rel))
		if len(m.ideBufs) == before { // binary, too large, …: skip quietly
			m.err = nil
			continue
		}
		b := m.ideBufs[len(m.ideBufs)-1]
		if bs.Dirty {
			if hashText(b.savedVal) != bs.Saved {
				b.stale = true // disk changed under the persisted edits
			}
			b.val = bs.Val
			b.dirty = b.val != b.savedVal
			b.hl = highlightSource(b.val, b.rel)
			m.ideEditor.SetValue(b.val)
			m.alignIDEEditor()
		}
		b.cursor = clampIdx(bs.Cursor, len(b.hl))
		b.scrollY = bs.ScrollY
	}
	if len(m.ideBufs) > 0 {
		m.activateIDEBuf(clampIdx(s.Cur, len(m.ideBufs)))
		m.settleIDEView()
	}
	m.ideFocus = ideFocusTree
	if s.Focus == ideFocusFile && m.ideBuf() != nil {
		m.ideFocus = ideFocusFile
	}
	m.ideSel = clampIdx(s.Sel, len(m.ideTree))
	m.ideScroll = s.Scroll
	m.settleIDETree()
	return tea.Batch(cmds...)
}
