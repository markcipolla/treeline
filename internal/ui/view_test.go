package ui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/linear"
)

// TestViewSmoke renders every main-screen layout at several sizes to catch
// panics from width/height arithmetic.
func TestViewSmoke(t *testing.T) {
	startTerm = func(dir string, cols, rows int) (*claudeSession, error) {
		return nil, errors.New("claude sessions disabled in tests")
	}

	cfg := &config.Config{BranchTypes: []string{"feature", "bugfix"}, SlugMaxLen: 48}
	for _, size := range [][2]int{{40, 10}, {80, 24}, {110, 30}, {200, 60}} {
		m := New(cfg, t.TempDir())
		mm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		model := mm.(Model)
		model.issues = []linear.Issue{
			{Identifier: "LAB-1", Title: "A test issue", State: "In Progress", StateType: "started", Assignee: "Sam"},
			{Identifier: "LAB-2", Title: "Another one", State: "Backlog", StateType: "backlog"},
		}
		model.refreshRows()
		if v := model.View(); v == "" {
			t.Fatalf("empty view at %dx%d", size[0], size[1])
		}
		for pane := 0; pane < 3; pane++ {
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
