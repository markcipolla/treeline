package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The panel layout is drawn as one border grid rather than as boxes pushed
// together: neighbouring panes share the line between them, and every junction
// gets the glyph for the lines that actually meet there. Two bordered boxes
// side by side would read as a doubled seam ("││"), and with four columns
// that is three seams of wasted space and weight.
//
// A pane hands the frame its inside — rows of an exact width — plus what each
// row presents to the borders on either side, so a rule inside a pane can run
// out into the frame as a proper ├ or ╞ instead of stopping at the edge.

// edgeKind is what a pane row presents to the border beside it.
type edgeKind uint8

const (
	edgeBody  edgeKind = iota // content: the border stays a plain │
	edgeRule                  // the pane's ═ title rule
	edgeCross                 // a ─ rule inside the pane (the grid's dividers)
)

// panePart is one pane's inside, ready to be framed. rows are exactly w
// printable columns wide, edges says what each presents to the borders, and
// topJoin/botJoin are the offsets of the pane's own vertical dividers where
// they meet the border rows above and below — that is what carries the issues
// grid's columns out into the frame as ┬ and ┴.
type panePart struct {
	w       int
	rows    []string
	edges   []edgeKind
	topJoin []int
	botJoin []int
	focused bool
}

// paneCol is panes stacked in one column, sharing the border where they meet.
type paneCol []panePart

// band is columns side by side. A layout is bands stacked top to bottom: the
// column layouts are a single band, the stacked one is the issues strip above
// a band of work panes.
type band []paneCol

func (c paneCol) width() int {
	if len(c) == 0 {
		return 0
	}
	return c[0].w
}

// width is the whole frame's width, borders included.
func (b band) width() int {
	w := 1
	for _, c := range b {
		w += c.width() + 1
	}
	return w
}

// lines flattens a column into the rows the frame draws between its top and
// bottom borders, with the shared border row where two panes meet.
func (c paneCol) lines() []colLine {
	var out []colLine
	for i, p := range c {
		if i > 0 {
			out = append(out, colLine{
				s:       joinFill(c[i-1], p),
				edge:    edgeCross,
				focused: c[i-1].focused || p.focused,
			})
		}
		for j, r := range p.rows {
			out = append(out, colLine{s: r, edge: p.edges[j], focused: p.focused})
		}
	}
	return out
}

// colLine is one row of one column: its content and what the borders on
// either side of it look like.
type colLine struct {
	s       string
	edge    edgeKind
	focused bool
}

// frame draws bands of pane columns as a single bordered grid.
func frame(bands ...band) string {
	var out []string
	var prev band
	for _, b := range bands {
		if len(b) == 0 {
			continue
		}
		out = append(out, borderRow(prev, b))
		out = append(out, bandRows(b)...)
		prev = b
	}
	if prev == nil {
		return ""
	}
	out = append(out, borderRow(prev, nil))
	return strings.Join(out, "\n")
}

// bandRows draws the rows between a band's top and bottom borders.
func bandRows(b band) []string {
	cols := make([][]colLine, len(b))
	h := 0
	for i, c := range b {
		cols[i] = c.lines()
		if len(cols[i]) > h {
			h = len(cols[i])
		}
	}
	rows := make([]string, 0, h)
	for y := 0; y < h; y++ {
		var rb runBuilder
		for i, lines := range cols {
			cur := lineAt(lines, y, b[i].width())
			if i == 0 {
				rb.add(edgeGlyph(edgeBody, cur.edge), cur.focused)
			}
			rb.addRaw(cur.s)
			next := colLine{edge: edgeBody}
			if i+1 < len(cols) {
				next = lineAt(cols[i+1], y, b[i+1].width())
			}
			rb.add(edgeGlyph(cur.edge, next.edge), cur.focused || next.focused)
		}
		rows = append(rows, rb.String())
	}
	return rows
}

// lineAt is a column's row, or blank filler when the columns of a band come
// out uneven — a mismatch should not shear the whole frame sideways.
func lineAt(lines []colLine, y, w int) colLine {
	if y < len(lines) {
		return lines[y]
	}
	return colLine{s: strings.Repeat(" ", w), edge: edgeBody}
}

// borderRow draws the horizontal border between two bands. Either side may be
// nil, for the top and bottom of the frame.
func borderRow(above, below band) string {
	b := above
	if b == nil {
		b = below
	}
	w := b.width()
	up, upFocus := verticals(above, true, w)
	down, downFocus := verticals(below, false, w)
	var rb runBuilder
	for x := 0; x < w; x++ {
		rb.add(boxGlyph(up[x], down[x], x > 0, x < w-1), upFocus[x] || downFocus[x])
	}
	return rb.String()
}

