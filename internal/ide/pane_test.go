package ide

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/markcipolla/treeline/internal/gitx"
)

// ideTestPane is a pane over a temp worktree: a file at the root, a folded
// directory with another inside it. width mirrors the app frame the tests
// were written against — the pane's content box comes out width-4.
func ideTestPane(t *testing.T, width int) *Pane {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIDEFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	writeIDEFile(t, dir, filepath.Join("sub", "util.go"), "package sub\n")
	p := New(zone.New(), true)
	p.SetSize(width-4, 36)
	p.SetWorktree(dir)
	return &p
}

func writeIDEFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// typeIDE feeds a string through the pane's key handler one rune at a time.
func typeIDE(t *testing.T, p *Pane, s string) {
	t.Helper()
	for _, r := range s {
		p.keyIDE(runes(string(r)))
	}
}

func keyIDE(t *testing.T, p *Pane, k tea.KeyMsg) {
	t.Helper()
	p.keyIDE(k)
}

// keyIDECmd is keyIDE keeping the command, for the flows that spawn one.
func keyIDECmd(t *testing.T, p *Pane, k tea.KeyMsg) tea.Cmd {
	t.Helper()
	return p.keyIDE(k)
}

// grepResult fakes a finished search landing back in the pane.
func grepResult(p *Pane, files ...gitx.GrepFile) {
	p.applyIDEGrepMsg(ideGrepMsg{dir: p.ideFor, query: p.ideGrepQ, files: files})
}

// indexOfIDERel finds a tree row by its relative path.
func indexOfIDERel(t *testing.T, p *Pane, rel string) int {
	t.Helper()
	for i, e := range p.ideTree {
		if e.rel == rel {
			return i
		}
	}
	t.Fatalf("no tree row for %q: %+v", rel, p.ideTree)
	return 0
}

func ideEditLines(p *Pane) []string { return strings.Split(p.ideEditor.Value(), "\n") }

// TestIDETreeListsAndOpens: the explorer lists the worktree with directories
// first, unfolds them in place, and enter on a file opens it highlighted.
func TestIDETreeListsAndOpens(t *testing.T) {
	p := ideTestPane(t, 200)

	if len(p.ideTree) != 2 {
		t.Fatalf("tree has %d rows, want 2: %+v", len(p.ideTree), p.ideTree)
	}
	if !p.ideTree[0].dir || p.ideTree[0].name != "sub" {
		t.Errorf("directories should sort first, got %+v", p.ideTree[0])
	}

	// unfold sub/: its child joins the tree under it
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if len(p.ideTree) != 3 || p.ideTree[1].rel != filepath.Join("sub", "util.go") {
		t.Fatalf("expected sub/util.go inlined after unfolding, got %+v", p.ideTree)
	}

	// open main.go (last row)
	p.ideSel = 2
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	b := p.ideBuf()
	if b == nil || b.rel != "main.go" {
		t.Fatalf("active buffer = %+v, want main.go", b)
	}
	if p.ideFocus != ideFocusFile {
		t.Error("opening a file should hand the keys to the file view")
	}
	if len(b.hl) != 4 { // three lines + the trailing newline's empty one
		t.Errorf("highlighted %d lines, want 4", len(b.hl))
	}

	title, _ := p.idePaneContent(60, 20)
	if !strings.Contains(title, "main.go") {
		t.Errorf("pane title %q should carry the open file", title)
	}
}

// TestIDEEditAndSave: e drops into the buffer, typing marks it dirty, and
// ctrl+s writes the file back and clears the mark.
func TestIDEEditAndSave(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true

	p.ideSel = 1 // main.go
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	keyIDE(t, p, runes("e"))
	if !p.ideEditing {
		t.Fatal("e should enter edit mode")
	}

	keyIDE(t, p, runes("x"))
	if !p.ideBuf().dirty {
		t.Fatal("typing should mark the buffer dirty")
	}

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyCtrlS})
	if p.ideBuf().dirty {
		t.Error("saving should clear the dirty mark")
	}
	data, err := os.ReadFile(filepath.Join(p.ideFor, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "xpackage main\n\nfunc main() {}\n"; string(data) != want {
		t.Errorf("saved file is %q, want %q", data, want)
	}
}

// TestIDESaveKeepsUntouchedWhitespace: the buffer flattens tabs to spaces on
// load, so a save must not spray that across the file — lines the edit never
// reached keep their own bytes.
func TestIDESaveKeepsUntouchedWhitespace(t *testing.T) {
	p := ideTestPane(t, 200)
	dir := p.ideFor
	writeIDEFile(t, dir, "tabs.go", "func a() {\n\tone()\n\ttwo()\n}\n")
	p.refreshIDETree()

	p.openIDEFile("tabs.go")
	if !strings.Contains(p.ideBuf().val, "    one()") {
		t.Fatalf("expected the buffer to carry flattened tabs: %q", p.ideBuf().val)
	}
	keyIDE(t, p, runes("e"))
	keyIDE(t, p, runes("x")) // edits line 1 only
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyCtrlS})

	data, err := os.ReadFile(filepath.Join(dir, "tabs.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := "xfunc a() {\n\tone()\n\ttwo()\n}\n"
	if string(data) != want {
		t.Errorf("saved file rewrote untouched whitespace:\ngot  %q\nwant %q", data, want)
	}
}

// TestRestoreWhitespaceMultiRegion: two separate edit regions must not bleed
// into the untouched lines between them — the line diff keeps the file's own
// bytes for every kept line, wherever it sits.
func TestRestoreWhitespaceMultiRegion(t *testing.T) {
	raw := []string{"top", "\tkeep1", "\tkeep2", "\tkeep3", "bottom", ""}
	base := []string{"top", "    keep1", "    keep2", "    keep3", "bottom", ""}
	cur := []string{"TOP", "    keep1", "    keep2", "    keep3", "BOTTOM", ""}
	got := restoreWhitespace(raw, base, cur)
	want := []string{"TOP", "\tkeep1", "\tkeep2", "\tkeep3", "BOTTOM", ""}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("multi-region restore:\ngot  %q\nwant %q", got, want)
	}

	// inserted lines land between kept ones without shifting their bytes
	cur2 := []string{"top", "    keep1", "new line", "    keep2", "    keep3", "bottom", ""}
	got2 := restoreWhitespace(raw, base, cur2)
	want2 := []string{"top", "\tkeep1", "new line", "\tkeep2", "\tkeep3", "bottom", ""}
	if strings.Join(got2, "\n") != strings.Join(want2, "\n") {
		t.Errorf("insertion restore:\ngot  %q\nwant %q", got2, want2)
	}

	// a deleted line vanishes; its neighbours keep their bytes
	cur3 := []string{"top", "    keep1", "    keep3", "bottom", ""}
	got3 := restoreWhitespace(raw, base, cur3)
	want3 := []string{"top", "\tkeep1", "\tkeep3", "bottom", ""}
	if strings.Join(got3, "\n") != strings.Join(want3, "\n") {
		t.Errorf("deletion restore:\ngot  %q\nwant %q", got3, want3)
	}
}

