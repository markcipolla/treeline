package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"

	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/gitx"
)

// newSetupPaneModel is newTestModel with the primary repo registered with a
// setup script, shown in the shell pane's tab or not.
func newSetupPaneModel(t *testing.T, width int, pane bool) Model {
	t.Helper()
	startTerm = func(dir string, cols, rows int, persist bool) (*agentSession, error) {
		return nil, errors.New("agent sessions disabled in tests")
	}
	startShell = func(dir string, cols, rows int, persist bool, kind string) (*agentSession, error) {
		return nil, errors.New("shell sessions disabled in tests")
	}
	startSetup = func(dir string, cols, rows int, persist bool, script string, env []string) (*agentSession, error) {
		return nil, errors.New("setup sessions disabled in tests")
	}

	root := t.TempDir()
	cfg := &config.Config{
		BranchTypes: []string{"feature"},
		SlugMaxLen:  48,
		Repos: map[string]config.RepoConfig{
			"main": {Path: root, Setup: "echo serving", SetupPane: pane},
		},
	}
	m := New(cfg, root)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	model := mm.(Model)
	model.loadingWT, model.loadingIssues = false, false
	model.wts = []gitx.Worktree{
		{Root: root, Path: root + "/.worktrees/first", Branch: "feature/lab-1-first"},
	}
	model.refreshRows()
	model.selectWorktree(model.wts[0].Path)
	return model
}

// fakeTermSession is a session that never touches a pty, enough for the
// model to hold in a tab.
func fakeTermSession(dir string) *agentSession {
	return &agentSession{
		dir:    dir,
		em:     vt.NewEmulator(80, 24),
		cols:   80,
		rows:   24,
		notify: make(chan struct{}, 1),
	}
}

func tabKinds(tabs []*termTab) []string {
	kinds := make([]string, 0, len(tabs))
	for _, t := range tabs {
		kinds = append(kinds, t.kind)
	}
	return kinds
}

// TestSetupTabLeadsShellPane: with the checkbox on, the shell pane's tab row
// carries the setup tab first; without it there is only the shell.
func TestSetupTabLeadsShellPane(t *testing.T) {
	m := newSetupPaneModel(t, 200, true)
	if !m.threePane() {
		t.Fatal("want panel layout at 200 cols")
	}
	dir := m.agentDir()
	kinds := tabKinds(m.termTabsFor(dir))
	if len(kinds) != 2 || kinds[0] != "setup" || kinds[1] != "shell" {
		t.Fatalf("tabs = %v, want [setup shell]", kinds)
	}

	off := newSetupPaneModel(t, 200, false)
	kinds = tabKinds(off.termTabsFor(off.agentDir()))
	if len(kinds) != 1 || kinds[0] != "shell" {
		t.Fatalf("tabs = %v, want just [shell]", kinds)
	}
}

// TestCreatedLaunchesSetupTab: with the checkbox on, worktree creation runs
// the setup script in the setup tab and brings it to the front.
func TestCreatedLaunchesSetupTab(t *testing.T) {
	m := newSetupPaneModel(t, 200, true)
	var gotScript string
	var gotEnv []string
	startSetup = func(dir string, cols, rows int, persist bool, script string, env []string) (*agentSession, error) {
		gotScript, gotEnv = script, env
		return fakeTermSession(dir), nil
	}

	path := m.root + "/.worktrees/lab-9"
	mm, _ := m.Update(createdMsg{path: path, branchName: "feature/lab-9-new", root: m.root})
	got := mm.(Model)

	if got.setupBusy {
		t.Error("tab mode must not flag the background setupBusy spinner")
	}
	if gotScript != "echo serving" {
		t.Errorf("startSetup script = %q, want the repo's setup hook", gotScript)
	}
	wantEnv := "TREELINE_WORKTREE=" + path
	if !containsStr(gotEnv, wantEnv) {
		t.Errorf("startSetup env %v missing %q", gotEnv, wantEnv)
	}
	tabs := got.termTabs[path]
	if len(tabs) == 0 || tabs[0].kind != "setup" || tabs[0].sess == nil {
		t.Fatalf("want a running setup tab first, got %v", tabKinds(tabs))
	}
	if got.termSel[path] != 0 {
		t.Errorf("active tab = %d, want the setup tab in front", got.termSel[path])
	}
}