// verticals marks every column of a border row that a vertical line meets
// from the given end of a band: the column seams and the frame's own edges,
// plus the dividers inside the panes there. The second result marks the
// columns a focused pane is responsible for.
func verticals(b band, bottom bool, w int) (lines, focus []bool) {
	lines, focus = make([]bool, w), make([]bool, w)
	if b == nil {
		return lines, focus
	}
	mark := func(s []bool, x int) {
		if x >= 0 && x < w {
			s[x] = true
		}
	}
	x := 0
	for _, c := range b {
		if len(c) == 0 {
			continue
		}
		p := c[0]
		joins := p.topJoin
		if bottom {
			p = c[len(c)-1]
			joins = p.botJoin
		}
		mark(lines, x)
		for _, j := range joins {
			mark(lines, x+1+j)
		}
		if p.focused {
			// a focused pane owns its span and the seams on both sides of it
			for k := x; k <= x+p.w+1; k++ {
				mark(focus, k)
			}
		}
		x += p.w + 1
	}
	mark(lines, x)
	return lines, focus
}

// joinFill is the border row shared by two stacked panes: a horizontal line
// carrying the dividers that run up into the pane above and down into the one
// below.
func joinFill(above, below panePart) string {
	up := make([]bool, above.w)
	for _, j := range above.botJoin {
		if j >= 0 && j < len(up) {
			up[j] = true
		}
	}
	down := make([]bool, above.w)
	for _, j := range below.topJoin {
		if j >= 0 && j < len(down) {
			down[j] = true
		}
	}
	var rb runBuilder
	for x := 0; x < above.w; x++ {
		rb.add(boxGlyph(up[x], down[x], true, true), above.focused || below.focused)
	}
	return rb.String()
}

// boxGlyph is the box-drawing character for the lines meeting at a junction.
// The outer corners come out rounded, matching the modals' borders.
func boxGlyph(up, down, left, right bool) rune {
	switch {
	case up && down && left && right:
		return '┼'
	case up && down && right:
		return '├'
	case up && down && left:
		return '┤'
	case up && down:
		return '│'
	case down && left && right:
		return '┬'
	case up && left && right:
		return '┴'
	case down && right:
		return '╭'
	case down && left:
		return '╮'
	case up && right:
		return '╰'
	case up && left:
		return '╯'
	case left || right:
		return '─'
	}
	return ' '
}

// edgeGlyph is the vertical border between two pane rows — or between a row
// and the outside, which counts as plain content.
func edgeGlyph(l, r edgeKind) rune {
	switch {
	case l == edgeRule && r == edgeRule:
		return '╪'
	case l == edgeRule && r == edgeCross, l == edgeCross && r == edgeRule:
		return '┼'
	case l == edgeRule:
		return '╡'
	case r == edgeRule:
		return '╞'
	case l == edgeCross && r == edgeCross:
		return '┼'
	case l == edgeCross:
		return '┤'
	case r == edgeCross:
		return '├'
	}
	return '│'
}

// runBuilder assembles a row, styling runs of border glyphs together rather
// than one escape sequence per character.
type runBuilder struct {
	b       strings.Builder
	run     []rune
	focused bool
}

func (r *runBuilder) add(g rune, focused bool) {
	if len(r.run) > 0 && focused != r.focused {
		r.flush()
	}
	r.focused = focused
	r.run = append(r.run, g)
}

// addRaw appends already-styled pane content.
func (r *runBuilder) addRaw(s string) {
	r.flush()
	r.b.WriteString(s)
}

func (r *runBuilder) flush() {
	if len(r.run) == 0 {
		return
	}
	st := metaStyle
	if r.focused {
		st = okStyle
	}
	r.b.WriteString(st.Render(string(r.run)))
	r.run = r.run[:0]
}

func (r *runBuilder) String() string {
	r.flush()
	return r.b.String()
}

// fitRows chops and pads a rendered block to exactly h rows of w columns. The
// frame needs every column to keep its width or the whole grid shears, so a
// pane that renders something too wide or too tall is cut to fit rather than
// left to push its neighbours around.
func fitRows(s string, w, h int) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = padTo(lines[i], w)
			continue
		}
		out[i] = strings.Repeat(" ", w)
	}
	return out
}

// padTo pads or truncates a styled line to exactly w printable columns.
func padTo(s string, w int) string {
	if w <= 0 {
		return ""
	}
	n := lipgloss.Width(s)
	if n > w {
		return maxWidthStyle(w).Render(s)
	}
	return s + strings.Repeat(" ", w-n)
}
