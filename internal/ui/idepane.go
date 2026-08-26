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

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The ide pane sits between claude and git: a file explorer over the selected
// worktree beside a syntax-highlighted view of the open file (a scaled-down
// port of croft, github.com/vitali87/croft). Browsing is read-only; e drops
// into an editable buffer, ctrl+s writes it back, esc climbs back out —
// editor → file view → tree.

// which half of the pane the keyboard works
const (
	ideFocusTree = iota
	ideFocusFile
)

// ideMaxFileSize keeps the editor to files a textarea buffer is comfortable
// with; anything bigger is for a real editor in the shell pane.
const ideMaxFileSize = 1 << 20

// ideEntry is one visible row of the explorer tree.
type ideEntry struct {
	rel   string // path relative to the worktree root
	name  string
	depth int
	dir   bool
}

func ideZoneID(i int) string { return "ide:t:" + strconv.Itoa(i) }

// resetIDE points the pane at another worktree. A dirty buffer pins the pane
// where it is — moving the cursor through the issues list must not throw away
// edits — until it is saved or closed; saving and closing re-sync themselves.
func (m *Model) resetIDE(dir string) {
	if m.ideDirty {
		return
	}
	m.ideFor = dir
	m.ideTree = nil
	m.ideSel, m.ideScroll = 0, 0
	m.ideExpanded = map[string]bool{}
	m.closeIDEFile()
	if dir != "" {
		m.refreshIDETree()
	}
}

// closeIDEFile drops the open buffer and hands the keys back to the tree.
func (m *Model) closeIDEFile() {
	m.ideFile = ""
	m.ideHL = nil
	m.ideEditor.SetValue("")
	m.ideSavedVal = ""
	m.ideRawLines, m.ideCRLF = nil, false
	m.ideDirty, m.ideEditing = false, false
	m.ideEditor.Blur()
	m.ideCursor, m.ideScrollY = 0, 0
	m.ideFocus = ideFocusTree
}

