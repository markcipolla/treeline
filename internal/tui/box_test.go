package tui

import "testing"

func TestBoxGlyphJunctions(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		up, down, left, right Line
		want                  rune
	}{
		{"light corner is rounded", NoLine, Light, NoLine, Light, '╭'},
		{"light tee", Light, Light, NoLine, Light, '├'},
		{"light cross", Light, Light, Light, Light, '┼'},
		{"double run", Double, Double, NoLine, NoLine, '║'},
		{"light branch off a double trunk", Double, Double, NoLine, Light, '╟'},
		{"light crossing a double trunk", Double, Double, Light, Light, '╫'},
		{"a double rule meeting a light border", Light, Light, NoLine, Double, '╞'},
		{"a light divider meeting a double rule", NoLine, Light, Double, Double, '╤'},
		{"nothing at all", NoLine, NoLine, NoLine, NoLine, ' '},
	} {
		if got := BoxGlyph(tc.up, tc.down, tc.left, tc.right); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBoxGlyphFallsBackWhenWeightChangesMidJunction(t *testing.T) {
	// nothing draws a line that arrives double and leaves light, so the
	// whole junction goes light rather than leaving a hole
	if got := BoxGlyph(Double, Light, NoLine, NoLine); got != '│' {
		t.Errorf("got %q, want %q", got, '│')
	}
}
