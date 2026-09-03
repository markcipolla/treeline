package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/markcipolla/treeline/internal/tui"
)

// The palette and the styles shared with the standalone ide (tide) live in
// internal/tui; they are aliased here so the rest of the package reads as it
// always has.
var (
	accent  = tui.Accent
	subtle  = tui.Subtle
	errCol  = tui.ErrCol
	warnCol = tui.WarnCol
	btnBg   = tui.BtnBg
	btnFg   = tui.BtnFg
	onAcc   = tui.OnAcc
	// the selected row's background. The accent foreground alone reads as
	// "this row is green", not "this row is where you are", once the grid is
	// a column among several — the band is what carries the cursor.
	selBg = lipgloss.AdaptiveColor{Light: "#dcfce7", Dark: "#14532d"}

	// Column dividers: every cell draws a left "│"; the header's bottom
	// rule meets each divider with a "┼" intersection.
	cellDivider   = lipgloss.Border{Left: "│"}
	headerDivider = lipgloss.Border{Left: "│", Bottom: "─", BottomLeft: "┼"}

	groupTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(subtle)
	groupTitleFocus = lipgloss.NewStyle().Bold(true).Foreground(accent)

	// the panel layout draws its own borders (see frame.go); the panes only
	// style their titles, and metaStyle/okStyle tint the frame by focus
	paneTitleStyle = tui.PaneTitleStyle
	paneTitleFocus = tui.PaneTitleFocus

	docStyle    = lipgloss.NewStyle().Padding(1, 2, 0, 2)
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	metaStyle   = tui.MetaStyle
	tabActive   = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true).Padding(0, 1)
	tabInactive = lipgloss.NewStyle().Foreground(subtle).Padding(0, 1)
	errStyle    = tui.ErrStyle
	warnStyle   = tui.WarnStyle
	okStyle     = tui.OkStyle
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle).Padding(1, 2)
	labelStyle  = lipgloss.NewStyle().Foreground(subtle).Width(9)
	cursorStyle = tui.CursorStyle
	dimStyle    = tui.DimStyle

	// searchHitStyle picks the query out of a search result's line
	searchHitStyle = tui.SearchHitStyle

	btnStyle        = lipgloss.NewStyle().Foreground(btnFg).Background(btnBg).Padding(0, 2)
	btnPrimaryStyle = lipgloss.NewStyle().Foreground(onAcc).Background(accent).Bold(true).Padding(0, 2)
)

// String helpers shared with the ide package, aliased for the same reason.
var (
	truncate      = tui.Truncate
	padTo         = tui.PadTo
	clampW        = tui.ClampW
	clampIdx      = tui.ClampIdx
	maxWidthStyle = tui.MaxWidth
)
