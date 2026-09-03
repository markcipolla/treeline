package ide

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/markcipolla/treeline/internal/gitx"
)

// The ide pane sits between agent and git: a file explorer over the selected
// worktree beside syntax-highlighted views of the open files (a scaled-down
// port of croft, github.com/vitali87/croft). Files open into tabs; browsing
// is read-only, e drops into an editable buffer, ctrl+s writes it back, esc
// climbs back out — editor → file view → tree. A git gutter marks the lines
// the worktree changed against HEAD.

// which half of the pane the keyboard works
const (
	ideFocusTree = iota
	ideFocusFile
)

// what the pane's input line is asking for
const (
	ideInputNone = iota
	ideInputFilter
	ideInputFind
	ideInputGrep
	ideInputNew
	ideInputRename
)

// ideMaxFileSize keeps the editor to files a textarea buffer is comfortable
// with; anything bigger is for a real editor in the shell pane.
const ideMaxFileSize = 1 << 20

// ideLiveHLMax is the largest buffer re-highlighted on every keystroke while
// editing. Past it the colors pause until esc — better plain than laggy.
const ideLiveHLMax = 256 << 10

// ideEditorWidth is the width the hidden textarea believes it has: wide
// enough that it never soft-wraps, so its rows stay the buffer's logical
// lines and its LineInfo speaks in columns of the real line. The pane draws
// the editor itself (highlighted, from b.hl) — the widget only keeps state.
const ideEditorWidth = 4000

// caps on the filter's recursive walk, so typing into a monorepo stays snappy
const (
	ideFilterMaxHits  = 200
	ideFilterMaxWalk  = 20000
	ideFilterMaxDepth = 16
)

// caps on the worktree search, so a one-letter query in a monorepo returns a
// list worth reading instead of every line in the repo
const (
	ideGrepMaxFiles   = 200
	ideGrepMaxMatches = 1000
)

// ideEntry is one visible row of the explorer tree.
type ideEntry struct {
	rel   string // path relative to the worktree root
	name  string
	depth int
	dir   bool
}

// ideBuf is one open file: its buffer, everything needed to save it
// faithfully, and its render state.
type ideBuf struct {
	rel      string
	val      string   // buffer text, kept current on every edit
	savedVal string   // as last loaded/saved (the textarea's flattened form)
	rawLines []string // the file's own lines: tabs and exotic bytes intact
	crlf     bool
	hl       []string     // highlighted rows, one per buffer line
	gutter   map[int]rune // 0-based line → '+' added / '~' changed vs HEAD
	cursor   int
	scrollY  int
	dirty    bool
	stale    bool // the disk changed under unsaved edits
	modTime  time.Time
}

func ideZoneID(i int) string     { return "ide:t:" + strconv.Itoa(i) }
func ideGrepZoneID(i int) string { return "ide:g:" + strconv.Itoa(i) }
func ideTabZoneID(i int) string  { return "ide:tab:" + strconv.Itoa(i) }
func ideTabCloseZoneID() string  { return "ide:tabx" }

// ideBuf is the active tab's buffer, nil with nothing open.
func (p *Pane) ideBuf() *ideBuf {
	if p.ideCur < 0 || p.ideCur >= len(p.ideBufs) {
		return nil
	}
	return p.ideBufs[p.ideCur]
}

func (p *Pane) ideAnyDirty() bool {
	for _, b := range p.ideBufs {
		if b.dirty {
			return true
		}
	}
	return false
}

// resetIDE points the pane at another worktree. Dirty buffers pin the pane
// where it is — moving the cursor through the issues list must not throw away
// edits — until they are saved or closed; saving and closing re-sync.
func (p *Pane) resetIDE(dir string) {
	if p.ideAnyDirty() {
		return
	}
	p.ideFor = dir
	p.ideTree = nil
	p.ideSel, p.ideScroll = 0, 0
	p.ideExpanded = map[string]bool{}
	p.ideBufs, p.ideCur = nil, 0
	p.ideEditor.SetValue("")
	p.ideEditor.Blur()
	p.ideEditing = false
	p.ideSelAnchor = -1
	p.clearIDEMulti()
	p.ideFocus = ideFocusTree
	p.closeIDEInput()
	p.ideFilter, p.ideFindQ, p.ideFindHits = "", "", nil
	p.clearIDEGrep()
	p.ideConfirm = ""
	if dir != "" {
		p.refreshIDETree()
	}
}

// ---- explorer tree ----

// refreshIDETree rebuilds the visible tree from disk: the worktree root's
// entries with expanded directories inlined depth-first, or — while a filter
// is typed — the paths that match it, found by a capped recursive walk.
func (p *Pane) refreshIDETree() {
	sel := ""
	if p.ideSel < len(p.ideTree) {
		sel = p.ideTree[p.ideSel].rel
	}
	if p.ideFilter != "" {
		p.ideTree = p.ideFilterWalk(p.ideFilter)
	} else {
		p.ideTree = p.ideTreeLevel("", 0)
	}
	if sel != "" {
		for i, e := range p.ideTree {
			if e.rel == sel {
				p.ideSel = i
				break
			}
		}
	}
	if p.ideSel >= len(p.ideTree) {
		p.ideSel = len(p.ideTree) - 1
	}
	if p.ideSel < 0 {
		p.ideSel = 0
	}
	p.settleIDETree()
}

func (p *Pane) ideTreeLevel(rel string, depth int) []ideEntry {
	ents, err := os.ReadDir(filepath.Join(p.ideFor, rel))
	if err != nil {
		return nil
	}
	sort.Slice(ents, func(i, j int) bool {
		if ents[i].IsDir() != ents[j].IsDir() {
			return ents[i].IsDir()
		}
		return strings.ToLower(ents[i].Name()) < strings.ToLower(ents[j].Name())
	})
	var out []ideEntry
	for _, e := range ents {
		if ideSkipDir(e.Name()) && e.IsDir() {
			continue
		}
		crel := filepath.Join(rel, e.Name())
		out = append(out, ideEntry{rel: crel, name: e.Name(), depth: depth, dir: e.IsDir()})
		if e.IsDir() && p.ideExpanded[crel] {
			out = append(out, p.ideTreeLevel(crel, depth+1)...)
		}
	}
	return out
}

// ideSkipDir names directories the explorer and the filter walk stay out of:
// git internals, dependency trees, and the worktrees nested inside the repo.
func ideSkipDir(name string) bool {
	return name == ".git" || name == "node_modules" || name == ".worktrees"
}

// ideFilterWalk finds paths matching the filter, flat, capped so a huge repo
// costs a bounded walk. Matching is a case-insensitive substring of the
// relative path.
func (p *Pane) ideFilterWalk(q string) []ideEntry {
	q = strings.ToLower(q)
	var out []ideEntry
	visited := 0
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		if len(out) >= ideFilterMaxHits || visited >= ideFilterMaxWalk || depth > ideFilterMaxDepth {
			return
		}
		ents, err := os.ReadDir(filepath.Join(p.ideFor, rel))
		if err != nil {
			return
		}
		sort.Slice(ents, func(i, j int) bool {
			if ents[i].IsDir() != ents[j].IsDir() {
				return ents[i].IsDir()
			}
			return strings.ToLower(ents[i].Name()) < strings.ToLower(ents[j].Name())
		})
		for _, e := range ents {
			if e.IsDir() && ideSkipDir(e.Name()) {
				continue
			}
			visited++
			crel := filepath.Join(rel, e.Name())
			if strings.Contains(strings.ToLower(crel), q) && len(out) < ideFilterMaxHits {
				out = append(out, ideEntry{rel: crel, name: crel, depth: 0, dir: e.IsDir()})
			}
			if e.IsDir() {
				walk(crel, depth+1)
			}
		}
	}
	walk("", 0)
	return out
}

// ---- buffers ----

// openIDEFile loads a file into a tab — or switches to it if it already has
// one — and asks git which of its lines the worktree changed.
func (p *Pane) openIDEFile(rel string) tea.Cmd {
	for i, b := range p.ideBufs {
		if b.rel == rel {
			p.activateIDEBuf(i)
			p.ideFocus = ideFocusFile
			return nil
		}
	}
	data, err := os.ReadFile(filepath.Join(p.ideFor, rel))
	if err != nil {
		p.Err = err
		return nil
	}
	if len(data) > ideMaxFileSize {
		p.Err = fmt.Errorf("%s is %dKB — too large for the ide pane", rel, len(data)/1024)
		return nil
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		p.Err = errors.New(rel + " looks binary")
		return nil
	}
	b := &ideBuf{rel: rel}
	fillIDEBuf(b, data)
	if fi, err := os.Stat(filepath.Join(p.ideFor, rel)); err == nil {
		b.modTime = fi.ModTime()
	}
	// stash before appending: with no tabs open, ideCur would already point
	// at the new buffer, and an activate-style stash would clobber its text
	// with whatever the idle editor held
	p.stashIDEBuf()
	p.ideBufs = append(p.ideBufs, b)
	p.ideCur = len(p.ideBufs) - 1
	p.ideEditor.SetValue(b.val)
	p.alignIDEEditor()
	p.ideFocus = ideFocusFile
	p.recomputeIDEFind()
	return loadIDEGutterCmd(p.ideFor, rel)
}

// fillIDEBuf loads file bytes into a buffer. The buffer holds a display
// form — ideSanitize flattens what the textarea would flatten — while the
// file's own lines are kept aside so a save can reconstruct whatever an edit
// didn't reach (restoreWhitespace).
func fillIDEBuf(b *ideBuf, data []byte) {
	text := string(data)
	b.crlf = strings.Contains(text, "\r\n")
	if b.crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	// a stray \r would become its own line in the buffer and shear the
	// line-for-line alignment the whitespace restore depends on
	text = strings.ReplaceAll(text, "\r", "\n")
	b.rawLines = strings.Split(text, "\n")
	b.val = ideSanitize(text)
	b.savedVal = b.val
	b.hl = highlightSource(b.val, b.rel)
	b.dirty, b.stale = false, false
}