// TestIDETabs: a second file opens into its own tab; switching tabs keeps
// each buffer's edits; closing a dirty tab takes a second x.
func TestIDETabs(t *testing.T) {
	p := ideTestPane(t, 200)
	p.openIDEFile("main.go")
	keyIDE(t, p, runes("e"))
	typeIDE(t, p, "zz")
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc}) // back to view, dirty

	p.openIDEFile(filepath.Join("sub", "util.go"))
	if len(p.ideBufs) != 2 || p.ideBuf().rel != filepath.Join("sub", "util.go") {
		t.Fatalf("want 2 tabs with util.go active, got %d, active %v", len(p.ideBufs), p.ideBuf())
	}

	keyIDE(t, p, runes("[")) // back to main.go
	b := p.ideBuf()
	if b.rel != "main.go" || !b.dirty || !strings.HasPrefix(b.val, "zz") {
		t.Fatalf("tab switch lost the dirty buffer: %+v", b)
	}
	if !strings.HasPrefix(p.ideEditor.Value(), "zz") {
		t.Error("the editor should hold the re-activated tab's text")
	}

	keyIDE(t, p, runes("x")) // dirty: first x only warns
	if len(p.ideBufs) != 2 {
		t.Fatal("a dirty tab must not close on the first x")
	}
	keyIDE(t, p, runes("x"))
	if len(p.ideBufs) != 1 || p.ideBuf().rel != filepath.Join("sub", "util.go") {
		t.Fatalf("second x should close the tab, got %d tabs", len(p.ideBufs))
	}
}

// TestIDEStaleSave: a file that changed on disk since it was loaded is not
// silently overwritten — ctrl+s refuses, R forces, r reloads.
func TestIDEStaleSave(t *testing.T) {
	p := ideTestPane(t, 200)
	dir := p.ideFor
	p.openIDEFile("main.go")
	keyIDE(t, p, runes("e"))
	keyIDE(t, p, runes("y"))
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc})

	// someone else rewrites the file underneath
	writeIDEFile(t, dir, "main.go", "package main // external\n")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "main.go"), future, future); err != nil {
		t.Fatal(err)
	}

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyCtrlS})
	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "external") {
		t.Fatal("ctrl+s overwrote a file that changed on disk")
	}
	if p.Err == nil || !p.ideBuf().stale {
		t.Errorf("blocked save should explain itself and mark the tab: err=%v stale=%v", p.Err, p.ideBuf().stale)
	}

	// R saves anyway
	keyIDE(t, p, runes("R"))
	data, _ = os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.HasPrefix(string(data), "y") {
		t.Errorf("R should force the save, disk has %q", data)
	}

	// ...and after an external change with a clean buffer, r reloads
	writeIDEFile(t, dir, "main.go", "package main // newer\n")
	keyIDE(t, p, runes("r"))
	if !strings.Contains(p.ideBuf().val, "newer") {
		t.Errorf("r should reload from disk, buffer has %q", p.ideBuf().val)
	}
}

// TestIDERefreshDisk: files changing under the pane re-read themselves when
// clean and are marked stale when dirty, instead of losing either side.
func TestIDERefreshDisk(t *testing.T) {
	p := ideTestPane(t, 200)
	dir := p.ideFor
	p.openIDEFile("main.go")
	p.openIDEFile(filepath.Join("sub", "util.go"))
	keyIDE(t, p, runes("e"))
	keyIDE(t, p, runes("q")) // util.go dirty
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc})

	future := time.Now().Add(2 * time.Second)
	writeIDEFile(t, dir, "main.go", "package main // fresh\n")
	writeIDEFile(t, dir, filepath.Join("sub", "util.go"), "package sub // fresh\n")
	for _, rel := range []string{"main.go", filepath.Join("sub", "util.go")} {
		if err := os.Chtimes(filepath.Join(dir, rel), future, future); err != nil {
			t.Fatal(err)
		}
	}

	p.refreshIDEDisk()
	var clean, dirty *ideBuf
	for _, b := range p.ideBufs {
		if b.rel == "main.go" {
			clean = b
		} else {
			dirty = b
		}
	}
	if !strings.Contains(clean.val, "fresh") {
		t.Errorf("clean buffer should follow the disk, has %q", clean.val)
	}
	if !dirty.stale || !strings.HasPrefix(dirty.val, "q") {
		t.Errorf("dirty buffer should keep its edits and go stale: stale=%v val=%q", dirty.stale, dirty.val)
	}
}

