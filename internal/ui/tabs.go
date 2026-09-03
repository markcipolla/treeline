package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The bordered tab strip the ide pane grew is worn by the git and shell panes
// too, so a "tab" reads the same everywhere: every tab hangs from one shelf
// line, the active one's bottom opens into the content below it, and a
// closable tab carries a clickable ✕ while it is active.

// tabBarH is the strip's height: a bordered tab is three rows tall.
const tabBarH = 3

// the tab shapes: every tab hangs from the same shelf line, the active one's
// bottom opens into the content below it (internal/ide draws its own copy —
// the pane also ships standalone as tide and cannot lean on this package)
var (
	tabBorder = lipgloss.Border{Top: "─", Bottom: "─", Left: "│", Right: "│",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "┴", BottomRight: "┴"}
	tabActiveBorder = lipgloss.Border{Top: "─", Bottom: " ", Left: "│", Right: "│",
		TopLeft: "╭", TopRight: "╮", BottomLeft: "┘", BottomRight: "└"}
)

// tabItem is one tab of a strip.
type tabItem struct {
	zone      string // click zone covering the whole tab
	label     string // drawn inside, markers included
	active    bool
	closeZone string // non-empty puts a ✕ (its own zone) on the tab while active
}

// tabBar renders a strip of tabs, w wide and tabBarH tall. A strip wider than
// w scrolls just enough that the active tab is fully visible, so an obscured
// tab is reached by selecting it; the shelf runs on to the right edge.
func (m Model) tabBar(w int, focused bool, items []tabItem) []string {
	activeText := paneTitleStyle
	activeBorder := subtle
	if focused {
		activeText = paneTitleFocus
		activeBorder = accent
	}
	parts := make([]string, 0, len(items))
	activeIdx := 0
	for i, it := range items {
		st := lipgloss.NewStyle().Border(tabBorder).BorderForeground(subtle).Padding(0, 1)
		body := dimStyle.Render(it.label)
		if it.active {
			activeIdx = i
			st = st.Border(tabActiveBorder).BorderForeground(activeBorder)
			body = activeText.Render(it.label)
			if it.closeZone != "" {
				body += " " + m.zones.Mark(it.closeZone, dimStyle.Render("✕"))
			}
		}
		part := st.Render(body)
		if it.zone != "" {
			part = m.zones.Mark(it.zone, part)
		}
		parts = append(parts, part)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	total := lipgloss.Width(row)
	off := 0
	if total > w {
		right := 0
		for i := 0; i <= activeIdx && i < len(parts); i++ {
			right += lipgloss.Width(parts[i])
		}
		if right > w {
			off = right - w
		}
	}
	lines := strings.Split(row, "\n")
	for i := range lines {
		lines[i] = ansi.Cut(lines[i], off, off+w)
	}
	// the shelf the tabs hang from runs to the content's right edge
	if fill := w - (total - off); fill > 0 {
		lines[len(lines)-1] += dimStyle.Render(strings.Repeat("─", fill))
	}
	return lines
}
