package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/markcipolla/treeline/internal/branch"
	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/github"
	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
	"github.com/markcipolla/treeline/internal/tmux"
)

type screen int

const (
	scrMain screen = iota
	scrDetail
	scrManual
	scrTypePick
	scrEditBranch
	scrCreating
	scrCreated
	scrDeleteConfirm
	scrAuth
	scrAuthWait
	scrGitHub
	scrSearch
	scrRepoPick
	scrSettings
	scrRepoEdit
)

// repoEntry is a repository treeline operates on.
type repoEntry struct {
	name    string
	path    string
	base    string // ref new branches start from
	setup   string // sh -c hook after worktree creation
	cleanup string // sh -c hook before worktree removal
}

type Model struct {
	cfg  *config.Config
	root string
	base string // primary repo's base ref

	repos    []repoEntry // primary first, then registered repos
	pendRepo repoEntry   // repo the create flow targets
	repoIdx  int         // cursor in the repo picker

	setupBusy bool // a setup script is running for the new worktree

	// settings: repo registry editor
	settingsIdx int
	setName     string             // entry being edited; "" = adding
	setInputs   [4]textinput.Model // name, path, setup, cleanup
	setFocus    int

	zones *zone.Manager

	screen screen
	width  int
	height int

	// the one table: issues grouped by status, worktrees inline
	table table.Model
	refs  []rowRef

	wts    []gitx.Worktree
	issues []linear.Issue

	// CI status per branch name
	ci      map[string]github.Status
	ghToken string
	ghOwner string
	ghRepo  string
	ghOK    bool // origin is a GitHub repo

	filterInput textinput.Model
	filtering   bool // filter input is focused

	viewport    viewport.Model
	detailIssue *linear.Issue

	help    help.Model
	spinner spinner.Model

	loadingWT     bool
	loadingIssues bool
	loadingCI     bool
	authed        bool
	viewer        linear.Viewer
	linearBusy    bool           // a card fetch is in flight (incl. background ticks)
	linearFail    bool           // the most recent fetch failed
	extraIssues   []linear.Issue // cards referenced by worktrees but not assigned
	extraFetching bool

	// create flow
	pendKey     string // issue key like LMAP-142; "" for free-form branches
	pendTitle   string
	typeIdx     int
	branchInput textinput.Model

	// manual entry
	manualInput   textinput.Model
	fetchingIssue bool

	// workspace-wide issue search (live, debounced)
	searchInput   textinput.Model
	searchResults []linear.Issue
	searchSel     int
	searching     bool
	searchSeq     int    // bumped per keystroke; stale ticks/results are dropped
	searchedFor   string // query the current results answer; "" = none yet

	// panel main screen (wide terminals): 0 issues, 1 claude, 2 git, 3 shell
	pane   int
	terms  map[string]*claudeSession // interactive claude per directory
	shells map[string]*claudeSession // shell per directory

	diffVP      viewport.Model
	diffRaw     string
	diffFor     string // worktree path the branch diff shows
	loadingDiff bool

	// git pane: file picker, hunk staging, commit log
	gitFor      string    // directory the pane operates on
	gitFreshAt  time.Time // last auto-refresh; throttles claude-driven reloads
	copiedUntil time.Time // "copied" flash after a drag selection
	copiedFrom  int       // and the pane it came from
	gitMode     int
	gitUnstaged []gitx.FileStatus
	gitStaged   []gitx.FileStatus
	gitCol      int // active column: 0 unstaged, 1 staged
	gitSelU     int // per-column selections
	gitSelS     int
	gitScroll   [2]int // per-column view offsets, scrolled by the wheel
	gitDiff     string // colored preview for the selected file
	// gitDiffScroll scrolls that preview on its own, so the wheel over the
	// diff doesn't drag the file lists around with it
	gitDiffScroll int
	gitSel        textSel // mouse text selection over the pane's rendered text
	hunkPath      string
	hunkStaged    bool
	hunkHeader    string
	hunks         []string
	hunkSel       int
	commits       []gitx.Commit
	commitSel     int

	// commit form
	commitSubject textinput.Model
	commitBody    textarea.Model
	commitFocus   int // 0 subject, 1 body
	generating    bool

	// delete confirm
	delTarget *gitx.Worktree
	delFocus  int // focused button: 0 remove, 1 remove+branch, 2 cancel
	// delForce is set once git has refused because the worktree is locked:
	// the same modal then asks again, and the buttons break the lock.
	delForce bool
	delErr   error // why the last attempt failed, shown in the modal
	removing bool

	// linear auth
	authInputs [2]textinput.Model
	authFocus  int
	authCancel context.CancelFunc

	// github device flow
	deviceCode *github.DeviceCode
	ghCancel   context.CancelFunc

	createdPath   string
	createdBranch string
	jumpPath      string
	// pendSelect is a worktree we want selected but which the worktree list
	// hasn't caught up with yet (a just-created one); worktreesMsg retries it.
	pendSelect string
	err        error
}

func New(cfg *config.Config, root string) Model {
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Bold(true).Foreground(subtle).BorderStyle(headerDivider).BorderForeground(subtle).BorderLeft(true).BorderBottom(true)
	// No BorderForeground here: a colored divider embeds an ANSI reset that
	// would cut the Selected row highlight off at the first "│". Dividers on
	// unselected rows are tinted in renderTable instead.
	styles.Cell = styles.Cell.BorderStyle(cellDivider).BorderLeft(true)
	styles.Selected = styles.Selected.Foreground(accent).Bold(true)

	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "KEY", Width: 10},
			{Title: "TITLE", Width: 40},
			{Title: "PRIORITY", Width: 8},
			{Title: "ASSIGNEE", Width: 14},
			{Title: "WORKTREE", Width: 24},
			{Title: "GIT", Width: 10},
			{Title: "CI", Width: 3},
		}),
		table.WithFocused(true),
	)
	t.SetStyles(styles)

	newInput := func(placeholder string) textinput.Model {
		ti := textinput.New()
		ti.CharLimit = 200
		ti.Prompt = "❯ "
		ti.PromptStyle = cursorStyle
		ti.Placeholder = placeholder
		return ti
	}

	filterInput := newInput("type to filter…")
	filterInput.Prompt = "/ "

	searchInput := newInput("text, state:review, @name…")
	// ghost-text completion à la the bubbletea autocomplete example:
	// tab accepts, fed from live results as they arrive
	searchInput.ShowSuggestions = true

	authInputs := [2]textinput.Model{
		newInput("OAuth client ID"),
		newInput("OAuth client secret (optional)"),
	}
	authInputs[1].EchoMode = textinput.EchoPassword

	setInputs := [4]textinput.Model{
		newInput("repo name, e.g. labmaster"),
		newInput("/path/to/primary/checkout"),
		newInput("optional setup script"),
		newInput("optional cleanup script"),
	}

	commitSubject := newInput("summary of the change…")
	commitBody := textarea.New()
	commitBody.Placeholder = "longer description (optional)…"
	commitBody.ShowLineNumbers = false
	commitBody.CharLimit = 0

	ghOwner, ghRepo, ghOK := github.RepoFromRemote(root)

	repos := buildRepos(cfg, root)

	return Model{
		cfg:           cfg,
		root:          root,
		base:          repos[0].base,
		repos:         repos,
		pendRepo:      repos[0],
		zones:         zone.New(),
		table:         t,
		ci:            map[string]github.Status{},
		ghToken:       github.Token(cfg.GitHub.Token),
		ghOwner:       ghOwner,
		ghRepo:        ghRepo,
		ghOK:          ghOK,
		filterInput:   filterInput,
		searchInput:   searchInput,
		setInputs:     setInputs,
		commitSubject: commitSubject,
		commitBody:    commitBody,
		terms:         map[string]*claudeSession{},
		shells:        map[string]*claudeSession{},
		diffVP:        viewport.New(0, 0),
		viewport:      viewport.New(0, 0),
		help:          help.New(),
		spinner:       spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(okStyle)),
		branchInput:   newInput(""),
		manualInput:   newInput("LMAP-142 or a/full/branch-name"),
		authInputs:    authInputs,
		loadingWT:     true,
		loadingIssues: cfg.Linear.Token().Usable(),
		linearBusy:    cfg.Linear.Token().Usable(),
		authed:        cfg.Linear.Token().Usable(),
	}
}

// buildRepos resolves the operating set: the repo treeline was launched in
// first, then every other registered repo that still exists.
func buildRepos(cfg *config.Config, root string) []repoEntry {
	repos := []repoEntry{{name: filepath.Base(root), path: root, base: gitx.DefaultBase(root)}}
	names := make([]string, 0, len(cfg.Repos))
	for n := range cfg.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rc := cfg.Repos[n]
		if rc.Path == root {
			repos[0].name, repos[0].setup, repos[0].cleanup = n, rc.Setup, rc.Cleanup
			continue
		}
		if _, err := gitx.RepoRoot(rc.Path); err != nil {
			continue
		}
		repos = append(repos, repoEntry{
			name: n, path: rc.Path, base: gitx.DefaultBase(rc.Path),
			setup: rc.Setup, cleanup: rc.Cleanup,
		})
	}
	return repos
}

