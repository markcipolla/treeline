// tide is treeline's ide pane on its own: the same file explorer, tabs,
// editor, search and git gutters (internal/ide), pointed at any directory
// and filling the terminal. Run it in a repo — or hand it a path — the way
// you'd crack open a file quickly without the rest of treeline around it.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/ide"
	"github.com/markcipolla/treeline/internal/tui"
)

var version = "dev"

const helpText = `tide — treeline's ide pane, standalone

usage:
  tide [directory]   open the editor on a directory (default: the current one)
  tide version       print version

keys are the ide pane's own: enter opens, e edits, ctrl+s saves, / filters,
ctrl+f finds in the file, ctrl+g searches the tree, a/R/d manage files.
ctrl+q quits (twice when edits are unsaved).
`

func main() {
	dir := "."
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("tide " + version)
			return
		case "help", "--help", "-h":
			fmt.Print(helpText)
			return
		default:
			dir = os.Args[1]
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fatal(err.Error())
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		fatal(abs + " is not a directory")
	}

	// tide reads treeline's config for the one setting it shares — whether
	// the tree draws nerd-font icons — and shrugs when there is none
	icons := true
	if cfg, err := config.Load(); err == nil {
		icons = cfg.Icons()
	}

	m := newModel(abs, icons)
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "tide: "+msg)
	os.Exit(1)
}

// tickMsg re-reads the worktree from disk every couple of seconds, the way
// treeline does when agent edits under the pane — here anything might.
type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type model struct {
	pane          ide.Pane
	zones         *zone.Manager
	dir           string
	width, height int
	err           error
	quitConfirm   bool // ctrl+q once already warned about unsaved edits
}

func newModel(dir string, icons bool) model {
	zones := zone.New()
	pane := ide.New(zones, icons)
	pane.SetFocused(true)
	pane.SetWorktree(dir)
	return model{pane: pane, zones: zones, dir: dir}
}

func (m model) Init() tea.Cmd {
	return tick()
}

// bodyTop is the rows of chrome above the pane's body: title and its rule.
const bodyTop = 2

func (m model) contentSize() (w, h int) {
	w, h = m.width, m.height-bodyTop-1 // the footer takes the last row
	if w < 4 {
		w = 4
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		w, h := m.contentSize()
		m.pane.SetSize(w, h)
		m.pane.SetInputWidth(w - 4)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			if m.pane.AnyDirty() && !m.quitConfirm {
				m.quitConfirm = true
				m.err = fmt.Errorf("unsaved edits — %s again quits and drops them, ctrl+s saves", msg.String())
				return m, nil
			}
			return m, tea.Quit
		}
		m.quitConfirm = false
		m.err = nil
		cmd := m.pane.Key(msg)
		m.err = m.pane.TakeErr()
		return m, cmd

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			if msg.Action == tea.MouseActionPress {
				x, _, ok := m.bodyPos(msg)
				m.pane.Scroll(x, ok, msg.Button == tea.MouseButtonWheelUp)
			}
			return m, nil
		case tea.MouseButtonLeft:
			if msg.Action != tea.MouseActionPress {
				return m, nil
			}
			x, y, ok := m.bodyPos(msg)
			cmd := m.pane.Click(msg, x, y, ok)
			if e := m.pane.TakeErr(); e != nil {
				m.err = e
			}
			return m, cmd
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(append(m.pane.RefreshDisk(), tick())...)

	case ide.GrepMsg:
		m.pane.ApplyGrep(msg)
		return m, nil
	case ide.GutterMsg:
		m.pane.ApplyGutter(msg)
		return m, nil
	case ide.ChangedMsg:
		return m, nil // no git pane here to refresh
	}
	return m, nil
}

// bodyPos converts a mouse event into a cell of the pane's body, which sits
// under the title and its rule.
func (m model) bodyPos(msg tea.MouseMsg) (x, y int, ok bool) {
	z := m.zones.Get("tide:body")
	if z == nil || z.IsZero() {
		return 0, 0, false
	}
	x, y = z.Pos(msg)
	return x, y, z.InBounds(msg)
}

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	w, h := m.contentSize()
	title, body := m.pane.Content(w, h)
	// the pane titles itself "ide — …"; tide wears its own name
	title = "tide" + strings.TrimPrefix(title, "ide")

	rows := strings.Split(body, "\n")
	for len(rows) < h {
		rows = append(rows, "")
	}
	for i, r := range rows {
		rows[i] = tui.PadTo(r, w)
	}

	foot := tui.DimStyle.Render(" enter opens · e edits · ctrl+s saves · ctrl+g searches · ctrl+q quits")
	if m.err != nil {
		foot = tui.ErrStyle.Render(" ✗ " + m.err.Error())
	}

	out := tui.PadTo(tui.PaneTitleFocus.Render(" "+title), w) + "\n" +
		tui.OkStyle.Render(strings.Repeat("═", w)) + "\n" +
		m.zones.Mark("tide:body", strings.Join(rows[:h], "\n")) + "\n" +
		tui.MaxWidth(w).Render(foot)
	return m.zones.Scan(out)
}
