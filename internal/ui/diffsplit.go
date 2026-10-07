package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// splitDiffMinWidth is the pane width from which the file preview splits into
// before/after columns: two columns at the layout's own floor for a readable
// diff, plus the divider between them. Narrower than this the halves are too
// thin to read code in, so the diff stays unified and wraps.
const splitDiffMinWidth = 2*minWorkCol + 3

// diffTab is what a tab expands to. Hard-wrapping and column padding both
// count cells, and a terminal renders a raw tab as its own width, so the tabs
// have to go or the divider between the columns wanders.
const diffTab = "    "

// diffRows renders git's coloured unified diff into display lines that fit a
// w-wide box. Long lines wrap instead of being clipped, and when there is room
// the removals and additions sit side by side the way GitHub shows them.
func diffRows(diff string, w int) []string {
	if diff == "" || w < 4 {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(diff, "\t", diffTab), "\n")
	if w < splitDiffMinWidth {
		return wrapDiffLines(lines, w)
	}
	return splitDiffRows(lines, w)
}

// wrapDiffLines hard-wraps each line at w. Code is wrapped rather than
// word-wrapped: indentation survives, and a long identifier doesn't push
// itself onto a line of its own.
func wrapDiffLines(lines []string, w int) []string {
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, strings.Split(ansi.Hardwrap(ln, w, true), "\n")...)
	}
	return out
}

// splitDiffRows lays a unified diff out in two columns: removals left,
// additions right, paired up in the order they appear in each run. Context is
// mirrored on both sides; the file header, hunk headers and git's notes span
// the full width. The left column is as wide as the file picker's, so the
// divider under the picker carries on down through the preview.
func splitDiffRows(lines []string, w int) []string {
	lw := (w - 3) / 2
	rw := w - 3 - lw
	pad := lipgloss.NewStyle().Width(lw).MaxWidth(lw).Inline(true)
	sep := metaStyle.Render(" │ ")

	var out, del, add []string
	// flush emits the removals and additions gathered since the last context
	// line, pairing the i-th removal with the i-th addition.
	flush := func() {
		for i := 0; i < max(len(del), len(add)); i++ {
			var l, r string
			if i < len(del) {
				l = del[i]
			}
			if i < len(add) {
				r = add[i]
			}
			out = append(out, pairRow(l, r, lw, rw, pad, sep)...)
		}
		del, add = nil, nil
	}
	for _, ln := range lines {
		plain := ansi.Strip(ln)
		switch {
		case strings.HasPrefix(plain, "-") && !strings.HasPrefix(plain, "---"):
			del = append(del, ln)
		case strings.HasPrefix(plain, "+") && !strings.HasPrefix(plain, "+++"):
			add = append(add, ln)
		case strings.HasPrefix(plain, " "):
			flush()
			out = append(out, pairRow(ln, ln, lw, rw, pad, sep)...)
		default:
			flush()
			out = append(out, ansi.Hardwrap(ln, w, true))
		}
	}
	flush()
	return out
}

// sgrPrefix is the colour git opens a diff line with. It opens once at the
// front and resets at the end, which is fine down a single column and not fine
// across two: a wrapped segment carries no escape of its own, so it would take
// on whatever the cell to its left left switched on. cellLines re-opens it.
var sgrPrefix = regexp.MustCompile(`^(?:\x1b\[[0-9;]*m)+`)

// cellLines hard-wraps one diff line into a cell w wide, each segment opening
// and closing its own colour.
func cellLines(s string, w int) []string {
	segs := strings.Split(ansi.Hardwrap(s, w, true), "\n")
	open := sgrPrefix.FindString(s)
	if open == "" {
		return segs
	}
	for i := range segs {
		if i > 0 {
			segs[i] = open + segs[i]
		}
		segs[i] += "\x1b[m"
	}
	return segs
}

// pairRow draws one before/after row. Either side may wrap to several lines,
// so the row is as tall as the taller cell and the shorter one is left blank.
func pairRow(l, r string, lw, rw int, pad lipgloss.Style, sep string) []string {
	ls := cellLines(l, lw)
	rs := cellLines(r, rw)
	rows := make([]string, max(len(ls), len(rs)))
	for i := range rows {
		var a, b string
		if i < len(ls) {
			a = ls[i]
		}
		if i < len(rs) {
			b = rs[i]
		}
		rows[i] = pad.Render(a) + sep + b
	}
	return rows
}