// multiRepo reports whether treeline is operating on more than one repo. The
// table carries a REPO column only then — with a single repo every row would
// say the same thing.
func (m Model) multiRepo() bool { return len(m.repos) > 1 }

// repoFor finds the entry owning a primary checkout path.
func (m Model) repoFor(root string) repoEntry {
	for _, r := range m.repos {
		if r.path == root {
			return r
		}
	}
	return repoEntry{name: filepath.Base(root), path: root, base: "HEAD"}
}

// syncExtras fetches cards referenced by worktree branches that aren't in
// the assigned list. all=true also refreshes ones already fetched.
func (m *Model) syncExtras(all bool) tea.Cmd {
	if !m.authed || m.extraFetching {
		return nil
	}
	known := map[string]bool{}
	for _, is := range m.issues {
		known[is.Identifier] = true
	}
	if !all {
		for _, is := range m.extraIssues {
			known[is.Identifier] = true
		}
	}
	var keys []string
	seen := map[string]bool{}
	for _, wt := range m.wts {
		if k := issueKeyFromBranch(wt.Branch); k != "" && !known[k] && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	m.extraFetching = true
	return fetchExtraIssuesCmd(m.cfg, keys)
}

// loadWorktrees lists worktrees across every operating repo.
func (m Model) loadWorktrees() tea.Cmd {
	paths := make([]string, len(m.repos))
	for i, r := range m.repos {
		paths[i] = r.path
	}
	return loadWorktreesCmd(paths)
}

// JumpPath is the worktree path the user chose to jump into, if any.
func (m Model) JumpPath() string { return m.jumpPath }

// Close shuts down any embedded sessions the panes started.
// Close shuts the embedded panes down on the way out. Persisted (tmux-backed)
// sessions are only detached, so the claude running in them keeps its context
// and the next launch attaches straight back to it.
func (m Model) Close() {
	for _, s := range m.terms {
		s.close()
	}
	for _, s := range m.shells {
		s.close()
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, m.loadWorktrees(), issuesTickCmd()}
	if m.cfg.Linear.Token().Usable() {
		cmds = append(cmds, loadIssuesCmd(m.cfg))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, m.syncPanes()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case worktreesMsg:
		m.loadingWT = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.wts = msg.wts
		m.refreshRows()
		var focus tea.Cmd
		if pend := m.pendSelect; pend != "" {
			m.pendSelect = ""
			if m.selectWorktree(pend) {
				var mm tea.Model
				mm, focus = m.focusPane(paneClaude)
				m = mm.(Model)
			}
		}
		if !m.paneEnabled(m.pane) {
			mm, _ := m.focusPane(paneIssues)
			m = mm.(Model)
		}
		return m, tea.Batch(m.maybeLoadCI(), m.syncPanes(), m.syncExtras(false), focus)

	case issuesTickMsg:
		// silent background refresh of the cards, re-armed every 30s
		cmds := []tea.Cmd{issuesTickCmd()}
		if m.authed {
			m.linearBusy = true
			cmds = append(cmds, loadIssuesCmd(m.cfg))
		}
		return m, tea.Batch(cmds...)

	case issuesMsg:
		m.linearBusy = false
		m.linearFail = msg.err != nil
		if msg.err != nil {
			// only surface failures the user asked for; background
			// refreshes retry in 30s anyway
			if m.loadingIssues {
				m.err = msg.err
			}
			m.loadingIssues = false
			return m, nil
		}
		m.loadingIssues = false
		m.viewer = msg.viewer
		m.issues = msg.issues
		m.refreshRows()
		// the rows just moved under the cursor; the panes follow the selection
		return m, tea.Batch(m.syncExtras(true), m.syncPanes())

	case ciMsg:
		m.loadingCI = false
		for b, s := range msg.statuses {
			m.ci[b] = s
		}
		m.refreshRows()
		return m, m.syncPanes()

	case searchDebounceMsg:
		if msg.seq != m.searchSeq || m.screen != scrSearch {
			return m, nil // superseded by a newer keystroke
		}
		q := strings.TrimSpace(m.searchInput.Value())
		if len(q) < 2 {
			m.searchResults = nil
			m.searchedFor = ""
			return m, nil
		}
		m.searching = true
		m.searchedFor = q
		return m, searchIssuesCmd(m.cfg, q, msg.seq)

	case searchMsg:
		if msg.seq != m.searchSeq {
			return m, nil // answer to an outdated query
		}
		m.searching = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.searchResults = msg.issues
		if m.searchSel >= len(m.searchResults) {
			m.searchSel = 0
		}
		sugs := make([]string, 0, len(msg.issues)*2)
		for _, is := range msg.issues {
			sugs = append(sugs, is.Title, is.Identifier)
		}
		m.searchInput.SetSuggestions(sugs)
		return m, nil

	case gitStatusMsg:
		if msg.dir != m.gitFor {
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.gitUnstaged, m.gitStaged = splitStatus(msg.files)
		m.clampGitSel()
		return m, m.loadSelectedFileDiff()

	case gitLogMsg:
		if msg.dir != m.gitFor {
			return m, nil
		}
		if msg.err == nil {
			m.commits = msg.commits
			if m.commitSel >= len(m.commits) {
				m.commitSel = 0
			}
		}
		return m, nil

	case gitFileDiffMsg:
		fs, staged, ok := m.selectedGitFile()
		if msg.dir != m.gitFor || !ok || fs.Path != msg.path || staged != msg.staged {
			return m, nil // selection moved on
		}
		diff := msg.diff
		if msg.err != nil {
			diff = errStyle.Render("✗ " + msg.err.Error())
		}
		if diff != m.gitDiff {
			m.gitDiff, m.gitDiffScroll = diff, 0
		}
		return m, nil

	case gitCommitMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.commitSubject.SetValue("")
		m.commitBody.SetValue("")
		m.closeCommitForm()
		return m, tea.Batch(m.reloadGit(), m.loadWorktrees())

	case genCommitMsg:
		m.generating = false
		if msg.dir != m.gitFor || m.gitMode != gitModeCommit {
			return m, nil // form was closed meanwhile
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.commitSubject.SetValue(msg.subject)
		m.commitBody.SetValue(msg.body)
		return m, nil

	case diffMsg:
		m.loadingDiff = false
		if msg.path != m.diffFor {
			return m, nil // selection moved on; stale diff
		}
		if msg.err != nil {
			m.diffRaw = errStyle.Render("✗ " + msg.err.Error())
		} else {
			m.diffRaw = msg.diff
		}
		m.setDiffContent()
		return m, nil

	case claudeTermMsg:
		s := msg.s
		if m.terms[s.dir] != s && m.shells[s.dir] != s {
			return m, nil // session was replaced or closed
		}
		cmds := []tea.Cmd{waitClaudeTerm(s)} // re-arm; the view reads the vt directly
		// claude and shell commands edit files; keep the git pane current.
		// Throttled, not debounced — claude's UI animates continuously.
		if s.dir == m.gitFor && time.Since(m.gitFreshAt) > 3*time.Second {
			m.gitFreshAt = time.Now()
			cmds = append(cmds, m.reloadGit(), m.loadSelectedFileDiff(), m.loadWorktrees())
		}
		return m, tea.Batch(cmds...)

	case extraIssuesMsg:
		m.extraFetching = false
		byID := map[string]linear.Issue{}
		for _, is := range m.extraIssues {
			byID[is.Identifier] = is
		}
		for _, is := range msg.issues {
			byID[is.Identifier] = is
		}
		// keep only cards a worktree still references
		m.extraIssues = m.extraIssues[:0]
		seen := map[string]bool{}
		for _, wt := range m.wts {
			k := issueKeyFromBranch(wt.Branch)
			if is, ok := byID[k]; ok && !seen[k] {
				seen[k] = true
				m.extraIssues = append(m.extraIssues, is)
			}
		}
		m.refreshRows()
		return m, m.syncPanes()

	case issueFetchedMsg:
		m.fetchingIssue = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.startCreateFlow(*msg.issue)
		return m, nil

	case createdMsg:
		if msg.err != nil {
			m.err = msg.err
			m.screen = scrEditBranch
			return m, nil
		}
		m.createdPath = msg.path
		m.createdBranch = msg.branchName
		m.screen = scrCreated
		m.loadingWT = true
		cmds := []tea.Cmd{m.loadWorktrees()}
		if repo := m.repoFor(msg.root); repo.setup != "" {
			m.setupBusy = true
			cmds = append(cmds, runSetupCmd(repo.setup, msg.path,
				scriptEnv(repo.name, msg.path, msg.branchName, m.pendKey)))
		}
		return m, tea.Batch(cmds...)

	case scriptDoneMsg:
		m.setupBusy = false
		if msg.err != nil {
			m.err = fmt.Errorf("setup script: %v — %s", msg.err, lastLine(msg.out))
		}
		return m, nil

	case removedMsg:
		m.removing = false
		if msg.err != nil {
			// A lock is worth a second ask rather than a dead end: keep the
			// modal up, explain what holds the worktree, and offer to break it.
			if errors.Is(msg.err, gitx.ErrLocked) && !m.delForce && m.delTarget != nil {
				m.delForce, m.delErr, m.delFocus = true, msg.err, 2 // default to cancel
				m.screen = scrDeleteConfirm
				return m, nil
			}
			m.err = msg.err
			m.delErr = msg.err
			return m, nil
		}
		if msg.warn != "" {
			m.err = errors.New(msg.warn)
		}
		m.delTarget, m.delForce, m.delErr = nil, false, nil
		m.screen = scrMain
		m.loadingWT = true
		return m, m.loadWorktrees()

	case authDoneMsg:
		m.authCancel = nil
		if m.screen != scrAuthWait {
			return m, nil // user backed out; ignore the late result
		}
		if msg.err != nil {
			m.err = msg.err
			m.screen = scrMain
			return m, nil
		}
		m.cfg.Linear.SetToken(msg.token)
		if err := m.cfg.Save(); err != nil {
			m.err = err
		}
		m.authed = true
		m.loadingIssues = true
		m.linearBusy = true
		m.screen = scrMain
		return m, loadIssuesCmd(m.cfg)

	case deviceCodeMsg:
		if m.screen != scrGitHub {
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err
			m.screen = scrMain
			return m, nil
		}
		m.deviceCode = msg.dc
		_ = openBrowser(msg.dc.VerificationURI)
		return m, pollDeviceTokenCmd(m.ghCtx(), m.ghClientID(), msg.dc)

	case ghTokenMsg:
		m.ghCancel = nil
		if m.screen != scrGitHub {
			return m, nil
		}
		m.deviceCode = nil
		if msg.err != nil {
			m.err = msg.err
			m.screen = scrMain
			return m, nil
		}
		m.cfg.GitHub.Token = msg.token
		if err := m.cfg.Save(); err != nil {
			m.err = err
		}
		m.ghToken = msg.token
		m.screen = scrMain
		return m, m.maybeLoadCI()

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// ghCtx returns a fresh cancellable context for the device flow, replacing
// any previous one.
func (m *Model) ghCtx() context.Context {
	if m.ghCancel != nil {
		m.ghCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.ghCancel = cancel
	return ctx
}

func (m Model) ghClientID() string {
	if m.cfg.GitHub.ClientID != "" {
		return m.cfg.GitHub.ClientID
	}
	return github.DefaultClientID
}

func (m Model) maybeLoadCI() tea.Cmd {
	if !m.ghOK || m.ghToken == "" || len(m.wts) == 0 {
		return nil
	}
	branches := make([]string, 0, len(m.wts))
	for _, wt := range m.wts {
		// the detected GitHub remote belongs to the primary repo only
		if wt.Branch != "" && wt.Root == m.root {
			branches = append(branches, wt.Branch)
		}
	}
	return loadCICmd(m.ghToken, m.ghOwner, m.ghRepo, branches)
}

func (m *Model) resize() {
	w := m.width - docStyle.GetHorizontalFrameSize()
	h := m.height - docStyle.GetVerticalFrameSize() - 6 // header, summary, filter, help
	if h < 3 {
		h = 3
	}
	if w < 40 {
		w = 40
	}

	if m.threePane() {
		topH, bottomH := m.panelHeights()
		// the grid is the pane: full width, and only the top border, the
		// title and the title rule sit above it
		m.setTableLayout(w, topH-2)
		lw := w / 2
		rw := w - lw
		inner := bottomH - 4 // borders + pane title + title rule
		for _, t := range m.terms {
			t.resize(lw-2, inner)
		}
		gitH, termH := m.rightSplit()
		gitInner := gitH - 4
		for _, t := range m.shells {
			t.resize(rw-2, termH-4)
		}
		m.diffVP.Width = rw - 2
		m.diffVP.Height = gitInner
		inner = gitInner
		m.commitSubject.Width = rw - 10
		m.commitBody.SetWidth(rw - 6)
		bh := inner - 9
		if bh < 3 {
			bh = 3
		}
		m.commitBody.SetHeight(bh)
		m.setDiffContent()
	} else {
		m.setTableLayout(w, h)
	}

	m.viewport.Width = w
	m.viewport.Height = h + 2
	if m.detailIssue != nil {
		m.viewport.SetContent(renderIssueDetail(*m.detailIssue, w))
	}

	m.help.Width = w
	inputW := w - 4
	if inputW > 80 {
		inputW = 80
	}
	m.filterInput.Width = inputW
	m.searchInput.Width = inputW
	for i := range m.setInputs {
		m.setInputs[i].Width = inputW
	}
	m.branchInput.Width = inputW
	m.manualInput.Width = inputW
	for i := range m.authInputs {
		m.authInputs[i].Width = inputW
	}
}

// setTableLayout sizes the issue table's columns for the given width.
// ASSIGNEE is the first column to go when the terminal is narrow, then REPO
// (whose cell only appears with several repos registered, and whose repo the
// worktree path names anyway): a zero width hides a column outright, which
// the table widget and the frame drawing in renderTable both honour.
func (m *Model) setTableLayout(w, h int) {
	pad := 3 // each cell: 1-char divider + 1 padding on both sides
	w--      // renderTable appends a right edge to every row
	keyW, priW, gitW, ciW := 10, 8, 10, 3
	const titleMin, wtMin, asgFull, repoMax = 12, 8, 14, 14

	// the columns that are always there, TITLE and WORKTREE at their floors
	fixed := keyW + priW + gitW + ciW + titleMin + wtMin
	// each optional column costs its width plus a divider and padding
	fits := func(widths ...int) bool {
		total := fixed + 6*pad
		for _, x := range widths {
			if x > 0 {
				total += x + pad
			}
		}
		return total <= w
	}

	// REPO is only as wide as the names it holds — repo names are short, and
	// the space is better spent on TITLE
	repoW := 0
	if m.multiRepo() {
		repoW = lipgloss.Width("REPO")
		for _, r := range m.repos {
			if n := lipgloss.Width(r.name); n > repoW {
				repoW = n
			}
		}
		if repoW > repoMax {
			repoW = repoMax
		}
	}
	asgW := asgFull
	if !fits(asgW, repoW) {
		asgW = 0
	}
	if !fits(asgW, repoW) {
		repoW = 0
	}
	cols := 6
	for _, x := range []int{asgW, repoW} {
		if x > 0 {
			cols++
		}
	}

	wtW := w * 22 / 100
	if wtW < 14 {
		wtW = 14
	}
	titleW := w - keyW - priW - asgW - repoW - gitW - ciW - wtW - cols*pad
	if titleW < titleMin {
		// give TITLE its floor back out of WORKTREE so the frame still fits
		wtW -= titleMin - titleW
		titleW = titleMin
		if wtW < wtMin {
			wtW = wtMin
		}
	}
	// hiding REPO hands the repo name back to the worktree cell, so rows built
	// under the old column set have to be rebuilt
	hadRepo := m.repoColumnVisible()

	columns := []table.Column{
		{Title: "KEY", Width: keyW},
		{Title: "TITLE", Width: titleW},
		{Title: "PRIORITY", Width: priW},
		{Title: "ASSIGNEE", Width: asgW},
	}
	if m.multiRepo() {
		columns = append(columns, table.Column{Title: "REPO", Width: repoW})
	}
	columns = append(columns,
		table.Column{Title: "WORKTREE", Width: wtW},
		table.Column{Title: "GIT", Width: gitW},
		table.Column{Title: "CI", Width: ciW},
	)
	m.table.SetColumns(columns)
	if m.repoColumnVisible() != hadRepo && len(m.refs) > 0 {
		m.refreshRows()
	}
	m.table.SetWidth(w)
	// renderTable adds a top and bottom frame line around the widget
	m.table.SetHeight(h - 2)
}

// refreshRows rebuilds the table: issues grouped by status (active work
// first), each with its worktree when one exists, then remaining worktrees.
func (m *Model) refreshRows() {
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))

	// assigned cards plus ones worktrees reference (e.g. PR reviews)
	all := make([]linear.Issue, 0, len(m.issues)+len(m.extraIssues))
	all = append(all, m.issues...)
	assigned := make(map[string]bool, len(m.issues))
	for _, is := range m.issues {
		assigned[is.Identifier] = true
	}
	for _, is := range m.extraIssues {
		if !assigned[is.Identifier] {
			all = append(all, is)
		}
	}

	// link worktrees to issues via the issue key in the branch name
	issueKeys := make(map[string]bool, len(all))
	for _, is := range all {
		issueKeys[is.Identifier] = true
	}
	linked := map[string]*gitx.Worktree{}
	var spare []*gitx.Worktree
	for i := range m.wts {
		wt := &m.wts[i]
		k := issueKeyFromBranch(wt.Branch)
		if !wt.IsPrimary && !wt.Prunable && k != "" && issueKeys[k] && linked[k] == nil {
			linked[k] = wt
		} else {
			spare = append(spare, wt)
		}
	}

	// group issues by state name, ordered by state type
	type group struct {
		name   string
		rank   int
		issues []*linear.Issue
	}
	var groups []*group
	byName := map[string]*group{}
	for i := range all {
		is := &all[i]
		wt := linked[is.Identifier]
		if q != "" && !strings.Contains(issueHaystack(*is, wt, m.root), q) {
			continue
		}
		g := byName[is.State]
		if g == nil {
			g = &group{name: is.State, rank: stateRank(is.StateType)}
			byName[is.State] = g
			groups = append(groups, g)
		}
		g.issues = append(g.issues, is)
	}
	sort.SliceStable(groups, func(a, b int) bool { return groups[a].rank < groups[b].rank })
	// within a group: urgent → low, unprioritized last, updatedAt preserved
	prio := func(p int) int {
		if p == 0 {
			return 5
		}
		return p
	}
	for _, g := range groups {
		is := g.issues
		sort.SliceStable(is, func(a, b int) bool { return prio(is[a].Priority) < prio(is[b].Priority) })
	}

	var rows []table.Row
	var refs []rowRef
	// row assembles cells in column order, dropping REPO when the column is
	// not in the set — renderRow indexes the columns per cell
	row := func(key, title, pri, asg, repo, wt, git, ci string) table.Row {
		r := table.Row{key, title, pri, asg}
		if m.multiRepo() {
			r = append(r, repo)
		}
		return append(r, wt, git, ci)
	}
	header := func(title string) {
		// plain text: styled cells break the table's width-based truncation
		rows = append(rows, row("", "▸ "+title, "", "", "", "", "", ""))
		refs = append(refs, rowRef{kind: rowHeader})
	}

	for _, g := range groups {
		header(strings.ToUpper(g.name))
		for _, is := range g.issues {
			wt := linked[is.Identifier]
			wtCell, gitCell, ciCell := "", "", ""
			if wt != nil {
				wtCell = m.wtLabel(wt)
				gitCell = wtStatus(*wt)
				ciCell = ciSymbol(m.ci[wt.Branch])
			}
			rows = append(rows, row(is.Identifier, is.Title, linear.PriorityName(is.Priority), is.Assignee, m.wtRepoName(wt), wtCell, gitCell, ciCell))
			refs = append(refs, rowRef{kind: rowIssue, issue: is, wt: wt})
		}
	}

	var wtRows []table.Row
	var wtRefs []rowRef
	for _, wt := range spare {
		if q != "" && !strings.Contains(wtHaystack(*wt, m.root), q) {
			continue
		}
		name := wt.Branch
		if name == "" {
			name = "(detached HEAD)"
		}
		if wt.IsPrimary {
			name += " ●"
		}
		wtRows = append(wtRows, row("", name, "", "", m.wtRepoName(wt), m.wtLabel(wt), wtStatus(*wt), ciSymbol(m.ci[wt.Branch])))
		wtRefs = append(wtRefs, rowRef{kind: rowWorktree, wt: wt})
	}
	if len(wtRows) > 0 {
		header("WORKTREES")
		rows = append(rows, wtRows...)
		refs = append(refs, wtRefs...)
	}

	cur := m.table.Cursor()
	want := m.selectedRef().keys()
	m.refs = refs
	m.table.SetRows(rows)
	if len(want) > 0 {
		// the rows moved (cards loaded, a filter changed): follow the
		// selection rather than letting the index point at a new row
		for i, ref := range refs {
			if sameRow(ref.keys(), want) {
				cur = i
				break
			}
		}
	}
	if cur >= len(rows) {
		cur = len(rows) - 1
	}
	if cur < 0 {
		cur = 0
	}
	m.table.SetCursor(cur)
	m.settleCursor(false)
}

// moveCards moves the cursor by n selectable rows, counting cards and
// worktrees only. Moving by raw table rows instead would count the group
// headers, so a single wheel notch in a list of short sections skipped past
// whole groups rather than stepping card to card.
func (m *Model) moveCards(n int) {
	if len(m.refs) == 0 {
		return
	}
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	cur := m.table.Cursor()
	if cur < 0 || cur >= len(m.refs) {
		cur = 0
	}
	for ; n > 0; n-- {
		next := -1
		for i := cur + step; i >= 0 && i < len(m.refs); i += step {
			if m.refs[i].kind != rowHeader {
				next = i
				break
			}
		}
		if next < 0 {
			break // already on the first or last card
		}
		cur = next
	}
	m.table.SetCursor(cur)
}

// settleCursor moves the cursor off group-header rows, preferring the given
// direction and falling back to the other.
func (m *Model) settleCursor(preferUp bool) {
	n := len(m.refs)
	if n == 0 {
		return
	}
	cur := m.table.Cursor()
	if cur < 0 || cur >= n {
		cur = 0
		m.table.SetCursor(0)
	}
	if m.refs[cur].kind != rowHeader {
		return
	}
	dirs := []int{1, -1}
	if preferUp {
		dirs = []int{-1, 1}
	}
	for _, d := range dirs {
		for i := cur + d; i >= 0 && i < n; i += d {
			if m.refs[i].kind != rowHeader {
				m.table.SetCursor(i)
				return
			}
		}
	}
}

// selectWorktree moves the table cursor to the row showing this worktree,
// reporting whether a row for it exists yet.
func (m *Model) selectWorktree(path string) bool {
	for i, ref := range m.refs {
		if ref.wt != nil && ref.wt.Path == path {
			m.table.SetCursor(i)
			return true
		}
	}
	return false
}

func (m Model) selectedRef() rowRef {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.refs) {
		return rowRef{}
	}
	return m.refs[i]
}

func (m *Model) clearFilter() {
	m.filterInput.SetValue("")
	m.filterInput.Blur()
	m.filtering = false
	m.refreshRows()
}

// ---- shared actions (reused by keyboard and mouse) ----

// openIssue opens a card: jump into its worktree when one exists; check out
// an existing branch tagged with the card's key (local or remote) into a new
// worktree; otherwise start the create-branch flow.
func (m Model) openIssue(is linear.Issue) (tea.Model, tea.Cmd) {
	if wt := m.worktreeForKey(is.Identifier); wt != nil {
		return m.openWorktree(wt.Path)
	}
	if root, local, remote := m.branchForKey(is.Identifier); local != "" {
		m.screen = scrCreating
		return m, createWorktreeCmd(root, local, "")
	} else if remote != "" {
		// strip the remote name; -b <name> from origin/<name> sets up tracking
		name := remote
		if i := strings.Index(remote, "/"); i > 0 {
			name = remote[i+1:]
		}
		m.screen = scrCreating
		return m, createWorktreeCmd(root, name, remote)
	}
	m.startCreateFlow(is)
	return m, nil
}

func (m *Model) startCreateFlow(is linear.Issue) {
	m.pendKey = is.Identifier
	m.pendTitle = is.Title
	m.typeIdx = m.guessTypeIdx(is.Labels)
	m.screen = scrTypePick
}

func (m Model) confirmType() (tea.Model, tea.Cmd) {
	m.branchInput.SetValue(m.branchPreview(m.cfg.BranchTypes[m.typeIdx]))
	return m.pickRepoThenEdit()
}

// pickRepoThenEdit inserts the repo picker when several repos are
// registered, otherwise goes straight to the branch editor.
func (m Model) pickRepoThenEdit() (tea.Model, tea.Cmd) {
	m.pendRepo = m.repos[0]
	if len(m.repos) > 1 {
		m.repoIdx = 0
		m.screen = scrRepoPick
		return m, nil
	}
	return m.toEditBranch()
}

func (m Model) toEditBranch() (tea.Model, tea.Cmd) {
	m.branchInput.CursorEnd()
	m.branchInput.Focus()
	m.screen = scrEditBranch
	return m, textinput.Blink
}

func (m Model) keyRepoPick(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.screen = scrMain
		return m, nil
	case "up", "k":
		if m.repoIdx > 0 {
			m.repoIdx--
		}
	case "down", "j":
		if m.repoIdx < len(m.repos)-1 {
			m.repoIdx++
		}
	case "enter":
		m.pendRepo = m.repos[m.repoIdx]
		return m.toEditBranch()
	}
	return m, nil
}

func (m Model) submitEdit() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.branchInput.Value())
	if err := branch.ValidateRef(name); err != nil {
		m.err = err
		return m, nil
	}
	m.screen = scrCreating
	return m, createWorktreeCmd(m.pendRepo.path, name, m.pendRepo.base)
}