// TestIDEDirtyBufferPinsThePane: pointing the pane at another worktree must
// not throw unsaved edits away — the pane stays where its edits are until
// they are saved, then follows the ask on its own.
func TestIDEDirtyBufferPinsThePane(t *testing.T) {
	p := ideTestPane(t, 200)
	first := p.ideFor
	second := t.TempDir()
	p.openIDEFile("main.go")
	keyIDE(t, p, runes("e"))
	keyIDE(t, p, runes("y"))

	p.SetWorktree(second)
	if p.ideFor != first || p.ideBuf() == nil {
		t.Fatalf("dirty buffer discarded: ideFor=%q buf=%v", p.ideFor, p.ideBuf())
	}

	// saving releases it: the pane re-syncs to the asked-for worktree
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyCtrlS})
	if p.ideFor != second {
		t.Errorf("after saving, ideFor = %q, want %q", p.ideFor, second)
	}
	if len(p.ideBufs) != 0 {
		t.Errorf("after re-sync the pane should be back on the tree, %d tabs open", len(p.ideBufs))
	}
}

// TestIDEFilter: / filters the tree by a capped recursive walk over the whole
// worktree; enter on a match opens it and esc restores the tree.
func TestIDEFilter(t *testing.T) {
	p := ideTestPane(t, 200)
	keyIDE(t, p, runes("/"))
	if p.ideInputKind != ideInputFilter {
		t.Fatal("/ in the tree should open the filter")
	}
	typeIDE(t, p, "util")
	if len(p.ideTree) != 1 || p.ideTree[0].rel != filepath.Join("sub", "util.go") {
		t.Fatalf("filter 'util' should match sub/util.go alone, got %+v", p.ideTree)
	}
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter}) // keep the filter, pick from the list
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter}) // open the match
	if b := p.ideBuf(); b == nil || b.rel != filepath.Join("sub", "util.go") {
		t.Fatalf("enter on a filter match should open it, got %v", p.ideBuf())
	}
	p.ideFocus = ideFocusTree
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc}) // drop the filter
	if p.ideFilter != "" || len(p.ideTree) < 2 {
		t.Errorf("esc should clear the filter and restore the tree, got filter=%q %d rows", p.ideFilter, len(p.ideTree))
	}
}

// TestIDEFind: ctrl+f searches the open file; enter jumps to the first match
// after the cursor and n/N walk the rest, wrapping.
func TestIDEFind(t *testing.T) {
	p := ideTestPane(t, 200)
	dir := p.ideFor
	writeIDEFile(t, dir, "notes.txt", "alpha\nbeta\nalpha again\ngamma\nalpha last\n")
	p.refreshIDETree()
	p.openIDEFile("notes.txt")

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyCtrlF})
	if p.ideInputKind != ideInputFind {
		t.Fatal("ctrl+f should open find")
	}
	typeIDE(t, p, "alpha")
	if len(p.ideFindHits) != 3 {
		t.Fatalf("find 'alpha' hits %v, want 3 lines", p.ideFindHits)
	}
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	b := p.ideBuf()
	if b.cursor != 2 { // cursor starts on 0, the next match is line 3 (idx 2)
		t.Errorf("enter jumped to line %d, want 2", b.cursor)
	}
	keyIDE(t, p, runes("n"))
	if p.ideBuf().cursor != 4 {
		t.Errorf("n jumped to line %d, want 4", p.ideBuf().cursor)
	}
	keyIDE(t, p, runes("n")) // wraps
	if p.ideBuf().cursor != 0 {
		t.Errorf("n should wrap to line 0, got %d", p.ideBuf().cursor)
	}
	keyIDE(t, p, runes("N")) // wraps back
	if p.ideBuf().cursor != 4 {
		t.Errorf("N should wrap back to line 4, got %d", p.ideBuf().cursor)
	}
}

// TestIDEFileOps: a creates (directories with a trailing slash), R renames —
// open tabs following — and d deletes on the second press.
func TestIDEFileOps(t *testing.T) {
	p := ideTestPane(t, 200)
	dir := p.ideFor

	// a: new file, parents made on the way
	keyIDE(t, p, runes("a"))
	if p.ideInputKind != ideInputNew {
		t.Fatal("a should ask for a new path")
	}
	p.ideInput.SetValue("pkg/new.go")
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(dir, "pkg", "new.go")); err != nil {
		t.Fatalf("new file missing: %v", err)
	}
	if b := p.ideBuf(); b == nil || b.rel != filepath.Join("pkg", "new.go") {
		t.Errorf("a new file should open, active buf %v", p.ideBuf())
	}

	// R: rename, the open tab follows
	p.ideFocus = ideFocusTree
	p.selectIDERel(filepath.Join("pkg", "new.go"))
	keyIDE(t, p, runes("R"))
	p.ideInput.SetValue("pkg/renamed.go")
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(dir, "pkg", "renamed.go")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if b := p.ideBuf(); b == nil || b.rel != filepath.Join("pkg", "renamed.go") {
		t.Errorf("the open tab should follow the rename, buf %v", p.ideBuf())
	}

	// d: delete needs a second press, and closes the tab
	p.selectIDERel(filepath.Join("pkg", "renamed.go"))
	keyIDE(t, p, runes("d"))
	if _, err := os.Stat(filepath.Join(dir, "pkg", "renamed.go")); err != nil {
		t.Fatal("first d must not delete")
	}
	keyIDE(t, p, runes("d"))
	if _, err := os.Stat(filepath.Join(dir, "pkg", "renamed.go")); err == nil {
		t.Fatal("second d should delete the file")
	}
	if len(p.ideBufs) != 0 {
		t.Errorf("deleting an open file should close its tab, %d left", len(p.ideBufs))
	}
}

