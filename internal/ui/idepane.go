package ui

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

// The ide pane sits between claude and git: a file explorer over the selected
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

func ideZoneID(i int) string    { return "ide:t:" + strconv.Itoa(i) }
func ideTabZoneID(i int) string { return "ide:tab:" + strconv.Itoa(i) }

// ideBuf is the active tab's buffer, nil with nothing open.
func (m *Model) ideBuf() *ideBuf {
	if m.ideCur < 0 || m.ideCur >= len(m.ideBufs) {
		return nil
	}
	return m.ideBufs[m.ideCur]
}

func (m *Model) ideAnyDirty() bool {
	for _, b := range m.ideBufs {
		if b.dirty {
			return true
		}
	}
	return false
}

// resetIDE points the pane at another worktree. Dirty buffers pin the pane
// where it is — moving the cursor through the issues list must not throw away
// edits — until they are saved or closed; saving and closing re-sync.
func (m *Model) resetIDE(dir string) {
	if m.ideAnyDirty() {
		return
	}
	m.ideFor = dir
	m.ideTree = nil
	m.ideSel, m.ideScroll = 0, 0
	m.ideExpanded = map[string]bool{}
	m.ideBufs, m.ideCur = nil, 0
	m.ideEditor.SetValue("")
	m.ideEditor.Blur()
	m.ideEditing = false
	m.ideFocus = ideFocusTree
	m.closeIDEInput()
	m.ideFilter, m.ideFindQ, m.ideFindHits = "", "", nil
	m.ideConfirm = ""
	if dir != "" {
		m.refreshIDETree()
	}
}

// ---- explorer tree ----

// refreshIDETree rebuilds the visible tree from disk: the worktree root's
// entries with expanded directories inlined depth-first, or — while a filter
// is typed — the paths that match it, found by a capped recursive walk.
func (m *Model) refreshIDETree() {
	sel := ""
	if m.ideSel < len(m.ideTree) {
		sel = m.ideTree[m.ideSel].rel
	}
	if m.ideFilter != "" {
		m.ideTree = m.ideFilterWalk(m.ideFilter)
	} else {
		m.ideTree = m.ideTreeLevel("", 0)
	}
	if sel != "" {
		for i, e := range m.ideTree {
			if e.rel == sel {
				m.ideSel = i
				break
			}
		}
	}
	if m.ideSel >= len(m.ideTree) {
		m.ideSel = len(m.ideTree) - 1
	}
	if m.ideSel < 0 {
		m.ideSel = 0
	}
	m.settleIDETree()
}

