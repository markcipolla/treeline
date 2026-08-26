package ui

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

	"github.com/markcipolla/treeline/internal/gitx"
)

// ideTestModel is newTestModel with the first worktree existing on disk:
// a file at the root, a folded directory with another inside it.
func ideTestModel(t *testing.T, width int) Model {
	t.Helper()
	m := newTestModel(t, width)
	dir := m.wts[0].Path
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIDEFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	writeIDEFile(t, dir, filepath.Join("sub", "util.go"), "package sub\n")
	if !m.selectWorktree(dir) {
		t.Fatal("no row for the first worktree")
	}
	m.syncPanes()
	return m
}

func writeIDEFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// typeIDE feeds a string through the pane's key handler one rune at a time.
func typeIDE(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		mm, _ := m.keyIDE(runes(string(r)))
		m = mm.(Model)
	}
	return m
}

func keyIDE(t *testing.T, m Model, k tea.KeyMsg) Model {
	t.Helper()
	mm, _ := m.keyIDE(k)
	return mm.(Model)
}

// TestIDETreeListsAndOpens: the explorer lists the worktree with directories
// first, unfolds them in place, and enter on a file opens it highlighted.
func TestIDETreeListsAndOpens(t *testing.T) {
	m := ideTestModel(t, 200)

	if len(m.ideTree) != 2 {
		t.Fatalf("tree has %d rows, want 2: %+v", len(m.ideTree), m.ideTree)
	}
	if !m.ideTree[0].dir || m.ideTree[0].name != "sub" {
		t.Errorf("directories should sort first, got %+v", m.ideTree[0])
	}

	// unfold sub/: its child joins the tree under it
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.ideTree) != 3 || m.ideTree[1].rel != filepath.Join("sub", "util.go") {
		t.Fatalf("expected sub/util.go inlined after unfolding, got %+v", m.ideTree)
	}

	// open main.go (last row)
	m.ideSel = 2
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	b := m.ideBuf()
	if b == nil || b.rel != "main.go" {
		t.Fatalf("active buffer = %+v, want main.go", b)
	}
	if m.ideFocus != ideFocusFile {
		t.Error("opening a file should hand the keys to the file view")
	}
	if len(b.hl) != 4 { // three lines + the trailing newline's empty one
		t.Errorf("highlighted %d lines, want 4", len(b.hl))
	}

	title, _ := m.idePaneContent(60, 20)
	if !strings.Contains(title, "main.go") {
		t.Errorf("pane title %q should carry the open file", title)
	}
}

// TestIDEEditAndSave: e drops into the buffer, typing marks it dirty, and
// ctrl+s writes the file back and clears the mark.
func TestIDEEditAndSave(t *testing.T) {
	m := ideTestModel(t, 200)
	mm, _ := m.focusPane(paneIDE)
	m = mm.(Model)

	m.ideSel = 1 // main.go
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = keyIDE(t, m, runes("e"))
	if !m.ideEditing {
		t.Fatal("e should enter edit mode")
	}

	m = keyIDE(t, m, runes("x"))
	if !m.ideBuf().dirty {
		t.Fatal("typing should mark the buffer dirty")
	}

	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.ideBuf().dirty {
		t.Error("saving should clear the dirty mark")
	}
	data, err := os.ReadFile(filepath.Join(m.wts[0].Path, "main.go"))
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
	m := ideTestModel(t, 200)
	dir := m.wts[0].Path
	writeIDEFile(t, dir, "tabs.go", "func a() {\n\tone()\n\ttwo()\n}\n")
	m.refreshIDETree()

	m.openIDEFile("tabs.go")
	if !strings.Contains(m.ideBuf().val, "    one()") {
		t.Fatalf("expected the buffer to carry flattened tabs: %q", m.ideBuf().val)
	}
	m = keyIDE(t, m, runes("e"))
	m = keyIDE(t, m, runes("x")) // edits line 1 only
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})

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
	m := ideTestModel(t, 200)
	m.openIDEFile("main.go")
	m = keyIDE(t, m, runes("e"))
	m = typeIDE(t, m, "zz")
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // back to view, dirty

	m.openIDEFile(filepath.Join("sub", "util.go"))
	if len(m.ideBufs) != 2 || m.ideBuf().rel != filepath.Join("sub", "util.go") {
		t.Fatalf("want 2 tabs with util.go active, got %d, active %v", len(m.ideBufs), m.ideBuf())
	}

	m = keyIDE(t, m, runes("[")) // back to main.go
	b := m.ideBuf()
	if b.rel != "main.go" || !b.dirty || !strings.HasPrefix(b.val, "zz") {
		t.Fatalf("tab switch lost the dirty buffer: %+v", b)
	}
	if !strings.HasPrefix(m.ideEditor.Value(), "zz") {
		t.Error("the editor should hold the re-activated tab's text")
	}

	m = keyIDE(t, m, runes("x")) // dirty: first x only warns
	if len(m.ideBufs) != 2 {
		t.Fatal("a dirty tab must not close on the first x")
	}
	m = keyIDE(t, m, runes("x"))
	if len(m.ideBufs) != 1 || m.ideBuf().rel != filepath.Join("sub", "util.go") {
		t.Fatalf("second x should close the tab, got %d tabs", len(m.ideBufs))
	}
}