// TestIDEEditingKeepsHighlight: dropping into edit mode must not strip the
// syntax colors — the pane draws the highlighted buffer itself, with a block
// cursor overlaid, re-coloring as the text changes.
func TestIDEEditingKeepsHighlight(t *testing.T) {
	p := ideTestPane(t, 200)
	p.openIDEFile("main.go")
	keyIDE(t, p, runes("e"))

	rows := p.ideEditorRows(60, 10)
	if len(rows) == 0 {
		t.Fatal("no editor rows while editing")
	}
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "\x1b[38;5;") {
		t.Error("editing rows lost the syntax colors")
	}
	if !strings.Contains(joined, "\x1b[7m") {
		t.Error("editing rows carry no block cursor")
	}

	// typing re-highlights: the new rune shows up in the colored rows
	keyIDE(t, p, runes("q"))
	b := p.ideBuf()
	if !strings.Contains(ansi.Strip(b.hl[0]), "q") {
		t.Errorf("typed rune missing from the highlighted line: %q", b.hl[0])
	}

	// a new line keeps the highlight aligned with the buffer
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if got, want := len(p.ideBuf().hl), strings.Count(p.ideBuf().val, "\n")+1; got != want {
		t.Errorf("highlight has %d rows for a %d-line buffer", got, want)
	}
}

// TestIDETabStripScrolls: a strip wider than the file half slides so the
// active tab is always fully visible, and never runs past the half's edge.
func TestIDETabStripScrolls(t *testing.T) {
	p := ideTestPane(t, 200)
	dir := p.ideFor
	for i := 0; i < 8; i++ {
		rel := fmt.Sprintf("a-rather-long-file-name-%d.go", i)
		writeIDEFile(t, dir, rel, "package x\n")
		p.openIDEFile(rel)
	}

	const w = 60
	bar := p.ideTabBar(w)
	for _, ln := range bar {
		if got := lipgloss.Width(ln); got != w {
			t.Fatalf("tab bar row width = %d, want %d (%q)", got, w, ln)
		}
	}
	strip := ansi.Strip(strings.Join(bar, "\n"))
	if !strings.Contains(strip, "a-rather-long-file-name-7.go") {
		t.Errorf("the active (last) tab should be visible:\n%s", strip)
	}
	if strings.Contains(strip, "a-rather-long-file-name-0.go") {
		t.Errorf("the first tab should be scrolled off:\n%s", strip)
	}

	p.activateIDEBuf(0)
	strip = ansi.Strip(strings.Join(p.ideTabBar(w), "\n"))
	if !strings.Contains(strip, "a-rather-long-file-name-0.go") {
		t.Errorf("activating the first tab should scroll back:\n%s", strip)
	}
}

// ctrlKey builds a control-key message the way bubbletea delivers it.
func ctrlKey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// TestIDECtrlFFromTree: ctrl+f opens the in-file find even with the tree
// focused — it used to be reachable only from the file half.
func TestIDECtrlFFromTree(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true
	if p.ideFocus != ideFocusTree {
		t.Fatal("want the tree focused to start")
	}
	// with nothing open there is no file to search: the pane says so
	keyIDE(t, p, ctrlKey(tea.KeyCtrlF))
	if p.ideInputKind != ideInputNone {
		t.Errorf("ctrl+f with no file open opened kind %d", p.ideInputKind)
	}
	if p.Err == nil {
		t.Error("ctrl+f with no file open should explain itself")
	}

	// open a file, return to the tree, and ctrl+f should still reach the find
	p.ideSel = indexOfIDERel(t, p, "main.go")
	p.keyIDE(tea.KeyMsg{Type: tea.KeyEnter})
	p.ideFocus = ideFocusTree
	p.Err = nil

	keyIDE(t, p, ctrlKey(tea.KeyCtrlF))
	if p.ideInputKind != ideInputFind {
		t.Fatalf("ctrl+f from the tree opened kind %d, want the find", p.ideInputKind)
	}
	if p.ideFocus != ideFocusFile {
		t.Error("the find should hand the file half the focus")
	}
}

// TestIDECtrlFWhileEditing: ctrl+f is caught ahead of the buffer, dropping out
// of the editor rather than being typed into it.
func TestIDECtrlFWhileEditing(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true
	p.ideSel = indexOfIDERel(t, p, "main.go")
	p.keyIDE(tea.KeyMsg{Type: tea.KeyEnter})
	keyIDE(t, p, runes("e"))
	if !p.ideEditing {
		t.Fatal("e should start editing")
	}
	before := p.ideBuf().val

	keyIDE(t, p, ctrlKey(tea.KeyCtrlF))
	if p.ideEditing {
		t.Error("ctrl+f should leave the editor — two widgets cannot share the keys")
	}
	if p.ideInputKind != ideInputFind {
		t.Errorf("ctrl+f mid-edit opened kind %d, want the find", p.ideInputKind)
	}
	if got := p.ideBuf().val; got != before {
		t.Errorf("ctrl+f changed the buffer: %q -> %q", before, got)
	}
}

