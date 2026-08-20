package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The panel layout is composed from closed boxes: every pane draws its own
// four borders, and they sit flush, so the seam between two panes is two lines
// thick. Sharing one line between neighbours reads tighter, but then a rule
// inside one pane — the issues grid's header rule and group dividers — has to
// T-join the border of the pane beside it, and a focused pane's highlight runs
// into its neighbour's edge. Every panel owning its own border keeps the two
// apart, which is worth a column per seam.
//
// A pane still hands the frame its inside rather than drawing itself: rows of
// an exact width, what each row presents to the borders beside it, and where
// its own dividers meet the borders above and below.

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

// paneCol is panes stacked in one column, each closed off in its own box.
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

// frame draws bands of pane columns, each pane boxed and flush with the next.
func frame(bands ...band) string {
	var out []string
	for _, b := range bands {
		out = append(out, bandRows(b)...)
	}
	return strings.Join(out, "\n")
}

// bandRows puts a band's columns side by side.
func bandRows(b band) []string {
	cols := make([][]string, len(b))
	h := 0
	for i, c := range b {
		cols[i] = c.boxed()
		if len(cols[i]) > h {
			h = len(cols[i])
		}
	}
	rows := make([]string, h)
	for y := range rows {
		var sb strings.Builder
		for i, lines := range cols {
			if y < len(lines) {
				sb.WriteString(lines[y])
				continue
			}
			// a column that came out short: blank, rather than shearing the
			// columns beside it sideways
			sb.WriteString(strings.Repeat(" ", b[i].width()+2))
		}
		rows[y] = sb.String()
	}
	return rows
}

// boxed stacks a column's panes, each in its own box.
func (c paneCol) boxed() []string {
	var out []string
	for _, p := range c {
		out = append(out, p.boxed()...)
	}
	return out
}

// boxed draws a pane as a closed box: its own four borders, with the pane's
// internal dividers carried into the top and bottom ones as ┬ and ┴, and its
// rules meeting the sides as ╞ ╡ or ├ ┤.
func (p panePart) boxed() []string {
	st := metaStyle
	if p.focused {
		st = okStyle
	}
	out := make([]string, 0, len(p.rows)+2)
	out = append(out, st.Render(borderFill('╭', '╮', p.topJoin, false, p.w)))
	for i, row := range p.rows {
		left, right := sideGlyphs(p.edges[i])
		out = append(out, st.Render(string(left))+row+st.Render(string(right)))
	}
	out = append(out, st.Render(borderFill('╰', '╯', p.botJoin, true, p.w)))
	return out
}

// borderFill is a pane's top or bottom border: the corners with a horizontal
// line between them, ticked where the pane's own dividers run into it.
func borderFill(left, right rune, joins []int, up bool, w int) string {
	tick := make([]bool, w)
	for _, j := range joins {
		if j >= 0 && j < w {
			tick[j] = true
		}
	}
	var sb strings.Builder
	sb.WriteRune(left)
	for x := 0; x < w; x++ {
		sb.WriteRune(boxGlyph(up && tick[x], !up && tick[x], true, true))
	}
	sb.WriteRune(right)
	return sb.String()
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

// sideGlyphs are the border characters beside a pane row: a plain edge for
// content, and the ends of a rule for the pane's own rules, so a rule reads as
// part of the box rather than stopping short of it.
func sideGlyphs(k edgeKind) (left, right rune) {
	switch k {
	case edgeRule:
		return '╞', '╡'
	case edgeCross:
		return '├', '┤'
	}
	return '│', '│'
}

// fitRows chops and pads a rendered block to exactly h rows of w columns. The
// frame needs every column to keep its width or the whole grid shears, so a
// pane that renders something too wide or too tall is cut to fit rather than
// left to push its neighbours around.
func fitRows(s string, w, h int) []string {
	if h < 0 {
		h = 0
	}
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
