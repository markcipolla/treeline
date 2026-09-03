package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/ide"
)

// The ide pane itself lives in internal/ide (cmd/tide ships it standalone);
// these wrappers are treeline's side of the seam: they hand the pane its
// size and focus before every interaction, convert mouse events into its
// body's coordinates, and surface its complaints as the app's error line.

// ideReady is the pane, told where it stands right now.
func (m *Model) ideReady() *ide.Pane {
	l := m.layout()
	m.ide.SetSize(l.ide.w-4, l.ide.h-4)
	m.ide.SetFocused(m.pane == paneIDE)
	return &m.ide
}

func (m *Model) pullIDEErr() {
	if e := m.ide.TakeErr(); e != nil {
		m.err = e
	}
}

func (m Model) keyIDE(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.ideReady().Key(k)
	m.pullIDEErr()
	return m, cmd
}

func (m Model) clickIDE(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	x, y, ok := m.paneBodyPos(paneIDE, msg)
	cmd := m.ideReady().Click(msg, x, y, ok)
	m.pullIDEErr()
	mm, fcmd := m.focusPane(paneIDE)
	return mm, tea.Batch(cmd, fcmd)
}

func (m *Model) scrollIDERegion(msg tea.MouseMsg, up bool) {
	x, _, ok := m.paneBodyPos(paneIDE, msg)
	m.ideReady().Scroll(x, ok, up)
}
