package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestSettingsButton: the header carries a settings button past the linear
// status, and clicking it opens the repo registry.
func TestSettingsButton(t *testing.T) {
	m := withIssues(newTestModel(t, 200))
	m.viewer.Email = "mark.cipolla@labflow.ai"

	v := m.View()
	if !strings.Contains(v, "⚙ settings") {
		t.Fatalf("header is missing the settings button:\n%s", strings.SplitN(v, "\n", 3)[1])
	}
	header := strings.Split(v, "\n")[1] // docStyle pads a blank line on top
	if !strings.Contains(header, "⚙") {
		t.Errorf("settings button is not on the header line: %q", header)
	}
	if i, j := strings.Index(header, "linear"), strings.Index(header, "⚙"); i < 0 || j < i {
		t.Errorf("settings button should sit right of the linear status: %q", header)
	}

	z := awaitZone(t, m, "btn:settings")
	click := tea.MouseMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}
	mm, _ := m.Update(click)
	if got := mm.(Model); got.screen != scrSettings {
		t.Errorf("clicking settings landed on screen %v, want scrSettings", got.screen)
	}
}

// TestSettingsButtonNarrowHeader: a tight header keeps the gear without
// pushing the line past the terminal edge.
func TestSettingsButtonNarrowHeader(t *testing.T) {
	for _, width := range []int{80, 100, 120, 200} {
		m := withIssues(newTestModel(t, width))
		m.viewer.Email = "mark.cipolla@labflow.ai"
		v := m.View()
		if !strings.Contains(v, "⚙") {
			t.Errorf("%d cols: no settings button in the header", width)
		}
		for n, line := range strings.Split(v, "\n") {
			if lw := lipgloss.Width(line); lw > width {
				t.Errorf("%d cols: line %d overflows at %d", width, n, lw)
			}
		}
	}
}

// TestSettingsButtonMainScreenOnly: the button belongs to the main screen —
// it would be pointless on the screen it opens.
func TestSettingsButtonMainScreenOnly(t *testing.T) {
	m := withIssues(newTestModel(t, 200))
	m.screen = scrSettings
	if v := m.View(); strings.Contains(v, "⚙") {
		t.Error("settings screen still shows the settings button")
	}
}