// TestIDECtrlGSearchesTheWorktree: ctrl+g opens the worktree search, enter
// runs it, and the results group by file under a header apiece.
func TestIDECtrlGSearchesTheWorktree(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true

	keyIDE(t, p, ctrlKey(tea.KeyCtrlG))
	if p.ideInputKind != ideInputGrep {
		t.Fatalf("ctrl+g opened kind %d, want the worktree search", p.ideInputKind)
	}
	typeIDE(t, p, "package")
	cmd := keyIDECmd(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should launch the search off the update loop")
	}
	if p.ideGrepQ != "package" {
		t.Errorf("query = %q", p.ideGrepQ)
	}
	if !p.ideGrepping {
		t.Error("the pane should show the search as in flight")
	}

	grepResult(p,
		gitx.GrepFile{Path: "main.go", Matches: []gitx.GrepMatch{{Line: 1, Text: "package main"}}},
		gitx.GrepFile{Path: "sub/util.go", Matches: []gitx.GrepMatch{
			{Line: 1, Text: "package sub"}, {Line: 4, Text: "// package again"}}},
	)
	if p.ideGrepping {
		t.Error("the result should clear the in-flight flag")
	}
	// 2 headers + 3 matches
	if len(p.ideGrepRows) != 5 {
		t.Fatalf("results list has %d rows, want 5: %+v", len(p.ideGrepRows), p.ideGrepRows)
	}
	if !p.ideGrepRows[0].hdr || p.ideGrepRows[0].rel != "main.go" {
		t.Errorf("row 0 should head main.go, got %+v", p.ideGrepRows[0])
	}
	if p.ideGrepRows[1].hdr || p.ideGrepRows[1].line != 1 {
		t.Errorf("row 1 should be main.go's match, got %+v", p.ideGrepRows[1])
	}
	if !p.ideGrepRows[2].hdr || p.ideGrepRows[2].rel != "sub/util.go" {
		t.Errorf("row 2 should head sub/util.go, got %+v", p.ideGrepRows[2])
	}
}

// TestIDEGrepFoldAndOpen: a header folds its file away, and enter on a match
// opens that file with the cursor on the matching line — and seeds the in-file
// find, so the hits are marked in the view the result jumped into.
func TestIDEGrepFoldAndOpen(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true
	p.ideGrepQ = "func"
	grepResult(p,
		gitx.GrepFile{Path: "main.go", Matches: []gitx.GrepMatch{{Line: 3, Text: "func main() {}"}}},
		gitx.GrepFile{Path: "sub/util.go", Matches: []gitx.GrepMatch{{Line: 1, Text: "package sub"}}},
	)
	if len(p.ideGrepRows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(p.ideGrepRows))
	}

	// fold main.go from its header
	p.ideGrepSel = 0
	keyIDECmd(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if len(p.ideGrepRows) != 3 {
		t.Errorf("folding main.go should hide its match, got %d rows", len(p.ideGrepRows))
	}
	if !p.ideGrepFold["main.go"] {
		t.Error("main.go should be folded")
	}
	// and unfold again
	keyIDECmd(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if len(p.ideGrepRows) != 4 {
		t.Errorf("unfolding should bring the match back, got %d rows", len(p.ideGrepRows))
	}

	// enter on the match opens the file there
	p.ideGrepSel = 1
	keyIDECmd(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	b := p.ideBuf()
	if b == nil || b.rel != "main.go" {
		t.Fatalf("enter on a match should open main.go, got %+v", b)
	}
	if b.cursor != 2 {
		t.Errorf("cursor on line index %d, want 2 (line 3)", b.cursor)
	}
	if p.ideFindQ != "func" {
		t.Errorf("the in-file find should inherit the query, got %q", p.ideFindQ)
	}
	if len(p.ideFindHits) == 0 {
		t.Error("the inherited query should have marked hits in the file")
	}
}

// TestIDEGrepEscRestoresTheTree: esc drops the results and the explorer comes
// back, rather than leaving the pane on an empty list.
func TestIDEGrepEscRestoresTheTree(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true
	p.ideGrepQ = "package"
	grepResult(p, gitx.GrepFile{Path: "main.go",
		Matches: []gitx.GrepMatch{{Line: 1, Text: "package main"}}})
	if !p.ideGrepActive() {
		t.Fatal("the results should have taken over the tree half")
	}

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc})
	if p.ideGrepActive() {
		t.Error("esc should clear the results")
	}
	if len(p.ideTree) != 2 {
		t.Errorf("the tree should be back with 2 rows, got %d", len(p.ideTree))
	}
}

// TestIDEGrepScopeSwitchKeepsTyping: typed into the wrong scope, ctrl+g/ctrl+f
// carries the query across instead of making you retype it.
func TestIDEGrepScopeSwitchKeepsTyping(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true
	p.ideSel = indexOfIDERel(t, p, "main.go")
	p.keyIDE(tea.KeyMsg{Type: tea.KeyEnter})

	keyIDE(t, p, ctrlKey(tea.KeyCtrlF))
	typeIDE(t, p, "main")
	keyIDE(t, p, ctrlKey(tea.KeyCtrlG))
	if p.ideInputKind != ideInputGrep {
		t.Fatalf("ctrl+g should switch scope, kind = %d", p.ideInputKind)
	}
	if got := p.ideInput.Value(); got != "main" {
		t.Errorf("the query should carry over, got %q", got)
	}
}

