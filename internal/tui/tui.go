// Package tui holds the pieces of treeline's look that more than one
// program draws with: the palette and text styles, and the small string
// helpers that cut and pad styled terminal cells. The ui package (the full
// treeline app) and the ide package (the editor pane, also shipped alone as
// tide) both build on it, so the two stay visually one tool.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	Accent  = lipgloss.AdaptiveColor{Light: "#16a34a", Dark: "#4ade80"}
	Subtle  = lipgloss.AdaptiveColor{Light: "#9ca3af", Dark: "#6b7280"}
	ErrCol  = lipgloss.AdaptiveColor{Light: "#dc2626", Dark: "#f87171"}
	WarnCol = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#fbbf24"}
	BtnBg   = lipgloss.AdaptiveColor{Light: "#e5e7eb", Dark: "#374151"}
	BtnFg   = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#e5e7eb"}
	OnAcc   = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#052e16"}

	// the panes only style their titles; the frame's chrome is tinted by
	// focus with MetaStyle/OkStyle
	PaneTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(Subtle)
	PaneTitleFocus = lipgloss.NewStyle().Bold(true).Foreground(Accent)

	MetaStyle   = lipgloss.NewStyle().Foreground(Subtle)
	ErrStyle    = lipgloss.NewStyle().Foreground(ErrCol)
	WarnStyle   = lipgloss.NewStyle().Foreground(WarnCol)
	OkStyle     = lipgloss.NewStyle().Foreground(Accent)
	CursorStyle = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	DimStyle    = lipgloss.NewStyle().Foreground(Subtle)

	// SearchHitStyle picks the query out of a search result's line
	SearchHitStyle = lipgloss.NewStyle().Foreground(OnAcc).Background(Accent)
)

// MaxWidth clips a styled string to w printable columns.
func MaxWidth(w int) lipgloss.Style {
	return lipgloss.NewStyle().MaxWidth(w)
}

// Truncate shortens s to at most w runes, ending with an ellipsis.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

// PadTo pads or truncates a styled line to exactly w printable columns.
func PadTo(s string, w int) string {
	if w <= 0 {
		return ""
	}
	n := lipgloss.Width(s)
	if n > w {
		return MaxWidth(w).Render(s)
	}
	return s + strings.Repeat(" ", w-n)
}

// ClampW clamps v into [lo, hi].
func ClampW(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ClampIdx clamps a cursor into a list of n rows: [0, n-1], and 0 for none.
func ClampIdx(v, n int) int {
	if v >= n {
		v = n - 1
	}
	if v < 0 {
		v = 0
	}
	return v
}