// ideSanitize is what the textarea does to inserted text, applied up front so
// the buffer and the editor's round-trip agree byte for byte: tabs become
// four spaces, line breaks fold to \n, other control runes drop.
func ideSanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
		case r == '\r' || r == '\n':
			b.WriteByte('\n')
		case r == '\t':
			b.WriteString("    ")
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stashIDEBuf folds the live editor text back into the active buffer before
// the editor is pointed elsewhere.
func (p *Pane) stashIDEBuf() {
	if b := p.ideBuf(); b != nil {
		b.val = p.ideEditor.Value()
		b.dirty = b.val != b.savedVal
	}
}

// activateIDEBuf makes tab i current and loads its text into the editor.
func (p *Pane) activateIDEBuf(i int) {
	if i < 0 || i >= len(p.ideBufs) {
		return
	}
	p.stashIDEBuf()
	p.ideCur = i
	b := p.ideBufs[i]
	p.ideEditor.SetValue(b.val)
	p.alignIDEEditor()
	p.recomputeIDEFind()
}

// switchIDEBuf moves delta tabs over, wrapping.
func (p *Pane) switchIDEBuf(delta int) {
	if n := len(p.ideBufs); n > 1 {
		p.activateIDEBuf(((p.ideCur+delta)%n + n) % n)
	}
}

// closeIDEBuf drops tab i; the neighbour (or the tree) takes over.
func (p *Pane) closeIDEBuf(i int) {
	if i < 0 || i >= len(p.ideBufs) {
		return
	}
	p.ideBufs = append(p.ideBufs[:i:i], p.ideBufs[i+1:]...)
	if p.ideCur > i || p.ideCur >= len(p.ideBufs) {
		p.ideCur--
	}
	if b := p.ideBuf(); b != nil {
		p.ideEditor.SetValue(b.val)
		p.alignIDEEditor()
	} else {
		p.ideCur = 0
		p.ideEditor.SetValue("")
		p.ideEditing = false
		p.ideSelAnchor = -1
		p.clearIDEMulti()
		p.ideEditor.Blur()
		p.ideFocus = ideFocusTree
	}
	p.recomputeIDEFind()
	// closing the last dirty buffer releases a pane pinned to an old worktree
	if p.want != p.ideFor {
		p.resetIDE(p.want)
	}
}

// alignIDEEditor moves the buffer's cursor to the view's current line, so
// editing starts where the reader was looking.
func (p *Pane) alignIDEEditor() {
	cur := 0
	if b := p.ideBuf(); b != nil {
		cur = b.cursor
	}
	p.setIDECursorAt(cur, 0)
}

// setIDECursorAt walks the hidden editor's cursor to a buffer row and column.
// The textarea only exposes relative row moves, so the row is reached by
// stepping; the column clamps to the line.
func (p *Pane) setIDECursorAt(row, col int) {
	for p.ideEditor.Line() > row {
		p.ideEditor.CursorUp()
	}
	for p.ideEditor.Line() < row {
		before := p.ideEditor.Line()
		p.ideEditor.CursorDown()
		if p.ideEditor.Line() == before {
			break // the buffer ran out of lines under the cursor
		}
	}
	p.ideEditor.SetCursor(col)
}

// saveIDEBuf writes the active buffer back, keeping the file's mode, and
// nudges the git pane — a save is exactly the kind of edit it exists to show.
// A file that changed on disk since it was loaded is not overwritten unless
// force says so (R); r reloads it instead.
func (p *Pane) saveIDEBuf(force bool) tea.Cmd {
	b := p.ideBuf()
	if b == nil {
		return nil
	}
	p.stashIDEBuf()
	path := filepath.Join(p.ideFor, b.rel)
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
		if !force && !fi.ModTime().Equal(b.modTime) {
			b.stale = true
			p.Err = errors.New(b.rel + " changed on disk — r reloads it (drops your edits), R saves anyway")
			return nil
		}
	}
	out := restoreWhitespace(b.rawLines, strings.Split(b.savedVal, "\n"), strings.Split(b.val, "\n"))
	content := strings.Join(out, "\n")
	if b.crlf {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		p.Err = err
		return nil
	}
	b.rawLines = out
	b.savedVal = b.val
	b.dirty, b.stale = false, false
	if fi, err := os.Stat(path); err == nil {
		b.modTime = fi.ModTime()
	}
	p.ideSavedAt = time.Now()
	b.hl = highlightSource(b.val, b.rel)
	cmds := []tea.Cmd{changedCmd(), loadIDEGutterCmd(p.ideFor, b.rel)}
	// a save may release a pane pinned by its dirty buffers (see resetIDE)
	if p.want != p.ideFor && !p.ideAnyDirty() {
		p.resetIDE(p.want)
	}
	return tea.Batch(cmds...)
}

// reloadIDEBuf re-reads a buffer's file from disk, dropping whatever the
// buffer held, and keeps the reader's place.
func (p *Pane) reloadIDEBuf(b *ideBuf) tea.Cmd {
	data, err := os.ReadFile(filepath.Join(p.ideFor, b.rel))
	if err != nil {
		b.stale = true
		return nil
	}
	cursor, scroll := b.cursor, b.scrollY
	fillIDEBuf(b, data)
	b.cursor = clampIdx(cursor, len(b.hl))
	b.scrollY = clampIdx(scroll, len(b.hl))
	if fi, err := os.Stat(filepath.Join(p.ideFor, b.rel)); err == nil {
		b.modTime = fi.ModTime()
	}
	if b == p.ideBuf() {
		p.ideEditor.SetValue(b.val)
		p.alignIDEEditor()
		p.recomputeIDEFind()
	}
	return loadIDEGutterCmd(p.ideFor, b.rel)
}

// refreshIDEDisk is the pane's answer to files changing under it — agent
// and shell commands edit the worktree constantly. The tree re-reads, clean
// buffers follow the disk, and dirty ones are marked stale rather than
// silently losing either side.
func (p *Pane) refreshIDEDisk() []tea.Cmd {
	if p.ideFor == "" {
		return nil
	}
	p.refreshIDETree()
	var cmds []tea.Cmd
	for _, b := range p.ideBufs {
		fi, err := os.Stat(filepath.Join(p.ideFor, b.rel))
		if err != nil {
			b.stale = true // deleted or unreadable; saving would need force
			continue
		}
		if fi.ModTime().Equal(b.modTime) {
			continue
		}
		if b.dirty {
			b.stale = true
			continue
		}
		if cmd := p.reloadIDEBuf(b); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// ---- whitespace restore ----

// restoreWhitespace rebuilds the file from the edited buffer, keeping the
// original bytes of every line the edit didn't reach: raw is the file as
// loaded, base its form in the buffer (tabs flattened), cur the buffer now.
// A minimal line diff of base against cur finds the untouched lines, so
// separate edit regions don't bleed into each other; on a rewrite too large
// for the capped search, the common prefix and suffix stand in.
func restoreWhitespace(raw, base, cur []string) []string {
	if len(raw) != len(base) {
		return cur // lost the alignment somehow; trust the buffer
	}
	if keep := diffKeep(base, cur); keep != nil {
		out := make([]string, len(cur))
		for j := range cur {
			if i := keep[j]; i >= 0 {
				out[j] = raw[i]
			} else {
				out[j] = cur[j]
			}
		}
		return out
	}
	p := 0
	for p < len(base) && p < len(cur) && base[p] == cur[p] {
		p++
	}
	s := 0
	for s < len(base)-p && s < len(cur)-p &&
		base[len(base)-1-s] == cur[len(cur)-1-s] {
		s++
	}
	out := append([]string{}, raw[:p]...)
	out = append(out, cur[p:len(cur)-s]...)
	return append(out, raw[len(raw)-s:]...)
}

// diffKeep maps each line of b to the line of a it kept in a minimal edit
// script (Myers), or -1 where it was typed. nil when the edit distance blows
// past the cap — the caller has a cruder answer for that.
func diffKeep(a, b []string) []int {
	n, mm := len(a), len(b)
	limit := n + mm
	const dCap = 400
	if limit > dCap {
		limit = dCap
	}
	off := limit
	v := make([]int, 2*limit+1)
	var trace [][]int
	dd := -1
	for d := 0; d <= limit && dd < 0; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < mm && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= mm {
				dd = d
				break
			}
		}
		trace = append(trace, append([]int(nil), v...))
	}
	if dd < 0 {
		return nil
	}
	keep := make([]int, mm)
	for i := range keep {
		keep[i] = -1
	}
	x, y := n, mm
	for d := dd; d > 0; d-- {
		v := trace[d-1]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[off+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY { // the snake: lines both sides kept
			keep[y-1] = x - 1
			x--
			y--
		}
		x, y = prevX, prevY
	}
	for x > 0 && y > 0 {
		keep[y-1] = x - 1
		x--
		y--
	}
	return keep
}

// ---- highlighting ----

// highlightSource renders source through chroma, lexer picked by filename.
func highlightSource(src, filename string) []string {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	styleName := "monokai"
	if !lipgloss.HasDarkBackground() {
		styleName = "friendly"
	}
	style := styles.Get(styleName)
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return strings.Split(src, "\n")
	}
	// tokens are formatted a line at a time: a token spanning lines (a comment
	// block, a raw string) would otherwise carry its color escape only on its
	// first line, and the rest would render plain once the rows are split
	f := formatters.Get("terminal256")
	var out []string
	for _, line := range chroma.SplitTokensIntoLines(it.Tokens()) {
		for i, tok := range line {
			line[i].Value = strings.TrimSuffix(tok.Value, "\n")
		}
		var b strings.Builder
		if err := f.Format(&b, style, chroma.Literator(line...)); err != nil {
			return strings.Split(src, "\n")
		}
		out = append(out, b.String())
	}
	// chroma drops the empty line a trailing newline implies; the buffer has
	// it, and the view's rows must line up with the buffer's for the cursor
	for len(out) < strings.Count(src, "\n")+1 {
		out = append(out, "")
	}
	return out
}