// TestIDEGrepRendersWithoutBleedingANSI: the results list draws inside its
// column — the styled match must not push rows past the tree's width.
func TestIDEGrepRendersWithoutBleedingANSI(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true
	p.ideGrepQ = "needle"
	grepResult(p, gitx.GrepFile{Path: "main.go", Matches: []gitx.GrepMatch{
		{Line: 7, Text: strings.Repeat("x", 40) + "needle" + strings.Repeat("y", 40)},
	}})
	w, h := p.idePaneSize()
	treeW := p.ideTreeWidth(w)
	for i, row := range p.ideGrepRowsView(treeW, h) {
		if got := lipgloss.Width(ansi.Strip(row)); got > treeW {
			t.Errorf("row %d is %d cells wide, over the %d column: %q",
				i, got, treeW, ansi.Strip(row))
		}
		if strings.Contains(ansi.Strip(row), "\x1b") {
			t.Errorf("row %d has a sheared escape in it: %q", i, row)
		}
	}
}

// TestIDESearchKeysLeaveThePathPromptsAlone: ctrl+f must not hijack a
// half-typed filename — the new-file and rename prompts keep their keys.
func TestIDESearchKeysLeaveThePathPromptsAlone(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true
	keyIDE(t, p, runes("a")) // the new-file prompt
	if p.ideInputKind != ideInputNew {
		t.Fatalf("a should open the new-file prompt, got kind %d", p.ideInputKind)
	}
	typeIDE(t, p, "notes")

	keyIDE(t, p, ctrlKey(tea.KeyCtrlF))
	if p.ideInputKind != ideInputNew {
		t.Errorf("ctrl+f hijacked the new-file prompt, kind = %d", p.ideInputKind)
	}
	keyIDE(t, p, ctrlKey(tea.KeyCtrlG))
	if p.ideInputKind != ideInputNew {
		t.Errorf("ctrl+g hijacked the new-file prompt, kind = %d", p.ideInputKind)
	}
	if got := p.ideInput.Value(); !strings.Contains(got, "notes") {
		t.Errorf("the typed path should have survived, got %q", got)
	}
}

// TestIDERulerTracksTheWindow: the thumb covers the whole track when the file
// fits, and follows the window down a file that does not.
func TestIDERulerTracksTheWindow(t *testing.T) {
	b := &ideBuf{hl: make([]string, 8)}
	for _, c := range ideRulerCells(b, 10) {
		if !c.thumb {
			t.Fatalf("a file shorter than the window should band the whole track: %+v", c)
		}
	}

	b = &ideBuf{hl: make([]string, 100)}
	top := ideRulerCells(b, 10)
	if !top[0].thumb {
		t.Error("unscrolled, the thumb should start at the top")
	}
	if top[9].thumb {
		t.Error("unscrolled, the thumb should not reach the bottom of a 100-line file")
	}

	b.scrollY = 90 // the last window of the file
	bottom := ideRulerCells(b, 10)
	if bottom[0].thumb {
		t.Error("scrolled to the end, the thumb should have left the top")
	}
	if !bottom[9].thumb {
		t.Error("scrolled to the end, the thumb should reach the bottom")
	}

	// the thumb is never empty, however long the file
	long := &ideBuf{hl: make([]string, 100000)}
	n := 0
	for _, c := range ideRulerCells(long, 10) {
		if c.thumb {
			n++
		}
	}
	if n == 0 {
		t.Error("a very long file should still show a thumb somewhere")
	}
}

// TestIDERulerCarriesTheDiff: the ruler picks up the gutter's marks, so a
// change outside the window is still visible on the track.
func TestIDERulerCarriesTheDiff(t *testing.T) {
	b := &ideBuf{
		hl:     make([]string, 100),
		gutter: map[int]rune{5: '+', 95: '~'},
	}
	cells := ideRulerCells(b, 10)
	if !cells[0].added {
		t.Errorf("line 5 should mark the first cell as added: %+v", cells[0])
	}
	if !cells[9].changed {
		t.Errorf("line 95 should mark the last cell as changed: %+v", cells[9])
	}
	// line 95 is far below the window, and the ruler still reports it
	if cells[9].thumb {
		t.Error("the changed line is off screen, so its cell is outside the thumb")
	}
	for i, c := range cells {
		if i != 0 && i != 9 && (c.added || c.changed) {
			t.Errorf("cell %d marked a diff it has no line for: %+v", i, c)
		}
	}

	// a cell standing for both kinds reports both
	both := &ideBuf{hl: make([]string, 100), gutter: map[int]rune{1: '+', 2: '~'}}
	if c := ideRulerCells(both, 10)[0]; !c.added || !c.changed {
		t.Errorf("a cell covering an add and a change should carry both: %+v", c)
	}
}

// TestIDEEditorRowsAlignTheRuler: every row is exactly the view's width, so
// the ruler stands in one column, and the track runs the full height even
// past the end of a short file.
func TestIDEEditorRowsAlignTheRuler(t *testing.T) {
	p := ideTestPane(t, 200)
	p.openIDEFile("main.go") // 3 lines, well short of the height below
	const w, h = 60, 12

	rows := p.ideEditorRows(w, h)
	if len(rows) != h {
		t.Fatalf("got %d rows, want the full height %d so the track reaches the bottom",
			len(rows), h)
	}
	for i, row := range rows {
		if got := lipgloss.Width(row); got != w {
			t.Errorf("row %d is %d cells, want %d: %q", i, got, w, ansi.Strip(row))
		}
	}
	// the rows past the end of the file are blank but for the track
	last := ansi.Strip(rows[h-1])
	if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(last), "│")) != "" {
		t.Errorf("the row past EOF should hold only the track, got %q", last)
	}
}

