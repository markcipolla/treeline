package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestPinClusters(t *testing.T) {
	in := "\x1b[31mok \"ප්‍රේෂණය\"\x1b[0m |"
	out := pinClusters(in)
	if ansi.StringWidth(out) != ansi.StringWidth(in) {
		t.Fatalf("width changed: %d → %d", ansi.StringWidth(in), ansi.StringWidth(out))
	}
	if want := "\x1b7රේ\x1b8\x1b[C"; !strings.Contains(out, want) {
		t.Fatalf("cluster not pinned: %q", out)
	}
	if plain := "abc ළ |"; pinClusters(plain) != plain {
		t.Fatalf("plain text altered: %q", pinClusters(plain))
	}
}
