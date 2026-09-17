package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
)

func TestWordAtCol(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		col  int
		want string
	}{
		{"a word in the middle", "one two three", 5, "two"},
		{"from its first column", "one two three", 4, "two"},
		{"from its last column", "one two three", 6, "two"},
		{"dots and slashes join a path", "  M internal/ui/term.go", 12, "internal/ui/term.go"},
		{"hyphens join a flag", "run --dry-run now", 8, "dry-run"},
		{"a diff's + is not part of the word", "+added line", 3, "added"},
		{"a diff's - is not part of the word", "-removed line", 3, "removed"},
		{"a trailing full stop is left out", "ends here.", 6, "here"},
		{"an underscore is a word rune", "a snake_case name", 8, "snake_case"},
		{"wide runes count as two columns", "● commit subject", 4, "commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, to, ok := wordAtCol(tc.line, tc.col)
			if !ok {
				t.Fatalf("no word at column %d of %q", tc.col, tc.line)
			}
			if got := sliceCols(tc.line, from, to); got != tc.want {
				t.Errorf("word = %q, want %q", got, tc.want)
			}
		})
	}
}

// Whitespace and lone punctuation are not words: the caller falls back to
// treating the press as the start of an ordinary drag.
func TestWordAtColRejectsNonWords(t *testing.T) {
	for _, tc := range []struct {
		line string
		col  int
	}{
		{"one two", 3},     // the space between
		{"(parens)", 0},    // lone punctuation
		{"+added line", 0}, // a diff marker on its own
		{"short", 40},      // past the end of the line
	} {
		if _, _, ok := wordAtCol(tc.line, tc.col); ok {
			t.Errorf("column %d of %q should hold no word", tc.col, tc.line)
		}
	}
}

// A double click leaves the word selected and, on release, copied — the same
// release path a drag takes.
func TestTextSelPressWordSelectsTheWord(t *testing.T) {
	lines := []string{"  M internal/ui/term.go"}
	from, to, ok := wordAtCol(lines[0], 12)
	if !ok {
		t.Fatal("no word under the pointer")
	}
	var sel textSel
	sel.pressWord(0, from, to)
	if !sel.release() {
		t.Fatal("a double click must count as a selection, not a click")
	}
	a, b, ok := sel.bounds()
	if !ok {
		t.Fatal("no selection after release")
	}
	if got := selectedText(lines, a, b); got != "internal/ui/term.go" {
		t.Errorf("selected %q, want the path", got)
	}
}

// Dragging on from a double click extends the selection but never eats into
// the word it started on, in either direction.
func TestWordDragKeepsTheWholeWord(t *testing.T) {
	lines := []string{"one two three"}
	from, to, _ := wordAtCol(lines[0], 5) // "two"

	right := textSel{}
	right.pressWord(0, from, to)
	right.drag(10, 0)
	a, b, _ := right.bounds()
	if got := selectedText(lines, a, b); got != "two thr" {
		t.Errorf("dragging right selected %q, want %q", got, "two thr")
	}

	left := textSel{}
	left.pressWord(0, from, to)
	left.drag(0, 0)
	a, b, _ = left.bounds()
	if got := selectedText(lines, a, b); got != "one two" {
		t.Errorf("dragging left selected %q, want %q", got, "one two")
	}

	// jitter inside the word leaves the word itself selected
	inside := textSel{}
	inside.pressWord(0, from, to)
	inside.drag(5, 0)
	a, b, _ = inside.bounds()
	if got := selectedText(lines, a, b); got != "two" {
		t.Errorf("a wobble inside the word selected %q, want %q", got, "two")
	}
}

func TestRegisterPress(t *testing.T) {
	var m Model
	first := tea.MouseMsg{X: 10, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	if m.registerPress(first) {
		t.Error("the first press cannot be a double click")
	}
	if !m.registerPress(first) {
		t.Error("a second press on the same cell is a double click")
	}
	if m.registerPress(first) {
		t.Error("a third press starts a fresh count")
	}

	m = Model{}
	m.registerPress(first)
	if m.registerPress(tea.MouseMsg{X: 11, Y: 4, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}) {
		t.Error("a press on another cell is not a double click")
	}

	m = Model{}
	m.registerPress(first)
	m.lastPress = time.Now().Add(-2 * doubleClickWindow)
	if m.registerPress(first) {
		t.Error("a press long after the first is not a double click")
	}
}

// End to end through Update: two quick presses on a word in the git pane
// select it, and the release puts it on the clipboard.
func TestDoubleClickInGitPaneCopiesTheWord(t *testing.T) {
	var copied string
	restore := copyToClipboard
	copyToClipboard = func(text string) error { copied = text; return nil }
	t.Cleanup(func() { copyToClipboard = restore })

	m := logModel(t)
	m.pane = paneDiff
	m.openGitLog()
	z := awaitZone(t, m, "pane:diff")

	// find a commit subject in the rendered body and aim at it
	w, h := m.gitPaneSize()
	_, body := m.gitPaneContent(w, h)
	line, col := -1, -1
	for i, ln := range strings.Split(body, "\n") {
		plain := invisibleRE.ReplaceAllString(ln, "")
		if k := strings.Index(plain, "newest"); k >= 0 {
			line, col = i, plainWidth(plain[:k])+2 // aim at the middle of it
			break
		}
	}
	if line < 0 {
		t.Fatalf("no commit subject in the pane:\n%s", body)
	}

	press := tea.MouseMsg{X: z.StartX + 1 + col, Y: z.StartY + 2 + line,
		Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	release := press
	release.Action = tea.MouseActionRelease

	for _, msg := range []tea.MouseMsg{press, release, press, release} {
		mm, _ := m.Update(msg)
		m = mm.(Model)
	}
	if copied != "newest" {
		t.Errorf("clipboard has %q, want %q", copied, "newest")
	}
	a, b, ok := m.gitSel.bounds()
	if !ok {
		t.Fatal("the word should stay highlighted after the copy")
	}
	if a.line != line || b.line != line {
		t.Errorf("selection spans lines %d..%d, want line %d only", a.line, b.line, line)
	}
}

// The same double click over a terminal pane's cells.
func TestSelPressWordOverTerminalCells(t *testing.T) {
	em := vt.NewEmulator(40, 5)
	em.SetScrollbackSize(50)
	em.Write([]byte("hello internal/ui/term.go there"))
	s := &claudeSession{dir: t.TempDir(), em: em, cols: 40, rows: 5, notify: make(chan struct{}, 1)}

	if !s.selPressWord(10, 0) {
		t.Fatal("no word under the pointer")
	}
	text, moved := s.selRelease()
	if !moved {
		t.Fatal("a double click must count as a selection, not a click")
	}
	if text != "internal/ui/term.go" {
		t.Errorf("selected %q, want the path", text)
	}

	if s.selPressWord(5, 0) {
		t.Error("the space between words holds no word")
	}
}