// TestIDERulerYieldsOnANarrowPane: too narrow to spare a column, the ruler
// steps aside rather than squeezing the code out.
func TestIDERulerYieldsOnANarrowPane(t *testing.T) {
	p := ideTestPane(t, 200)
	p.openIDEFile("main.go")
	rows := p.ideEditorRows(7, 4)
	for i, row := range rows {
		if strings.Contains(ansi.Strip(row), "│") {
			t.Errorf("row %d kept the ruler on a 7-cell pane: %q", i, ansi.Strip(row))
		}
	}
}

// TestIDEFileIcons: the explorer marks a file's type by name first, then
// extension, folders follow their fold, and the whole thing switches off.
func TestIDEFileIcons(t *testing.T) {
	p := ideTestPane(t, 200)

	if got := p.ideFileIcon("main.go", false, false); got != iconGo {
		t.Errorf("main.go icon = %q, want the go glyph", got)
	}
	if got := p.ideFileIcon("app/models/user.rb", false, false); got != iconRuby {
		t.Errorf("user.rb icon = %q, want the ruby glyph", got)
	}
	// a known name beats the extension: go.mod is go, not a bare .mod
	if got := p.ideFileIcon("go.mod", false, false); got != iconGo {
		t.Errorf("go.mod icon = %q, want the go glyph", got)
	}
	if got := p.ideFileIcon("Dockerfile", false, false); got != iconDocker {
		t.Errorf("Dockerfile icon = %q, want the docker glyph", got)
	}
	if got := p.ideFileIcon("DOCKERFILE", false, false); got != iconDocker {
		t.Error("the name match should be case-insensitive")
	}
	if got := p.ideFileIcon("notes.wat", false, false); got != iconFile {
		t.Errorf("an unknown extension = %q, want the plain file glyph", got)
	}
	if got := p.ideFileIcon("sub", true, false); got != iconFolder {
		t.Errorf("a folded directory = %q", got)
	}
	if got := p.ideFileIcon("sub", true, true); got != iconFolderOpen {
		t.Errorf("an unfolded directory = %q", got)
	}

	// the icons show up in the tree, and the config switches them off
	w, _ := p.idePaneSize()
	if rows := p.ideTreeRows(p.ideTreeWidth(w), 10); !strings.Contains(
		ansi.Strip(strings.Join(rows, "\n")), iconGo) {
		t.Error("the tree drew no go icon for main.go")
	}
	p.icons = false
	if got := p.ideFileIcon("main.go", false, false); got != "" {
		t.Errorf("icons off should give nothing, got %q", got)
	}
	if got := p.ideIconCell("main.go", false, false); got != "" {
		t.Errorf("icons off should leave no gap, got %q", got)
	}
	rows := p.ideTreeRows(p.ideTreeWidth(w), 10)
	if strings.Contains(ansi.Strip(strings.Join(rows, "\n")), iconGo) {
		t.Error("the tree still drew icons with them switched off")
	}
}

// TestIDESelectionBandFillsTheColumn: the selected row's background runs the
// width of the explorer. The icons and fold markers are multi-byte, so a
// byte-counting pad would leave the band short.
func TestIDESelectionBandFillsTheColumn(t *testing.T) {
	p := ideTestPane(t, 200)
	p.focused = true
	p.ideFocus = ideFocusTree
	p.ideSel = indexOfIDERel(t, p, "main.go")
	w, _ := p.idePaneSize()
	treeW := p.ideTreeWidth(w)

	rows := p.ideTreeRows(treeW, 10)
	if len(rows) <= p.ideSel {
		t.Fatalf("only %d rows drawn", len(rows))
	}
	if got := lipgloss.Width(ansi.Strip(rows[p.ideSel])); got != treeW-1 {
		t.Errorf("the selected row spans %d cells, want the %d column",
			got, treeW-1)
	}
}

// TestIDEEnterAutoIndents: breaking a line carries its indentation onto the
// new line, one unit deeper when the break lands after an opening bracket.
func TestIDEEnterAutoIndents(t *testing.T) {
	p := ideTestPane(t, 200)
	writeIDEFile(t, p.ideFor, "ind.txt", "if x {\n    body\n}\n")
	p.openIDEFile("ind.txt")
	keyIDE(t, p, runes("e"))

	p.setIDECursorAt(0, 6) // end of "if x {"
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if got := ideEditLines(p); got[1] != "    " || got[2] != "    body" {
		t.Fatalf("break after { should indent one unit, got %q", got)
	}
	if p.ideEditor.Line() != 1 || p.ideEditor.LineInfo().ColumnOffset != 4 {
		t.Errorf("cursor should sit at the end of the new indent, got line %d col %d",
			p.ideEditor.Line(), p.ideEditor.LineInfo().ColumnOffset)
	}

	p.setIDECursorAt(2, 8) // end of "    body": plain keep-indent
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEnter})
	if got := ideEditLines(p); got[3] != "    " {
		t.Errorf("break after body should keep the four-space indent, got %q", got[3])
	}
}

