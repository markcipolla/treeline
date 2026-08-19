package ui

import "github.com/charmbracelet/bubbles/key"

// Key bindings, grouped for the bubbles/help bar. Movement keys inside the
// table and viewports are the components' own defaults.
var (
	keyOpen     = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open/create"))
	keyJump     = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "cd into worktree"))
	keyCreate   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "create worktree"))
	keyView     = key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "details"))
	keyNew      = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new"))
	keyDelete   = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	keyRefresh  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
	keyFilter   = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	keySearchL  = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "search linear"))
	keyAuth     = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "linear"))
	keyGitHub   = key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "github ci"))
	keyQuit     = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	keyBack     = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
	keyCancel   = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	keyChoose   = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "choose"))
	keyConfirm  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue"))
	keyDoIt     = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "create"))
	keyChoose2  = key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "choose"))
	keyConfirm2 = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm"))
	keyRemove   = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "remove"))
	keyRemoveB  = key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "remove + delete branch"))
	keyField    = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch field"))
	keyApply    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply"))
	keyClear    = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter"))
	keyOpenURL  = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open browser"))
	keyPane     = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch pane"))
	keyTermEsc  = key.NewBinding(key.WithKeys("ctrl+q"), key.WithHelp("ctrl+q", "next pane"))
)
