package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestReauthButton: the linear status carries a refresh button that restarts
// the OAuth cycle, and it only rides along on the main screen.
func TestReauthButton(t *testing.T) {
	m := withIssues(newTestModel(t, 200))
	m.viewer.Email = "mark.cipolla@example.com"

	header := strings.Split(m.View(), "\n")[1] // docStyle pads a blank line on top
	if i, j, k := strings.Index(header, "linear"), strings.Index(header, "↻"), strings.Index(header, "⚙"); j < i || k < j {
		t.Errorf("want the ↻ between the linear status and the gear: %q", header)
	}

	z := awaitZone(t, m, "btn:reauth")
	click := tea.MouseMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	mm, _ := m.Update(click)
	// treeline ships its own client_id, so the cycle goes straight to OAuth
	got := mm.(Model)
	if got.screen != scrAuthWait {
		t.Errorf("clicking ↻ landed on screen %v, want scrAuthWait", got.screen)
	}
	if got.authCancel != nil {
		got.authCancel()
	}

	m.screen = scrSettings
	if v := m.View(); strings.Contains(v, "↻") {
		t.Error("settings screen still shows the reauth button")
	}
}

// TestReauthButtonNarrowHeader: the extra button keeps the header inside the
// terminal.
func TestReauthButtonNarrowHeader(t *testing.T) {
	for _, width := range []int{80, 100, 120, 200} {
		m := withIssues(newTestModel(t, width))
		m.viewer.Email = "mark.cipolla@example.com"
		v := m.View()
		if !strings.Contains(v, "↻") {
			t.Errorf("%d cols: no reauth button in the header", width)
		}
		for n, line := range strings.Split(v, "\n") {
			if lw := lipgloss.Width(line); lw > width {
				t.Errorf("%d cols: line %d overflows at %d", width, n, lw)
			}
		}
	}
}
