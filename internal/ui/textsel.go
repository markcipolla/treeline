package ui

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// textSel is a mouse text selection over a block of already-rendered lines —
// the git pane's diff and file lists, which are strings rather than terminal
// cells, so they can't use claudeSession's cell-level selection. Coordinates
// are body-relative: line, then visible column.
type textSel struct {
	on    bool // drag in progress
	moved bool // the pointer travelled, so this is a selection and not a click
	shown bool // highlight persists after release until something clears it
	word  bool // started as a double click, so it can't shrink below the word
	a, b  selPoint
	// wordA and wordB are the double-clicked word, the anchor a later drag
	// grows from in either direction.
	wordA, wordB selPoint
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
	if t.word {
		// a drag that began on a word keeps the whole word: it grows from
		// whichever end of it the pointer left
		if p.before(t.wordA) {
			t.a, t.b = t.wordB, p
		} else if t.wordB.before(p) {
			t.a, t.b = t.wordA, p
		} else {
			t.a, t.b = t.wordA, t.wordB
		}
		return
	}
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

// ---- double click ----

// A double click takes the word under the pointer. Words are the runs a
// reader would call one: letters, digits and underscores, plus the hyphens,
// dots and slashes that join them into paths, flags and qualified names.
// Those joiners only count between two word runes, so the leading +/- of a
// diff line, or a sentence's final full stop, stay out of the selection.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func isJoinRune(r rune) bool { return r == '-' || r == '.' || r == '/' }

// wordAtCol finds the word under a visible column of a line and returns its
// span as a half-open range of columns. It reports false when the column
// holds no word — whitespace or lone punctuation — so the caller can treat
// the double click as an ordinary click instead.
func wordAtCol(s string, col int) (from, to int, ok bool) {
	rs := []rune(s)
	starts := make([]int, len(rs))
	c := 0
	for i, r := range rs {
		starts[i] = c
		c += runeCols(r)
	}
	i := -1
	for k, r := range rs {
		if col >= starts[k] && col < starts[k]+runeCols(r) {
			i = k
			break
		}
	}
	if i < 0 {
		return 0, 0, false
	}
	inner := isJoinRune(rs[i]) && i > 0 && i+1 < len(rs) &&
		isWordRune(rs[i-1]) && isWordRune(rs[i+1])
	if !isWordRune(rs[i]) && !inner {
		return 0, 0, false
	}
	lo, hi := i, i
	for lo > 0 {
		if isWordRune(rs[lo-1]) {
			lo--
			continue
		}
		if isJoinRune(rs[lo-1]) && lo >= 2 && isWordRune(rs[lo-2]) {
			lo -= 2
			continue
		}
		break
	}
	for hi < len(rs)-1 {
		if isWordRune(rs[hi+1]) {
			hi++
			continue
		}
		if isJoinRune(rs[hi+1]) && hi+2 < len(rs) && isWordRune(rs[hi+2]) {
			hi += 2
			continue
		}
		break
	}
	return starts[lo], starts[hi] + runeCols(rs[hi]), true
}

// before orders two points in reading order.
func (p selPoint) before(q selPoint) bool {
	return p.line < q.line || (p.line == q.line && p.col < q.col)
}

// pressWord starts a selection already covering one word, as a double click
// does. The button is still down, so it stays a drag: carrying on from here
// extends the selection, and releasing copies it like any other.
func (t *textSel) pressWord(line, from, to int) {
	t.a = selPoint{line: line, col: from}
	t.b = selPoint{line: line, col: to - 1}
	t.wordA, t.wordB = t.a, t.b
	t.on, t.moved, t.shown, t.word = true, true, true, true
}