// TestIDETabIndentsSelection: shift+down grows a line selection; tab shifts
// the block right one unit, shift+tab walks it back and stops at column zero.
func TestIDETabIndentsSelection(t *testing.T) {
	p := ideTestPane(t, 200)
	writeIDEFile(t, p.ideFor, "ind.txt", "if x {\n    body\n}\n")
	p.openIDEFile("ind.txt")
	keyIDE(t, p, runes("e"))

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyShiftDown}) // lines 0-1
	if p.ideSelAnchor != 0 || p.ideEditor.Line() != 1 {
		t.Fatalf("shift+down should anchor at 0 and move to 1, got anchor %d line %d",
			p.ideSelAnchor, p.ideEditor.Line())
	}

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyTab})
	if got := ideEditLines(p); got[0] != "    if x {" || got[1] != "        body" || got[2] != "}" {
		t.Fatalf("tab should indent only the selected lines, got %q", got)
	}
	if p.ideSelAnchor != 0 {
		t.Error("indenting should keep the selection")
	}
	if !p.ideBuf().dirty {
		t.Error("indenting should mark the buffer dirty")
	}

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyShiftTab})
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := ideEditLines(p); got[0] != "if x {" || got[1] != "body" {
		t.Fatalf("shift+tab should outdent to column zero and stop, got %q", got)
	}

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc})
	if p.ideSelAnchor != -1 || !p.ideEditing {
		t.Error("first esc should only drop the selection")
	}
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc})
	if p.ideEditing {
		t.Error("second esc should leave editing")
	}
}

// TestIDETabAtCursor: with no selection tab types spaces to the next stop.
func TestIDETabAtCursor(t *testing.T) {
	p := ideTestPane(t, 200)
	writeIDEFile(t, p.ideFor, "ind.txt", "ab\n")
	p.openIDEFile("ind.txt")
	keyIDE(t, p, runes("e"))

	p.setIDECursorAt(0, 2)
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyTab})
	if got := ideEditLines(p); got[0] != "ab  " {
		t.Fatalf("tab at col 2 should pad to the next stop, got %q", got[0])
	}
	if p.ideEditor.LineInfo().ColumnOffset != 4 {
		t.Errorf("cursor should land on the stop, got col %d", p.ideEditor.LineInfo().ColumnOffset)
	}
}

// TestIDEMultiCursor: ctrl+e on a selection puts a cursor on every line's
// end; typing and backspace hit all of them, esc drops back to one cursor.
func TestIDEMultiCursor(t *testing.T) {
	p := ideTestPane(t, 200)
	writeIDEFile(t, p.ideFor, "multi.txt", "aa\nbbbb\ncc\n")
	p.openIDEFile("multi.txt")
	keyIDE(t, p, runes("e"))

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyShiftDown})
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyShiftDown}) // lines 0-2
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyCtrlE})
	if p.ideMultiLo != 0 || len(p.ideMultiCols) != 3 || p.ideSelAnchor != -1 {
		t.Fatalf("ctrl+e should open a 3-cursor block, got lo %d cols %v anchor %d",
			p.ideMultiLo, p.ideMultiCols, p.ideSelAnchor)
	}

	typeIDE(t, p, ",")
	if got := ideEditLines(p); got[0] != "aa," || got[1] != "bbbb," || got[2] != "cc," {
		t.Fatalf("typing should land on every line end, got %q", got)
	}

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyBackspace})
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyBackspace})
	if got := ideEditLines(p); got[0] != "a" || got[1] != "bbb" || got[2] != "c" {
		t.Fatalf("backspace should trim every line, got %q", got)
	}

	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc})
	if p.ideMultiLo != -1 || !p.ideEditing {
		t.Error("esc should drop the cursors and stay editing")
	}
}

// TestCaptureApplyState: the pane's snapshot restores a fresh pane over the
// same worktree — tabs in order, the dirty edit laid back highlighted, and
// marked stale when the disk moved on underneath.
func TestCaptureApplyState(t *testing.T) {
	p := ideTestPane(t, 200)
	p.openIDEFile("main.go")
	keyIDE(t, p, runes("e"))
	typeIDE(t, p, "zz")
	keyIDE(t, p, tea.KeyMsg{Type: tea.KeyEsc}) // dirty now
	p.openIDEFile(filepath.Join("sub", "util.go"))
	s := p.Capture()

	p2 := New(zone.New(), true)
	p2.SetSize(196, 36)
	p2.SetWorktree(p.ideFor)
	p2.ApplyState(s)
	if tabs := p2.Tabs(); len(tabs) != 2 || tabs[0] != "main.go" {
		t.Fatalf("tabs = %v, want main.go and sub/util.go", tabs)
	}
	if p2.ideCur != 1 {
		t.Errorf("active tab = %d, want 1 (util.go)", p2.ideCur)
	}
	b := p2.ideBufs[0]
	if !b.dirty || !strings.HasPrefix(b.val, "zz") || b.stale {
		t.Errorf("main.go should be dirty with its edits, not stale: dirty=%v stale=%v val=%q",
			b.dirty, b.stale, b.val)
	}
	if !strings.HasPrefix(b.hl[0], "\x1b") && !strings.Contains(b.hl[0], "zz") {
		t.Errorf("the restored buffer should be highlighted, got %q", b.hl[0])
	}

	// disk moved on under the persisted edits: dirty and marked stale
	writeIDEFile(t, p.ideFor, "main.go", "package main // rewritten\n")
	p3 := New(zone.New(), true)
	p3.SetSize(196, 36)
	p3.SetWorktree(p.ideFor)
	p3.ApplyState(s)
	b = p3.ideBufs[0]
	if !b.dirty || !b.stale {
		t.Errorf("dirty=%v stale=%v, want both after the disk changed", b.dirty, b.stale)
	}
}
