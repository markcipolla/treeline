package ide

import "github.com/markcipolla/treeline/internal/tui"

// The pane draws with treeline's shared palette (internal/tui), aliased so
// the code moved here from the ui package reads as it always has.
var (
	accent  = tui.Accent
	subtle  = tui.Subtle
	warnCol = tui.WarnCol
	btnBg   = tui.BtnBg
	btnFg   = tui.BtnFg

	paneTitleStyle = tui.PaneTitleStyle
	paneTitleFocus = tui.PaneTitleFocus
	metaStyle      = tui.MetaStyle
	errStyle       = tui.ErrStyle
	warnStyle      = tui.WarnStyle
	okStyle        = tui.OkStyle
	cursorStyle    = tui.CursorStyle
	dimStyle       = tui.DimStyle
	searchHitStyle = tui.SearchHitStyle

	truncate      = tui.Truncate
	padTo         = tui.PadTo
	clampW        = tui.ClampW
	maxWidthStyle = tui.MaxWidth
)
