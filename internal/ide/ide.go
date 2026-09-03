// Package ide is treeline's editor pane as a component of its own: a file
// explorer over one worktree beside syntax-highlighted, editable views of its
// files. Treeline embeds it as the middle pane; cmd/tide ships the same pane
// alone as a small standalone editor. The host owns the app loop and the
// chrome — it feeds the pane keys, clicks and sizes, and draws what Content
// returns.
package ide

import (
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/tui"
)

// Pane is the component's whole state. It is a value the same way a bubbles
// model is: hosts keep it as a struct field and call its methods, and the
// pane reports work to do as tea.Cmds and messages (GrepMsg, GutterMsg,
// ChangedMsg) the host routes back in.
type Pane struct {
	// Err is the last complaint a user action raised — a refused save, an
	// unreadable file. The host shows it wherever its errors go and clears it
	// (or TakeErr does both).
	Err error

	ideFor       string          // worktree the pane shows
	want         string          // worktree the host last asked for (SetWorktree)
	ideExpanded  map[string]bool // unfolded directories, by rel path
	ideTree      []ideEntry      // visible rows, depth-first (or filter hits)
	ideSel       int             // cursor in the tree
	ideScroll    int             // tree window offset
	ideFocus     int             // which half has the keys: tree or file
	ideBufs      []*ideBuf       // open files, one tab each
	ideCur       int             // the active tab
	ideEditing   bool            // the textarea has the keys
	ideEditor    textarea.Model
	ideSelAnchor int             // line anchoring a shift+↑/↓ selection while editing; -1 none
	ideMultiLo   int             // first line of a multi-cursor block (ctrl+e on a selection); -1 none
	ideMultiCols []int           // per-line rune cursor of the block, from ideMultiLo down
	ideInput     textinput.Model // the ask-line: filter, find, search, new, rename
	ideInputKind int
	ideFilter    string          // applied tree filter
	ideFindQ     string          // in-file search query, kept for n/N
	ideFindHits  []int           // lines of the active buffer matching it
	ideGrepQ     string          // worktree-wide search query; non-empty shows results
	ideGrepFiles []gitx.GrepFile // hits grouped by file, as git found them
	ideGrepRows  []ideGrepRow    // the visible results list, folds applied
	ideGrepFold  map[string]bool // collapsed files in the results list
	ideGrepSel   int             // cursor in the results list
	ideGrepScrol int             // results window offset
	ideGrepMore  bool            // a cap cut the results short
	ideGrepping  bool            // a search is in flight
	ideConfirm   string          // pending destructive action awaiting a second press
	ideSavedAt   time.Time       // "saved ✓" flash

	width, height int  // content area, as the host last said (SetSize)
	focused       bool // the pane holds the app's focus (SetFocused)
	icons         bool // draw nerd-font file icons
	zones         *zone.Manager
}

// New builds a pane. zones is the host app's bubblezone manager — the pane
// marks its clickable rows through it, so it must be the one the host scans
// its frames with. icons draws nerd-font file glyphs in the tree and tabs.
func New(zones *zone.Manager, icons bool) Pane {
	// the pane draws the editor itself, highlighted (see ideEditorRows); the
	// textarea only keeps the buffer and cursor, and its width is set past
	// any real line so it never soft-wraps
	ed := textarea.New()
	ed.ShowLineNumbers = false
	ed.CharLimit = 0
	ed.MaxHeight = 0
	ed.MaxWidth = 0
	ed.Prompt = ""
	ed.SetWidth(ideEditorWidth)

	in := textinput.New()
	in.CharLimit = 200
	in.Prompt = "❯ "
	in.PromptStyle = cursorStyle

	return Pane{
		ideEditor:    ed,
		ideInput:     in,
		ideSelAnchor: -1,
		ideMultiLo:   -1,
		ideExpanded:  map[string]bool{},
		ideGrepFold:  map[string]bool{},
		zones:        zones,
		icons:        icons,
	}
}

// ChangedMsg announces that the pane changed the worktree — a save, a new
// file, a rename, a delete — so the host can refresh whatever else watches it
// (treeline reloads its git pane). tide ignores it.
type ChangedMsg struct{}

func changedCmd() tea.Cmd {
	return func() tea.Msg { return ChangedMsg{} }
}

// GrepMsg carries a finished worktree search; route it to ApplyGrep.
type GrepMsg = ideGrepMsg

// GutterMsg carries one file's git-changed lines; route it to ApplyGutter.
type GutterMsg = ideGutterMsg

// SetSize tells the pane its content area: the host's pane box less chrome.
func (p *Pane) SetSize(w, h int) {
	p.width, p.height = w, h
}

