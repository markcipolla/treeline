package gitx

import (
	"strings"

	"github.com/markcipolla/treeline/internal/tui"
)

// The graph rails are drawn from the commit parentage rather than from
// git's own ASCII art: git's `/` and `\` diagonals cannot meet a `|` on the
// row above, so a character-for-character translation always comes out
// broken. Laying the lanes out here means every row knows which sides of
// each cell the line touches, and the glyph follows from that — so a lane
// leaving a commit is a single unbroken ├─╮ rather than two stray strokes.

// railCell records the sides of one character cell the rails touch, and how
// heavily: the trunk is drawn in the double line the panes' title rules use,
// every other lane in the light one their borders use.
type railCell struct {
	up, down, left, right tui.Line
	node                  bool // a commit sits in this cell
}

func (c railCell) glyph() rune {
	if c.node {
		return '●'
	}
	return tui.BoxGlyph(c.up, c.down, c.left, c.right)
}

// hline joins two cells on the same row, turning whatever the cells already
// carry into a corner or a tee. Endpoints only gain the side facing inward,
// so a lane arriving from above and turning left becomes ╯ and not ┴.
func hline(cells []railCell, a, b int) {
	if a > b {
		a, b = b, a
	}
	for col := a; col <= b; col++ {
		if col > a {
			cells[col].left = tui.Light
		}
		if col < b {
			cells[col].right = tui.Light
		}
	}
}

// railBuilder lays commits out in lanes, one lane per line of descent, and
// renders each transition between lanes as its own row.
type railBuilder struct {
	lanes    []string   // full hash each lane is waiting for; "" when free
	prevDown []tui.Line // what weight, if any, the row above left hanging
	rows     []LogRow
	commits  []Commit
	// trunk is the hash the spine is waiting for: HEAD, then its first
	// parent, and so on down. That one lane is drawn double, so the line of
	// descent you are working on stands out from the branches around it.
	trunk string
}

// trunkLane is the lane the spine currently runs in, or -1. When more than
// one lane waits on the same commit the leftmost is the spine, which is the
// one the others fold into.
func (b *railBuilder) trunkLane() int {
	if b.trunk == "" {
		return -1
	}
	for i, h := range b.lanes {
		if h == b.trunk {
			return i
		}
	}
	return -1
}

// weight is how heavily lane i is drawn.
func (b *railBuilder) weight(i int) tui.Line {
	if i == b.trunkLane() {
		return tui.Double
	}
	return tui.Light
}

// lane columns sit two cells apart so there is room for the horizontals.
func laneCol(i int) int { return i * 2 }

func (b *railBuilder) cells() []railCell {
	return make([]railCell, laneCol(len(b.lanes))+1)
}

// emit records a row, taking each cell's upward connection from what the
// row above left hanging, so lanes join up without anyone tracking them.
func (b *railBuilder) emit(cells []railCell, row LogRow) {
	down := make([]tui.Line, len(cells))
	for i := range cells {
		if i < len(b.prevDown) {
			cells[i].up = b.prevDown[i]
		}
		down[i] = cells[i].down
	}
	var sb strings.Builder
	for _, c := range cells {
		sb.WriteRune(c.glyph())
	}
	row.Graph = strings.TrimRight(sb.String(), " ")
	if row.Commit >= 0 {
		row.Graph += "  " // gap between the rails and the commit text
	}
	b.prevDown = down
	b.rows = append(b.rows, row)
}

// occupied marks every live lane as passing through this row.
func (b *railBuilder) occupied(cells []railCell) {
	for i, h := range b.lanes {
		if h != "" {
			cells[laneCol(i)].down = b.weight(i)
		}
	}
}

func (b *railBuilder) find(hash string) []int {
	var hits []int
	for i, h := range b.lanes {
		if h == hash {
			hits = append(hits, i)
		}
	}
	return hits
}

// alloc takes the leftmost free lane after min, widening the graph only
// when every lane to the right is busy.
func (b *railBuilder) alloc(hash string, min int) int {
	for i := min; i < len(b.lanes); i++ {
		if b.lanes[i] == "" {
			b.lanes[i] = hash
			return i
		}
	}
	b.lanes = append(b.lanes, hash)
	return len(b.lanes) - 1
}

// trim drops free lanes on the right so the graph narrows again once a
// branch has been merged away.
func (b *railBuilder) trim() {
	for len(b.lanes) > 0 && b.lanes[len(b.lanes)-1] == "" {
		b.lanes = b.lanes[:len(b.lanes)-1]
	}
}

// add appends the rows for one commit: the lanes that have converged on it
// fold in above it, then the commit itself, then the lanes its extra
// parents open up below it.
func (b *railBuilder) add(c Commit, parents []string) {
	hits := b.find(c.Hash)
	if len(hits) == 0 {
		// a branch tip, or a commit whose children fell outside the range
		hits = []int{b.alloc(c.Hash, 0)}
	}
	home := hits[0]

	if extras := hits[1:]; len(extras) > 0 {
		cells := b.cells()
		b.occupied(cells)
		for _, i := range extras {
			hline(cells, laneCol(home), laneCol(i))
			cells[laneCol(i)].down = tui.NoLine // the lane ends here
		}
		b.emit(cells, LogRow{Commit: -1})
		for _, i := range extras {
			b.lanes[i] = ""
		}
		b.trim()
	}

	b.commits = append(b.commits, c)
	cells := b.cells()
	b.occupied(cells)
	cells[laneCol(home)].node = true
	cells[laneCol(home)].down = tui.NoLine
	if len(parents) > 0 {
		cells[laneCol(home)].down = b.weight(home)
	}
	b.emit(cells, LogRow{Commit: len(b.commits) - 1})

	if c.Hash == b.trunk {
		b.trunk = "" // the spine carries on into this commit's first parent
		if len(parents) > 0 {
			b.trunk = parents[0]
		}
	}
	if len(parents) == 0 {
		b.lanes[home] = ""
		b.trim()
		return
	}
	b.lanes[home] = parents[0]
	if len(parents) == 1 {
		return
	}

	// Extra parents fan out to the right, reusing a lane that is already
	// waiting on that parent rather than opening a duplicate.
	var targets []int
	for _, p := range parents[1:] {
		if hits := b.find(p); len(hits) > 0 {
			targets = append(targets, hits[0])
			continue
		}
		targets = append(targets, b.alloc(p, home+1))
	}
	cells = b.cells()
	b.occupied(cells)
	for _, t := range targets {
		if t == home {
			continue
		}
		hline(cells, laneCol(home), laneCol(t))
	}
	b.emit(cells, LogRow{Commit: -1})
}
