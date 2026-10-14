package ui

import (
	"errors"
	"testing"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/linear"
)

// TestViewFitsTerminal holds every main-screen state to the terminal's rows.
// One line over and the alt screen scrolls under the renderer, which leaves
// stale rows above the header for the rest of the session.
func TestViewFitsTerminal(t *testing.T) {
	startTerm = func(dir string, cols, rows int, persist bool, agent string) (*claudeSession, error) {
		return nil, errors.New("claude sessions disabled in tests")
	}
	startShell = func(dir string, cols, rows int, persist bool, kind string) (*claudeSession, error) {
		return startTerm(dir, cols, rows, persist, "true")
	}

	cfg := &config.Config{BranchTypes: []string{"feature"}, SlugMaxLen: 48}
	for _, size := range [][2]int{{80, 24}, {110, 30}, {180, 40}, {200, 60}, {300, 80}} {
		m := New(cfg, t.TempDir())
		mm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		base := mm.(Model)
		base.authed = true
		base.issues = []linear.Issue{{Identifier: "LAB-1", Title: "A test issue", StateType: "started"}}
		base.refreshRows()

		filtering := base
		filtering.filtering = true
		failed := base
		failed.err = errors.New("git: fatal: it went wrong\nand kept going\nfor three lines")

		for name, model := range map[string]Model{"plain": base, "filtering": filtering, "error": failed} {
			for pane := 0; pane < paneCount; pane++ {
				fm, _ := model.focusPane(pane)
				if h := lipgloss.Height(fm.(Model).View()); h > size[1] {
					t.Errorf("%s, pane %d at %dx%d: %d rows > %d",
						name, pane, size[0], size[1], h, size[1])
				}
			}
		}
	}
}
