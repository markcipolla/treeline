package ui

// Main-screen layouts, by terminal width. A narrow terminal gets the issues
// table on its own; as the space grows the work panes join it, first stacked
// under a full-width issues strip, then as columns of their own. The ide pane
// sits between claude and git wherever the work panes show.
//
//	layTable   layStack          layCols                 layFour
//	┌──────┐   ┌────────────┐   ┌────┬────┬───┬────┐   ┌───┬───┬───┬───┬───┐
//	│issues│   │   issues   │   │    │    │   │git │   │   │   │   │   │   │
//	│      │   ├────┬───┬───┤   │iss.│cld.│ide├────┤   │is.│cl.│ide│git│sh.│
//	│      │   │cld.│ide│git│   │    │    │   │shl │   │   │   │   │   │   │
//	└──────┘   │    │   ├───┤   └────┴────┴───┴────┘   └───┴───┴───┴───┴───┘
//	           │    │   │shl│
//	           └────┴───┴───┘
const (
	layTable = iota // just the issues table
	layStack        // issues strip on top, claude | git over shell below
	layCols         // issues | claude | git over shell
	layFour         // issues | claude | git | shell
)

// Width thresholds between the layouts. Each step has to leave every column
// wide enough to be worth having: the issues grid needs ~50 columns before it
// starts shedding cells, and a terminal pane below ~50 is uncomfortable for
// claude and for a shell alike.
const (
	minPanels = 110 // claude, ide and git fit beside the issues strip
	minCols   = 180 // ...and the issues list fits beside them
	minFour   = 280 // ...and the shell earns a column of its own
)

// Grid geometry. KEY, PRIORITY, GIT, CI, ASSIGNEE and REPO are fixed width;
// TITLE and WORKTREE are elastic, with a floor they will not go below and a
// target they stop growing at. setTableLayout fits these to the space it has,
// shedding columns from the widest-to-least-useful end; gridFullWidth is the
// width at which it sheds nothing.
const (
	gridPad = 3 // each cell: 1-char divider + 1 padding on both sides

	colKeyW, colPriW, colGitW, colCiW = 10, 8, 10, 3
	colAsgW, repoMax                  = 14, 14
	titleMin, titleWant               = 12, 28
	wtMin, wtWant                     = 8, 24

	// below this the grid is a column beside the work panes (or a toy
	// terminal), and TITLE has to be bought out of the cells around it
	narrowGrid = 80

	// minWorkCol is the narrowest a work pane is worth showing at: below this
	// a shell wraps everything and a diff is unreadable. workFloor is what
	// protects it — the focused issues column stops growing before any work
	// column's share of the rest would drop under it.
	minWorkCol = 34
)

func (m Model) layoutMode() int {
	switch {
	case m.width >= minFour:
		return layFour
	case m.width >= minCols:
		return layCols
	case m.width >= minPanels:
		return layStack
	}
	return layTable
}

// threePane reports whether the terminal is wide enough for the work panes to
// show at all, whichever way they are arranged.
func (m Model) threePane() bool { return m.layoutMode() != layTable }

// columnLayout reports whether the issues list is a column beside the work
// panes rather than a strip above them. As a column it is always full height,
// so it never collapses to a single line the way the strip does — it widens
// on focus instead (see issuesColWidth).
func (m Model) columnLayout() bool { return m.layoutMode() >= layCols }

// box is a pane's rendered size, borders included.
type box struct{ w, h int }

// layout is the main screen's geometry: the box each pane is drawn in. Every
// piece of the panel layout measures itself from here — the renderer, the
// resize of the embedded terminals, and the mouse handlers that need to know
// which cell a click landed on.
type layout struct {
	mode                           int
	issues, claude, ide, git, term box
}

func (m Model) layout() layout {
	l := layout{mode: m.layoutMode()}
	w := m.width - docStyle.GetHorizontalFrameSize()
	if w < 40 {
		w = 40
	}
	// doc frame, header + divider, help, spare
	avail := m.height - 6
	if avail < 12 {
		avail = 12
	}

	switch l.mode {
	case layStack:
		topH, bottomH := stackHeights(avail, m.pane == paneIssues)
		gitH, termH := splitRight(bottomH)
		cols := m.bandCols(w)
		l.issues = box{w, topH}
		l.claude = box{cols[0], bottomH}
		l.ide = box{cols[1], bottomH}
		l.git = box{cols[2], gitH}
		l.term = box{cols[2], termH}

	case layCols:
		gitH, termH := splitRight(avail)
		cols := m.bandCols(w)
		l.issues = box{cols[0], avail}
		l.claude = box{cols[1], avail}
		l.ide = box{cols[2], avail}
		l.git = box{cols[3], gitH}
		l.term = box{cols[3], termH}

	case layFour:
		cols := m.bandCols(w)
		l.issues = box{cols[0], avail}
		l.claude = box{cols[1], avail}
		l.ide = box{cols[2], avail}
		l.git = box{cols[3], avail}
		l.term = box{cols[4], avail}

	default:
		h := m.height - docStyle.GetVerticalFrameSize() - 4
		if h < 3 {
			h = 3
		}
		l.issues = box{w, h}
	}
	return l
}