// ---- git gutter ----

type ideGutterMsg struct {
	dir   string
	rel   string
	marks map[int]rune
}

// loadIDEGutterCmd asks git which lines of the file the worktree changed.
func loadIDEGutterCmd(dir, rel string) tea.Cmd {
	if dir == "" {
		return nil
	}
	return func() tea.Msg {
		marks, _ := gitx.ChangedLines(dir, rel) // no gutter is fine
		return ideGutterMsg{dir: dir, rel: rel, marks: marks}
	}
}

// ---- worktree search ----

// ideGrepRow is one row of the results list: a file header, or one matching
// line under it.
type ideGrepRow struct {
	rel  string
	line int    // 1-based; 0 on a header
	text string // the matching line; the match count on a header
	hdr  bool
}

// ideGrepMsg carries a finished search back to the model. The query rides
// along so a stale result — the user kept typing — can be dropped.
type ideGrepMsg struct {
	dir   string
	query string
	files []gitx.GrepFile
	more  bool
	err   error
}

// grepIDECmd runs the worktree search off the update loop: git grep over a
// monorepo is far too slow to block a keystroke on.
func grepIDECmd(dir, query string) tea.Cmd {
	if dir == "" || strings.TrimSpace(query) == "" {
		return nil
	}
	return func() tea.Msg {
		files, more, err := gitx.Grep(dir, query, ideGrepMaxFiles, ideGrepMaxMatches)
		return ideGrepMsg{dir: dir, query: query, files: files, more: more, err: err}
	}
}

// ideGrepActive reports whether the results list has taken over the tree half.
func (p *Pane) ideGrepActive() bool { return p.ideGrepQ != "" }

// rebuildIDEGrepRows flattens the grouped hits into the visible list: a header
// per file, its matches beneath unless the file is folded.
func (p *Pane) rebuildIDEGrepRows() {
	sel := ""
	if p.ideGrepSel < len(p.ideGrepRows) {
		r := p.ideGrepRows[p.ideGrepSel]
		sel = r.rel + ":" + strconv.Itoa(r.line)
	}
	p.ideGrepRows = nil
	for _, f := range p.ideGrepFiles {
		p.ideGrepRows = append(p.ideGrepRows, ideGrepRow{
			rel:  f.Path,
			text: strconv.Itoa(len(f.Matches)),
			hdr:  true,
		})
		if p.ideGrepFold[f.Path] {
			continue
		}
		for _, hit := range f.Matches {
			p.ideGrepRows = append(p.ideGrepRows, ideGrepRow{
				rel:  f.Path,
				line: hit.Line,
				text: hit.Text,
			})
		}
	}
	if sel != "" {
		for i, r := range p.ideGrepRows {
			if r.rel+":"+strconv.Itoa(r.line) == sel {
				p.ideGrepSel = i
				break
			}
		}
	}
	p.ideGrepSel = clampIdx(p.ideGrepSel, len(p.ideGrepRows))
	p.settleIDEGrep()
}

// clearIDEGrep puts the file tree back.
func (p *Pane) clearIDEGrep() {
	p.ideGrepQ = ""
	p.ideGrepFiles, p.ideGrepRows = nil, nil
	p.ideGrepFold = map[string]bool{}
	p.ideGrepSel, p.ideGrepScrol = 0, 0
	p.ideGrepMore, p.ideGrepping = false, false
}

// settleIDEGrep keeps the selected result inside the list's window.
func (p *Pane) settleIDEGrep() {
	h := p.ideContentH()
	if p.ideGrepSel < p.ideGrepScrol {
		p.ideGrepScrol = p.ideGrepSel
	}
	if p.ideGrepSel >= p.ideGrepScrol+h {
		p.ideGrepScrol = p.ideGrepSel - h + 1
	}
}

func (p *Pane) moveIDEGrepSel(n int) {
	p.ideGrepSel = clampIdx(p.ideGrepSel+n, len(p.ideGrepRows))
	p.settleIDEGrep()
}

// openIDEGrepSel acts on the selected result: a header folds, a match opens
// its file at that line.
func (p *Pane) openIDEGrepSel() tea.Cmd {
	if p.ideGrepSel >= len(p.ideGrepRows) {
		return nil
	}
	r := p.ideGrepRows[p.ideGrepSel]
	if r.hdr {
		p.ideGrepFold[r.rel] = !p.ideGrepFold[r.rel]
		p.rebuildIDEGrepRows()
		return nil
	}
	return p.openIDEFileAt(r.rel, r.line)
}

// jumpIDEGrep moves to the next (dir>0) or previous match row — headers
// skipped — and opens it, so a whole result set can be walked with one key.
func (p *Pane) jumpIDEGrep(dir int) tea.Cmd {
	for i := p.ideGrepSel + dir; i >= 0 && i < len(p.ideGrepRows); i += dir {
		if p.ideGrepRows[i].hdr {
			continue
		}
		p.ideGrepSel = i
		p.settleIDEGrep()
		return p.openIDEFileAt(p.ideGrepRows[i].rel, p.ideGrepRows[i].line)
	}
	return nil
}

// openIDEFileAt opens a file and parks the cursor on one line. The worktree
// query is handed to the in-file find as well, so the lines that matched are
// marked in the file view the result just jumped into.
func (p *Pane) openIDEFileAt(rel string, line int) tea.Cmd {
	cmd := p.openIDEFile(rel)
	b := p.ideBuf()
	if b == nil || b.rel != rel {
		return cmd // the open failed; p.Err already says why
	}
	if p.ideGrepQ != "" {
		p.ideFindQ = p.ideGrepQ
		p.recomputeIDEFind()
	}
	b.cursor = clampIdx(line-1, len(b.hl))
	p.settleIDEView()
	return cmd
}

// ---- find ----

// recomputeIDEFind rebuilds the match list for the active buffer.
func (p *Pane) recomputeIDEFind() {
	p.ideFindHits = nil
	b := p.ideBuf()
	if b == nil || p.ideFindQ == "" {
		return
	}
	q := strings.ToLower(p.ideFindQ)
	for i, ln := range strings.Split(b.val, "\n") {
		if strings.Contains(strings.ToLower(ln), q) {
			p.ideFindHits = append(p.ideFindHits, i)
		}
	}
}

// jumpIDEFind moves the cursor to the next (dir>0) or previous match,
// wrapping around the file.
func (p *Pane) jumpIDEFind(dir int) {
	b := p.ideBuf()
	if b == nil || len(p.ideFindHits) == 0 {
		return
	}
	if dir > 0 {
		for _, h := range p.ideFindHits {
			if h > b.cursor {
				b.cursor = h
				p.settleIDEView()
				return
			}
		}
		b.cursor = p.ideFindHits[0]
	} else {
		for i := len(p.ideFindHits) - 1; i >= 0; i-- {
			if p.ideFindHits[i] < b.cursor {
				b.cursor = p.ideFindHits[i]
				p.settleIDEView()
				return
			}
		}
		b.cursor = p.ideFindHits[len(p.ideFindHits)-1]
	}
	p.settleIDEView()
}

// ---- layout & rendering ----

// idePaneSize is the pane's inner content area, as the host last told it
// (SetSize) — treeline's layout box less the chrome, or tide's window.
func (p *Pane) idePaneSize() (w, h int) {
	return p.width, p.height
}

// ideTreeWidth is the explorer strip's width: a quarter-ish column, held
// steady whether or not a file is open so the pane doesn't reshape itself
// the moment one is.
func (p *Pane) ideTreeWidth(w int) int {
	return TreeWidth(w)
}

// ideContentH is the pane rows left for the halves once the input bar (when
// open) is taken off the top — three rows, the border included.
func (p *Pane) ideContentH() int {
	_, h := p.idePaneSize()
	if p.ideInputKind != ideInputNone {
		h -= 3
	}
	if h < 1 {
		h = 1
	}
	return h
}

// ideViewH is the file half's content rows: the bordered tab bar's three rows
// sit above them.
func (p *Pane) ideViewH() int {
	h := p.ideContentH() - 3
	if h < 1 {
		h = 1
	}
	return h
}