func (m Model) backFromEdit() (tea.Model, tea.Cmd) {
	if m.pendKey != "" {
		m.screen = scrTypePick
	} else {
		m.screen = scrMain
	}
	return m, nil
}

func (m Model) submitManual() (tea.Model, tea.Cmd) {
	if m.fetchingIssue {
		return m, nil
	}
	v := strings.TrimSpace(m.manualInput.Value())
	if v == "" {
		return m, nil
	}
	if key := branch.ParseIssueKey(v); key != "" {
		m.pendKey = key
		if m.authed {
			m.fetchingIssue = true
			return m, fetchIssueCmd(m.cfg, key)
		}
		m.pendTitle = ""
		m.typeIdx = 0
		m.screen = scrTypePick
		return m, nil
	}
	// free-form branch name: skip the type picker
	m.pendKey = ""
	m.pendTitle = ""
	m.branchInput.SetValue(v)
	return m.pickRepoThenEdit()
}

// ---- workspace-wide issue search ----

func (m Model) openSearch() (tea.Model, tea.Cmd) {
	if !m.authed {
		return m.startAuth()
	}
	m.searchInput.Focus()
	m.screen = scrSearch
	return m, textinput.Blink
}

func (m Model) closeSearch() (tea.Model, tea.Cmd) {
	m.searchInput.Blur()
	m.screen = scrMain
	return m, nil
}

