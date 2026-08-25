package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/linear"
)

// TestViewSmoke renders every main-screen layout at several sizes to catch
// panics from width/height arithmetic.
func TestViewSmoke(t *testing.T) {
	startTerm = func(dir string, cols, rows int, persist bool) (*claudeSession, error) {
		return nil, errors.New("claude sessions disabled in tests")
	}
	startShell = func(dir string, cols, rows int, persist bool, kind string) (*claudeSession, error) {
		return startTerm(dir, cols, rows, persist)
	}

	cfg := &config.Config{BranchTypes: []string{"feature", "bugfix"}, SlugMaxLen: 48}
	for _, size := range [][2]int{{40, 10}, {80, 24}, {110, 30}, {180, 40}, {200, 60}, {260, 60}} {
		m := New(cfg, t.TempDir())
		mm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model := mm.(Model)
		model.authed = true // the unauth+loading summary line is allowed to be long
		model.loadingWT, model.loadingIssues = false, false
		model.issues = []linear.Issue{
			{Identifier: "LAB-1", Title: "A test issue", State: "In Progress", StateType: "started", Assignee: "Sam"},
			{Identifier: "LAB-2", Title: "Another one", State: "Backlog", StateType: "backlog"},
		}
		model.refreshRows()
		v := model.View()
		if v == "" {
			t.Fatalf("empty view at %dx%d", size[0], size[1])
		}
		// the narrow layout boxes the table on its own; the panel layout
		// merges it into the issues pane, whose corners are rounded
		frame := []string{"┌", "┬", "┐", "├", "┤", "└", "┴", "┘"}
		if model.threePane() {
			frame = []string{"╭", "╤", "╮", "├", "┤", "╰", "┴", "╯"}
		}
		for _, join := range frame {
			if !strings.Contains(v, join) {
				t.Fatalf("table frame missing %q at %dx%d", join, size[0], size[1])
			}
		}
		// minimum column widths overflow toy terminals by design
		if size[0] >= 80 {
			for n, line := range strings.Split(v, "\n") {
				if lw := lipgloss.Width(line); lw > size[0] {
					t.Fatalf("line %d overflows: %d > %d cols", n, lw, size[0])
				}
			}
		}
		for pane := 0; pane < paneCount; pane++ {
			mp, _ := model.focusPane(pane)
			if v := mp.(Model).View(); v == "" {
				t.Fatalf("empty view, pane %d at %dx%d", pane, size[0], size[1])
			}
		}
		model.screen = scrSearch
		model.searchResults = model.issues
		model.searchedFor = "test"
		if v := model.View(); v == "" {
			t.Fatalf("empty search view at %dx%d", size[0], size[1])
		}
	}
}