// idePaneContent renders the pane's title and body for a w×h content area.
func (p *Pane) idePaneContent(w, h int) (string, string) {
	b := p.ideBufs
	var cur *ideBuf
	if p.ideCur >= 0 && p.ideCur < len(b) {
		cur = b[p.ideCur]
	}
	title := "ide"
	if cur != nil {
		title += " — " + cur.rel
		switch {
		case cur.dirty:
			title += warnStyle.Render(" ●")
		case time.Now().Before(p.ideSavedAt.Add(2 * time.Second)):
			title += okStyle.Render(" · saved ✓")
		}
		if cur.stale {
			title += errStyle.Render(" · changed on disk")
		}
	}
	switch {
	case p.ideEditing:
		switch lo, hi, sel := p.ideSelSpan(); {
		case p.ideMultiLo >= 0:
			title += okStyle.Render(fmt.Sprintf(" · %d cursors", len(p.ideMultiCols))) +
				dimStyle.Render(" — typing edits every line, esc drops")
		case sel:
			title += okStyle.Render(fmt.Sprintf(" · %d lines", hi-lo+1)) +
				dimStyle.Render(" — tab indents, ctrl+e adds cursors, esc drops")
		default:
			title += okStyle.Render(" · editing") + dimStyle.Render(" — ctrl+s saves, esc views")
		}
	case p.ideAnyDirty() && p.ideFor != p.want:
		title += warnStyle.Render(" · unsaved edits hold this worktree")
	case p.ideFindQ != "" && cur != nil:
		title += dimStyle.Render(fmt.Sprintf(" · ⌕%s %d", p.ideFindQ, len(p.ideFindHits)))
	}
	if p.ideGrepActive() {
		switch {
		case p.ideGrepping:
			title += dimStyle.Render(" · ⌕⌕" + p.ideGrepQ + " searching…")
		default:
			n := 0
			for _, f := range p.ideGrepFiles {
				n += len(f.Matches)
			}
			sum := fmt.Sprintf(" · ⌕⌕%s %d in %d file", p.ideGrepQ, n, len(p.ideGrepFiles))
			if len(p.ideGrepFiles) != 1 {
				sum += "s"
			}
			if p.ideGrepMore {
				sum += "+"
			}
			title += dimStyle.Render(sum)
		}
	}
	if w < 4 || h < 1 {
		return title, ""
	}

	var rows []string
	if p.ideInputKind != ideInputNone {
		bar := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(0, 1).
			Width(w - 2).
			Render(p.ideInput.View())
		rows = append(rows, strings.Split(maxWidthStyle(w).Render(bar), "\n")...)
	}
	ch := p.ideContentH()
	treeW := p.ideTreeWidth(w)
	tree := p.ideTreeRows(treeW, ch)
	if p.ideGrepActive() {
		tree = p.ideGrepRowsView(treeW, ch)
	}
	// the divider between the halves wears the pane's chrome: it continues
	// into the title rule and bottom border (see idePart), and the whole
	// line follows focus with them
	div := metaStyle.Render(" │ ")
	if p.focused {
		div = okStyle.Render(" │ ")
	}
	edW := w - treeW - 3 // " │ " between the halves
	var right []string
	if len(p.ideBufs) == 0 {
		// the editor half keeps its place while empty, so opening a file
		// fills it in rather than reshaping the pane
		right = strings.Split(ideZeroState(edW, ch), "\n")
	} else {
		right = append(p.ideTabBar(edW), p.ideEditorRows(edW, p.ideViewH())...)
	}
	for i := 0; i < ch; i++ {
		t, e := "", ""
		if i < len(tree) {
			t = tree[i]
		}
		if i < len(right) {
			e = right[i]
		}
		rows = append(rows, padTo(t, treeW)+div+e)
	}
	return title, strings.Join(rows, "\n")
}

// ideZeroState fills the file half before anything is open: the key hints sit
// centered in the space the editor will take over, each key drawn as a chip.
func ideZeroState(w, h int) string {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	key := lipgloss.NewStyle().Foreground(btnFg).Background(btnBg).Padding(0, 1)
	hint := func(k, rest string) string {
		return key.Render(k) + dimStyle.Render(" "+rest)
	}
	body := lipgloss.JoinVertical(lipgloss.Center,
		metaStyle.Bold(true).Render("nothing open yet"),
		"",
		hint("enter", "or a click opens a file"),
		"",
		hint("/", "filters the tree"),
		"",
		hint("ctrl+g", "searches every file"),
		"",
		hint("a", "makes a file"),
	)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center,
		maxWidthStyle(w).Render(body))
}

// the tab shapes: every tab hangs from the same shelf line, the active one's
// bottom opens into the editor below it
var (
	ideTabBorder = lipgloss.Border{Top: "─", Bottom: "─", Left: "│", Right: "│",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "┴", BottomRight: "┴"}
	ideTabActiveBorder = lipgloss.Border{Top: "─", Bottom: " ", Left: "│", Right: "│",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "┘", BottomRight: "└"}
)