// TestCreatedKeepsBackgroundRun: without the checkbox the old path holds —
// the script runs headless with the spinner up.
func TestCreatedKeepsBackgroundRun(t *testing.T) {
	m := newSetupPaneModel(t, 200, false)
	called := false
	startSetup = func(dir string, cols, rows int, persist bool, script string, env []string) (*agentSession, error) {
		called = true
		return fakeTermSession(dir), nil
	}

	path := m.root + "/.worktrees/lab-9"
	mm, _ := m.Update(createdMsg{path: path, branchName: "feature/lab-9-new", root: m.root})
	got := mm.(Model)

	if !got.setupBusy {
		t.Error("background mode should flag setupBusy")
	}
	if called {
		t.Error("background mode must not start a tab session")
	}
}

// TestShellTabsAddAndCycle: ctrl+t opens numbered shells that all read
// "shell", ctrl+←/→ move between tabs, and x closes an exited extra tab.
func TestShellTabsAddAndCycle(t *testing.T) {
	m := newSetupPaneModel(t, 200, true)
	startShell = func(dir string, cols, rows int, persist bool, kind string) (*agentSession, error) {
		return fakeTermSession(dir), nil
	}
	startSetup = func(dir string, cols, rows int, persist bool, script string, env []string) (*agentSession, error) {
		return fakeTermSession(dir), nil
	}
	dir := m.agentDir()
	mm, _ := m.focusPane(paneTerm)

	mm, _ = mm.(Model).keyShell(tea.KeyMsg{Type: tea.KeyCtrlT})
	got := mm.(Model)
	kinds := tabKinds(got.termTabs[dir])
	want := []string{"setup", "shell", "shell2"}
	if strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Fatalf("tabs after ctrl+t = %v, want %v", kinds, want)
	}
	if got.termSel[dir] != 2 {
		t.Fatalf("active tab = %d, want the new shell (2)", got.termSel[dir])
	}
	if got.termTabs[dir][2].sess == nil {
		t.Fatal("the new tab should have started its shell")
	}

	mm, _ = got.keyShell(tea.KeyMsg{Type: tea.KeyCtrlRight})
	if sel := mm.(Model).termSel[dir]; sel != 0 {
		t.Errorf("ctrl+right from the last tab should wrap to 0, got %d", sel)
	}
	mm, _ = mm.(Model).keyShell(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	got = mm.(Model)
	if sel := got.termSel[dir]; sel != 2 {
		t.Errorf("ctrl+left should wrap back to the last tab, got %d", sel)
	}

	// the extra shell exits: x gives the tab back
	got.termTabs[dir][2].sess.exited.Store(true)
	mm, _ = got.keyShell(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got = mm.(Model)
	kinds = tabKinds(got.termTabs[dir])
	if strings.Join(kinds, " ") != "setup shell" {
		t.Errorf("tabs after x = %v, want [setup shell]", kinds)
	}
}

// TestRepoEditPaneCheckbox: the form loads the flag, space toggles it on its
// own field, and the view draws the box checked.
func TestRepoEditPaneCheckbox(t *testing.T) {
	m := newSetupPaneModel(t, 200, true)

	mm, _ := m.openRepoEdit("main")
	got := mm.(Model)
	if !got.setPaneOn {
		t.Fatal("opening the form should load setup_pane from the config")
	}

	// tab from name down to the checkbox, then toggle it off with space
	for i := 0; i < setPaneField; i++ {
		mm, _ = mm.(Model).keyRepoEdit(tea.KeyMsg{Type: tea.KeyTab})
	}
	if f := mm.(Model).setFocus; f != setPaneField {
		t.Fatalf("focus = %d, want the checkbox at %d", f, setPaneField)
	}
	if !strings.Contains(mm.(Model).viewRepoEdit(), "[x] show setup in a pane") {
		t.Error("view should draw the checkbox checked")
	}
	mm, _ = mm.(Model).keyRepoEdit(tea.KeyMsg{Type: tea.KeySpace})
	got = mm.(Model)
	if got.setPaneOn {
		t.Error("space on the checkbox should toggle it off")
	}
	if !strings.Contains(got.viewRepoEdit(), "[ ] show setup in a pane") {
		t.Error("view should draw the checkbox cleared")
	}

	// typing into the checkbox field must not leak into an input
	before := got.setInputs[3].Value()
	mm, _ = got.keyRepoEdit(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if v := mm.(Model).setInputs[3].Value(); v != before {
		t.Errorf("checkbox focus leaked a keystroke into the cleanup input: %q", v)
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
