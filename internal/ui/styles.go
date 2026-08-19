package ui

import "github.com/charmbracelet/lipgloss"

var (
	accent  = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#4ade80"}
	subtle  = lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#6b7280"}
	errCol  = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f87171"}
	warnCol = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#fbbf24"}
	btnBg   = lipgloss.AdaptiveColor{Light: "#e5e7eb", Dark: "#374151"}
	btnFg   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#e5e7eb"}
	onAcc   = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#052e16"}

	// Column dividers: every cell draws a left "│"; the header's bottom
	// rule meets each divider with a "┼" intersection.
	cellDivider   = lipgloss.Border{Left: "│"}
	headerDivider = lipgloss.Border{Left: "│", Bottom: "─", BottomLeft: "┼"}

	groupTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(subtle)

	paneStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle)
	paneFocusStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent)
	paneTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(subtle)
	paneTitleFocus = lipgloss.NewStyle().Bold(true).Foreground(accent)

	docStyle    = lipgloss.NewStyle().Padding(1, 2)
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	metaStyle   = lipgloss.NewStyle().Foreground(subtle)
	tabActive   = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true).Padding(0, 1)
	tabInactive = lipgloss.NewStyle().Foreground(subtle).Padding(0, 1)
	errStyle    = lipgloss.NewStyle().Foreground(errCol)
	warnStyle   = lipgloss.NewStyle().Foreground(warnCol)
	okStyle     = lipgloss.NewStyle().Foreground(accent)
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle).Padding(1, 2)
	labelStyle  = lipgloss.NewStyle().Foreground(subtle).Width(9)
	cursorStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(subtle)

	btnStyle        = lipgloss.NewStyle().Foreground(btnFg).Background(btnBg).Padding(0, 2)
	btnPrimaryStyle = lipgloss.NewStyle().Foreground(onAcc).Background(accent).Bold(true).Padding(0, 2)
)