// ideTabBar is the file half's top rows: one bordered tab per open file, the
// active one open at the bottom and wearing a clickable ✕, dirty and stale
// marked on the name. Three rows tall — ideViewH leaves room for it. A strip
// wider than the half scrolls just enough that the active tab is fully
// visible, so an obscured tab is reached by selecting it ([ ] or the tree).
func (p *Pane) ideTabBar(w int) []string {
	activeText := paneTitleStyle
	activeBorder := subtle
	if p.focused {
		activeText = paneTitleFocus
		activeBorder = accent
	}
	parts := make([]string, 0, len(p.ideBufs))
	for i, b := range p.ideBufs {
		label := p.ideIconCell(b.rel, false, false) + filepath.Base(b.rel)
		if b.dirty {
			label += " ●"
		}
		if b.stale {
			label += " ⚠"
		}
		st := lipgloss.NewStyle().Border(ideTabBorder).BorderForeground(subtle).Padding(0, 1)
		body := dimStyle.Render(label)
		if i == p.ideCur {
			st = st.Border(ideTabActiveBorder).BorderForeground(activeBorder)
			body = activeText.Render(label) + " " +
				p.zones.Mark(ideTabCloseZoneID(), dimStyle.Render("✕"))
		}
		parts = append(parts, p.zones.Mark(ideTabZoneID(i), st.Render(body)))
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	total := lipgloss.Width(row)
	off := 0
	if total > w {
		right := 0
		for i := 0; i <= p.ideCur && i < len(parts); i++ {
			right += lipgloss.Width(parts[i])
		}
		if right > w {
			off = right - w
		}
	}
	lines := strings.Split(row, "\n")
	for i := range lines {
		lines[i] = ansi.Cut(lines[i], off, off+w)
	}
	// the shelf the tabs hang from runs to the editor's right edge
	if fill := w - (total - off); fill > 0 {
		lines[len(lines)-1] += dimStyle.Render(strings.Repeat("─", fill))
	}
	return lines
}

// ideTreeRows is the explorer: the visible window of the tree, the selected
// row carrying the cursor when the tree has the keys.
func (p *Pane) ideTreeRows(w, h int) []string {
	if len(p.ideTree) == 0 {
		hint := "the worktree is empty"
		if p.ideFilter != "" {
			hint = "nothing matches “" + p.ideFilter + "”"
		}
		return []string{dimStyle.Render("no files"), "", dimStyle.Render(truncate(hint, w))}
	}
	start := clampIdx(p.ideScroll, len(p.ideTree))
	openRel := ""
	if b := p.ideBuf(); b != nil {
		openRel = b.rel
	}
	var rows []string
	for i := start; i < len(p.ideTree) && i < start+h; i++ {
		e := p.ideTree[i]
		marker := "  "
		if e.dir {
			marker = "▸ "
			if p.ideExpanded[e.rel] {
				marker = "▾ "
			}
		}
		name := e.name
		if e.dir {
			name += "/"
		}
		indent := e.depth
		if p.ideFilter != "" {
			indent = 0
		}
		icon := p.ideIconCell(e.rel, e.dir, p.ideExpanded[e.rel])
		line := strings.Repeat("  ", indent) + marker + icon + name
		switch {
		case i == p.ideSel && p.focused && p.ideFocus == ideFocusTree:
			line = cursorStyle.Render(padCells(truncate(line, w-1), w-1))
		case i == p.ideSel:
			line = okStyle.Render(padCells(truncate(line, w-1), w-1))
		case e.dir:
			line = truncate(line, w)
		default:
			if e.rel == openRel {
				line = okStyle.Render(truncate(line, w))
			} else {
				line = dimStyle.Render(truncate(line, w))
			}
		}
		rows = append(rows, p.zones.Mark(ideZoneID(i), line))
	}
	return rows
}

// ideGrepRowsView draws the results list in the tree half: one header per
// file with its match count, the matching lines beneath it as line number and
// text, with the query picked out so the eye lands on the hit.
//
// Each row is built twice — plain and styled. truncate and padRight count
// bytes and runes, not display cells, so they only ever see the plain form;
// the selected row is padded plain and then rendered in one go, exactly as
// ideTreeRows does it.
func (p *Pane) ideGrepRowsView(w, h int) []string {
	if p.ideGrepping {
		return []string{dimStyle.Render(truncate("searching "+p.ideGrepQ+"…", w))}
	}
	if len(p.ideGrepRows) == 0 {
		return []string{
			dimStyle.Render("no matches"),
			"",
			dimStyle.Render(truncate("nothing here holds “"+p.ideGrepQ+"”", w)),
			"",
			dimStyle.Render(truncate("esc restores the tree", w)),
		}
	}
	focused := p.focused && p.ideFocus == ideFocusTree
	start := clampIdx(p.ideGrepScrol, len(p.ideGrepRows))
	var rows []string
	for i := start; i < len(p.ideGrepRows) && i < start+h; i++ {
		r := p.ideGrepRows[i]
		selected := i == p.ideGrepSel
		// the selection paints the whole row one colour, so it needs the plain
		// text; an unselected row is styled piece by piece
		avail := w
		if selected {
			avail = w - 1
		}
		var plain, styled string
		if r.hdr {
			marker := "▾ "
			if p.ideGrepFold[r.rel] {
				marker = "▸ "
			}
			count := " " + r.text
			icon := p.ideIconCell(r.rel, false, false)
			room := avail - lipgloss.Width(marker) - lipgloss.Width(icon) - lipgloss.Width(count)
			name := truncate(r.rel, room)
			plain = marker + icon + name + count
			styled = marker + icon + name + dimStyle.Render(count)
		} else {
			num := fmt.Sprintf("%5d ", r.line)
			body, at := ideGrepWindow(strings.TrimSpace(r.text), p.ideGrepQ,
				avail-lipgloss.Width(num))
			plain = num + body
			styled = dimStyle.Render(num) + ideGrepPaint(body, at, len(p.ideGrepQ))
		}
		line := styled
		switch {
		case selected && focused:
			line = cursorStyle.Render(padCells(plain, w-1))
		case selected:
			line = okStyle.Render(padCells(plain, w-1))
		}
		rows = append(rows, p.zones.Mark(ideGrepZoneID(i), line))
	}
	if p.ideGrepMore && len(rows) < h {
		rows = append(rows, dimStyle.Render(truncate("…capped — narrow the search", w)))
	}
	return rows
}

// ideGrepWindow fits a matching line into w cells and reports where the query
// sits inside what is left, or -1 when it fell outside. A hit far to the right
// of a long line slides the window along, a little context kept ahead of it.
func ideGrepWindow(text, query string, w int) (string, int) {
	if w < 1 {
		return "", -1
	}
	at := -1
	if query != "" {
		at = strings.Index(strings.ToLower(text), strings.ToLower(query))
	}
	if at < 0 {
		return truncate(text, w), -1
	}
	if at+len(query) > w {
		cut := at - w/3
		if cut < 0 {
			cut = 0
		}
		text, at = "…"+text[cut:], at-cut+1
	}
	out := truncate(text, w)
	if at+len(query) > len(out) {
		return out, -1 // the ellipsis ate the hit
	}
	return out, at
}

// ideGrepPaint colours a windowed line, the match at byte offset at picked out
// of it. at < 0 leaves the line dim.
func ideGrepPaint(text string, at, n int) string {
	if at < 0 || at+n > len(text) {
		return dimStyle.Render(text)
	}
	return dimStyle.Render(text[:at]) +
		searchHitStyle.Render(text[at:at+n]) +
		dimStyle.Render(text[at+n:])
}

// ideEditorRows is the file half under the tab bar: the highlighted buffer
// behind a gutter of git marks and line numbers. Editing renders the same
// rows — the hidden textarea only keeps the state — with a block cursor
// overlaid where it stands, and the window shifted right when the cursor
// walks past the pane's edge.
func (p *Pane) ideEditorRows(w, h int) []string {
	b := p.ideBuf()
	if b == nil {
		return nil
	}
	lines := b.hl
	numW := len(strconv.Itoa(len(lines)))
	// the ruler gives up its column when the pane is too narrow to spare one
	rulerW := ideRulerW
	if w-numW-2-rulerW < 4 {
		rulerW = 0
	}
	avail := w - numW - 2 - rulerW
	if avail < 1 {
		avail = 1
	}
	start := clampIdx(b.scrollY, len(lines))
	curRow, curCol, hoff := -1, 0, 0
	var plain []string
	if p.ideEditing {
		curRow = p.ideEditor.Line()
		curCol = p.ideEditor.LineInfo().CharOffset
		if curCol >= avail {
			hoff = curCol - avail + 1
		}
		plain = strings.Split(b.val, "\n")
	}
	selLo, selHi, selOn := p.ideSelSpan()
	// the multi-cursor block: line → display column of its extra cursor
	multi := map[int]int{}
	if p.ideEditing && p.ideMultiLo >= 0 {
		for i, col := range p.ideMultiCols {
			row := p.ideMultiLo + i
			if row >= len(plain) {
				continue
			}
			r := []rune(plain[row])
			if col > len(r) {
				col = len(r)
			}
			multi[row] = lipgloss.Width(string(r[:col]))
		}
	}
	hits := map[int]bool{}
	for _, hit := range p.ideFindHits {
		hits[hit] = true
	}
	ruler := ideRuler(b, h)
	// every row runs the full width so the ruler lands in one column, and the
	// view is filled to its height so the track reaches the bottom of a file
	// shorter than the window
	body := w - rulerW
	var rows []string
	for r := 0; r < h; r++ {
		i := start + r
		var row string
		if i < len(lines) {
			mark := " "
			switch b.gutter[i] {
			case '+':
				mark = okStyle.Render("▎")
			case '~':
				mark = warnStyle.Render("▎")
			}
			g := dimStyle
			_, onMulti := multi[i]
			switch {
			case selOn && i >= selLo && i <= selHi, onMulti:
				g = cursorStyle // the selected or multi-cursor block bands the gutter
			case i == curRow, i == b.cursor && !p.ideEditing && p.focused && p.ideFocus == ideFocusFile:
				g = cursorStyle
			case hits[i]:
				g = okStyle
			}
			line := ansi.Cut(lines[i], hoff, hoff+avail)
			if c, ok := multi[i]; ok && i < len(plain) {
				line = overlayIDECursor(line, plain[i], c, hoff)
			} else if i == curRow && curRow < len(plain) {
				line = overlayIDECursor(line, plain[i], curCol, hoff)
			}
			row = mark + g.Render(fmt.Sprintf("%*d ", numW, i+1)) + line
		}
		if rulerW > 0 {
			rows = append(rows, padCells(row, body)+ruler[r])
			continue
		}
		if i >= len(lines) {
			break // nothing to draw and no track to carry down the page
		}
		rows = append(rows, row)
	}
	return rows
}

// padCells pads a rendered string out to w display cells, escapes and
// double-width runes accounted for. A string already at or over the width is
// left alone — truncating a highlighted line here would shear its escapes.
//
// The selection bands need this rather than padRight: padRight counts bytes,
// so a row carrying a multi-byte glyph — the fold markers, the file icons —
// would stop its background short of the column's edge.
func padCells(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// ideRulerW is the scroll ruler's column: one cell on the file view's right.
const ideRulerW = 1

// ideRulerCell is one cell of the ruler, before it is styled: which diff
// marks the file lines it stands for carry, and whether the window covers it.
type ideRulerCell struct {
	added   bool
	changed bool
	thumb   bool
}

// ideRulerCells lays the whole file out on a track h cells tall — the
// geometry of the ruler, with no styling on it.
//
// Two things ride on the track. The thumb marks the rows the window is
// showing, so the position in a long file is legible at a glance. The diff
// marks come from the same gutter data as the per-line marks, which means a
// change further down the file announces itself without scrolling there.
//
// With the file shorter than the window the thumb covers the whole track —
// "all of it is on screen" — rather than vanishing, so the column never
// reflows the code beside it.
func ideRulerCells(b *ideBuf, h int) []ideRulerCell {
	if h < 1 {
		return nil
	}
	total := len(b.hl)
	thumbLo, thumbHi := 0, h
	if total > h {
		thumbLo = b.scrollY * h / total
		thumbHi = (b.scrollY + h) * h / total
		if thumbHi <= thumbLo {
			thumbHi = thumbLo + 1
		}
		if thumbHi > h {
			thumbHi = h
		}
	}
	cells := make([]ideRulerCell, h)
	for r := 0; r < h; r++ {
		// the file lines this cell stands for
		lo, hi := r, r+1
		if total > h {
			lo, hi = r*total/h, (r+1)*total/h
			if hi <= lo {
				hi = lo + 1
			}
		}
		c := ideRulerCell{thumb: r >= thumbLo && r < thumbHi}
		for i := lo; i < hi && i < total; i++ {
			switch b.gutter[i] {
			case '+':
				c.added = true
			case '~':
				c.changed = true
			}
		}
		cells[r] = c
	}
	return cells
}

// ideRuler renders the track: the diff in the glyph's colour, matching the
// gutter's accent-for-added and amber-for-changed, and the thumb as a
// background band behind it so the two never compete for the same cell.
func ideRuler(b *ideBuf, h int) []string {
	cells := ideRulerCells(b, h)
	out := make([]string, len(cells))
	for i, c := range cells {
		// a cell standing for many lines can hold both; the amber reads as
		// "something moved here", which is the question the ruler answers
		glyph, fg := "│", subtle
		switch {
		case c.changed:
			glyph, fg = "█", warnCol
		case c.added:
			glyph, fg = "█", accent
		}
		st := lipgloss.NewStyle().Foreground(fg)
		if c.thumb {
			st = st.Background(btnBg)
		}
		out[i] = st.Render(glyph)
	}
	return out
}

// overlayIDECursor draws a block cursor into an already-windowed highlighted
// line: the cell at the cursor's column rendered in reverse video, hardcoded
// the way the highlighter's own escapes are.
func overlayIDECursor(line, plain string, col, hoff int) string {
	c := col - hoff
	if c < 0 {
		return line
	}
	cell := ansi.Cut(plain, col, col+1)
	if cell == "" {
		cell = " " // the cursor rests past the end of the line
	}
	return ansi.Cut(line, 0, c) + "\x1b[7m" + cell + "\x1b[27m" + ansi.TruncateLeft(line, c+1, "")
}

// ---- keyboard ----

func (p *Pane) keyIDE(k tea.KeyMsg) tea.Cmd {
	// the two searches are caught before anything else in the pane can claim
	// the key, so they work the same from the tree, a file, and mid-edit. The
	// path prompts are the exception: they are modal and short-lived, and
	// hijacking one would silently throw away a half-typed filename.
	askingPath := p.ideInputKind == ideInputNew || p.ideInputKind == ideInputRename
	if !askingPath {
		switch k.String() {
		case "ctrl+f":
			return p.openIDESearch(ideInputFind)
		case "ctrl+g":
			return p.openIDESearch(ideInputGrep)
		}
	}
	if p.ideEditing {
		return p.keyIDEEditing(k)
	}
	if p.ideInputKind != ideInputNone {
		return p.keyIDEInput(k)
	}
	confirm := p.ideConfirm
	p.ideConfirm = "" // any key but the confirming one drops the question
	if p.ideGrepActive() && p.ideFocus == ideFocusTree {
		return p.keyIDEGrep(k)
	}
	if p.ideFocus == ideFocusFile && p.ideBuf() != nil {
		return p.keyIDEFile(k, confirm)
	}
	return p.keyIDETree(k, confirm)
}

// openIDESearch opens the ask-line for one of the two searches. Editing is
// left first — the buffer and the ask-line cannot both hold the keys — and a
// query already typed into the other search carries over, so landing in the
// wrong scope costs one keystroke rather than retyping.
func (p *Pane) openIDESearch(kind int) tea.Cmd {
	if p.ideEditing {
		p.leaveIDEEditing()
	}
	seed := p.ideFindQ
	if kind == ideInputGrep {
		seed = p.ideGrepQ
	}
	if p.ideInputKind == ideInputFind || p.ideInputKind == ideInputGrep {
		seed = p.ideInput.Value() // switching scope keeps what is typed
	}
	if kind == ideInputFind {
		if p.ideBuf() == nil {
			p.Err = errors.New("no file open — ctrl+g searches the whole worktree")
			return nil
		}
		p.ideFocus = ideFocusFile
		return p.openIDEInput(ideInputFind, "⌕ ", "find in this file…", seed)
	}
	p.ideFocus = ideFocusTree
	return p.openIDEInput(ideInputGrep, "⌕⌕ ", "search every file in the worktree…", seed)
}

func (p *Pane) keyIDEEditing(k tea.KeyMsg) tea.Cmd {
	if p.ideMultiLo >= 0 {
		return p.keyIDEMulti(k)
	}
	b := p.ideBuf()
	switch k.String() {
	case "esc":
		if p.ideSelAnchor >= 0 {
			p.ideSelAnchor = -1 // drop the selection first; esc again leaves
			return nil
		}
		p.leaveIDEEditing()
		return nil
	case "ctrl+s":
		return p.saveIDEBuf(false)
	case "ctrl+e", "ctrl+shift+i":
		// a cursor on every selected line's end — vscode's ctrl+shift+i.
		// Most terminals hand ctrl+shift+i to us as tab (indent), so ctrl+e
		// ("line ends") carries the binding; without a selection it stays the
		// textarea's own jump-to-line-end.
		if lo, hi, ok := p.ideSelSpan(); ok {
			p.startIDEMulti(lo, hi)
			return nil
		}
	case "shift+up", "shift+down":
		// grow a line selection from the cursor; tab/shift+tab act on it
		if p.ideSelAnchor < 0 {
			p.ideSelAnchor = p.ideEditor.Line()
		}
		if k.String() == "shift+up" {
			p.ideEditor.CursorUp()
		} else {
			p.ideEditor.CursorDown()
		}
		b.cursor = clampIdx(p.ideEditor.Line(), len(b.hl))
		p.settleIDEView()
		return nil
	case "tab", "shift+tab":
		before := b.val
		lo, hi, sel := p.ideSelSpan()
		switch {
		case k.String() == "tab" && !sel:
			// plain tab types indentation at the cursor, to the next tab stop
			col := p.ideEditor.LineInfo().ColumnOffset
			p.ideEditor.InsertString(strings.Repeat(" ", ideIndentUnit-col%ideIndentUnit))
		case k.String() == "tab":
			p.indentIDESpan(lo, hi, 1)
		case !sel:
			p.indentIDESpan(p.ideEditor.Line(), p.ideEditor.Line(), -1)
		default:
			p.indentIDESpan(lo, hi, -1)
		}
		p.finishIDEEdit(b, before)
		return nil
	case "enter":
		// break the line the way vscode does: the new line inherits the
		// indentation, one unit deeper after an opening bracket
		indent := ""
		if lines := strings.Split(b.val, "\n"); p.ideEditor.Line() < len(lines) {
			indent = ideAutoIndent(lines[p.ideEditor.Line()], p.ideEditor.LineInfo().ColumnOffset)
		}
		p.ideSelAnchor = -1
		before := b.val
		var cmd tea.Cmd
		p.ideEditor, cmd = p.ideEditor.Update(k)
		if indent != "" {
			p.ideEditor.InsertString(indent)
		}
		p.finishIDEEdit(b, before)
		return cmd
	}
	p.ideSelAnchor = -1 // any other key works at the cursor, selection dropped
	before := b.val
	var cmd tea.Cmd
	p.ideEditor, cmd = p.ideEditor.Update(k)
	p.finishIDEEdit(b, before)
	return cmd
}

// finishIDEEdit settles the buffer after a keystroke changed (or may have
// changed) the editor: text stashed, colors redone under the live budget, the
// cursor line tracked and kept in the window.
func (p *Pane) finishIDEEdit(b *ideBuf, before string) {
	p.stashIDEBuf()
	if b.val != before {
		b.hl = liveHighlight(b.val, b.rel)
	}
	b.cursor = clampIdx(p.ideEditor.Line(), len(b.hl))
	p.settleIDEView()
}

// startIDEMulti turns a line selection into a multi-cursor block: one cursor
// on the end of every selected line. Typing, backspace, delete, tab and ←/→
// then work on all of them at once; anything else drops back to the single
// cursor.
func (p *Pane) startIDEMulti(lo, hi int) {
	p.ideSelAnchor = -1
	lines := strings.Split(p.ideEditor.Value(), "\n")
	p.ideMultiLo = lo
	p.ideMultiCols = make([]int, 0, hi-lo+1)
	for i := lo; i <= hi && i < len(lines); i++ {
		p.ideMultiCols = append(p.ideMultiCols, len([]rune(lines[i])))
	}
	if len(p.ideMultiCols) == 0 {
		p.ideMultiLo = -1
		return
	}
	// the real cursor rides the block's last line, keeping it in the window
	p.setIDECursorAt(lo+len(p.ideMultiCols)-1, p.ideMultiCols[len(p.ideMultiCols)-1])
}

func (p *Pane) clearIDEMulti() {
	p.ideMultiLo, p.ideMultiCols = -1, nil
}

// keyIDEMulti works the multi-cursor block: the same edit is applied at every
// line's cursor. Paste folds to one line — the per-line cursors have no way to
// share a multi-line insert.
func (p *Pane) keyIDEMulti(k tea.KeyMsg) tea.Cmd {
	b := p.ideBuf()
	// apply runs one edit at every cursor and rebuilds the editor around it
	apply := func(fn func(r []rune, col int) ([]rune, int)) {
		before := b.val
		lines := strings.Split(p.ideEditor.Value(), "\n")
		for i, col := range p.ideMultiCols {
			row := p.ideMultiLo + i
			if row >= len(lines) {
				continue
			}
			r := []rune(lines[row])
			if col > len(r) {
				col = len(r)
			}
			r, col = fn(r, col)
			lines[row] = string(r)
			p.ideMultiCols[i] = col
		}
		p.ideEditor.SetValue(strings.Join(lines, "\n"))
		p.setIDECursorAt(p.ideMultiLo+len(p.ideMultiCols)-1, p.ideMultiCols[len(p.ideMultiCols)-1])
		p.finishIDEEdit(b, before)
	}
	switch k.String() {
	case "esc":
		p.clearIDEMulti()
		return nil
	case "ctrl+s":
		p.clearIDEMulti()
		return p.saveIDEBuf(false)
	case "backspace":
		apply(func(r []rune, col int) ([]rune, int) {
			if col == 0 {
				return r, 0
			}
			return append(r[:col-1], r[col:]...), col - 1
		})
		return nil
	case "delete":
		apply(func(r []rune, col int) ([]rune, int) {
			if col < len(r) {
				r = append(r[:col], r[col+1:]...)
			}
			return r, col
		})
		return nil
	case "left":
		apply(func(r []rune, col int) ([]rune, int) { return r, max(col-1, 0) })
		return nil
	case "right":
		apply(func(r []rune, col int) ([]rune, int) { return r, min(col+1, len(r)) })
		return nil
	case "home":
		apply(func(r []rune, col int) ([]rune, int) { return r, 0 })
		return nil
	case "end":
		apply(func(r []rune, col int) ([]rune, int) { return r, len(r) })
		return nil
	case "tab":
		apply(func(r []rune, col int) ([]rune, int) {
			pad := []rune(strings.Repeat(" ", ideIndentUnit-col%ideIndentUnit))
			return append(r[:col:col], append(pad, r[col:]...)...), col + len(pad)
		})
		return nil
	}
	var ins []rune
	switch {
	case k.Type == tea.KeyRunes:
		ins = k.Runes
	case k.Type == tea.KeySpace:
		ins = []rune{' '}
	}
	// newlines can't be split across the per-line cursors; a pasted block
	// folds onto each line rather than shearing the block apart
	ins = []rune(strings.ReplaceAll(ideSanitize(string(ins)), "\n", " "))
	if len(ins) == 0 {
		// any other key ends the block and lands on the single cursor
		p.clearIDEMulti()
		return p.keyIDEEditing(k)
	}
	apply(func(r []rune, col int) ([]rune, int) {
		return append(r[:col:col], append(append([]rune{}, ins...), r[col:]...)...), col + len(ins)
	})
	return nil
}

// ideIndentUnit is the editor's indent step, matching the four spaces
// ideSanitize flattens a tab into.
const ideIndentUnit = 4

// ideLineIndent counts a line's leading spaces.
func ideLineIndent(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

// ideAutoIndent is the indentation a new line inherits when enter breaks a
// line at col: the line's leading spaces — capped at the cursor, so a break
// inside the indent doesn't double it — one unit deeper when the cursor sits
// right after an opening bracket or a colon.
func ideAutoIndent(line string, col int) string {
	r := []rune(line)
	if col > len(r) {
		col = len(r)
	}
	n := ideLineIndent(line)
	if n > col {
		n = col
	}
	head := strings.TrimRight(string(r[:col]), " ")
	if head != "" && strings.ContainsRune("{([:", rune(head[len(head)-1])) {
		n += ideIndentUnit
	}
	return strings.Repeat(" ", n)
}

// ideSelSpan is the selected line range while shift+↑/↓ holds one open,
// anchored at ideSelAnchor with the editor cursor as the moving end.
func (p *Pane) ideSelSpan() (lo, hi int, ok bool) {
	if !p.ideEditing || p.ideSelAnchor < 0 {
		return 0, 0, false
	}
	lo, hi = p.ideSelAnchor, p.ideEditor.Line()
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi, true
}

// indentIDESpan shifts the buffer lines lo..hi one indent unit right (dir>0)
// or left, the way vscode's tab/shift+tab work on a selection: blank lines
// stay put on an indent, an outdent takes what leading spaces are there, up
// to the unit. The editor cursor keeps its place in the text it was on.
func (p *Pane) indentIDESpan(lo, hi, dir int) {
	lines := strings.Split(p.ideEditor.Value(), "\n")
	row := p.ideEditor.Line()
	col := p.ideEditor.LineInfo().ColumnOffset
	shift := 0
	for i := lo; i <= hi && i < len(lines); i++ {
		if dir > 0 {
			if strings.TrimSpace(lines[i]) == "" {
				continue
			}
			lines[i] = strings.Repeat(" ", ideIndentUnit) + lines[i]
			if i == row {
				shift = ideIndentUnit
			}
		} else {
			n := ideLineIndent(lines[i])
			if n > ideIndentUnit {
				n = ideIndentUnit
			}
			lines[i] = lines[i][n:]
			if i == row {
				shift = -n
			}
		}
	}
	p.ideEditor.SetValue(strings.Join(lines, "\n"))
	p.setIDECursorAt(row, col+shift)
}

// leaveIDEEditing drops out of the editor back to the read-only file view,
// settling the buffer: the text is stashed, the colors are redone in full
// (live highlighting may have been skipped under the size budget), and the
// find hits are recomputed against whatever the edit left behind.
func (p *Pane) leaveIDEEditing() {
	b := p.ideBuf()
	if b == nil {
		p.ideEditing = false
		p.ideSelAnchor = -1
		p.clearIDEMulti()
		return
	}
	p.ideEditing = false
	p.ideSelAnchor = -1
	p.clearIDEMulti()
	p.ideEditor.Blur()
	p.stashIDEBuf()
	b.hl = highlightSource(b.val, b.rel)
	b.cursor = clampIdx(p.ideEditor.Line(), len(b.hl))
	p.settleIDEView()
	p.recomputeIDEFind()
}

// liveHighlight is highlightSource under a budget: a buffer too large to
// re-color on every keystroke stays plain — but line-aligned — until esc.
func liveHighlight(src, filename string) []string {
	if len(src) > ideLiveHLMax {
		return strings.Split(src, "\n")
	}
	return highlightSource(src, filename)
}

func (p *Pane) keyIDEFile(k tea.KeyMsg, confirm string) tea.Cmd {
	b := p.ideBuf()
	h := p.ideViewH()
	switch k.String() {
	case "esc":
		if p.ideFindQ != "" {
			p.ideFindQ, p.ideFindHits = "", nil
			return nil
		}
		p.ideFocus = ideFocusTree
		return nil
	case "e", "i", "enter":
		p.ideEditing = true
		p.alignIDEEditor()
		return p.ideEditor.Focus()
	case "ctrl+s":
		return p.saveIDEBuf(false)
	case "R":
		return p.saveIDEBuf(true)
	case "r":
		if b.dirty && confirm != "reload" {
			p.ideConfirm = "reload"
			p.Err = errors.New("unsaved edits — r again reloads from disk and drops them")
			return nil
		}
		return p.reloadIDEBuf(b)
	case "x":
		if b.dirty && confirm != "close" {
			p.ideConfirm = "close"
			p.Err = errors.New("unsaved edits — x again closes and drops them, ctrl+s saves")
			return nil
		}
		p.closeIDEBuf(p.ideCur)
		return nil
	case "[", "ctrl+left":
		p.switchIDEBuf(-1)
		return nil
	case "]", "ctrl+right":
		p.switchIDEBuf(1)
		return nil
	case "/":
		return p.openIDEInput(ideInputFind, "⌕ ", "find in this file…", p.ideFindQ)
	case "n":
		p.jumpIDEFind(1)
	case "N":
		p.jumpIDEFind(-1)
	case "up", "k":
		p.moveIDECursor(-1)
	case "down", "j":
		p.moveIDECursor(1)
	case "pgup":
		p.moveIDECursor(-h)
	case "pgdown", "f":
		p.moveIDECursor(h)
	case "g", "home":
		b.cursor = 0
		p.settleIDEView()
	case "G", "end":
		b.cursor = len(b.hl) - 1
		p.settleIDEView()
	}
	return nil
}

// keyIDEGrep works the results list while it stands in for the tree.
func (p *Pane) keyIDEGrep(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		p.moveIDEGrepSel(-1)
	case "down", "j":
		p.moveIDEGrepSel(1)
	case "enter", "l", "right":
		return p.openIDEGrepSel()
	case "n":
		return p.jumpIDEGrep(1)
	case "N":
		return p.jumpIDEGrep(-1)
	case "h", "left":
		// fold this file, from a match row as well as its header
		if p.ideGrepSel < len(p.ideGrepRows) {
			rel := p.ideGrepRows[p.ideGrepSel].rel
			p.ideGrepFold[rel] = true
			p.rebuildIDEGrepRows()
			for i, r := range p.ideGrepRows {
				if r.rel == rel && r.hdr {
					p.ideGrepSel = i
					p.settleIDEGrep()
					break
				}
			}
		}
	case "g", "home":
		p.ideGrepSel = 0
		p.settleIDEGrep()
	case "G", "end":
		p.ideGrepSel = clampIdx(len(p.ideGrepRows)-1, len(p.ideGrepRows))
		p.settleIDEGrep()
	case "pgup":
		p.moveIDEGrepSel(-p.ideContentH())
	case "pgdown":
		p.moveIDEGrepSel(p.ideContentH())
	case "esc":
		p.clearIDEGrep()
		p.refreshIDETree()
	case "tab":
		if p.ideBuf() != nil {
			p.ideFocus = ideFocusFile
		}
	case "ctrl+s":
		return p.saveIDEBuf(false)
	}
	return nil
}

func (p *Pane) keyIDETree(k tea.KeyMsg, confirm string) tea.Cmd {
	switch k.String() {
	case "up", "k":
		p.moveIDESel(-1)
	case "down", "j":
		p.moveIDESel(1)
	case "enter", "l", "right":
		return p.openIDESel()
	case "h", "left":
		p.collapseIDESel()
	case "/":
		return p.openIDEInput(ideInputFilter, "/ ", "type to filter files…", p.ideFilter)
	case "a":
		seed := ""
		if e, ok := p.ideSelEntry(); ok {
			if e.dir {
				seed = e.rel + string(filepath.Separator)
			} else if d := filepath.Dir(e.rel); d != "." {
				seed = d + string(filepath.Separator)
			}
		}
		return p.openIDEInput(ideInputNew, "+ ", "path/for/the/new-file…", seed)
	case "R":
		if e, ok := p.ideSelEntry(); ok {
			return p.openIDEInput(ideInputRename, "→ ", "new path…", e.rel)
		}
	case "d":
		e, ok := p.ideSelEntry()
		if !ok {
			return nil
		}
		if confirm != "del:"+e.rel {
			p.ideConfirm = "del:" + e.rel
			p.Err = errors.New("delete " + e.rel + "? d again confirms")
			return nil
		}
		return p.deleteIDEEntry(e)
	case "esc":
		if p.ideFilter != "" {
			p.ideFilter = ""
			p.refreshIDETree()
			return nil
		}
		if p.ideBuf() != nil {
			p.ideFocus = ideFocusFile
		}
	case "ctrl+s":
		return p.saveIDEBuf(false)
	case "r":
		return tea.Batch(p.refreshIDEDisk()...)
	}
	return nil
}

func (p *Pane) ideSelEntry() (ideEntry, bool) {
	if p.ideSel >= len(p.ideTree) {
		return ideEntry{}, false
	}
	return p.ideTree[p.ideSel], true
}

// ---- the input line ----

func (p *Pane) openIDEInput(kind int, prompt, placeholder, seed string) tea.Cmd {
	p.ideInputKind = kind
	p.ideInput.Prompt = prompt
	p.ideInput.Placeholder = placeholder
	p.ideInput.SetValue(seed)
	p.ideInput.CursorEnd()
	return p.ideInput.Focus()
}

func (p *Pane) closeIDEInput() {
	p.ideInputKind = ideInputNone
	p.ideInput.Blur()
	p.ideInput.SetValue("")
}

func (p *Pane) keyIDEInput(k tea.KeyMsg) tea.Cmd {
	kind := p.ideInputKind
	switch k.String() {
	case "esc":
		p.closeIDEInput()
		switch kind {
		case ideInputFilter:
			p.ideFilter = ""
			p.refreshIDETree()
		case ideInputFind:
			p.ideFindQ, p.ideFindHits = "", nil
		case ideInputGrep:
			p.clearIDEGrep()
			p.refreshIDETree()
		}
		return nil
	case "enter":
		val := strings.TrimSpace(p.ideInput.Value())
		p.closeIDEInput()
		switch kind {
		case ideInputFilter:
			// the filter stays applied; enter moves to picking from the list
		case ideInputFind:
			p.jumpIDEFind(1)
		case ideInputGrep:
			return p.startIDEGrep(val)
		case ideInputNew:
			return p.createIDEEntry(val)
		case ideInputRename:
			if e, ok := p.ideSelEntry(); ok {
				return p.renameIDEEntry(e, val)
			}
		}
		return nil
	}
	var cmd tea.Cmd
	p.ideInput, cmd = p.ideInput.Update(k)
	switch kind {
	case ideInputFilter:
		p.ideFilter = strings.TrimSpace(p.ideInput.Value())
		p.ideSel, p.ideScroll = 0, 0
		p.refreshIDETree()
	case ideInputFind:
		p.ideFindQ = p.ideInput.Value()
		p.recomputeIDEFind()
	}
	return cmd
}

// startIDEGrep kicks off a worktree search. Unlike the in-file find this one
// waits for enter: every keystroke would be a git grep over the whole tree.
func (p *Pane) startIDEGrep(query string) tea.Cmd {
	if query == "" {
		p.clearIDEGrep()
		p.refreshIDETree()
		return nil
	}
	p.clearIDEGrep()
	p.ideGrepQ = query
	p.ideGrepping = true
	p.ideFocus = ideFocusTree
	return grepIDECmd(p.ideFor, query)
}

// applyIDEGrepMsg takes a finished search, ignoring one whose query or
// worktree has moved on since it was started.
func (p *Pane) applyIDEGrepMsg(msg ideGrepMsg) {
	if msg.dir != p.ideFor || msg.query != p.ideGrepQ {
		return
	}
	p.ideGrepping = false
	if msg.err != nil {
		p.Err = msg.err
		p.clearIDEGrep()
		return
	}
	p.ideGrepFiles, p.ideGrepMore = msg.files, msg.more
	p.ideGrepSel, p.ideGrepScrol = 0, 0
	p.rebuildIDEGrepRows()
}

// ---- file operations ----

// createIDEEntry makes a file — or a directory, when the name ends with a
// slash — relative to the worktree root, parents included.
func (p *Pane) createIDEEntry(rel string) tea.Cmd {
	if rel == "" || p.ideFor == "" {
		return nil
	}
	isDir := strings.HasSuffix(rel, string(filepath.Separator)) || strings.HasSuffix(rel, "/")
	rel = filepath.Clean(strings.TrimRight(rel, "/"+string(filepath.Separator)))
	if rel == "." || strings.HasPrefix(rel, "..") {
		p.Err = errors.New("the new path has to stay inside the worktree")
		return nil
	}
	path := filepath.Join(p.ideFor, rel)
	if isDir {
		if err := os.MkdirAll(path, 0o755); err != nil {
			p.Err = err
			return nil
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			p.Err = err
			return nil
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			p.Err = err
			return nil
		}
		f.Close()
	}
	for d := filepath.Dir(rel); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
		p.ideExpanded[d] = true // unfold down to what was just made
	}
	p.refreshIDETree()
	p.selectIDERel(rel)
	var cmds []tea.Cmd
	if !isDir {
		cmds = append(cmds, p.openIDEFile(rel))
	}
	return tea.Batch(append(cmds, changedCmd())...)
}

// renameIDEEntry moves a file or directory within the worktree.
func (p *Pane) renameIDEEntry(e ideEntry, to string) tea.Cmd {
	to = filepath.Clean(to)
	if to == "" || to == "." || strings.HasPrefix(to, "..") {
		p.Err = errors.New("the new path has to stay inside the worktree")
		return nil
	}
	if to == e.rel {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(p.ideFor, to)), 0o755); err != nil {
		p.Err = err
		return nil
	}
	if err := os.Rename(filepath.Join(p.ideFor, e.rel), filepath.Join(p.ideFor, to)); err != nil {
		p.Err = err
		return nil
	}
	for _, b := range p.ideBufs { // open tabs follow their file
		switch {
		case b.rel == e.rel:
			b.rel = to
		case e.dir && strings.HasPrefix(b.rel, e.rel+string(filepath.Separator)):
			b.rel = to + strings.TrimPrefix(b.rel, e.rel)
		}
	}
	p.refreshIDETree()
	p.selectIDERel(to)
	return changedCmd()
}