func (m Model) keySearch(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		return m.closeSearch()
	case "up":
		if m.searchSel > 0 {
			m.searchSel--
		}
		return m, nil
	case "down":
		if m.searchSel < len(m.searchResults)-1 {
			m.searchSel++
		}
		return m, nil
	case "enter":
		return m.openSearchResult()
	}
	before := m.searchInput.Value()
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(k)
	if m.searchInput.Value() == before {
		return m, cmd
	}
	m.searchSeq++
	return m, tea.Batch(cmd, searchDebounceCmd(m.searchSeq))
}

// openSearchResult jumps into the selected issue's existing worktree, or
// starts the create-worktree flow for it.
func (m Model) openSearchResult() (tea.Model, tea.Cmd) {
	if m.searchSel < 0 || m.searchSel >= len(m.searchResults) {
		return m, nil
	}
	is := m.searchResults[m.searchSel]
	m.searchInput.Blur()
	return m.openIssue(is)
}

func (m Model) openManual() (tea.Model, tea.Cmd) {
	m.manualInput.SetValue("")
	m.manualInput.Focus()
	m.screen = scrManual
	return m, textinput.Blink
}

func (m Model) openDetail() (tea.Model, tea.Cmd) {
	ref := m.selectedRef()
	if ref.issue == nil {
		return m, nil
	}
	m.detailIssue = ref.issue
	w := m.viewport.Width
	if w <= 0 {
		w = 80
	}
	m.viewport.SetContent(renderIssueDetail(*ref.issue, w))
	m.viewport.GotoTop()
	m.screen = scrDetail
	return m, nil
}

