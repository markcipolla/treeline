package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	mm, _ := m.keyIDE(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if len(m.ideTree) != 3 || m.ideTree[1].rel != filepath.Join("sub", "util.go") {
		t.Fatalf("expected sub/util.go inlined after unfolding, got %+v", m.ideTree)
	}

	// open main.go (last row)
	m.ideSel = 2
	mm, _ = m.keyIDE(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if m.ideFile != "main.go" {
		t.Fatalf("ideFile = %q, want main.go", m.ideFile)
	}
	if m.ideFocus != ideFocusFile {
		t.Error("opening a file should hand the keys to the file view")
	}
	if len(m.ideHL) != 4 { // three lines + the trailing newline's empty one
		t.Errorf("highlighted %d lines, want 4", len(m.ideHL))
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
	mm, _ = m.keyIDE(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	mm, _ = m.keyIDE(runes("e"))
	m = mm.(Model)
	if !m.ideEditing {
		t.Fatal("e should enter edit mode")
	}

	mm, _ = m.keyIDE(runes("x"))
	m = mm.(Model)
	if !m.ideDirty {
		t.Fatal("typing should mark the buffer dirty")
	}

	mm, _ = m.keyIDE(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = mm.(Model)
	if m.ideDirty {
		t.Error("saving should clear the dirty mark")
	}
	data, err := os.ReadFile(filepath.Join(m.wts[0].Path, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != m.ideEditor.Value() {
		t.Errorf("file on disk %q, buffer %q", data, m.ideEditor.Value())
	}
	if !strings.HasPrefix(string(data), "x") {
		t.Errorf("edit missing from the saved file: %q", data)
	}
}

// TestIDESaveKeepsUntouchedWhitespace: the textarea flattens tabs to spaces
// on load, so a save must not spray that across the file — lines the edit
// never reached keep their own bytes.
func TestIDESaveKeepsUntouchedWhitespace(t *testing.T) {
	m := ideTestModel(t, 200)
	dir := m.wts[0].Path
	writeIDEFile(t, dir, "tabs.go", "func a() {\n\tone()\n\ttwo()\n}\n")
	m.refreshIDETree()

	m.openIDEFile("tabs.go")
	if !strings.Contains(m.ideEditor.Value(), "    one()") {
		t.Fatalf("expected the buffer to carry flattened tabs: %q", m.ideEditor.Value())
	}
	mm, _ := m.keyIDE(runes("e"))
	m = mm.(Model)
	mm, _ = m.keyIDE(runes("x")) // edits line 1 only
	m = mm.(Model)
	mm, _ = m.keyIDE(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = mm.(Model)

	data, err := os.ReadFile(filepath.Join(dir, "tabs.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := "xfunc a() {\n\tone()\n\ttwo()\n}\n"
	if string(data) != want {
		t.Errorf("saved file rewrote untouched whitespace:\ngot  %q\nwant %q", data, want)
	}
}

// TestIDEDirtyBufferPinsThePane: moving the cursor to another worktree must
// not throw unsaved edits away — the pane stays where its edits are until
// they are saved.
func TestIDEDirtyBufferPinsThePane(t *testing.T) {
	m := ideTestModel(t, 200)
	first := m.wts[0].Path
	m.ideSel = 1
	m.openIDESel()
	mm, _ := m.keyIDE(runes("e"))
	m = mm.(Model)
	mm, _ = m.keyIDE(runes("y"))
	m = mm.(Model)

	if !m.selectWorktree(m.wts[1].Path) {
		t.Fatal("no row for the second worktree")
	}
	m.syncPanes()
	if m.ideFor != first || m.ideFile != "main.go" {
		t.Fatalf("dirty buffer discarded: ideFor=%q ideFile=%q", m.ideFor, m.ideFile)
	}

	// saving releases it: the pane re-syncs to the selected worktree
	mm, _ = m.keyIDE(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = mm.(Model)
	if m.ideFor != m.wts[1].Path {
		t.Errorf("after saving, ideFor = %q, want %q", m.ideFor, m.wts[1].Path)
	}
	if m.ideFile != "" {
		t.Errorf("after re-sync the pane should be back on the tree, ideFile=%q", m.ideFile)
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