// SetInputWidth sizes the ask-line (the input bar drawn over the pane).
func (p *Pane) SetInputWidth(w int) { p.ideInput.Width = w }

// SetFocused tells the pane whether it holds the app's focus, which the
// cursor bands and the chrome tint follow.
func (p *Pane) SetFocused(on bool) { p.focused = on }

// SetWorktree points the pane at a worktree. Dirty buffers pin the pane
// where it is until they are saved or closed — the pane remembers the ask
// and follows once it is clean. The same worktree again is a cheap no-op.
func (p *Pane) SetWorktree(dir string) {
	p.want = dir
	if dir != p.ideFor {
		p.resetIDE(dir)
		return
	}
	if dir != "" && len(p.ideTree) == 0 && p.ideFilter == "" {
		p.refreshIDETree()
	}
}

// Dir is the worktree the pane currently shows.
func (p *Pane) Dir() string { return p.ideFor }

// Editing reports whether the editor buffer has the keys.
func (p *Pane) Editing() bool { return p.ideEditing }

// InputActive reports whether the ask-line (filter, find, search, new,
// rename) has the keys.
func (p *Pane) InputActive() bool { return p.ideInputKind != ideInputNone }

// FileFocused reports whether the file half (rather than the tree) has the
// keys — the host picks its help line by it.
func (p *Pane) FileFocused() bool { return p.ideFocus == ideFocusFile }

// AnyDirty reports whether any open buffer holds unsaved edits.
func (p *Pane) AnyDirty() bool { return p.ideAnyDirty() }

// TakeErr returns the pending error and clears it.
func (p *Pane) TakeErr() error {
	e := p.Err
	p.Err = nil
	return e
}

// Key handles one keystroke. The host routes every key here while the pane
// is focused; the pane's own modes (tree, file, editing, ask-line) decide
// what it means.
func (p *Pane) Key(k tea.KeyMsg) tea.Cmd { return p.keyIDE(k) }

// Click handles a mouse press inside the pane. x and y are the pointer's
// cell in the pane's body, ok false when the host couldn't place it (zone
// clicks still work). Focusing the pane afterwards is the host's business.
func (p *Pane) Click(msg tea.MouseMsg, x, y int, ok bool) tea.Cmd {
	return p.clickIDE(msg, x, y, ok)
}

// Scroll wheels whichever half of the pane the pointer is over. x is the
// pointer's column in the pane's body, ok false when unknown.
func (p *Pane) Scroll(x int, ok, up bool) { p.scrollIDERegion(x, ok, up) }

// Content renders the pane's title line and body for a w×h content area.
func (p *Pane) Content(w, h int) (title, body string) {
	return p.idePaneContent(w, h)
}

// OpenFile opens one file into a tab (or switches to its tab) and hands the
// keys to the file half — the host's way to jump the pane somewhere, the way
// treeline's git pane opens the file behind a diff.
func (p *Pane) OpenFile(rel string) tea.Cmd { return p.openIDEFile(rel) }

// RefreshDisk re-reads the tree and the open buffers after something else
// wrote to the worktree; dirty buffers are marked stale instead of reloaded.
func (p *Pane) RefreshDisk() []tea.Cmd { return p.refreshIDEDisk() }

// ApplyGrep folds a finished worktree search into the results list.
func (p *Pane) ApplyGrep(msg GrepMsg) { p.applyIDEGrepMsg(msg) }

// ApplyGutter folds one file's git-changed lines into its buffer.
func (p *Pane) ApplyGutter(msg GutterMsg) {
	if msg.dir != p.ideFor {
		return
	}
	for _, b := range p.ideBufs {
		if b.rel == msg.rel {
			b.gutter = msg.marks
		}
	}
}

// Tabs lists the open files, in tab order.
func (p *Pane) Tabs() []string {
	out := make([]string, len(p.ideBufs))
	for i, b := range p.ideBufs {
		out[i] = b.rel
	}
	return out
}

// ActiveFile is the active tab's file, "" with nothing open.
func (p *Pane) ActiveFile() string {
	if b := p.ideBuf(); b != nil {
		return b.rel
	}
	return ""
}

// TabCloseZoneID is the mouse zone of the active tab's ✕ — exposed for the
// host's click tests.
func TabCloseZoneID() string { return ideTabCloseZoneID() }

// TreeWidth is the explorer strip's width for a pane w columns wide — the
// host draws the divider through its chrome at this offset.
func TreeWidth(w int) int {
	return tui.ClampW(w*30/100, 12, 26)
}