// openWorktree opens a worktree the way the layout allows: in the panel
// layout it selects the worktree and hands focus to the claude pane so the
// card can be worked in place, otherwise it quits so the shell wrapper cd's
// into the directory. "o" always jumps out to the shell regardless.
func (m Model) openWorktree(path string) (tea.Model, tea.Cmd) {
	if !m.threePane() {
		return m.jumpTo(path)
	}
	m.screen = scrMain
	if !m.selectWorktree(path) {
		// freshly created and loadWorktrees is still in flight, so there is no
		// row to move the cursor to. Park the path: claudeDir and syncPanes
		// defer to it, and worktreesMsg selects it once the list arrives.
		m.pendSelect = path
		return m, nil
	}
	sync := m.syncPanes()
	mm, cmd := m.focusPane(paneClaude)
	return mm, tea.Batch(cmd, sync)
}

func (m Model) jumpTo(path string) (tea.Model, tea.Cmd) {
	m.jumpPath = path
	return m, tea.Quit
}

func (m Model) doRemove(deleteBranch bool) (tea.Model, tea.Cmd) {
	if m.removing || m.delTarget == nil {
		return m, nil
	}
	m.removing = true
	m.dropSessions(m.delTarget.Path) // nothing should be left running in a dir about to go
	repo := m.repoFor(m.delTarget.Root)
	return m, removeWorktreeCmd(*m.delTarget, deleteBranch, m.delForce, repo.cleanup, repo.name)
}

// dropSessions ends the claude and shell panes for a directory. Persisted
// sessions are killed rather than detached: a worktree that is going away has
// no work worth keeping alive in it.
func (m Model) dropSessions(dir string) {
	if m.cfg.Persist() {
		// covers sessions this Model never opened — left running by an earlier
		// treeline, and about to be orphaned in a deleted directory
		tmux.KillDir(dir)
	}
	for _, set := range []map[string]*claudeSession{m.terms, m.shells} {
		if s := set[dir]; s != nil {
			if s.tmuxName != "" {
				_ = tmux.Kill(s.tmuxName)
			}
			s.close()
			delete(set, dir)
		}
	}
}

func (m Model) startAuth() (tea.Model, tea.Cmd) {
	if m.cfg.Linear.App().ClientID == "" {
		m.authInputs[0].SetValue(m.cfg.Linear.ClientID)
		m.authInputs[1].SetValue(m.cfg.Linear.ClientSecret)
		m.setAuthFocus(0)
		m.screen = scrAuth
		return m, textinput.Blink
	}
	return m.launchOAuth()
}

func (m *Model) setAuthFocus(i int) {
	m.authFocus = i
	for j := range m.authInputs {
		if j == i {
			m.authInputs[j].Focus()
		} else {
			m.authInputs[j].Blur()
		}
	}
}

func (m Model) launchOAuth() (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.authCancel = cancel
	m.screen = scrAuthWait
	return m, authCmd(ctx, m.cfg.Linear.App())
}

func (m Model) submitAuth() (tea.Model, tea.Cmd) {
	id := strings.TrimSpace(m.authInputs[0].Value())
	secret := strings.TrimSpace(m.authInputs[1].Value())
	if id == "" {
		m.err = errMissingCreds
		return m, nil
	}
	m.cfg.Linear.ClientID = id
	m.cfg.Linear.ClientSecret = secret
	if err := m.cfg.Save(); err != nil {
		m.err = err
		return m, nil
	}
	return m.launchOAuth()
}

func (m Model) startGitHub() (tea.Model, tea.Cmd) {
	if m.ghToken != "" {
		m.loadingCI = true
		return m, m.maybeLoadCI()
	}
	m.deviceCode = nil
	m.screen = scrGitHub
	return m, requestDeviceCodeCmd(m.ghCtx(), m.ghClientID())
}

func (m Model) startDelete() (tea.Model, tea.Cmd) {
	if ref := m.selectedRef(); ref.wt != nil && !ref.wt.IsPrimary {
		m.delTarget = ref.wt
		m.delFocus = 0
		m.delForce, m.delErr = false, nil
		m.screen = scrDeleteConfirm
	}
	return m, nil
}

func (m Model) branchPreview(typ string) string {
	slug := branch.Slugify(m.pendTitle, m.cfg.SlugMaxLen)
	return branch.Name(typ, m.pendKey, slug)
}

func (m Model) guessTypeIdx(labels []string) int {
	bugIdx := -1
	featureIdx := 0
	for i, t := range m.cfg.BranchTypes {
		if t == "bug" {
			bugIdx = i
		}
		if t == "feature" {
			featureIdx = i
		}
	}
	if bugIdx >= 0 {
		for _, l := range labels {
			if strings.Contains(strings.ToLower(l), "bug") {
				return bugIdx
			}
		}
	}
	return featureIdx
}

// ---- keyboard ----

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		if m.screen == scrMain && m.threePane() && (m.pane == paneClaude || m.pane == paneTerm) {
			if s := m.paneSession(m.pane); s != nil && !s.exited.Load() {
				s.pty.Write([]byte{0x03})
				return m, nil
			}
		}
		if m.authCancel != nil {
			m.authCancel()
		}
		if m.ghCancel != nil {
			m.ghCancel()
		}
		return m, tea.Quit
	}
	m.err = nil

	switch m.screen {
	case scrMain:
		return m.keyMain(k)
	case scrDetail:
		return m.keyDetail(k)
	case scrManual:
		return m.keyManual(k)
	case scrTypePick:
		return m.keyTypePick(k)
	case scrEditBranch:
		return m.keyEditBranch(k)
	case scrCreated:
		return m.keyCreated(k)
	case scrDeleteConfirm:
		return m.keyDeleteConfirm(k)
	case scrAuth:
		return m.keyAuth(k)
	case scrSearch:
		return m.keySearch(k)
	case scrRepoPick:
		return m.keyRepoPick(k)
	case scrSettings:
		return m.keySettings(k)
	case scrRepoEdit:
		return m.keyRepoEdit(k)
	case scrAuthWait:
		if k.String() == "esc" {
			if m.authCancel != nil {
				m.authCancel()
				m.authCancel = nil
			}
			m.screen = scrMain
		}
		return m, nil
	case scrGitHub:
		switch k.String() {
		case "esc":
			if m.ghCancel != nil {
				m.ghCancel()
				m.ghCancel = nil
			}
			m.deviceCode = nil
			m.screen = scrMain
		case "o":
			if m.deviceCode != nil {
				_ = openBrowser(m.deviceCode.VerificationURI)
			}
		}
		return m, nil
	case scrCreating:
		return m, nil
	}
	return m, nil
}

