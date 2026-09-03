package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/ide"
)

// The pane's own behavior is tested in internal/ide; what stays here is the
// seam — the pane inside treeline's frame, keys and clicks routed through the
// app, the git pane handing it files, worktree selection driving it.

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
	if got := m.ide.ActiveFile(); got != "main.go" {
		t.Fatalf("active buffer = %q, want main.go", got)
	}
	if !m.ide.FileFocused() {
		t.Error("the opened file should have the keys")
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
	m.ideReady().OpenFile("main.go")

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
	m.ideReady().OpenFile("main.go")
	m.ideReady().OpenFile(filepath.Join("sub", "util.go"))

	z := awaitZone(t, m, ide.TabCloseZoneID())
	click := tea.MouseMsg{X: z.StartX, Y: z.StartY,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	mm, _ := m.Update(click)
	m = mm.(Model)
	if tabs := m.ide.Tabs(); len(tabs) != 1 || m.ide.ActiveFile() != "main.go" {
		t.Fatalf("✕ should close util.go, got %d tabs, active %q", len(tabs), m.ide.ActiveFile())
	}

	m = keyIDE(t, m, runes("e"))
	m = typeIDE(t, m, "zz")
	m = keyIDE(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // dirty now

	// the dirty dot widens the tab, moving its ✕; the zone updates off the
	// render loop, so wait for the moved bounds rather than clicking the old
	z = awaitZoneMoved(t, m, ide.TabCloseZoneID(), z.StartX)
	click = tea.MouseMsg{X: z.StartX, Y: z.StartY,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	mm, _ = m.Update(click)
	m = mm.(Model)
	if len(m.ide.Tabs()) != 1 {
		t.Fatal("a dirty tab must not close on the first ✕")
	}
	mm, _ = m.Update(click)
	m = mm.(Model)
	if len(m.ide.Tabs()) != 0 {
		t.Fatalf("second ✕ should close the tab, got %d", len(m.ide.Tabs()))
	}
}