// deleteIDEEntry removes a file or directory, closing any tab it had open.
func (p *Pane) deleteIDEEntry(e ideEntry) tea.Cmd {
	if err := os.RemoveAll(filepath.Join(p.ideFor, e.rel)); err != nil {
		p.Err = err
		return nil
	}
	for i := len(p.ideBufs) - 1; i >= 0; i-- {
		rel := p.ideBufs[i].rel
		if rel == e.rel || (e.dir && strings.HasPrefix(rel, e.rel+string(filepath.Separator))) {
			p.closeIDEBuf(i)
		}
	}
	p.refreshIDETree()
	return changedCmd()
}

func (p *Pane) selectIDERel(rel string) {
	for i, e := range p.ideTree {
		if e.rel == rel {
			p.ideSel = i
			p.settleIDETree()
			return
		}
	}
}

// openIDESel acts on the tree's selected row: folders fold, files open. With
// a filter applied, a folder jumps the tree there instead — the flat match
// list has nowhere to unfold into.
func (p *Pane) openIDESel() tea.Cmd {
	e, ok := p.ideSelEntry()
	if !ok {
		return nil
	}
	if e.dir {
		if p.ideFilter != "" {
			p.ideFilter = ""
			p.closeIDEInput()
			for d := e.rel; d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
				p.ideExpanded[d] = true
			}
			p.refreshIDETree()
			p.selectIDERel(e.rel)
			return nil
		}
		p.ideExpanded[e.rel] = !p.ideExpanded[e.rel]
		p.refreshIDETree()
		return nil
	}
	return p.openIDEFile(e.rel)
}