func (m Model) keyMain(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.threePane() && !m.filtering {
		if m.pane == paneClaude {
			return m.keyClaude(k) // claude gets everything; ctrl+q leaves
		}
		if m.pane == paneTerm {
			return m.keyShell(k) // the shell gets everything; ctrl+q leaves
		}
		if m.pane == paneDiff && m.gitMode == gitModeCommit && k.String() != "ctrl+q" {
			return m.keyGit(k) // the form owns tab/enter while typing
		}
		switch k.String() {
		case "tab", "ctrl+q":
			return m.focusPane(m.cyclePane(1))
		case "shift+tab":
			return m.focusPane(m.cyclePane(-1))
		}
		if m.pane == paneDiff {
			return m.keyGit(k)
		}
	}
	if m.filtering {
		switch k.String() {
		case "esc":
			m.clearFilter()
			return m, m.syncPanes()
		case "enter":
			m.filterInput.Blur()
			m.filtering = false
			return m, nil
		case "up", "down":
			// let arrow keys move the table while filtering
		default:
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(k)
			m.refreshRows()
			return m, tea.Batch(cmd, m.syncPanes())
		}
	}

	switch k.String() {
	case "q":
		return m, tea.Quit

	case "esc":
		if m.filterInput.Value() != "" {
			m.clearFilter()
			return m, m.syncPanes()
		}
		return m, nil

	case "/":
		m.filtering = true
		m.filterInput.Focus()
		return m, textinput.Blink

	case "r":
		m.loadingWT = true
		cmds := []tea.Cmd{m.loadWorktrees()}
		if m.authed {
			m.loadingIssues = true
			m.linearBusy = true
			cmds = append(cmds, loadIssuesCmd(m.cfg))
		}
		if c := m.reloadGit(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)

	case "n":
		return m.openManual()

	case "a":
		return m.startAuth()

	case "g":
		return m.startGitHub()

	case "s":
		return m.openSearch()

	case ",":
		return m.openSettings()

	case "v":
		return m.openDetail()

	case "d":
		return m.startDelete()

	case "enter":
		ref := m.selectedRef()
		switch {
		case ref.wt != nil && ref.wt.Prunable:
			return m.startDelete() // directory is gone; offer cleanup
		case ref.wt != nil && m.threePane():
			// work on the card in place; "o" jumps out to the shell
			return m.focusPane(paneClaude)
		case ref.wt != nil:
			return m.jumpTo(ref.wt.Path)
		case ref.issue != nil:
			return m.openIssue(*ref.issue)
		}
		return m, nil

	case "o":
		if ref := m.selectedRef(); ref.wt != nil && !ref.wt.Prunable {
			return m.jumpTo(ref.wt.Path)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(k)
	ks := k.String()
	m.settleCursor(ks == "up" || ks == "k" || ks == "pgup" || ks == "home")
	return m, tea.Batch(cmd, m.syncPanes())
}

// ---- three-panel plumbing ----

const (
	paneIssues = 0
	paneClaude = 1
	paneDiff   = 2
	paneTerm   = 3
	paneCount  = 4
)

// threePane reports whether the terminal is wide enough for the panel layout.
func (m Model) threePane() bool { return m.width >= 110 }

// paneEnabled reports whether a pane can hold focus: claude, git and the shell
// all need a worktree to work in, so with none checked out the issues list is
// the only pane there is.
func (m Model) paneEnabled(p int) bool { return p == paneIssues || m.claudeDir() != "" }

// cyclePane is the pane delta steps from the current one, skipping any that
// have no worktree behind them.
func (m Model) cyclePane(delta int) int {
	for i := 1; i <= paneCount; i++ {
		p := ((m.pane+i*delta)%paneCount + paneCount) % paneCount
		if m.paneEnabled(p) {
			return p
		}
	}
	return paneIssues
}

func (m Model) focusPane(p int) (tea.Model, tea.Cmd) {
	if !m.paneEnabled(p) {
		p = paneIssues // nothing checked out to focus on
	}
	m.pane = p
	m.resize() // the issues strip grows/shrinks with focus
	if p == paneClaude {
		return m, m.ensureTerm()
	}
	if p == paneTerm {
		return m, m.ensureShell()
	}
	if p == paneDiff {
		m.gitFreshAt = time.Now()
		return m, tea.Batch(m.reloadGit(), m.loadSelectedFileDiff())
	}
	return m, nil
}

// ensureTerm starts (or reattaches) the claude session for the selected
// worktree, sized to the pane.
func (m *Model) ensureTerm() tea.Cmd {
	dir := m.claudeDir()
	if dir == "" || m.terms[dir] != nil {
		return nil
	}
	cols, rows := m.termSize()
	s, err := startTerm(dir, cols, rows, m.cfg.Persist())
	if err != nil {
		m.err = err
		return nil
	}
	m.terms[dir] = s
	return waitClaudeTerm(s)
}

// termSize is the claude pane's inner grid: half width, bottom panel height.
func (m Model) termSize() (cols, rows int) {
	w := m.width - docStyle.GetHorizontalFrameSize()
	_, bottomH := m.panelHeights()
	return w/2 - 2, bottomH - 4
}

// gitPaneSize is the git pane's inner grid: the right column's width and its
// share of the bottom panel. It mirrors viewPanels' arithmetic so the mouse
// handler measures the same box the renderer drew.
func (m Model) gitPaneSize() (w, h int) {
	full := m.width - docStyle.GetHorizontalFrameSize()
	gitH, _ := m.rightSplit()
	return full - full/2 - 4, gitH - 4
}

// rightSplit divides the right column between the git pane and the shell.
func (m Model) rightSplit() (gitH, termH int) {
	_, bottomH := m.panelHeights()
	gitH = bottomH * 3 / 5
	if gitH < 8 {
		gitH = 8
	}
	termH = bottomH - gitH
	if termH < 6 {
		termH = 6
		gitH = bottomH - termH
	}
	return gitH, termH
}

// shellSize is the shell pane's inner grid.
func (m Model) shellSize() (cols, rows int) {
	w := m.width - docStyle.GetHorizontalFrameSize()
	_, termH := m.rightSplit()
	return w - w/2 - 2, termH - 4
}

// ensureShell starts (or reattaches) the shell for the selected worktree.
func (m *Model) ensureShell() tea.Cmd {
	dir := m.claudeDir()
	if dir == "" || m.shells[dir] != nil {
		return nil
	}
	cols, rows := m.shellSize()
	s, err := startShell(dir, cols, rows, m.cfg.Persist())
	if err != nil {
		m.err = err
		return nil
	}
	m.shells[dir] = s
	return waitClaudeTerm(s)
}

// paneSession maps an embedded-terminal pane to its session for the
// selected directory.
func (m Model) paneSession(pane int) *claudeSession {
	if pane == paneTerm {
		return m.shells[m.claudeDir()]
	}
	return m.terms[m.claudeDir()]
}

func (m Model) keyShell(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		return m.focusPane(m.cyclePane(1))
	}
	dir := m.claudeDir()
	if dir == "" {
		return m.focusPane(paneIssues) // the worktree went away under us
	}
	s := m.shells[dir]
	if s == nil {
		return m, m.ensureShell()
	}
	if s.exited.Load() {
		if k.String() == "enter" { // restart in place
			delete(m.shells, dir)
			return m, m.ensureShell()
		}
		return m, nil
	}
	if b := encodeKey(k); len(b) > 0 {
		s.scrollLive()
		s.clearSel()
		s.pty.Write(b)
	}
	return m, nil
}

// panelHeights splits the vertical space: the issues strip on top is one
// line when unfocused and half the screen when focused; claude and diff
// share what remains below.
func (m Model) panelHeights() (topH, bottomH int) {
	avail := m.height - 8 // doc frame, header + divider, summary, help, spare
	if avail < 12 {
		avail = 12
	}
	topH = 3 // border + summary line + border
	if m.pane == paneIssues {
		topH = avail / 2
		if topH < 8 {
			topH = 8
		}
	}
	bottomH = avail - topH
	if bottomH < 5 {
		bottomH = 5
		topH = avail - bottomH
		if topH < 3 {
			topH = 3
		}
	}
	return topH, bottomH
}

// claudeDir is the directory the claude pane talks in: the selected issue's
// worktree, or "" when the selection has no usable worktree. It deliberately
// does not fall back to the repo root — with no worktree checked out the
// claude, git and shell panes have nothing to show and stay empty.
func (m Model) claudeDir() string {
	if m.pendSelect != "" {
		return m.pendSelect
	}
	if ref := m.selectedRef(); ref.wt != nil && !ref.wt.Prunable {
		return ref.wt.Path
	}
	return ""
}

func (m *Model) setDiffContent() {
	trunc := lipgloss.NewStyle().MaxWidth(m.diffVP.Width)
	var b strings.Builder
	for _, ln := range strings.Split(m.diffRaw, "\n") {
		b.WriteString(trunc.Render(ln) + "\n")
	}
	m.diffVP.SetContent(b.String())
	m.diffVP.GotoTop()
}

// syncPanes points the git and chat panes at the selected worktree.
func (m *Model) syncPanes() tea.Cmd {
	if !m.threePane() || m.pendSelect != "" {
		// nothing to point the panes at yet: syncing now would load the diff
		// and git status of whichever row the cursor is parked on
		return nil
	}
	var cmds []tea.Cmd

	var path string
	if ref := m.selectedRef(); ref.wt != nil && !ref.wt.Prunable {
		path = ref.wt.Path
	}
	if path != m.diffFor {
		m.diffFor = path
		m.diffRaw = ""
		m.setDiffContent()
		if path != "" {
			m.loadingDiff = true
			var root string
			if ref := m.selectedRef(); ref.wt != nil {
				root = ref.wt.Root
			}
			cmds = append(cmds, loadDiffCmd(path, m.repoFor(root).base))
		}
	}

	if dir := m.claudeDir(); dir != m.gitFor {
		m.gitFor = dir
		m.gitMode = gitModeFiles
		m.gitScroll = [2]int{}
		m.gitDiffScroll = 0
		m.gitSel.clear()
		m.gitUnstaged, m.gitStaged, m.gitDiff = nil, nil, ""
		m.gitCol, m.gitSelU, m.gitSelS = 0, 0, 0
		m.hunks, m.commits, m.commitSel = nil, nil, 0
		if dir != "" {
			cmds = append(cmds, loadGitStatusCmd(dir), loadGitLogCmd(dir))
		}
	}
	return tea.Batch(cmds...)
}

func (m Model) keyClaude(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		return m.focusPane(m.cyclePane(1))
	}
	dir := m.claudeDir()
	if dir == "" {
		return m.focusPane(paneIssues) // the worktree went away under us
	}
	s := m.terms[dir]
	if s == nil {
		return m, m.ensureTerm()
	}
	if s.exited.Load() {
		if k.String() == "enter" { // restart in place
			delete(m.terms, dir)
			return m, m.ensureTerm()
		}
		return m, nil
	}
	if b := encodeKey(k); len(b) > 0 {
		s.scrollLive() // typing snaps back to the live screen
		s.clearSel()
		s.pty.Write(b)
	}
	return m, nil
}

