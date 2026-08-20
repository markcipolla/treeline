package ui

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/gitx"
)

// The settings screen manages the repo registry: each entry has a primary
// checkout path plus optional setup/cleanup hooks run around worktrees.

func (m Model) settingsNames() []string {
	names := make([]string, 0, len(m.cfg.Repos))
	for n := range m.cfg.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (m Model) openSettings() (tea.Model, tea.Cmd) {
	m.settingsIdx = 0
	m.screen = scrSettings
	return m, nil
}

func (m Model) keySettings(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	names := m.settingsNames()
	switch k.String() {
	case "esc", "q":
		m.screen = scrMain
		return m, nil
	case "up", "k":
		if m.settingsIdx > 0 {
			m.settingsIdx--
		}
	case "down", "j":
		if m.settingsIdx < len(names)-1 {
			m.settingsIdx++
		}
	case "enter", "e":
		if m.settingsIdx < len(names) {
			return m.openRepoEdit(names[m.settingsIdx])
		}
	case "a", "n":
		return m.openRepoEdit("")
	case "x", "d":
		if m.settingsIdx < len(names) {
			delete(m.cfg.Repos, names[m.settingsIdx])
			if m.settingsIdx > 0 {
				m.settingsIdx--
			}
			return m, m.applyRepoChanges()
		}
	}
	return m, nil
}

// openRepoEdit fills the form; name == "" starts a new entry.
func (m Model) openRepoEdit(name string) (tea.Model, tea.Cmd) {
	m.setName = name
	rc := m.cfg.Repos[name]
	m.setInputs[0].SetValue(name)
	m.setInputs[1].SetValue(rc.Path)
	m.setInputs[2].SetValue(rc.Setup)
	m.setInputs[3].SetValue(rc.Cleanup)
	m.screen = scrRepoEdit
	return m, m.setRepoEditFocus(0)
}

func (m *Model) setRepoEditFocus(f int) tea.Cmd {
	m.setFocus = f
	var cmd tea.Cmd
	for i := range m.setInputs {
		if i == f {
			cmd = m.setInputs[i].Focus()
		} else {
			m.setInputs[i].Blur()
		}
	}
	return cmd
}

func (m Model) keyRepoEdit(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.screen = scrSettings
		return m, nil
	case "tab", "down":
		return m, m.setRepoEditFocus((m.setFocus + 1) % len(m.setInputs))
	case "shift+tab", "up":
		return m, m.setRepoEditFocus((m.setFocus + len(m.setInputs) - 1) % len(m.setInputs))
	case "enter":
		if m.setFocus < len(m.setInputs)-1 {
			return m, m.setRepoEditFocus(m.setFocus + 1)
		}
		return m.saveRepoEdit()
	case "ctrl+s":
		return m.saveRepoEdit()
	}
	var cmd tea.Cmd
	m.setInputs[m.setFocus], cmd = m.setInputs[m.setFocus].Update(k)
	return m, cmd
}

func (m Model) saveRepoEdit() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.setInputs[0].Value())
	path := strings.TrimSpace(m.setInputs[1].Value())
	if name == "" || path == "" {
		m.err = errors.New("a repo needs a name and a path")
		return m, nil
	}
	root, err := gitx.RepoRoot(path)
	if err != nil {
		m.err = errors.New(path + " is not a git repository")
		return m, nil
	}
	if m.cfg.Repos == nil {
		m.cfg.Repos = map[string]config.RepoConfig{}
	}
	if m.setName != "" && m.setName != name {
		delete(m.cfg.Repos, m.setName)
	}
	m.cfg.Repos[name] = config.RepoConfig{
		Path:    root,
		Setup:   strings.TrimSpace(m.setInputs[2].Value()),
		Cleanup: strings.TrimSpace(m.setInputs[3].Value()),
	}
	m.screen = scrSettings
	return m, m.applyRepoChanges()
}

// applyRepoChanges persists the registry and rebuilds the operating set.
func (m *Model) applyRepoChanges() tea.Cmd {
	if err := m.cfg.Save(); err != nil {
		m.err = err
		return nil
	}
	m.repos = buildRepos(m.cfg, m.root)
	m.base = m.repos[0].base
	m.pendRepo = m.repos[0]
	m.loadingWT = true
	return m.loadWorktrees()
}

func (m Model) viewSettings() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Settings — repos") + "\n")
	b.WriteString(dimStyle.Render("worktrees from every repo show in the list; setup runs after a worktree is created, cleanup before it is removed") + "\n\n")
	names := m.settingsNames()
	if len(names) == 0 {
		b.WriteString(dimStyle.Render("  no repos registered — press a to add one") + "\n")
	}
	for i, n := range names {
		rc := m.cfg.Repos[n]
		marks := ""
		if rc.Setup != "" {
			marks += " ⚙setup"
		}
		if rc.Cleanup != "" {
			marks += " ⌫cleanup"
		}
		line := padRight(n, 16) + rc.Path + dimStyle.Render(marks)
		if i == m.settingsIdx {
			b.WriteString(cursorStyle.Render("❯ ") + okStyle.Render(line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\n" + dimStyle.Render("config: "+config.PathHint()) + "\n\n")
	b.WriteString(m.statusOrHelp([]key.Binding{keyChoose, keyEditRepo, keyAddRepo, keyDelRepo, keyBack}))
	return b.String()
}

func (m Model) viewRepoEdit() string {
	labels := [4]string{"name", "path", "setup", "cleanup"}
	hints := [4]string{
		"",
		"path to the primary checkout",
		"sh -c hook after a worktree is created (env: TREELINE_REPO/WORKTREE/BRANCH/ISSUE)",
		"sh -c hook before a worktree is removed",
	}
	title := "Edit repo"
	if m.setName == "" {
		title = "Add repo"
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")
	for i := range m.setInputs {
		b.WriteString(labelStyle.Render(labels[i]) + "\n" + m.setInputs[i].View() + "\n")
		if hints[i] != "" && i == m.setFocus {
			b.WriteString(dimStyle.Render("  "+hints[i]) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(m.statusOrHelp([]key.Binding{keyField, keyCommitGo, keyCancel}))
	return b.String()
}

func repoZoneID(i int) string { return "repo:" + strconv.Itoa(i) }

func (m Model) viewRepoPick() string {
	var b strings.Builder
	title := "New worktree"
	if m.pendKey != "" {
		title += " for " + m.pendKey
	}
	b.WriteString(titleStyle.Render(title) + "\n\n")
	b.WriteString("Repository:\n\n")
	for i, r := range m.repos {
		line := padRight(r.name, 16) + r.path + dimStyle.Render("  base "+r.base)
		if i == m.repoIdx {
			b.WriteString(m.zones.Mark(repoZoneID(i), cursorStyle.Render("❯ ")+okStyle.Render(line)) + "\n")
		} else {
			b.WriteString(m.zones.Mark(repoZoneID(i), "  "+line) + "\n")
		}
	}
	b.WriteString("\n" + m.statusOrHelp([]key.Binding{keyChoose, keyConfirm, keyBack}))
	return b.String()
}