// collapseIDESel folds the selected directory, or climbs to the parent of a
// file (or an already-folded directory).
func (p *Pane) collapseIDESel() {
	e, ok := p.ideSelEntry()
	if !ok || p.ideFilter != "" {
		return
	}
	if e.dir && p.ideExpanded[e.rel] {
		p.ideExpanded[e.rel] = false
		p.refreshIDETree()
		return
	}
	for i := p.ideSel - 1; i >= 0; i-- {
		if p.ideTree[i].dir && p.ideTree[i].depth == e.depth-1 {
			p.ideSel = i
			p.settleIDETree()
			return
		}
	}
}

func (p *Pane) moveIDESel(n int) {
	p.ideSel = clampIdx(p.ideSel+n, len(p.ideTree))
	p.settleIDETree()
}

// settleIDETree keeps the selection inside the explorer's window.
func (p *Pane) settleIDETree() {
	h := p.ideContentH()
	if p.ideSel < p.ideScroll {
		p.ideScroll = p.ideSel
	}
	if p.ideSel >= p.ideScroll+h {
		p.ideScroll = p.ideSel - h + 1
	}
}

func (p *Pane) moveIDECursor(n int) {
	if b := p.ideBuf(); b != nil {
		b.cursor = clampIdx(b.cursor+n, len(b.hl))
		p.settleIDEView()
	}
}

