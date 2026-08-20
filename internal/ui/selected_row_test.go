package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestSelectedRowHasBackground: the accent foreground alone is easy to lose
// among the grid's other green, so the cursor row carries a background band.
func TestSelectedRowHasBackground(t *testing.T) {
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(restore)

	m := withIssues(newTestModel(t, 200))
	m.pane = paneIssues
	m.resize()

	// park the cursor on a card: the default lands wherever the cursor was
	// following, which with worktrees present is one of their rows
	key := ""
	for i, ref := range m.refs {
		if ref.issue != nil {
			m.table.SetCursor(i)
			key = ref.issue.Identifier
			break
		}
	}
	if key == "" {
		t.Fatal("no card row in the table")
	}

	var found, withBg bool
	for _, line := range strings.Split(m.View(), "\n") {
		if !strings.Contains(sgr.ReplaceAllString(line, ""), key) {
			continue
		}
		found = true
		if strings.Contains(line, "48;2;") {
			withBg = true
			break
		}
	}
	if !found {
		t.Fatalf("no rendered row for the selected card %s", key)
	}
	if !withBg {
		t.Errorf("selected row %s has no background", key)
	}
}
