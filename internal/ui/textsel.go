package ui

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// textSel is a mouse text selection over a block of already-rendered lines —
// the git pane's diff and file lists, which are strings rather than terminal
// cells, so they can't use agentSession's cell-level selection. Coordinates
// are body-relative: line, then visible column.
type textSel struct {
	on    bool // drag in progress
	moved bool // the pointer travelled, so this is a selection and not a click
	shown bool // highlight persists after release until something clears it
	a, b  selPoint
}

func (t *textSel) press(col, line int) {
	t.a = selPoint{line: line, col: col}
	t.b = t.a
	t.on, t.moved, t.shown = true, false, true
}

func (t *textSel) drag(col, line int) {
	if !t.on {
		return
	}
	p := selPoint{line: line, col: col}
	if p != t.b {
		t.moved = true
	}
	t.b = p
}

// release ends a drag and reports whether it was a selection rather than a
// plain click (a click leaves nothing highlighted and should act as a click).
func (t *textSel) release() bool {
	t.on = false
	if !t.moved {
		t.shown = false
		return false
	}
	return true
}

func (t *textSel) clear() { *t = textSel{} }

// bounds returns the selection in reading order, if one is visible.
func (t textSel) bounds() (a, b selPoint, ok bool) {
	if !t.shown {
		return a, b, false
	}
	a, b = t.a, t.b
	if a.line > b.line || (a.line == b.line && a.col > b.col) {
		a, b = b, a
	}
	return a, b, true
}

// invisibleRE matches everything in a rendered line that takes no columns:
// lipgloss's SGR colour sequences and bubblezone's mouse-zone markers
// (ESC[<n>z), which wrap clickable rows and would otherwise be counted as
// text — skewing every column past them and landing in the clipboard.
var invisibleRE = regexp.MustCompile("\x1b\\[[0-9;]*[mz]")

// isReset reports whether an SGR sequence clears every attribute, which would
// also clear a selection highlight painted over it.
func isReset(seq string) bool {
	if !strings.HasSuffix(seq, "m") {
		return false // a zone marker, not an SGR sequence
	}
	params := seq[2 : len(seq)-1]
	if params == "" {
		return true // ESC[m is ESC[0m
	}
	for _, p := range strings.Split(params, ";") {
		if p == "0" || p == "00" || p == "" {
			return true
		}
	}
	return false
}

// spanOf returns the visible columns the selection covers on one line of a
// block, as a half-open range: whole lines in the middle, and up to the
// anchors on the first and last. The column released on is part of the
// selection, as it is when dragging in a terminal.
func spanOf(line int, a, b selPoint, width int) (from, to int, ok bool) {
	if line < a.line || line > b.line {
		return 0, 0, false
	}
	from, to = 0, width
	if line == a.line {
		from = a.col
	}
	if line == b.line && b.col+1 < to {
		to = b.col + 1
	}
	if to <= from {
		return 0, 0, false
	}
	return from, to, true
}

// highlightSel reverses the selected span of each rendered line, leaving the
// line's own colours intact around it.
func highlightSel(lines []string, a, b selPoint) []string {
	out := make([]string, len(lines))
	for i, ln := range lines {
		from, to, ok := spanOf(i, a, b, plainWidth(ln))
		if !ok {
			out[i] = ln
			continue
		}
		out[i] = highlightSpan(ln, from, to)
	}
	return out
}

// highlightSpan paints columns from..to of one rendered line in reverse video.
// Every SGR reset inside the span would drop the highlight, so it is asserted
// again after each one.
func highlightSpan(line string, from, to int) string {
	var b strings.Builder
	col, inSel := 0, false
	open := func() {
		if !inSel {
			b.WriteString("\x1b[7m")
			inSel = true
		}
	}
	close := func() {
		if inSel {
			b.WriteString("\x1b[27m")
			inSel = false
		}
	}
	for i := 0; i < len(line); {
		if loc := invisibleRE.FindStringIndex(line[i:]); loc != nil && loc[0] == 0 {
			seq := line[i : i+loc[1]]
			b.WriteString(seq)
			if inSel && isReset(seq) {
				inSel = false
				open()
			}
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if col >= from && col < to {
			open()
		} else {
			close()
		}
		b.WriteRune(r)
		col += runeCols(r)
		i += size
	}
	close()
	return b.String()
}

// selectedText is the plain text of a selection over rendered lines, ready for
// the clipboard.
func selectedText(lines []string, a, b selPoint) string {
	var out []string
	for i, ln := range lines {
		plain := invisibleRE.ReplaceAllString(ln, "")
		from, to, ok := spanOf(i, a, b, plainWidth(ln))
		if !ok {
			continue
		}
		out = append(out, strings.TrimRight(sliceCols(plain, from, to), " "))
	}
	return strings.Join(out, "\n")
}

// sliceCols cuts a plain string to the visible columns [from, to).
func sliceCols(s string, from, to int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		if col >= from && col < to {
			b.WriteRune(r)
		}
		col += runeCols(r)
	}
	return b.String()
}

// plainWidth is a rendered line's width in columns, ignoring its escapes.
func plainWidth(s string) int {
	w := 0
	for _, r := range invisibleRE.ReplaceAllString(s, "") {
		w += runeCols(r)
	}
	return w
}

// runeCols is a rune's column count, never less than one so that control
// characters can't make columns and runes drift apart.
func runeCols(r rune) int {
	if w := runewidth.RuneWidth(r); w > 0 {
		return w
	}
	return 1
}
