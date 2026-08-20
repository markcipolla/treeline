package ui

// Main-screen layouts, by terminal width. A narrow terminal gets the issues
// table on its own; as the space grows the work panes join it, first stacked
// under a full-width issues strip, then as columns of their own.
//
//	layTable   layStack        layCols            layFour
//	┌──────┐   ┌──────────┐   ┌────┬────┬────┐   ┌───┬───┬───┬───┐
//	│issues│   │  issues  │   │    │    │git │   │   │   │   │   │
//	│      │   ├─────┬────┤   │iss.│cld.├────┤   │is.│cl.│git│sh.│
//	│      │   │claude│git│   │    │    │shl │   │   │   │   │   │
//	└──────┘   │      ├───┤   └────┴────┴────┘   └───┴───┴───┴───┘
//	           │      │shl│
//	           └──────┴───┘
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
	minPanels = 110 // claude and git fit beside the issues strip
	minCols   = 180 // ...and the issues list fits beside them
	minFour   = 240 // ...and the shell earns a column of its own
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
	// a shell wraps everything and a diff is unreadable. focusedIssuesPct is
	// what protects it — capping the focused issues column at a share of the
	// band leaves every work pane above minWorkCol at all the widths that
	// reach a column layout.
	minWorkCol       = 34
	focusedIssuesPct = 55
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
	mode                      int
	issues, claude, git, term box
}

func (m Model) layout() layout {
	l := layout{mode: m.layoutMode()}
	w := m.width - docStyle.GetHorizontalFrameSize()
	if w < 40 {
		w = 40
	}
	// doc frame, header + divider, summary, help, spare
	avail := m.height - 8
	if avail < 12 {
		avail = 12
	}

	switch l.mode {
	case layStack:
		// panes that meet share the border between them, so the boxes add up
		// to one row (and column) more than the space they fill, per seam
		topH, bottomH := stackHeights(avail+1, m.pane == paneIssues)
		gitH, termH := splitRight(bottomH + 1)
		lw := (w + 1) / 2
		l.issues = box{w, topH}
		l.claude = box{lw, bottomH}
		l.git = box{w + 1 - lw, gitH}
		l.term = box{w + 1 - lw, termH}

	case layCols:
		iw := m.issuesColWidth(clampW(w*32/100, 50, 72), w)
		rest := w + 2 - iw
		cw := rest * 55 / 100
		gitH, termH := splitRight(avail + 1)
		l.issues = box{iw, avail}
		l.claude = box{cw, avail}
		l.git = box{rest - cw, gitH}
		l.term = box{rest - cw, termH}

	case layFour:
		iw := m.issuesColWidth(clampW(w*26/100, 50, 64), w)
		rest := w + 3 - iw
		cw := rest * 38 / 100
		gw := rest * 34 / 100
		l.issues = box{iw, avail}
		l.claude = box{cw, avail}
		l.git = box{gw, avail}
		l.term = box{rest - cw - gw, avail}

	default:
		h := m.height - docStyle.GetVerticalFrameSize() - 6
		if h < 3 {
			h = 3
		}
		l.issues = box{w, h}
	}
	return l
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
func splitRight(h int) (gitH, termH int) {
	gitH = h * 3 / 5
	if gitH < 8 {
		gitH = 8
	}
	termH = h - gitH
	if termH < 6 {
		termH = 6
		gitH = h - termH
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
	room := w * focusedIssuesPct / 100
	if room < base {
		room = base // a terminal this tight has nothing to lend
	}
	return clampW(m.gridFullWidth(), base, room)
}
