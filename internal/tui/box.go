package tui

// The box-drawing vocabulary everything in treeline draws lines with: the
// panes' borders, the double rules under their titles, and the git graph's
// rails. A line reaches a cell from up to four sides, each side absent,
// light or double, and the character follows from that — so a rule meeting
// a border, or a branch leaving the trunk, comes out as one junction rather
// than two strokes that nearly touch.

// Line is the weight of one arm meeting a cell.
type Line uint8

const (
	NoLine Line = iota
	Light
	Double
)

// arms packs the four weights into a lookup key.
func arms(up, down, left, right Line) int {
	return int(up)*27 + int(down)*9 + int(left)*3 + int(right)
}

// glyphs is every junction Unicode draws, keyed by its four arm weights:
// 0 absent, 1 light, 2 double. Light corners are the rounded ones, which is
// what the pane borders and the graph's branches are drawn with; a line
// that runs into nothing keeps its own weight so a stub still reads as part
// of the line it came from.
var glyphs = map[int]rune{}

func init() {
	for spec, r := range map[string]rune{
		"0000": ' ',
		// light
		"1000": '│', "0100": '│', "1100": '│',
		"0010": '─', "0001": '─', "0011": '─',
		"1001": '╰', "1010": '╯', "0101": '╭', "0110": '╮',
		"1101": '├', "1110": '┤', "0111": '┬', "1011": '┴', "1111": '┼',
		// double
		"2000": '║', "0200": '║', "2200": '║',
		"0020": '═', "0002": '═', "0022": '═',
		"2002": '╚', "2020": '╝', "0202": '╔', "0220": '╗',
		"2202": '╠', "2220": '╣', "0222": '╦', "2022": '╩', "2222": '╬',
		// a double line crossed or met by light ones
		"2201": '╟', "2210": '╢', "2211": '╫', "0211": '╥', "2011": '╨',
		"0201": '╓', "0210": '╖', "2001": '╙', "2010": '╜',
		// a light line crossed or met by double ones
		"1102": '╞', "1120": '╡', "1122": '╪', "0122": '╤', "1022": '╧',
		"0102": '╒', "0120": '╕', "1002": '╘', "1020": '╛',
	} {
		w := [4]Line{}
		for i, d := range spec {
			w[i] = Line(d - '0')
		}
		glyphs[arms(w[0], w[1], w[2], w[3])] = r
	}
}

// BoxGlyph is the character where lines of these weights meet.
func BoxGlyph(up, down, left, right Line) rune {
	if r, ok := glyphs[arms(up, down, left, right)]; ok {
		return r
	}
	// Nothing draws a line that changes weight halfway through a junction —
	// a double rail turning into a light one, say. Those come out light.
	return glyphs[arms(thin(up), thin(down), thin(left), thin(right))]
}

func thin(l Line) Line {
	if l == NoLine {
		return NoLine
	}
	return Light
}