// TestIDEStaleSave: a file that changed on disk since it was loaded is not
// silently overwritten — ctrl+s refuses, R forces, r reloads.
func TestIDEStaleSave(t *testing.T) {
	m := ideTestModel(t, 200)
	dir := m.wts[0].Path
	m.openIDEFile("main.go")
	m = keyIDE(t, m, runes("e"))
	m = keyIDE(t, m, runes("y"))
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	// someone else rewrites the file underneath
	writeIDEFile(t, dir, "main.go", "package main // external\n")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "main.go"), future, future); err != nil {
		t.Fatal(err)
	}

	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	data, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.Contains(string(data), "external") {
		t.Fatal("ctrl+s overwrote a file that changed on disk")
	}
	if m.err == nil || !m.ideBuf().stale {
		t.Errorf("blocked save should explain itself and mark the tab: err=%v stale=%v", m.err, m.ideBuf().stale)
	}

	// R saves anyway
	m = keyIDE(t, m, runes("R"))
	data, _ = os.ReadFile(filepath.Join(dir, "main.go"))
	if !strings.HasPrefix(string(data), "y") {
		t.Errorf("R should force the save, disk has %q", data)
	}

	// ...and after an external change with a clean buffer, r reloads
	writeIDEFile(t, dir, "main.go", "package main // newer\n")
	m = keyIDE(t, m, runes("r"))
	if !strings.Contains(m.ideBuf().val, "newer") {
		t.Errorf("r should reload from disk, buffer has %q", m.ideBuf().val)
	}
}