// settleIDEView keeps the cursor line inside the file view's window.
func (p *Pane) settleIDEView() {
	b := p.ideBuf()
	if b == nil {
		return
	}
	h := p.ideViewH()
	if b.cursor < b.scrollY {
		b.scrollY = b.cursor
	}
	if b.cursor >= b.scrollY+h {
		b.scrollY = b.cursor - h + 1
	}
}

func clampIdx(v, n int) int {
	if v >= n {
		v = n - 1
	}
	if v < 0 {
		v = 0
	}
	return v
}

// ---- mouse ----

// scrollIDERegion wheels whichever half of the pane the pointer is over: the
// explorer strip or the open file. x is the pointer's column in the pane's
// body (ok false when the host couldn't place it).
func (p *Pane) scrollIDERegion(x int, ok, up bool) {
	w, _ := p.idePaneSize()
	overTree := !ok || x < p.ideTreeWidth(w) || len(p.ideBufs) == 0
	step := 3
	if up {
		step = -3
	}
	switch {
	case overTree && p.ideGrepActive():
		max := len(p.ideGrepRows) - p.ideContentH()
		if max < 0 {
			max = 0
		}
		p.ideGrepScrol = clampW(p.ideGrepScrol+step, 0, max)
	case overTree:
		max := len(p.ideTree) - p.ideContentH()
		if max < 0 {
			max = 0
		}
		p.ideScroll = clampW(p.ideScroll+step, 0, max)
	case p.ideEditing:
		for i := 0; i < 3; i++ {
			if up {
				p.ideEditor.CursorUp()
			} else {
				p.ideEditor.CursorDown()
			}
		}
		if b := p.ideBuf(); b != nil {
			b.cursor = clampIdx(p.ideEditor.Line(), len(b.hl))
			p.settleIDEView()
		}
	default:
		if b := p.ideBuf(); b != nil {
			max := len(b.hl) - p.ideViewH()
			if max < 0 {
				max = 0
			}
			b.scrollY = clampW(b.scrollY+step, 0, max)
		}
	}
}

// clickIDE handles a click inside the pane: tabs and tree rows select and
// open, a click in the file half moves the cursor to that line. x and y are
// the pointer's cell in the pane's body (ok false when the host couldn't
// place it); giving the pane the app's focus is the host's business.
func (p *Pane) clickIDE(msg tea.MouseMsg, x, y int, ok bool) tea.Cmd {
	if b := p.ideBuf(); b != nil && p.clicked(msg, ideTabCloseZoneID()) {
		confirm := p.ideConfirm
		p.ideConfirm = ""
		if b.dirty && confirm != "close" {
			p.ideConfirm = "close"
			p.Err = errors.New("unsaved edits — ✕ again closes and drops them, ctrl+s saves")
			return nil
		}
		p.closeIDEBuf(p.ideCur)
		return nil
	}
	for i := range p.ideBufs {
		if p.clicked(msg, ideTabZoneID(i)) {
			p.activateIDEBuf(i)
			p.ideFocus = ideFocusFile
			return nil
		}
	}
	for i := range p.ideGrepRows {
		if p.clicked(msg, ideGrepZoneID(i)) {
			p.ideGrepSel = i
			p.ideFocus = ideFocusTree
			return p.openIDEGrepSel()
		}
	}
	for i := range p.ideTree {
		if p.clicked(msg, ideZoneID(i)) {
			p.ideSel = i
			p.ideFocus = ideFocusTree
			return p.openIDESel()
		}
	}
	if b := p.ideBuf(); b != nil && !p.ideEditing && ok {
		if p.ideInputKind != ideInputNone {
			y -= 3 // the bordered input bar sits above both halves
		}
		w, _ := p.idePaneSize()
		if x >= p.ideTreeWidth(w) && y >= 3 { // rows 0-2 are the tab bar
			p.ideFocus = ideFocusFile
			b.cursor = clampIdx(b.scrollY+y-3, len(b.hl))
		}
	}
	return nil
}

// clicked reports whether a mouse event landed in one of the pane's zones.
func (p *Pane) clicked(msg tea.MouseMsg, id string) bool {
	z := p.zones.Get(id)
	return z != nil && !z.IsZero() && z.InBounds(msg)
}
