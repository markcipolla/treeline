package ide

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
)

// State is the pane's place, snapshotted for the next launch: which worktree,
// what was unfolded, the open tabs — dirty buffers carrying their text, so a
// restart never eats an edit. The host persists it however it persists things.
type State struct {
	For      string     `json:"for,omitempty"` // worktree the pane showed
	Expanded []string   `json:"expanded,omitempty"`
	Sel      int        `json:"sel"`
	Scroll   int        `json:"scroll"`
	Focus    int        `json:"focus"`
	Cur      int        `json:"cur"`
	Bufs     []BufState `json:"bufs,omitempty"`
}

// BufState is one open tab in a State.
type BufState struct {
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

// Capture snapshots the pane for the next launch.
func (p *Pane) Capture() State {
	p.stashIDEBuf() // fold live editor text into the active buffer first
	s := State{For: p.ideFor, Sel: p.ideSel, Scroll: p.ideScroll,
		Focus: p.ideFocus, Cur: p.ideCur}
	for rel, on := range p.ideExpanded {
		if on {
			s.Expanded = append(s.Expanded, rel)
		}
	}
	sort.Strings(s.Expanded)
	for _, b := range p.ideBufs {
		bs := BufState{Rel: b.rel, Cursor: b.cursor, ScrollY: b.scrollY}
		if b.dirty {
			bs.Dirty = true
			bs.Val = b.val
			bs.Saved = hashText(b.savedVal)
		}
		s.Bufs = append(s.Bufs, bs)
	}
	return s
}

// ApplyState reopens the pane the way it was left: folders unfolded, the
// same tabs in the same order, dirty text laid back over what disk holds now —
// marked stale when disk moved on underneath. Call after SetWorktree pointed
// the pane at the restored worktree.
func (p *Pane) ApplyState(s State) tea.Cmd {
	for _, rel := range s.Expanded {
		p.ideExpanded[rel] = true
	}
	p.refreshIDETree()
	var cmds []tea.Cmd
	for _, bs := range s.Bufs {
		if _, err := os.Stat(filepath.Join(p.ideFor, bs.Rel)); err != nil {
			continue // the file is gone; its tab goes with it
		}
		before := len(p.ideBufs)
		cmds = append(cmds, p.openIDEFile(bs.Rel))
		if len(p.ideBufs) == before { // binary, too large, …: skip quietly
			p.Err = nil
			continue
		}
		b := p.ideBufs[len(p.ideBufs)-1]
		if bs.Dirty {
			if hashText(b.savedVal) != bs.Saved {
				b.stale = true // disk changed under the persisted edits
			}
			b.val = bs.Val
			b.dirty = b.val != b.savedVal
			b.hl = highlightSource(b.val, b.rel)
			p.ideEditor.SetValue(b.val)
			p.alignIDEEditor()
		}
		b.cursor = clampIdx(bs.Cursor, len(b.hl))
		b.scrollY = bs.ScrollY
	}
	if len(p.ideBufs) > 0 {
		p.activateIDEBuf(clampIdx(s.Cur, len(p.ideBufs)))
		p.settleIDEView()
	}
	p.ideFocus = ideFocusTree
	if s.Focus == ideFocusFile && p.ideBuf() != nil {
		p.ideFocus = ideFocusFile
	}
	p.ideSel = clampIdx(s.Sel, len(p.ideTree))
	p.ideScroll = s.Scroll
	p.settleIDETree()
	return tea.Batch(cmds...)
}