func (m Model) keyDetail(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q", "v":
		m.detailIssue = nil
		m.screen = scrMain
		return m, nil
	case "enter":
		if m.detailIssue != nil {
			is := *m.detailIssue
			m.detailIssue = nil
			m.startCreateFlow(is)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(k)
	return m, cmd
}

func (m Model) keyManual(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		if !m.fetchingIssue {
			m.screen = scrMain
		}
		return m, nil
	case "enter":
		return m.submitManual()
	}
	var cmd tea.Cmd
	m.manualInput, cmd = m.manualInput.Update(k)
	return m, cmd
}

func (m Model) keyTypePick(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.screen = scrMain
		return m, nil
	case "up", "k":
		if m.typeIdx > 0 {
			m.typeIdx--
		}
		return m, nil
	case "down", "j":
		if m.typeIdx < len(m.cfg.BranchTypes)-1 {
			m.typeIdx++
		}
		return m, nil
	case "enter":
		return m.confirmType()
	}
	return m, nil
}

func (m Model) keyEditBranch(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		return m.backFromEdit()
	case "enter":
		return m.submitEdit()
	}
	var cmd tea.Cmd
	m.branchInput, cmd = m.branchInput.Update(k)
	return m, cmd
}

func (m Model) keyCreated(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		return m.openWorktree(m.createdPath)
	case "esc", "q":
		m.screen = scrMain
		return m, nil
	}
	return m, nil
}

func (m Model) keyDeleteConfirm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.removing {
		return m, nil
	}
	switch k.String() {
	case "esc", "n":
		m.cancelDelete()
		return m, nil
	case "y":
		return m.doRemove(false)
	case "b":
		return m.doRemove(true)
	case "left", "shift+tab", "h":
		m.delFocus = (m.delFocus + 2) % 3
		return m, nil
	case "right", "tab", "l":
		m.delFocus = (m.delFocus + 1) % 3
		return m, nil
	case "enter":
		switch m.delFocus {
		case 0:
			return m.doRemove(false)
		case 1:
			return m.doRemove(true)
		}
		m.cancelDelete()
		return m, nil
	}
	return m, nil
}

// cancelDelete closes the remove modal, forgetting any force escalation.
func (m *Model) cancelDelete() {
	m.delTarget, m.delForce, m.delErr = nil, false, nil
	m.screen = scrMain
}

func (m Model) keyAuth(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.screen = scrMain
		return m, nil
	case "tab", "shift+tab", "up", "down":
		m.setAuthFocus((m.authFocus + 1) % 2)
		return m, textinput.Blink
	case "enter":
		if m.authFocus == 0 {
			m.setAuthFocus(1)
			return m, textinput.Blink
		}
		return m.submitAuth()
	}
	var cmd tea.Cmd
	m.authInputs[m.authFocus], cmd = m.authInputs[m.authFocus].Update(k)
	return m, cmd
}

// ---- mouse ----

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		up := msg.Button == tea.MouseButtonWheelUp
		switch m.screen {
		case scrMain:
			if m.threePane() && (m.pane == paneClaude || m.pane == paneTerm) {
				if s := m.paneSession(m.pane); s != nil {
					if up {
						s.scrollBy(3)
					} else {
						s.scrollBy(-3)
					}
				}
				return m, nil
			}
			if m.threePane() && m.overGitPane(msg) {
				m.gitSel.clear()
				switch m.gitMode {
				case gitModeFiles:
					m.scrollGitRegion(msg, up)
					return m, nil
				case gitModeLog:
					if up && m.commitSel > 0 {
						m.commitSel--
					} else if !up && m.commitSel < len(m.commits)-1 {
						m.commitSel++
					}
					return m, nil
				}
				var cmd tea.Cmd
				m.diffVP, cmd = m.diffVP.Update(msg)
				return m, cmd
			}
			if up {
				m.moveCards(-1)
			} else {
				m.moveCards(1)
			}
			return m, m.syncPanes()
		case scrDetail:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.MouseButtonLeft, tea.MouseButtonNone:
		// drag selection, like a normal terminal: press anchors, drag
		// extends, release copies to the clipboard. The git pane selects over
		// its rendered text; the claude and shell panes over terminal cells.
		if m.screen == scrMain && m.threePane() {
			if done, cmd := m.selectGitText(msg); done {
				return m, cmd
			}
			s, z := m.terms[m.claudeDir()], m.zones.Get("pane:claude")
			if sh, zt := m.shells[m.claudeDir()], m.zones.Get("pane:term"); sh != nil && (sh.selecting() || (zt != nil && !zt.IsZero() && zt.InBounds(msg))) {
				s, z = sh, zt
			}
			if s != nil {
				inPane := z != nil && !z.IsZero() && z.InBounds(msg)
				switch msg.Action {
				case tea.MouseActionPress:
					if msg.Button == tea.MouseButtonLeft && inPane {
						x, y := z.Pos(msg)
						s.selPress(x-1, y-3) // border col; border+title+rule rows
						return m, nil
					}
					s.clearSel()
				case tea.MouseActionMotion:
					if s.selecting() && z != nil && !z.IsZero() {
						x, y := z.Pos(msg)
						s.selDrag(x-1, y-3)
						return m, nil
					}
				case tea.MouseActionRelease:
					if s.selecting() {
						text, moved := s.selRelease()
						if moved {
							if text != "" {
								if err := copyToClipboard(text); err != nil {
									m.err = err
								} else {
									m.copiedUntil = time.Now().Add(2 * time.Second)
									m.copiedFrom = paneClaude
								}
							}
							return m, nil
						}
						// no movement: fall through and treat as a click
					}
				}
			}
		}
		if msg.Action != tea.MouseActionRelease || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		return m.handleClick(msg)
	}
	return m, nil
}