func (m *Model) ideTreeLevel(rel string, depth int) []ideEntry {
	ents, err := os.ReadDir(filepath.Join(m.ideFor, rel))
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
		if e.IsDir() && m.ideExpanded[crel] {
			out = append(out, m.ideTreeLevel(crel, depth+1)...)
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
func (m *Model) ideFilterWalk(q string) []ideEntry {
	q = strings.ToLower(q)
	var out []ideEntry
	visited := 0
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		if len(out) >= ideFilterMaxHits || visited >= ideFilterMaxWalk || depth > ideFilterMaxDepth {
			return
		}
		ents, err := os.ReadDir(filepath.Join(m.ideFor, rel))
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
func (m *Model) openIDEFile(rel string) tea.Cmd {
	for i, b := range m.ideBufs {
		if b.rel == rel {
			m.activateIDEBuf(i)
			m.ideFocus = ideFocusFile
			return nil
		}
	}
	data, err := os.ReadFile(filepath.Join(m.ideFor, rel))
	if err != nil {
		m.err = err
		return nil
	}
	if len(data) > ideMaxFileSize {
		m.err = fmt.Errorf("%s is %dKB — too large for the ide pane", rel, len(data)/1024)
		return nil
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		m.err = errors.New(rel + " looks binary")
		return nil
	}
	b := &ideBuf{rel: rel}
	fillIDEBuf(b, data)
	if fi, err := os.Stat(filepath.Join(m.ideFor, rel)); err == nil {
		b.modTime = fi.ModTime()
	}
	// stash before appending: with no tabs open, ideCur would already point
	// at the new buffer, and an activate-style stash would clobber its text
	// with whatever the idle editor held
	m.stashIDEBuf()
	m.ideBufs = append(m.ideBufs, b)
	m.ideCur = len(m.ideBufs) - 1
	m.ideEditor.SetValue(b.val)
	m.alignIDEEditor()
	m.ideFocus = ideFocusFile
	m.recomputeIDEFind()
	return loadIDEGutterCmd(m.ideFor, rel)
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
func (m *Model) stashIDEBuf() {
	if b := m.ideBuf(); b != nil {
		b.val = m.ideEditor.Value()
		b.dirty = b.val != b.savedVal
	}
}

// activateIDEBuf makes tab i current and loads its text into the editor.
func (m *Model) activateIDEBuf(i int) {
	if i < 0 || i >= len(m.ideBufs) {
		return
	}
	m.stashIDEBuf()
	m.ideCur = i
	b := m.ideBufs[i]
	m.ideEditor.SetValue(b.val)
	m.alignIDEEditor()
	m.recomputeIDEFind()
}

// switchIDEBuf moves delta tabs over, wrapping.
func (m *Model) switchIDEBuf(delta int) {
	if n := len(m.ideBufs); n > 1 {
		m.activateIDEBuf(((m.ideCur+delta)%n + n) % n)
	}
}

// closeIDEBuf drops tab i; the neighbour (or the tree) takes over.
func (m *Model) closeIDEBuf(i int) {
	if i < 0 || i >= len(m.ideBufs) {
		return
	}
	m.ideBufs = append(m.ideBufs[:i:i], m.ideBufs[i+1:]...)
	if m.ideCur > i || m.ideCur >= len(m.ideBufs) {
		m.ideCur--
	}
	if b := m.ideBuf(); b != nil {
		m.ideEditor.SetValue(b.val)
		m.alignIDEEditor()
	} else {
		m.ideCur = 0
		m.ideEditor.SetValue("")
		m.ideEditing = false
		m.ideEditor.Blur()
		m.ideFocus = ideFocusTree
	}
	m.recomputeIDEFind()
	// closing the last dirty buffer releases a pane pinned to an old worktree
	if dir := m.claudeDir(); dir != m.ideFor {
		m.resetIDE(dir)
	}
}

// alignIDEEditor moves the buffer's cursor to the view's current line, so
// editing starts where the reader was looking.
func (m *Model) alignIDEEditor() {
	cur := 0
	if b := m.ideBuf(); b != nil {
		cur = b.cursor
	}
	m.ideEditor.CursorStart()
	for m.ideEditor.Line() > cur {
		m.ideEditor.CursorUp()
	}
	for m.ideEditor.Line() < cur {
		before := m.ideEditor.Line()
		m.ideEditor.CursorDown()
		if m.ideEditor.Line() == before {
			break // the buffer ran out of lines under the cursor
		}
	}
}

// saveIDEBuf writes the active buffer back, keeping the file's mode, and
// nudges the git pane — a save is exactly the kind of edit it exists to show.
// A file that changed on disk since it was loaded is not overwritten unless
// force says so (R); r reloads it instead.
func (m *Model) saveIDEBuf(force bool) tea.Cmd {
	b := m.ideBuf()
	if b == nil {
		return nil
	}
	m.stashIDEBuf()
	path := filepath.Join(m.ideFor, b.rel)
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
		if !force && !fi.ModTime().Equal(b.modTime) {
			b.stale = true
			m.err = errors.New(b.rel + " changed on disk — r reloads it (drops your edits), R saves anyway")
			return nil
		}
	}
	out := restoreWhitespace(b.rawLines, strings.Split(b.savedVal, "\n"), strings.Split(b.val, "\n"))
	content := strings.Join(out, "\n")
	if b.crlf {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		m.err = err
		return nil
	}
	b.rawLines = out
	b.savedVal = b.val
	b.dirty, b.stale = false, false
	if fi, err := os.Stat(path); err == nil {
		b.modTime = fi.ModTime()
	}
	m.ideSavedAt = time.Now()
	b.hl = highlightSource(b.val, b.rel)
	cmds := []tea.Cmd{m.reloadGit(), loadIDEGutterCmd(m.ideFor, b.rel)}
	// a save may release a pane pinned by its dirty buffers (see resetIDE)
	if dir := m.claudeDir(); dir != m.ideFor && !m.ideAnyDirty() {
		m.resetIDE(dir)
	}
	return tea.Batch(cmds...)
}

// reloadIDEBuf re-reads a buffer's file from disk, dropping whatever the
// buffer held, and keeps the reader's place.
func (m *Model) reloadIDEBuf(b *ideBuf) tea.Cmd {
	data, err := os.ReadFile(filepath.Join(m.ideFor, b.rel))
	if err != nil {
		b.stale = true
		return nil
	}
	cursor, scroll := b.cursor, b.scrollY
	fillIDEBuf(b, data)
	b.cursor = clampIdx(cursor, len(b.hl))
	b.scrollY = clampIdx(scroll, len(b.hl))
	if fi, err := os.Stat(filepath.Join(m.ideFor, b.rel)); err == nil {
		b.modTime = fi.ModTime()
	}
	if b == m.ideBuf() {
		m.ideEditor.SetValue(b.val)
		m.alignIDEEditor()
		m.recomputeIDEFind()
	}
	return loadIDEGutterCmd(m.ideFor, b.rel)
}

// refreshIDEDisk is the pane's answer to files changing under it — claude
// and shell commands edit the worktree constantly. The tree re-reads, clean
// buffers follow the disk, and dirty ones are marked stale rather than
// silently losing either side.
func (m *Model) refreshIDEDisk() []tea.Cmd {
	if m.ideFor == "" {
		return nil
	}
	m.refreshIDETree()
	var cmds []tea.Cmd
	for _, b := range m.ideBufs {
		fi, err := os.Stat(filepath.Join(m.ideFor, b.rel))
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
		if cmd := m.reloadIDEBuf(b); cmd != nil {
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

// ---- find ----

// recomputeIDEFind rebuilds the match list for the active buffer.
func (m *Model) recomputeIDEFind() {
	m.ideFindHits = nil
	b := m.ideBuf()
	if b == nil || m.ideFindQ == "" {
		return
	}
	q := strings.ToLower(m.ideFindQ)
	for i, ln := range strings.Split(b.val, "\n") {
		if strings.Contains(strings.ToLower(ln), q) {
			m.ideFindHits = append(m.ideFindHits, i)
		}
	}
}

// jumpIDEFind moves the cursor to the next (dir>0) or previous match,
// wrapping around the file.
func (m *Model) jumpIDEFind(dir int) {
	b := m.ideBuf()
	if b == nil || len(m.ideFindHits) == 0 {
		return
	}
	if dir > 0 {
		for _, h := range m.ideFindHits {
			if h > b.cursor {
				b.cursor = h
				m.settleIDEView()
				return
			}
		}
		b.cursor = m.ideFindHits[0]
	} else {
		for i := len(m.ideFindHits) - 1; i >= 0; i-- {
			if m.ideFindHits[i] < b.cursor {
				b.cursor = m.ideFindHits[i]
				m.settleIDEView()
				return
			}
		}
		b.cursor = m.ideFindHits[len(m.ideFindHits)-1]
	}
	m.settleIDEView()
}

// ---- layout & rendering ----

// idePaneSize is the ide pane's inner content area, mirroring gitPaneSize.
func (m Model) idePaneSize() (w, h int) {
	b := m.layout().ide
	return b.w - 4, b.h - 4
}

// ideTreeWidth is the explorer strip's width: a quarter-ish column, held
// steady whether or not a file is open so the pane doesn't reshape itself
// the moment one is.
func (m Model) ideTreeWidth(w int) int {
	return clampW(w*30/100, 12, 26)
}

// ideContentH is the pane rows left for the halves once the input bar (when
// open) is taken off the top — three rows, the border included.
func (m Model) ideContentH() int {
	_, h := m.idePaneSize()
	if m.ideInputKind != ideInputNone {
		h -= 3
	}
	if h < 1 {
		h = 1
	}
	return h
}

// ideViewH is the file half's content rows: the bordered tab bar's three rows
// sit above them.
func (m Model) ideViewH() int {
	h := m.ideContentH() - 3
	if h < 1 {
		h = 1
	}
	return h
}

// idePaneContent renders the pane's title and body for a w×h content area.
func (m Model) idePaneContent(w, h int) (string, string) {
	b := m.ideBufs
	var cur *ideBuf
	if m.ideCur >= 0 && m.ideCur < len(b) {
		cur = b[m.ideCur]
	}
	title := "ide"
	if cur != nil {
		title += " — " + cur.rel
		switch {
		case cur.dirty:
			title += warnStyle.Render(" ●")
		case time.Now().Before(m.ideSavedAt.Add(2 * time.Second)):
			title += okStyle.Render(" · saved ✓")
		}
		if cur.stale {
			title += errStyle.Render(" · changed on disk")
		}
	}
	switch {
	case m.ideEditing:
		title += okStyle.Render(" · editing") + dimStyle.Render(" — ctrl+s saves, esc views")
	case m.ideAnyDirty() && m.ideFor != m.claudeDir():
		title += warnStyle.Render(" · unsaved edits hold this worktree")
	case m.ideFindQ != "" && cur != nil:
		title += dimStyle.Render(fmt.Sprintf(" · ⌕%s %d", m.ideFindQ, len(m.ideFindHits)))
	}
	if w < 4 || h < 1 {
		return title, ""
	}

	var rows []string
	if m.ideInputKind != ideInputNone {
		bar := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(0, 1).
			Width(w - 2).
			Render(m.ideInput.View())
		rows = append(rows, strings.Split(maxWidthStyle(w).Render(bar), "\n")...)
	}
	ch := m.ideContentH()
	treeW := m.ideTreeWidth(w)
	tree := m.ideTreeRows(treeW, ch)
	// the divider between the halves wears the pane's chrome: it continues
	// into the title rule and bottom border (see idePart), and the whole
	// line follows focus with them
	div := metaStyle.Render(" │ ")
	if m.pane == paneIDE {
		div = okStyle.Render(" │ ")
	}
	edW := w - treeW - 3 // " │ " between the halves
	var right []string
	if len(m.ideBufs) == 0 {
		// the editor half keeps its place while empty, so opening a file
		// fills it in rather than reshaping the pane
		right = strings.Split(ideZeroState(edW, ch), "\n")
	} else {
		right = append(m.ideTabBar(edW), m.ideEditorRows(edW, m.ideViewH())...)
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
// active one open at the bottom, dirty and stale marked on the name. Three
// rows tall — ideViewH leaves room for it.
func (m Model) ideTabBar(w int) []string {
	activeText := paneTitleStyle
	activeBorder := subtle
	if m.pane == paneIDE {
		activeText = paneTitleFocus
		activeBorder = accent
	}
	parts := make([]string, 0, len(m.ideBufs))
	for i, b := range m.ideBufs {
		label := filepath.Base(b.rel)
		if b.dirty {
			label += " ●"
		}
		if b.stale {
			label += " ⚠"
		}
		st := lipgloss.NewStyle().Border(ideTabBorder).BorderForeground(subtle).Padding(0, 1)
		txt := dimStyle
		if i == m.ideCur {
			st = st.Border(ideTabActiveBorder).BorderForeground(activeBorder)
			txt = activeText
		}
		parts = append(parts, m.zones.Mark(ideTabZoneID(i), st.Render(txt.Render(label))))
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	lines := strings.Split(maxWidthStyle(w).Render(row), "\n")
	// the shelf the tabs hang from runs the editor's full width
	if n := len(lines); n > 0 {
		if used := lipgloss.Width(row); used < w {
			lines[n-1] += dimStyle.Render(strings.Repeat("─", w-used))
		}
	}
	return lines
}

// ideTreeRows is the explorer: the visible window of the tree, the selected
// row carrying the cursor when the tree has the keys.
func (m Model) ideTreeRows(w, h int) []string {
	if len(m.ideTree) == 0 {
		hint := "the worktree is empty"
		if m.ideFilter != "" {
			hint = "nothing matches “" + m.ideFilter + "”"
		}
		return []string{dimStyle.Render("no files"), "", dimStyle.Render(truncate(hint, w))}
	}
	start := clampIdx(m.ideScroll, len(m.ideTree))
	openRel := ""
	if b := m.ideBuf(); b != nil {
		openRel = b.rel
	}
	var rows []string
	for i := start; i < len(m.ideTree) && i < start+h; i++ {
		e := m.ideTree[i]
		marker := "  "
		if e.dir {
			marker = "▸ "
			if m.ideExpanded[e.rel] {
				marker = "▾ "
			}
		}
		name := e.name
		if e.dir {
			name += "/"
		}
		indent := e.depth
		if m.ideFilter != "" {
			indent = 0
		}
		line := strings.Repeat("  ", indent) + marker + name
		switch {
		case i == m.ideSel && m.pane == paneIDE && m.ideFocus == ideFocusTree:
			line = cursorStyle.Render(padRight(truncate(line, w-1), w-1))
		case i == m.ideSel:
			line = okStyle.Render(padRight(truncate(line, w-1), w-1))
		case e.dir:
			line = truncate(line, w)
		default:
			if e.rel == openRel {
				line = okStyle.Render(truncate(line, w))
			} else {
				line = dimStyle.Render(truncate(line, w))
			}
		}
		rows = append(rows, m.zones.Mark(ideZoneID(i), line))
	}
	return rows
}

// ideEditorRows is the file half under the tab bar: the highlighted buffer
// behind a gutter of git marks and line numbers. Editing renders the same
// rows — the hidden textarea only keeps the state — with a block cursor
// overlaid where it stands, and the window shifted right when the cursor
// walks past the pane's edge.
func (m Model) ideEditorRows(w, h int) []string {
	b := m.ideBuf()
	if b == nil {
		return nil
	}
	lines := b.hl
	numW := len(strconv.Itoa(len(lines)))
	avail := w - numW - 2
	if avail < 1 {
		avail = 1
	}
	start := clampIdx(b.scrollY, len(lines))
	curRow, curCol, hoff := -1, 0, 0
	var plain []string
	if m.ideEditing {
		curRow = m.ideEditor.Line()
		curCol = m.ideEditor.LineInfo().CharOffset
		if curCol >= avail {
			hoff = curCol - avail + 1
		}
		plain = strings.Split(b.val, "\n")
	}
	hits := map[int]bool{}
	for _, hit := range m.ideFindHits {
		hits[hit] = true
	}
	var rows []string
	for i := start; i < len(lines) && i < start+h; i++ {
		mark := " "
		switch b.gutter[i] {
		case '+':
			mark = okStyle.Render("▎")
		case '~':
			mark = warnStyle.Render("▎")
		}
		g := dimStyle
		switch {
		case i == curRow, i == b.cursor && !m.ideEditing && m.pane == paneIDE && m.ideFocus == ideFocusFile:
			g = cursorStyle
		case hits[i]:
			g = okStyle
		}
		line := ansi.Cut(lines[i], hoff, hoff+avail)
		if i == curRow && curRow < len(plain) {
			line = overlayIDECursor(line, plain[i], curCol, hoff)
		}
		rows = append(rows, mark+g.Render(fmt.Sprintf("%*d ", numW, i+1))+line)
	}
	return rows
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

func (m Model) keyIDE(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ideEditing {
		return m.keyIDEEditing(k)
	}
	if m.ideInputKind != ideInputNone {
		return m.keyIDEInput(k)
	}
	confirm := m.ideConfirm
	m.ideConfirm = "" // any key but the confirming one drops the question
	if m.ideFocus == ideFocusFile && m.ideBuf() != nil {
		return m.keyIDEFile(k, confirm)
	}
	return m.keyIDETree(k, confirm)
}

func (m Model) keyIDEEditing(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	b := m.ideBuf()
	switch k.String() {
	case "esc":
		m.ideEditing = false
		m.ideEditor.Blur()
		m.stashIDEBuf()
		b.hl = highlightSource(b.val, b.rel)
		b.cursor = clampIdx(m.ideEditor.Line(), len(b.hl))
		m.settleIDEView()
		m.recomputeIDEFind()
		return m, nil
	case "ctrl+s":
		return m, m.saveIDEBuf(false)
	}
	before := b.val
	var cmd tea.Cmd
	m.ideEditor, cmd = m.ideEditor.Update(k)
	m.stashIDEBuf()
	if b.val != before {
		b.hl = liveHighlight(b.val, b.rel)
	}
	b.cursor = clampIdx(m.ideEditor.Line(), len(b.hl))
	m.settleIDEView()
	return m, cmd
}

// liveHighlight is highlightSource under a budget: a buffer too large to
// re-color on every keystroke stays plain — but line-aligned — until esc.
func liveHighlight(src, filename string) []string {
	if len(src) > ideLiveHLMax {
		return strings.Split(src, "\n")
	}
	return highlightSource(src, filename)
}

func (m Model) keyIDEFile(k tea.KeyMsg, confirm string) (tea.Model, tea.Cmd) {
	b := m.ideBuf()
	h := m.ideViewH()
	switch k.String() {
	case "esc":
		if m.ideFindQ != "" {
			m.ideFindQ, m.ideFindHits = "", nil
			return m, nil
		}
		m.ideFocus = ideFocusTree
		return m, nil
	case "e", "i", "enter":
		m.ideEditing = true
		m.alignIDEEditor()
		return m, m.ideEditor.Focus()
	case "ctrl+s":
		return m, m.saveIDEBuf(false)
	case "R":
		return m, m.saveIDEBuf(true)
	case "r":
		if b.dirty && confirm != "reload" {
			m.ideConfirm = "reload"
			m.err = errors.New("unsaved edits — r again reloads from disk and drops them")
			return m, nil
		}
		return m, m.reloadIDEBuf(b)
	case "x":
		if b.dirty && confirm != "close" {
			m.ideConfirm = "close"
			m.err = errors.New("unsaved edits — x again closes and drops them, ctrl+s saves")
			return m, nil
		}
		m.closeIDEBuf(m.ideCur)
		return m, nil
	case "[", "ctrl+left":
		m.switchIDEBuf(-1)
		return m, nil
	case "]", "ctrl+right":
		m.switchIDEBuf(1)
		return m, nil
	case "/", "ctrl+f":
		return m.openIDEInput(ideInputFind, "⌕ ", "find in this file…", m.ideFindQ)
	case "n":
		m.jumpIDEFind(1)
	case "N":
		m.jumpIDEFind(-1)
	case "up", "k":
		m.moveIDECursor(-1)
	case "down", "j":
		m.moveIDECursor(1)
	case "pgup":
		m.moveIDECursor(-h)
	case "pgdown", "f":
		m.moveIDECursor(h)
	case "g", "home":
		b.cursor = 0
		m.settleIDEView()
	case "G", "end":
		b.cursor = len(b.hl) - 1
		m.settleIDEView()
	}
	return m, nil
}

func (m Model) keyIDETree(k tea.KeyMsg, confirm string) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "up", "k":
		m.moveIDESel(-1)
	case "down", "j":
		m.moveIDESel(1)
	case "enter", "l", "right":
		return m, m.openIDESel()
	case "h", "left":
		m.collapseIDESel()
	case "/":
		return m.openIDEInput(ideInputFilter, "/ ", "type to filter files…", m.ideFilter)
	case "a":
		seed := ""
		if e, ok := m.ideSelEntry(); ok {
			if e.dir {
				seed = e.rel + string(filepath.Separator)
			} else if d := filepath.Dir(e.rel); d != "." {
				seed = d + string(filepath.Separator)
			}
		}
		return m.openIDEInput(ideInputNew, "+ ", "path/for/the/new-file…", seed)
	case "R":
		if e, ok := m.ideSelEntry(); ok {
			return m.openIDEInput(ideInputRename, "→ ", "new path…", e.rel)
		}
	case "d":
		e, ok := m.ideSelEntry()
		if !ok {
			return m, nil
		}
		if confirm != "del:"+e.rel {
			m.ideConfirm = "del:" + e.rel
			m.err = errors.New("delete " + e.rel + "? d again confirms")
			return m, nil
		}
		return m, m.deleteIDEEntry(e)
	case "esc":
		if m.ideFilter != "" {
			m.ideFilter = ""
			m.refreshIDETree()
			return m, nil
		}
		if m.ideBuf() != nil {
			m.ideFocus = ideFocusFile
		}
	case "ctrl+s":
		return m, m.saveIDEBuf(false)
	case "r":
		return m, tea.Batch(m.refreshIDEDisk()...)
	}
	return m, nil
}

func (m Model) ideSelEntry() (ideEntry, bool) {
	if m.ideSel >= len(m.ideTree) {
		return ideEntry{}, false
	}
	return m.ideTree[m.ideSel], true
}

// ---- the input line ----

func (m Model) openIDEInput(kind int, prompt, placeholder, seed string) (tea.Model, tea.Cmd) {
	m.ideInputKind = kind
	m.ideInput.Prompt = prompt
	m.ideInput.Placeholder = placeholder
	m.ideInput.SetValue(seed)
	m.ideInput.CursorEnd()
	return m, m.ideInput.Focus()
}

func (m *Model) closeIDEInput() {
	m.ideInputKind = ideInputNone
	m.ideInput.Blur()
	m.ideInput.SetValue("")
}

func (m Model) keyIDEInput(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	kind := m.ideInputKind
	switch k.String() {
	case "esc":
		m.closeIDEInput()
		switch kind {
		case ideInputFilter:
			m.ideFilter = ""
			m.refreshIDETree()
		case ideInputFind:
			m.ideFindQ, m.ideFindHits = "", nil
		}
		return m, nil
	case "enter":
		val := strings.TrimSpace(m.ideInput.Value())
		m.closeIDEInput()
		switch kind {
		case ideInputFilter:
			// the filter stays applied; enter moves to picking from the list
		case ideInputFind:
			m.jumpIDEFind(1)
		case ideInputNew:
			return m, m.createIDEEntry(val)
		case ideInputRename:
			if e, ok := m.ideSelEntry(); ok {
				return m, m.renameIDEEntry(e, val)
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.ideInput, cmd = m.ideInput.Update(k)
	switch kind {
	case ideInputFilter:
		m.ideFilter = strings.TrimSpace(m.ideInput.Value())
		m.ideSel, m.ideScroll = 0, 0
		m.refreshIDETree()
	case ideInputFind:
		m.ideFindQ = m.ideInput.Value()
		m.recomputeIDEFind()
	}
	return m, cmd
}

// ---- file operations ----

// createIDEEntry makes a file — or a directory, when the name ends with a
// slash — relative to the worktree root, parents included.
func (m *Model) createIDEEntry(rel string) tea.Cmd {
	if rel == "" || m.ideFor == "" {
		return nil
	}
	isDir := strings.HasSuffix(rel, string(filepath.Separator)) || strings.HasSuffix(rel, "/")
	rel = filepath.Clean(strings.TrimRight(rel, "/"+string(filepath.Separator)))
	if rel == "." || strings.HasPrefix(rel, "..") {
		m.err = errors.New("the new path has to stay inside the worktree")
		return nil
	}
	path := filepath.Join(m.ideFor, rel)
	if isDir {
		if err := os.MkdirAll(path, 0o755); err != nil {
			m.err = err
			return nil
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			m.err = err
			return nil
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			m.err = err
			return nil
		}
		f.Close()
	}
	for d := filepath.Dir(rel); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
		m.ideExpanded[d] = true // unfold down to what was just made
	}
	m.refreshIDETree()
	m.selectIDERel(rel)
	var cmds []tea.Cmd
	if !isDir {
		cmds = append(cmds, m.openIDEFile(rel))
	}
	return tea.Batch(append(cmds, m.reloadGit())...)
}

// renameIDEEntry moves a file or directory within the worktree.
func (m *Model) renameIDEEntry(e ideEntry, to string) tea.Cmd {
	to = filepath.Clean(to)
	if to == "" || to == "." || strings.HasPrefix(to, "..") {
		m.err = errors.New("the new path has to stay inside the worktree")
		return nil
	}
	if to == e.rel {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(m.ideFor, to)), 0o755); err != nil {
		m.err = err
		return nil
	}
	if err := os.Rename(filepath.Join(m.ideFor, e.rel), filepath.Join(m.ideFor, to)); err != nil {
		m.err = err
		return nil
	}
	for _, b := range m.ideBufs { // open tabs follow their file
		switch {
		case b.rel == e.rel:
			b.rel = to
		case e.dir && strings.HasPrefix(b.rel, e.rel+string(filepath.Separator)):
			b.rel = to + strings.TrimPrefix(b.rel, e.rel)
		}
	}
	m.refreshIDETree()
	m.selectIDERel(to)
	return m.reloadGit()
}

// deleteIDEEntry removes a file or directory, closing any tab it had open.
func (m *Model) deleteIDEEntry(e ideEntry) tea.Cmd {
	if err := os.RemoveAll(filepath.Join(m.ideFor, e.rel)); err != nil {
		m.err = err
		return nil
	}
	for i := len(m.ideBufs) - 1; i >= 0; i-- {
		rel := m.ideBufs[i].rel
		if rel == e.rel || (e.dir && strings.HasPrefix(rel, e.rel+string(filepath.Separator))) {
			m.closeIDEBuf(i)
		}
	}
	m.refreshIDETree()
	return m.reloadGit()
}

func (m *Model) selectIDERel(rel string) {
	for i, e := range m.ideTree {
		if e.rel == rel {
			m.ideSel = i
			m.settleIDETree()
			return
		}
	}
}

// openIDESel acts on the tree's selected row: folders fold, files open. With
// a filter applied, a folder jumps the tree there instead — the flat match
// list has nowhere to unfold into.
func (m *Model) openIDESel() tea.Cmd {
	e, ok := m.ideSelEntry()
	if !ok {
		return nil
	}
	if e.dir {
		if m.ideFilter != "" {
			m.ideFilter = ""
			m.closeIDEInput()
			for d := e.rel; d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
				m.ideExpanded[d] = true
			}
			m.refreshIDETree()
			m.selectIDERel(e.rel)
			return nil
		}
		m.ideExpanded[e.rel] = !m.ideExpanded[e.rel]
		m.refreshIDETree()
		return nil
	}
	return m.openIDEFile(e.rel)
}

// collapseIDESel folds the selected directory, or climbs to the parent of a
// file (or an already-folded directory).
func (m *Model) collapseIDESel() {
	e, ok := m.ideSelEntry()
	if !ok || m.ideFilter != "" {
		return
	}
	if e.dir && m.ideExpanded[e.rel] {
		m.ideExpanded[e.rel] = false
		m.refreshIDETree()
		return
	}
	for i := m.ideSel - 1; i >= 0; i-- {
		if m.ideTree[i].dir && m.ideTree[i].depth == e.depth-1 {
			m.ideSel = i
			m.settleIDETree()
			return
		}
	}
}

func (m *Model) moveIDESel(n int) {
	m.ideSel = clampIdx(m.ideSel+n, len(m.ideTree))
	m.settleIDETree()
}

// settleIDETree keeps the selection inside the explorer's window.
func (m *Model) settleIDETree() {
	h := m.ideContentH()
	if m.ideSel < m.ideScroll {
		m.ideScroll = m.ideSel
	}
	if m.ideSel >= m.ideScroll+h {
		m.ideScroll = m.ideSel - h + 1
	}
}

func (m *Model) moveIDECursor(n int) {
	if b := m.ideBuf(); b != nil {
		b.cursor = clampIdx(b.cursor+n, len(b.hl))
		m.settleIDEView()
	}
}

// settleIDEView keeps the cursor line inside the file view's window.
func (m *Model) settleIDEView() {
	b := m.ideBuf()
	if b == nil {
		return
	}
	h := m.ideViewH()
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
// explorer strip or the open file.
func (m *Model) scrollIDERegion(msg tea.MouseMsg, up bool) {
	w, _ := m.idePaneSize()
	x, _, ok := m.paneBodyPos(paneIDE, msg)
	overTree := !ok || x < m.ideTreeWidth(w) || len(m.ideBufs) == 0
	step := 3
	if up {
		step = -3
	}
	switch {
	case overTree:
		max := len(m.ideTree) - m.ideContentH()
		if max < 0 {
			max = 0
		}
		m.ideScroll = clampW(m.ideScroll+step, 0, max)
	case m.ideEditing:
		for i := 0; i < 3; i++ {
			if up {
				m.ideEditor.CursorUp()
			} else {
				m.ideEditor.CursorDown()
			}
		}
		if b := m.ideBuf(); b != nil {
			b.cursor = clampIdx(m.ideEditor.Line(), len(b.hl))
			m.settleIDEView()
		}
	default:
		if b := m.ideBuf(); b != nil {
			max := len(b.hl) - m.ideViewH()
			if max < 0 {
				max = 0
			}
			b.scrollY = clampW(b.scrollY+step, 0, max)
		}
	}
}

// clickIDE handles a click inside the pane: tabs and tree rows select and
// open, a click in the file half moves the cursor to that line.
func (m Model) clickIDE(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	for i := range m.ideBufs {
		if m.clicked(msg, ideTabZoneID(i)) {
			m.activateIDEBuf(i)
			m.ideFocus = ideFocusFile
			return m.focusPane(paneIDE)
		}
	}
	for i := range m.ideTree {
		if m.clicked(msg, ideZoneID(i)) {
			m.ideSel = i
			m.ideFocus = ideFocusTree
			cmd := m.openIDESel()
			mm, fcmd := m.focusPane(paneIDE)
			return mm, tea.Batch(cmd, fcmd)
		}
	}
	if b := m.ideBuf(); b != nil && !m.ideEditing {
		if x, y, ok := m.paneBodyPos(paneIDE, msg); ok {
			if m.ideInputKind != ideInputNone {
				y -= 3 // the bordered input bar sits above both halves
			}
			w, _ := m.idePaneSize()
			if x >= m.ideTreeWidth(w) && y >= 3 { // rows 0-2 are the tab bar
				m.ideFocus = ideFocusFile
				b.cursor = clampIdx(b.scrollY+y-3, len(b.hl))
			}
		}
	}
	return m.focusPane(paneIDE)
}