// bandCols is the main band's column widths for the current mode: the
// percentage split, then whatever seams the user has dragged applied on top
// (dragSeamTo), no column pressed under minDragCol. In the stacked layout
// the band is the work panes under the issues strip; in the column layouts
// the issues list is its first column.
func (m Model) bandCols(w int) []int {
	var cols []int
	switch m.layoutMode() {
	case layStack:
		lw, ew := w*36/100, w*32/100
		cols = []int{lw, ew, w - lw - ew}
	case layCols:
		iw := m.issuesColWidth(clampW(w*32/100, 50, 72), w)
		rest := w - iw
		cw, ew := rest*38/100, rest*30/100
		cols = []int{iw, cw, ew, rest - cw - ew}
	case layFour:
		iw := m.issuesColWidth(clampW(w*26/100, 50, 64), w)
		rest := w - iw
		cw, ew, gw := rest*28/100, rest*26/100, rest*24/100
		cols = []int{iw, cw, ew, gw, rest - cw - ew - gw}
	default:
		return nil
	}
	for b, d := range m.seamDrag[m.layoutMode()] {
		if b >= len(cols)-1 {
			break
		}
		d = clampSeam(d, cols[b], cols[b+1])
		cols[b] += d
		cols[b+1] -= d
	}
	return cols
}

// bandWidth is the width bandCols divides up — the terminal less the doc
// frame, floored the way layout() floors it.
func (m Model) bandWidth() int {
	w := m.width - docStyle.GetHorizontalFrameSize()
	if w < 40 {
		w = 40
	}
	return w
}

// minDragCol is as narrow as a dragged seam may press a column: past this a
// pane is a sliver of border with nothing readable inside.
const minDragCol = 24

// clampSeam holds a seam's offset to what its two columns can trade.
func clampSeam(d, left, right int) int {
	if left+right < 2*minDragCol {
		return 0 // nothing to trade at this width
	}
	if d > right-minDragCol {
		d = right - minDragCol
	}
	if d < minDragCol-left {
		d = minDragCol - left
	}
	return d
}

// stackHeights splits the vertical space in the stacked layout: the issues
// strip on top is one line when unfocused and half the screen when focused;
// the work panes share what remains below.
func stackHeights(avail int, issuesFocused bool) (topH, bottomH int) {
	topH = 3 // border + summary line + border
	if issuesFocused {
		topH = avail / 2
		if topH < 8 {
			topH = 8
		}
	}
	bottomH = avail - topH
	if bottomH < 5 {
		bottomH = 5
		topH = avail - bottomH
		if topH < 3 {
			topH = 3
		}
	}
	return topH, bottomH
}

// splitRight divides a column between the git pane and the shell below it.
// Both need their chrome — two borders, a title and its rule — before any
// content shows, so on a column too short for the preferred split they halve
// what there is rather than one of them squeezing the other out of existence.
func splitRight(h int) (gitH, termH int) {
	const minPane = 5
	gitH = h * 3 / 5
	if gitH < 8 {
		gitH = 8
	}
	termH = h - gitH
	if termH < 6 {
		termH = 6
		gitH = h - termH
	}
	if gitH < minPane || termH < minPane {
		gitH = h / 2
		termH = h - gitH
	}
	return gitH, termH
}

func clampW(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// issuesColWidth is the issues column's width in a column layout. Unfocused
// it keeps base, a share of the terminal; focused it grows towards whatever
// the grid needs to show every column, so selecting the list stops truncating
// titles and dropping cells. The cap is what keeps the work panes usable —
// the point is to read the list, not to lose the panes — so on a terminal too
// narrow to afford the whole grid the list grows as far as it can and still
// sheds the rest.
func (m Model) issuesColWidth(base, w int) int {
	if m.pane != paneIssues {
		return base
	}
	room := w - m.workFloor()
	if room < base {
		room = base // a terminal this tight has nothing to lend
	}
	return clampW(m.gridFullWidth(), base, room)
}

// workFloor is the band the work panes cannot spare: enough that the smallest
// column share stays at minWorkCol once the rest is divided up.
func (m Model) workFloor() int {
	share := 30 // ide's cut, the smallest of three columns (layCols)
	if m.layoutMode() == layFour {
		share = 22 // the shell's, the smallest of four
	}
	return (minWorkCol*100 + share - 1) / share
}