// refreshIDETree rebuilds the visible tree from disk: the worktree root's
// entries, with expanded directories inlined depth-first. Directories load
// lazily, so a huge repo costs only what is unfolded.
func (m *Model) refreshIDETree() {
	sel := ""
	if m.ideSel < len(m.ideTree) {
		sel = m.ideTree[m.ideSel].rel
	}
	m.ideTree = m.ideTreeLevel("", 0)
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
		if e.Name() == ".git" {
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

// openIDEFile loads a file into the buffer and highlights it.
func (m *Model) openIDEFile(rel string) {
	data, err := os.ReadFile(filepath.Join(m.ideFor, rel))
	if err != nil {
		m.err = err
		return
	}
	if len(data) > ideMaxFileSize {
		m.err = fmt.Errorf("%s is %dKB — too large for the ide pane", rel, len(data)/1024)
		return
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		m.err = errors.New(rel + " looks binary")
		return
	}
	m.closeIDEFile()
	m.ideFile = rel
	// the textarea's sanitizer flattens tabs to spaces and doubles CRLF, so
	// the buffer is a display form: the file's own lines are kept aside and
	// a save reconstructs whatever an edit didn't reach (restoreWhitespace)
	text := string(data)
	m.ideCRLF = strings.Contains(text, "\r\n")
	if m.ideCRLF {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	m.ideRawLines = strings.Split(text, "\n")
	m.ideEditor.SetValue(text)
	m.ideSavedVal = m.ideEditor.Value()
	m.ideHL = highlightSource(m.ideSavedVal, rel)
	m.ideFocus = ideFocusFile
	m.alignIDEEditor() // SetValue leaves the buffer's cursor at the end
}

// alignIDEEditor moves the buffer's cursor to the view's current line, so
// editing starts where the reader was looking.
func (m *Model) alignIDEEditor() {
	m.ideEditor.CursorStart()
	for m.ideEditor.Line() > m.ideCursor {
		m.ideEditor.CursorUp()
	}
	for m.ideEditor.Line() < m.ideCursor {
		before := m.ideEditor.Line()
		m.ideEditor.CursorDown()
		if m.ideEditor.Line() == before {
			break // the buffer ran out of lines under the cursor
		}
	}
}

// saveIDEFile writes the buffer back, keeping the file's mode, and nudges the
// git pane — a save is exactly the kind of edit it exists to show.
func (m *Model) saveIDEFile() tea.Cmd {
	if m.ideFile == "" {
		return nil
	}
	path := filepath.Join(m.ideFor, m.ideFile)
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
	}
	out := restoreWhitespace(m.ideRawLines,
		strings.Split(m.ideSavedVal, "\n"), strings.Split(m.ideEditor.Value(), "\n"))
	content := strings.Join(out, "\n")
	if m.ideCRLF {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		m.err = err
		return nil
	}
	m.ideRawLines = out
	m.ideSavedVal = m.ideEditor.Value()
	m.ideDirty = false
	m.ideSavedAt = time.Now()
	m.ideHL = highlightSource(m.ideSavedVal, m.ideFile)
	cmd := m.reloadGit()
	// a save releases a pane pinned by its dirty buffer (see resetIDE)
	if dir := m.claudeDir(); dir != m.ideFor {
		m.resetIDE(dir)
	}
	return cmd
}

// restoreWhitespace rebuilds the file from the edited buffer, keeping the
// original bytes of every line the edit didn't reach: raw is the file as
// loaded, base its form in the buffer (tabs flattened), cur the buffer now.
// Lines outside the edited span — the common prefix and suffix of base and
// cur — come from raw; the span itself is whatever was typed.
func restoreWhitespace(raw, base, cur []string) []string {
	if len(raw) != len(base) {
		return cur // lost the alignment somehow; trust the buffer
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

// idePaneSize is the ide pane's inner content area, mirroring gitPaneSize.
func (m Model) idePaneSize() (w, h int) {
	b := m.layout().ide
	return b.w - 4, b.h - 4
}

// ideTreeWidth is the explorer strip's width: the whole pane while nothing is
// open, a quarter-ish column beside the editor once something is.
func (m Model) ideTreeWidth(w int) int {
	if m.ideFile == "" {
		return w
	}
	return clampW(w*30/100, 12, 26)
}

// idePaneContent renders the pane's title and body for a w×h content area.
func (m Model) idePaneContent(w, h int) (string, string) {
	title := "ide"
	if m.ideFile != "" {
		title += " — " + m.ideFile
	}
	switch {
	case m.ideDirty:
		title += warnStyle.Render(" ●")
	case time.Now().Before(m.ideSavedAt.Add(2 * time.Second)):
		title += okStyle.Render(" · saved ✓")
	}
	if m.ideEditing {
		title += okStyle.Render(" · editing") + dimStyle.Render(" — ctrl+s saves, esc views")
	} else if m.ideDirty && m.ideFor != m.claudeDir() {
		title += warnStyle.Render(" · unsaved edits hold this worktree")
	}
	if w < 4 || h < 1 {
		return title, ""
	}

	treeW := m.ideTreeWidth(w)
	tree := m.ideTreeRows(treeW, h)
	if m.ideFile == "" {
		return title, strings.Join(tree, "\n")
	}

	edW := w - treeW - 3 // " │ " between the halves
	var body []string
	editor := m.ideEditorRows(edW, h)
	div := dimStyle.Render(" │ ")
	for i := 0; i < h; i++ {
		t, e := "", ""
		if i < len(tree) {
			t = tree[i]
		}
		if i < len(editor) {
			e = editor[i]
		}
		body = append(body, padTo(t, treeW)+div+e)
	}
	return title, strings.Join(body, "\n")
}

// ideTreeRows is the explorer: the visible window of the tree, the selected
// row carrying the cursor when the tree has the keys.
func (m Model) ideTreeRows(w, h int) []string {
	if len(m.ideTree) == 0 {
		return []string{dimStyle.Render("no files"), "", dimStyle.Render(truncate("the worktree is empty", w))}
	}
	start := m.ideScroll
	if start > len(m.ideTree)-1 {
		start = len(m.ideTree) - 1
	}
	if start < 0 {
		start = 0
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
		line := strings.Repeat("  ", e.depth) + marker + name
		switch {
		case i == m.ideSel && m.pane == paneIDE && m.ideFocus == ideFocusTree:
			line = cursorStyle.Render(padRight(truncate(line, w-1), w-1))
		case i == m.ideSel:
			line = okStyle.Render(padRight(truncate(line, w-1), w-1))
		case e.dir:
			line = truncate(line, w)
		default:
			if e.rel == m.ideFile {
				line = okStyle.Render(truncate(line, w))
			} else {
				line = dimStyle.Render(truncate(line, w))
			}
		}
		rows = append(rows, m.zones.Mark(ideZoneID(i), line))
	}
	return rows
}

// ideEditorRows is the pane's right half: the textarea while editing, and
// otherwise the highlighted file with a line-number gutter.
func (m Model) ideEditorRows(w, h int) []string {
	if m.ideEditing {
		rows := strings.Split(m.ideEditor.View(), "\n")
		for i, r := range rows {
			rows[i] = maxWidthStyle(w).Render(r)
		}
		return rows
	}
	lines := m.ideHL
	numW := len(strconv.Itoa(len(lines)))
	start := m.ideScrollY
	if start > len(lines)-1 {
		start = len(lines) - 1
	}
	if start < 0 {
		start = 0
	}
	var rows []string
	for i := start; i < len(lines) && i < start+h; i++ {
		g := dimStyle
		if i == m.ideCursor && m.pane == paneIDE && m.ideFocus == ideFocusFile {
			g = cursorStyle
		}
		gutter := g.Render(fmt.Sprintf("%*d ", numW, i+1))
		rows = append(rows, gutter+maxWidthStyle(w-numW-1).Render(lines[i]))
	}
	return rows
}

// ---- keyboard ----

func (m Model) keyIDE(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ideEditing {
		switch k.String() {
		case "esc":
			m.ideEditing = false
			m.ideEditor.Blur()
			m.ideDirty = m.ideEditor.Value() != m.ideSavedVal
			m.ideHL = highlightSource(m.ideEditor.Value(), m.ideFile)
			m.ideCursor = clampIdx(m.ideEditor.Line(), len(m.ideHL))
			m.settleIDEView()
			return m, nil
		case "ctrl+s":
			m.ideDirty = m.ideEditor.Value() != m.ideSavedVal
			return m, m.saveIDEFile()
		}
		var cmd tea.Cmd
		m.ideEditor, cmd = m.ideEditor.Update(k)
		m.ideDirty = m.ideEditor.Value() != m.ideSavedVal
		return m, cmd
	}

	if m.ideFocus == ideFocusFile && m.ideFile != "" {
		_, h := m.idePaneSize()
		if h < 1 {
			h = 1
		}
		switch k.String() {
		case "esc":
			m.ideFocus = ideFocusTree
			return m, nil
		case "e", "i", "enter":
			m.ideEditing = true
			m.alignIDEEditor()
			return m, m.ideEditor.Focus()
		case "ctrl+s":
			return m, m.saveIDEFile()
		case "up", "k":
			m.moveIDECursor(-1)
		case "down", "j":
			m.moveIDECursor(1)
		case "pgup", "b":
			m.moveIDECursor(-h)
		case "pgdown", "f":
			m.moveIDECursor(h)
		case "g", "home":
			m.ideCursor = 0
			m.settleIDEView()
		case "G", "end":
			m.ideCursor = len(m.ideHL) - 1
			m.settleIDEView()
		}
		return m, nil
	}

	// the explorer has the keys
	switch k.String() {
	case "up", "k":
		m.moveIDESel(-1)
	case "down", "j":
		m.moveIDESel(1)
	case "enter", "l", "right":
		return m, m.openIDESel()
	case "h", "left":
		m.collapseIDESel()
	case "esc":
		if m.ideFile != "" {
			// a second esc from the tree closes the file — dirty buffers ask
			// for a save first rather than vanishing
			if m.ideDirty {
				m.err = errors.New("unsaved edits — ctrl+s saves them first")
				return m, nil
			}
			m.closeIDEFile()
			if dir := m.claudeDir(); dir != m.ideFor {
				m.resetIDE(dir)
			}
		}
	case "ctrl+s":
		return m, m.saveIDEFile()
	case "r":
		m.refreshIDETree()
	}
	return m, nil
}

// openIDESel acts on the tree's selected row: folders fold, files open.
func (m *Model) openIDESel() tea.Cmd {
	if m.ideSel >= len(m.ideTree) {
		return nil
	}
	e := m.ideTree[m.ideSel]
	if e.dir {
		m.ideExpanded[e.rel] = !m.ideExpanded[e.rel]
		m.refreshIDETree()
		return nil
	}
	if m.ideDirty && e.rel != m.ideFile {
		m.err = errors.New("unsaved edits — ctrl+s saves them first")
		return nil
	}
	m.openIDEFile(e.rel)
	return nil
}

// collapseIDESel folds the selected directory, or climbs to the parent of a
// file (or an already-folded directory).
func (m *Model) collapseIDESel() {
	if m.ideSel >= len(m.ideTree) {
		return
	}
	e := m.ideTree[m.ideSel]
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
	_, h := m.idePaneSize()
	if h < 1 {
		h = 1
	}
	if m.ideSel < m.ideScroll {
		m.ideScroll = m.ideSel
	}
	if m.ideSel >= m.ideScroll+h {
		m.ideScroll = m.ideSel - h + 1
	}
}

func (m *Model) moveIDECursor(n int) {
	m.ideCursor = clampIdx(m.ideCursor+n, len(m.ideHL))
	m.settleIDEView()
}

// settleIDEView keeps the cursor line inside the editor's window.
func (m *Model) settleIDEView() {
	_, h := m.idePaneSize()
	if h < 1 {
		h = 1
	}
	if m.ideCursor < m.ideScrollY {
		m.ideScrollY = m.ideCursor
	}
	if m.ideCursor >= m.ideScrollY+h {
		m.ideScrollY = m.ideCursor - h + 1
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
	w, h := m.idePaneSize()
	x, _, ok := m.paneBodyPos(paneIDE, msg)
	overTree := m.ideFile == "" || (ok && x < m.ideTreeWidth(w))
	step := 3
	if up {
		step = -3
	}
	switch {
	case overTree:
		max := len(m.ideTree) - h
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
	default:
		max := len(m.ideHL) - h
		if max < 0 {
			max = 0
		}
		m.ideScrollY = clampW(m.ideScrollY+step, 0, max)
	}
}

// clickIDE handles a click inside the pane: tree rows select and open, a
// click in the file half moves the cursor to that line.
func (m Model) clickIDE(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	for i := range m.ideTree {
		if m.clicked(msg, ideZoneID(i)) {
			m.ideSel = i
			m.ideFocus = ideFocusTree
			cmd := m.openIDESel()
			mm, fcmd := m.focusPane(paneIDE)
			return mm, tea.Batch(cmd, fcmd)
		}
	}
	if m.ideFile != "" && !m.ideEditing {
		if x, y, ok := m.paneBodyPos(paneIDE, msg); ok {
			w, _ := m.idePaneSize()
			if x >= m.ideTreeWidth(w) {
				m.ideFocus = ideFocusFile
				m.ideCursor = clampIdx(m.ideScrollY+y, len(m.ideHL))
			}
		}
	}
	return m.focusPane(paneIDE)
}