// TestIDERefreshDisk: files changing under the pane re-read themselves when
// clean and are marked stale when dirty, instead of losing either side.
func TestIDERefreshDisk(t *testing.T) {
	m := ideTestModel(t, 200)
	dir := m.wts[0].Path
	m.openIDEFile("main.go")
	m.openIDEFile(filepath.Join("sub", "util.go"))
	m = keyIDE(t, m, runes("e"))
	m = keyIDE(t, m, runes("q")) // util.go dirty
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	future := time.Now().Add(2 * time.Second)
	writeIDEFile(t, dir, "main.go", "package main // fresh\n")
	writeIDEFile(t, dir, filepath.Join("sub", "util.go"), "package sub // fresh\n")
	for _, rel := range []string{"main.go", filepath.Join("sub", "util.go")} {
		if err := os.Chtimes(filepath.Join(dir, rel), future, future); err != nil {
			t.Fatal(err)
		}
	}

	m.refreshIDEDisk()
	var clean, dirty *ideBuf
	for _, b := range m.ideBufs {
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

// TestIDEDirtyBufferPinsThePane: moving the cursor to another worktree must
// not throw unsaved edits away — the pane stays where its edits are until
// they are saved.
func TestIDEDirtyBufferPinsThePane(t *testing.T) {
	m := ideTestModel(t, 200)
	first := m.wts[0].Path
	m.openIDEFile("main.go")
	m = keyIDE(t, m, runes("e"))
	m = keyIDE(t, m, runes("y"))

	if !m.selectWorktree(m.wts[1].Path) {
		t.Fatal("no row for the second worktree")
	}
	m.syncPanes()
	if m.ideFor != first || m.ideBuf() == nil {
		t.Fatalf("dirty buffer discarded: ideFor=%q buf=%v", m.ideFor, m.ideBuf())
	}

	// saving releases it: the pane re-syncs to the selected worktree
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.ideFor != m.wts[1].Path {
		t.Errorf("after saving, ideFor = %q, want %q", m.ideFor, m.wts[1].Path)
	}
	if len(m.ideBufs) != 0 {
		t.Errorf("after re-sync the pane should be back on the tree, %d tabs open", len(m.ideBufs))
	}
}

// TestIDEFilter: / filters the tree by a capped recursive walk over the whole
// worktree; enter on a match opens it and esc restores the tree.
func TestIDEFilter(t *testing.T) {
	m := ideTestModel(t, 200)
	m = keyIDE(t, m, runes("/"))
	if m.ideInputKind != ideInputFilter {
		t.Fatal("/ in the tree should open the filter")
	}
	m = typeIDE(t, m, "util")
	if len(m.ideTree) != 1 || m.ideTree[0].rel != filepath.Join("sub", "util.go") {
		t.Fatalf("filter 'util' should match sub/util.go alone, got %+v", m.ideTree)
	}
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // keep the filter, pick from the list
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open the match
	if b := m.ideBuf(); b == nil || b.rel != filepath.Join("sub", "util.go") {
		t.Fatalf("enter on a filter match should open it, got %v", m.ideBuf())
	}
	m.ideFocus = ideFocusTree
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // drop the filter
	if m.ideFilter != "" || len(m.ideTree) < 2 {
		t.Errorf("esc should clear the filter and restore the tree, got filter=%q %d rows", m.ideFilter, len(m.ideTree))
	}
}

// TestIDEFind: ctrl+f searches the open file; enter jumps to the first match
// after the cursor and n/N walk the rest, wrapping.
func TestIDEFind(t *testing.T) {
	m := ideTestModel(t, 200)
	dir := m.wts[0].Path
	writeIDEFile(t, dir, "notes.txt", "alpha\nbeta\nalpha again\ngamma\nalpha last\n")
	m.refreshIDETree()
	m.openIDEFile("notes.txt")

	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.ideInputKind != ideInputFind {
		t.Fatal("ctrl+f should open find")
	}
	m = typeIDE(t, m, "alpha")
	if len(m.ideFindHits) != 3 {
		t.Fatalf("find 'alpha' hits %v, want 3 lines", m.ideFindHits)
	}
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	b := m.ideBuf()
	if b.cursor != 2 { // cursor starts on 0, the next match is line 3 (idx 2)
		t.Errorf("enter jumped to line %d, want 2", b.cursor)
	}
	m = keyIDE(t, m, runes("n"))
	if m.ideBuf().cursor != 4 {
		t.Errorf("n jumped to line %d, want 4", m.ideBuf().cursor)
	}
	m = keyIDE(t, m, runes("n")) // wraps
	if m.ideBuf().cursor != 0 {
		t.Errorf("n should wrap to line 0, got %d", m.ideBuf().cursor)
	}
	m = keyIDE(t, m, runes("N")) // wraps back
	if m.ideBuf().cursor != 4 {
		t.Errorf("N should wrap back to line 4, got %d", m.ideBuf().cursor)
	}
}

// TestIDEFileOps: a creates (directories with a trailing slash), R renames —
// open tabs following — and d deletes on the second press.
func TestIDEFileOps(t *testing.T) {
	m := ideTestModel(t, 200)
	dir := m.wts[0].Path

	// a: new file, parents made on the way
	m = keyIDE(t, m, runes("a"))
	if m.ideInputKind != ideInputNew {
		t.Fatal("a should ask for a new path")
	}
	m.ideInput.SetValue("pkg/new.go")
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(dir, "pkg", "new.go")); err != nil {
		t.Fatalf("new file missing: %v", err)
	}
	if b := m.ideBuf(); b == nil || b.rel != filepath.Join("pkg", "new.go") {
		t.Errorf("a new file should open, active buf %v", m.ideBuf())
	}

	// R: rename, the open tab follows
	m.ideFocus = ideFocusTree
	m.selectIDERel(filepath.Join("pkg", "new.go"))
	m = keyIDE(t, m, runes("R"))
	m.ideInput.SetValue("pkg/renamed.go")
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, err := os.Stat(filepath.Join(dir, "pkg", "renamed.go")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if b := m.ideBuf(); b == nil || b.rel != filepath.Join("pkg", "renamed.go") {
		t.Errorf("the open tab should follow the rename, buf %v", m.ideBuf())
	}

	// d: delete needs a second press, and closes the tab
	m.selectIDERel(filepath.Join("pkg", "renamed.go"))
	m = keyIDE(t, m, runes("d"))
	if _, err := os.Stat(filepath.Join(dir, "pkg", "renamed.go")); err != nil {
		t.Fatal("first d must not delete")
	}
	m = keyIDE(t, m, runes("d"))
	if _, err := os.Stat(filepath.Join(dir, "pkg", "renamed.go")); err == nil {
		t.Fatal("second d should delete the file")
	}
	if len(m.ideBufs) != 0 {
		t.Errorf("deleting an open file should close its tab, %d left", len(m.ideBufs))
	}
}

// TestIDEPaneRenders: the pane shows up between claude and git on the title
// row of every panel layout.
func TestIDEPaneRenders(t *testing.T) {
	for _, width := range []int{160, 200, 300} {
		m := ideTestModel(t, width)
		var titleRow string
		for _, line := range strings.Split(m.View(), "\n") {
			plain := ansiRE.ReplaceAllString(line, "")
			if strings.Contains(plain, "claude") && strings.Contains(plain, "git") {
				titleRow = plain
				break
			}
		}
		if titleRow == "" {
			t.Fatalf("%d cols: no title row carries claude and git", width)
		}
		c, i, g := strings.Index(titleRow, "claude"), strings.Index(titleRow, " ide"), strings.Index(titleRow, "git")
		if i < 0 {
			t.Fatalf("%d cols: ide pane missing from %q", width, titleRow)
		}
		if !(c < i && i < g) {
			t.Errorf("%d cols: ide is not between claude and git: %q", width, titleRow)
		}
	}
}

// TestGitPaneOpensInIDE: o in the git pane's file lists hands the selected
// file to the ide pane, focused, in a tab.
func TestGitPaneOpensInIDE(t *testing.T) {
	m := ideTestModel(t, 200)
	mm, _ := m.focusPane(paneDiff)
	m = mm.(Model)
	m.gitUnstaged = []gitx.FileStatus{{Path: "main.go", Unstaged: 'M'}}
	m.gitCol, m.gitSelU = 0, 0

	mm, _ = m.keyGit(runes("o"))
	m = mm.(Model)
	if m.pane != paneIDE {
		t.Fatalf("pane = %d, want paneIDE", m.pane)
	}
	if b := m.ideBuf(); b == nil || b.rel != "main.go" {
		t.Fatalf("active buffer = %v, want main.go", m.ideBuf())
	}
	if m.ideFocus != ideFocusFile {
		t.Error("the opened file should have the keys")
	}
}

// TestIDEEditingKeepsHighlight: dropping into edit mode must not strip the
// syntax colors — the pane draws the highlighted buffer itself, with a block
// cursor overlaid, re-coloring as the text changes.
func TestIDEEditingKeepsHighlight(t *testing.T) {
	m := ideTestModel(t, 200)
	m.openIDEFile("main.go")
	m = keyIDE(t, m, runes("e"))

	rows := m.ideEditorRows(60, 10)
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
	m = keyIDE(t, m, runes("q"))
	b := m.ideBuf()
	if !strings.Contains(ansiRE.ReplaceAllString(b.hl[0], ""), "q") {
		t.Errorf("typed rune missing from the highlighted line: %q", b.hl[0])
	}

	// a new line keeps the highlight aligned with the buffer
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if got, want := len(m.ideBuf().hl), strings.Count(m.ideBuf().val, "\n")+1; got != want {
		t.Errorf("highlight has %d rows for a %d-line buffer", got, want)
	}
}

// TestIDEDividerRunsFullHeight: the explorer/editor divider is part of the
// pane's chrome — ╤ where it meets the title rule, │ on every body row, ┴
// into the bottom border — not a line hanging in the middle.
func TestIDEDividerRunsFullHeight(t *testing.T) {
	m := ideTestModel(t, 220)
	m.height = 30
	m.resize()
	mm, _ := m.focusPane(paneIDE)
	m = mm.(Model)
	m.openIDEFile("main.go")

	var lines [][]rune
	for _, l := range strings.Split(m.View(), "\n") {
		lines = append(lines, []rune(ansiRE.ReplaceAllString(l, "")))
	}
	titleRow := -1
	for i, l := range lines {
		if strings.Contains(string(l), "ide — main.go") {
			titleRow = i
			break
		}
	}
	if titleRow < 0 {
		t.Fatal("no ide title row")
	}
	after := strings.Index(string(lines[titleRow]), "ide —")
	col := -1
	for x := after; x < len(lines[titleRow+1]); x++ {
		if lines[titleRow+1][x] == '╤' {
			col = x
			break
		}
	}
	if col < 0 {
		t.Fatalf("the ide title rule carries no ╤: %q", string(lines[titleRow+1]))
	}
	closed := false
	for i := titleRow + 2; i < len(lines) && !closed; i++ {
		if col >= len(lines[i]) {
			t.Fatalf("row %d too short for the divider column", i)
		}
		switch lines[i][col] {
		case '│':
		case '┴':
			closed = true
		default:
			t.Fatalf("row %d breaks the divider at col %d: %q", i, col, string(lines[i][col]))
		}
	}
	if !closed {
		t.Error("the divider never reaches the bottom border's ┴")
	}
}

// TestSeamDragResizesPanes: pressing on the border between two panes grabs
// the seam; dragging trades width between them, floored so neither pane
// collapses, and the shape holds after release.
func TestSeamDragResizesPanes(t *testing.T) {
	m := newTestModel(t, 220)
	za := awaitZone(t, m, "pane:claude")
	before := m.layout()

	seamX, y := za.EndX+1, za.StartY+2
	mm, _ := m.Update(tea.MouseMsg{X: seamX, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = mm.(Model)
	if m.dragSeam < 0 {
		t.Fatal("press on the seam did not grab it")
	}
	mm, _ = m.Update(tea.MouseMsg{X: seamX + 8, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = mm.(Model)
	mm, _ = m.Update(tea.MouseMsg{X: seamX + 8, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	m = mm.(Model)
	if m.dragSeam >= 0 {
		t.Error("release should let the seam go")
	}

	after := m.layout()
	if after.claude.w != before.claude.w+8 || after.ide.w != before.ide.w-8 {
		t.Errorf("dragged +8: claude %d→%d, ide %d→%d",
			before.claude.w, after.claude.w, before.ide.w, after.ide.w)
	}
	if got, want := after.issues.w+after.claude.w+after.ide.w+after.git.w,
		before.issues.w+before.claude.w+before.ide.w+before.git.w; got != want {
		t.Errorf("band total drifted: %d, want %d", got, want)
	}

	// the app renders between updates, refreshing the zones; the test has to
	// do the same before grabbing the moved seam again
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.View()
		if z := m.zones.Get("pane:claude"); z != nil && z.EndX == za.EndX+8 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claude zone never caught up with the drag")
		}
		time.Sleep(5 * time.Millisecond)
	}
	seamX += 8

	// hauling the seam far past the floor pins the pane at minDragCol
	mm, _ = m.Update(tea.MouseMsg{X: seamX, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = mm.(Model)
	mm, _ = m.Update(tea.MouseMsg{X: 0, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = mm.(Model)
	if got := m.layout().claude.w; got != minDragCol {
		t.Errorf("claude pane dragged past the floor: %d, want %d", got, minDragCol)
	}
	// ...and dragging straight back answers without unwinding a phantom debt
	mm, _ = m.Update(tea.MouseMsg{X: 6, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	m = mm.(Model)
	if got := m.layout().claude.w; got != minDragCol+6 {
		t.Errorf("claude pane should follow the pointer back: %d, want %d", got, minDragCol+6)
	}
}

// TestIDETabCloseClick: the active tab wears a ✕; clicking it closes the tab,
// a dirty buffer warning first the way the x key does.
func TestIDETabCloseClick(t *testing.T) {
	m := ideTestModel(t, 200)
	m.openIDEFile("main.go")
	m.openIDEFile(filepath.Join("sub", "util.go"))

	z := awaitZone(t, m, ideTabCloseZoneID())
	click := tea.MouseMsg{X: z.StartX, Y: z.StartY,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	mm, _ := m.Update(click)
	m = mm.(Model)
	if len(m.ideBufs) != 1 || m.ideBuf().rel != "main.go" {
		t.Fatalf("✕ should close util.go, got %d tabs, active %v", len(m.ideBufs), m.ideBuf())
	}

	m = keyIDE(t, m, runes("e"))
	m = typeIDE(t, m, "zz")
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // dirty now

	z = awaitZone(t, m, ideTabCloseZoneID())
	click = tea.MouseMsg{X: z.StartX, Y: z.StartY,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	mm, _ = m.Update(click)
	m = mm.(Model)
	if len(m.ideBufs) != 1 {
		t.Fatal("a dirty tab must not close on the first ✕")
	}
	mm, _ = m.Update(click)
	m = mm.(Model)
	if len(m.ideBufs) != 0 {
		t.Fatalf("second ✕ should close the tab, got %d", len(m.ideBufs))
	}
}

// TestIDETabStripScrolls: a strip wider than the file half slides so the
// active tab is always fully visible, and never runs past the half's edge.
func TestIDETabStripScrolls(t *testing.T) {
	m := ideTestModel(t, 200)
	dir := m.wts[0].Path
	for i := 0; i < 8; i++ {
		rel := fmt.Sprintf("a-rather-long-file-name-%d.go", i)
		writeIDEFile(t, dir, rel, "package x\n")
		m.openIDEFile(rel)
	}

	const w = 60
	bar := m.ideTabBar(w)
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

	m.activateIDEBuf(0)
	strip = ansi.Strip(strings.Join(m.ideTabBar(w), "\n"))
	if !strings.Contains(strip, "a-rather-long-file-name-0.go") {
		t.Errorf("activating the first tab should scroll back:\n%s", strip)
	}
}