// overGitPane reports whether a mouse event lands in the git pane. Scrolling
// follows the pointer rather than the focused pane, the way a terminal does.
func (m Model) overGitPane(msg tea.MouseMsg) bool {
	z := m.zones.Get("pane:diff")
	return z != nil && !z.IsZero() && z.InBounds(msg)
}

// gitBodyPos converts a mouse event into a position in the git pane's body:
// column and line past the border, title and rule.
func (m Model) gitBodyPos(msg tea.MouseMsg) (col, line int) {
	z := m.zones.Get("pane:diff")
	if z == nil || z.IsZero() {
		return -1, -1
	}
	x, y := z.Pos(msg)
	return x - 1, y - 3
}

// files-mode regions of the git pane, each scrolling on its own.
const (
	gitRegionUnstaged = iota
	gitRegionStaged
	gitRegionDiff
)

// gitRegionAt tells which region the pointer is over in files mode.
func (m Model) gitRegionAt(msg tea.MouseMsg) int {
	w, h := m.gitPaneSize()
	listH, lw := m.gitFilesLayout(w, h)
	col, line := m.gitBodyPos(msg)
	if line >= listH || line < 0 {
		return gitRegionDiff
	}
	if col <= lw {
		return gitRegionUnstaged
	}
	return gitRegionStaged
}

// scrollGitRegion moves the view under the pointer: one row for the file
// lists, which are short, and three lines for the diff below them.
func (m *Model) scrollGitRegion(msg tea.MouseMsg, up bool) {
	w, h := m.gitPaneSize()
	listH, _ := m.gitFilesLayout(w, h)
	switch r := m.gitRegionAt(msg); r {
	case gitRegionUnstaged, gitRegionStaged:
		off := &m.gitScroll[r]
		if up {
			*off--
		} else {
			*off++
		}
		*off = clampScroll(*off, len(m.gitList(r)), listH-1)
	default:
		if up {
			m.scrollGitDiff(-3)
		} else {
			m.scrollGitDiff(3)
		}
	}
}

// selectGitText runs a drag selection over the git pane's rendered text:
// press anchors, motion extends, release copies. It reports whether it
// consumed the event.
func (m *Model) selectGitText(msg tea.MouseMsg) (bool, tea.Cmd) {
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft || !m.overGitPane(msg) {
			m.gitSel.clear()
			return false, nil
		}
		col, line := m.gitBodyPos(msg)
		m.gitSel.press(col, line)
		return true, nil
	case tea.MouseActionMotion:
		if !m.gitSel.on {
			return false, nil
		}
		col, line := m.gitBodyPos(msg)
		m.gitSel.drag(col, line)
		return true, nil
	case tea.MouseActionRelease:
		if !m.gitSel.on {
			return false, nil
		}
		if !m.gitSel.release() {
			return false, nil // a click, not a selection: let it through
		}
		a, b, ok := m.gitSel.bounds()
		if !ok {
			return true, nil
		}
		w, h := m.gitPaneSize()
		_, body := m.gitPaneContent(w, h)
		if text := selectedText(strings.Split(body, "\n"), a, b); text != "" {
			if err := copyToClipboard(text); err != nil {
				m.err = err
			} else {
				m.copiedUntil = time.Now().Add(2 * time.Second)
				m.copiedFrom = paneDiff
			}
		}
		return true, nil
	}
	return false, nil
}

func (m Model) clicked(msg tea.MouseMsg, id string) bool {
	z := m.zones.Get(id)
	return z != nil && !z.IsZero() && z.InBounds(msg)
}

func (m Model) handleClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.err = nil
	switch m.screen {
	case scrMain:
		switch {
		case m.clicked(msg, "btn:connect"):
			return m.startAuth()
		case m.clicked(msg, "btn:new"):
			return m.openManual()
		case m.clicked(msg, "btn:search"):
			return m.openSearch()
		case m.clicked(msg, "btn:settings"):
			return m.openSettings()
		case m.clicked(msg, "pane:issues"):
			return m.focusPane(paneIssues)
		}
		if m.threePane() && m.gitMode == gitModeCommit {
			switch {
			case m.clicked(msg, "btn:commit"):
				return m, m.submitCommit()
			case m.clicked(msg, "btn:gen"):
				if !m.generating {
					m.generating = true
					return m, generateCommitMsgCmd(m.gitFor)
				}
				return m, nil
			case m.clicked(msg, "btn:commit-cancel"):
				m.closeCommitForm()
				return m, nil
			}
		}
		if m.threePane() && m.gitMode == gitModeFiles {
			for i := range m.gitUnstaged {
				if m.clicked(msg, gitZoneID(false, i)) {
					return m.clickGitFile(false, i)
				}
			}
			for i := range m.gitStaged {
				if m.clicked(msg, gitZoneID(true, i)) {
					return m.clickGitFile(true, i)
				}
			}
		}
		switch {
		case m.clicked(msg, "pane:claude"):
			// a click on a URL inside the terminal opens it
			if s := m.terms[m.claudeDir()]; s != nil {
				z := m.zones.Get("pane:claude")
				x, y := z.Pos(msg)
				// pane chrome: border column, then border+title+rule rows
				if url := s.urlAt(x-1, y-3); url != "" {
					_ = openBrowser(url)
					return m, nil
				}
			}
			return m.focusPane(paneClaude)
		case m.clicked(msg, "pane:diff"):
			return m.focusPane(paneDiff)
		case m.clicked(msg, "pane:term"):
			if s := m.shells[m.claudeDir()]; s != nil {
				z := m.zones.Get("pane:term")
				x, y := z.Pos(msg)
				if url := s.urlAt(x-1, y-3); url != "" {
					_ = openBrowser(url)
					return m, nil
				}
			}
			return m.focusPane(paneTerm)
		}
		return m, nil

	case scrDetail:
		switch {
		case m.clicked(msg, "btn:create"):
			if m.detailIssue != nil {
				is := *m.detailIssue
				m.detailIssue = nil
				m.startCreateFlow(is)
			}
		case m.clicked(msg, "btn:back"):
			m.detailIssue = nil
			m.screen = scrMain
		}
		return m, nil

	case scrRepoPick:
		for i := range m.repos {
			if m.clicked(msg, repoZoneID(i)) {
				m.repoIdx = i
				m.pendRepo = m.repos[i]
				return m.toEditBranch()
			}
		}
		return m, nil

	case scrSearch:
		for i := range m.searchResults {
			if m.clicked(msg, searchZoneID(i)) {
				m.searchSel = i
				return m.openSearchResult()
			}
		}
		if m.clicked(msg, "btn:back") {
			return m.closeSearch()
		}
		return m, nil

	case scrManual:
		switch {
		case m.clicked(msg, "btn:continue"):
			return m.submitManual()
		case m.clicked(msg, "btn:back"):
			if !m.fetchingIssue {
				m.screen = scrMain
			}
		}
		return m, nil

	case scrTypePick:
		for i := range m.cfg.BranchTypes {
			if m.clicked(msg, typeZoneID(i)) {
				m.typeIdx = i
				return m.confirmType()
			}
		}
		if m.clicked(msg, "btn:back") {
			m.screen = scrMain
		}
		return m, nil

	case scrEditBranch:
		switch {
		case m.clicked(msg, "btn:create"):
			return m.submitEdit()
		case m.clicked(msg, "btn:back"):
			return m.backFromEdit()
		}
		return m, nil

	case scrCreated:
		switch {
		case m.clicked(msg, "btn:jump"):
			return m.openWorktree(m.createdPath)
		case m.clicked(msg, "btn:back"):
			m.screen = scrMain
		}
		return m, nil

	case scrDeleteConfirm:
		switch {
		case m.clicked(msg, "btn:remove"):
			return m.doRemove(false)
		case m.clicked(msg, "btn:remove-branch"):
			return m.doRemove(true)
		case m.clicked(msg, "btn:cancel"):
			if !m.removing {
				m.cancelDelete()
			}
		}
		return m, nil

	case scrGitHub:
		if m.clicked(msg, "btn:open") && m.deviceCode != nil {
			_ = openBrowser(m.deviceCode.VerificationURI)
		}
		if m.clicked(msg, "btn:cancel") {
			if m.ghCancel != nil {
				m.ghCancel()
				m.ghCancel = nil
			}
			m.deviceCode = nil
			m.screen = scrMain
		}
		return m, nil

	case scrAuth:
		switch {
		case m.clicked(msg, "auth:0"):
			m.setAuthFocus(0)
		case m.clicked(msg, "auth:1"):
			m.setAuthFocus(1)
		case m.clicked(msg, "btn:connect"):
			return m.submitAuth()
		case m.clicked(msg, "btn:cancel"):
			m.screen = scrMain
		}
		return m, textinput.Blink
	}
	return m, nil
}
