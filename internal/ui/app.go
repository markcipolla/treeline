package ui

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

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
)

type Model struct {
	cfg  *config.Config
	root string
	base string // ref new branches start from

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

	// three-panel main screen (wide terminals): 0 issues, 1 claude, 2 diff
	pane  int
	terms map[string]*claudeSession // interactive claude per directory

	diffVP      viewport.Model
	diffRaw     string
	diffFor     string // worktree path the branch diff shows
	loadingDiff bool

	// git pane: file picker, hunk staging, commit log
	gitFor      string // directory the pane operates on
	gitMode     int
	gitUnstaged []gitx.FileStatus
	gitStaged   []gitx.FileStatus
	gitCol      int // active column: 0 unstaged, 1 staged
	gitSelU     int // per-column selections
	gitSelS     int
	gitDiff     string // colored preview for the selected file
	hunkPath    string
	hunkStaged  bool
	hunkHeader  string
	hunks       []string
	hunkSel     int
	commits     []gitx.Commit
	commitSel   int

	// commit form
	commitSubject textinput.Model
	commitBody    textarea.Model
	commitFocus   int // 0 subject, 1 body
	generating    bool

	// delete confirm
	delTarget *gitx.Worktree
	delFocus  int // focused button: 0 remove, 1 remove+branch, 2 cancel
	removing  bool

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
	err           error
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

	commitSubject := newInput("summary of the change…")
	commitBody := textarea.New()
	commitBody.Placeholder = "longer description (optional)…"
	commitBody.ShowLineNumbers = false
	commitBody.CharLimit = 0

	ghOwner, ghRepo, ghOK := github.RepoFromRemote(root)

	return Model{
		cfg:           cfg,
		root:          root,
		base:          gitx.DefaultBase(root),
		zones:         zone.New(),
		table:         t,
		ci:            map[string]github.Status{},
		ghToken:       github.Token(cfg.GitHub.Token),
		ghOwner:       ghOwner,
		ghRepo:        ghRepo,
		ghOK:          ghOK,
		filterInput:   filterInput,
		searchInput:   searchInput,
		commitSubject: commitSubject,
		commitBody:    commitBody,
		terms:         map[string]*claudeSession{},
		diffVP:        viewport.New(0, 0),
		viewport:      viewport.New(0, 0),
		help:          help.New(),
		spinner:       spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(okStyle)),
		branchInput:   newInput(""),
		manualInput:   newInput("LMAP-142 or a/full/branch-name"),
		authInputs:    authInputs,
		loadingWT:     true,
		loadingIssues: cfg.Linear.Token().Usable(),
		authed:        cfg.Linear.Token().Usable(),
	}
}

// JumpPath is the worktree path the user chose to jump into, if any.
func (m Model) JumpPath() string { return m.jumpPath }

// Close shuts down any claude sessions the panes started.
func (m Model) Close() {
	for _, s := range m.terms {
		s.close()
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, loadWorktreesCmd(m.root)}
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
		return m, tea.Batch(m.maybeLoadCI(), m.syncPanes())

	case issuesMsg:
		m.loadingIssues = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.viewer = msg.viewer
		m.issues = msg.issues
		m.refreshRows()
		return m, nil

	case ciMsg:
		m.loadingCI = false
		for b, s := range msg.statuses {
			m.ci[b] = s
		}
		m.refreshRows()
		return m, nil

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
		if msg.err != nil {
			m.gitDiff = errStyle.Render("✗ " + msg.err.Error())
		} else {
			m.gitDiff = msg.diff
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
		return m, tea.Batch(m.reloadGit(), loadWorktreesCmd(m.root))

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
		s := m.terms[msg.dir]
		if s == nil {
			return m, nil
		}
		return m, waitClaudeTerm(s) // re-arm; the view reads the vt directly

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
		return m, loadWorktreesCmd(m.root)

	case removedMsg:
		m.removing = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.delTarget = nil
		m.screen = scrMain
		m.loadingWT = true
		return m, loadWorktreesCmd(m.root)

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
		if wt.Branch != "" {
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
		m.setTableLayout(w-2, topH-4) // borders + pane title + title rule
		lw := w / 2
		rw := w - lw
		inner := bottomH - 4 // borders + pane title + title rule
		for _, t := range m.terms {
			t.resize(lw-2, inner)
		}
		m.diffVP.Width = rw - 2
		m.diffVP.Height = inner
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
	m.branchInput.Width = inputW
	m.manualInput.Width = inputW
	for i := range m.authInputs {
		m.authInputs[i].Width = inputW
	}
}

// setTableLayout sizes the issue table's columns for the given width.
func (m *Model) setTableLayout(w, h int) {
	pad := 3 // each cell: 1-char divider + 1 padding on both sides
	w--      // renderTable appends a right edge to every row
	keyW, priW, gitW, ciW := 10, 8, 10, 3
	wtW := w * 22 / 100
	if wtW < 14 {
		wtW = 14
	}
	titleW := w - keyW - priW - gitW - ciW - wtW - 6*pad
	if titleW < 12 {
		// give TITLE its floor back out of WORKTREE so the frame still fits
		wtW -= 12 - titleW
		titleW = 12
		if wtW < 8 {
			wtW = 8
		}
	}
	m.table.SetColumns([]table.Column{
		{Title: "KEY", Width: keyW},
		{Title: "TITLE", Width: titleW},
		{Title: "PRIORITY", Width: priW},
		{Title: "WORKTREE", Width: wtW},
		{Title: "GIT", Width: gitW},
		{Title: "CI", Width: ciW},
	})
	m.table.SetWidth(w)
	// renderTable adds a top and bottom frame line around the widget
	m.table.SetHeight(h - 2)
}

// refreshRows rebuilds the table: issues grouped by status (active work
// first), each with its worktree when one exists, then remaining worktrees.
func (m *Model) refreshRows() {
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))

	// link worktrees to issues via the issue key in the branch name
	issueKeys := make(map[string]bool, len(m.issues))
	for _, is := range m.issues {
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
	for i := range m.issues {
		is := &m.issues[i]
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

	var rows []table.Row
	var refs []rowRef
	header := func(title string) {
		// plain text: styled cells break the table's width-based truncation
		rows = append(rows, table.Row{"", "▸ " + title, "", "", "", ""})
		refs = append(refs, rowRef{kind: rowHeader})
	}

	for _, g := range groups {
		header(strings.ToUpper(g.name))
		for _, is := range g.issues {
			wt := linked[is.Identifier]
			wtCell, gitCell, ciCell := "", "", ""
			if wt != nil {
				wtCell = relPath(m.root, wt.Path)
				gitCell = wtStatus(*wt)
				ciCell = ciSymbol(m.ci[wt.Branch])
			}
			rows = append(rows, table.Row{is.Identifier, is.Title, linear.PriorityName(is.Priority), wtCell, gitCell, ciCell})
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
		wtRows = append(wtRows, table.Row{"", name, "", relPath(m.root, wt.Path), wtStatus(*wt), ciSymbol(m.ci[wt.Branch])})
		wtRefs = append(wtRefs, rowRef{kind: rowWorktree, wt: wt})
	}
	if len(wtRows) > 0 {
		header("WORKTREES")
		rows = append(rows, wtRows...)
		refs = append(refs, wtRefs...)
	}

	cur := m.table.Cursor()
	m.refs = refs
	m.table.SetRows(rows)
	if cur >= len(rows) {
		cur = len(rows) - 1
	}
	if cur < 0 {
		cur = 0
	}
	m.table.SetCursor(cur)
	m.settleCursor(false)
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

// selectWorktree moves the table cursor to the row showing this worktree.
func (m *Model) selectWorktree(path string) {
	for i, ref := range m.refs {
		if ref.wt != nil && ref.wt.Path == path {
			m.table.SetCursor(i)
			return
		}
	}
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
		if m.threePane() {
			m.screen = scrMain
			m.selectWorktree(wt.Path)
			sync := m.syncPanes()
			mm, cmd := m.focusPane(paneClaude)
			return mm, tea.Batch(cmd, sync)
		}
		return m.jumpTo(wt.Path)
	}
	if local, remote := m.branchForKey(is.Identifier); local != "" {
		m.screen = scrCreating
		return m, createWorktreeCmd(m.root, local, "")
	} else if remote != "" {
		// strip the remote name; -b <name> from origin/<name> sets up tracking
		name := remote
		if i := strings.Index(remote, "/"); i > 0 {
			name = remote[i+1:]
		}
		m.screen = scrCreating
		return m, createWorktreeCmd(m.root, name, remote)
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
	m.branchInput.CursorEnd()
	m.branchInput.Focus()
	m.screen = scrEditBranch
	return m, textinput.Blink
}

func (m Model) submitEdit() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.branchInput.Value())
	if err := branch.ValidateRef(name); err != nil {
		m.err = err
		return m, nil
	}
	m.screen = scrCreating
	return m, createWorktreeCmd(m.root, name, m.base)
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
	m.branchInput.CursorEnd()
	m.branchInput.Focus()
	m.screen = scrEditBranch
	return m, textinput.Blink
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

func (m Model) jumpTo(path string) (tea.Model, tea.Cmd) {
	m.jumpPath = path
	return m, tea.Quit
}

func (m Model) doRemove(deleteBranch bool) (tea.Model, tea.Cmd) {
	if m.removing || m.delTarget == nil {
		return m, nil
	}
	m.removing = true
	return m, removeWorktreeCmd(m.root, *m.delTarget, deleteBranch)
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

func (m Model) repoName() string {
	return filepath.Base(m.root)
}

// ---- keyboard ----

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		if m.screen == scrMain && m.threePane() && m.pane == paneClaude {
			if s := m.terms[m.claudeDir()]; s != nil && !s.exited.Load() {
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
		if m.pane == paneDiff && m.gitMode == gitModeCommit && k.String() != "ctrl+q" {
			return m.keyGit(k) // the form owns tab/enter while typing
		}
		switch k.String() {
		case "tab", "ctrl+q":
			return m.focusPane((m.pane + 1) % 3)
		case "shift+tab":
			return m.focusPane((m.pane + 2) % 3)
		}
		if m.pane == paneDiff {
			return m.keyGit(k)
		}
	}
	if m.filtering {
		switch k.String() {
		case "esc":
			m.clearFilter()
			return m, nil
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
			return m, cmd
		}
	}

	switch k.String() {
	case "q":
		return m, tea.Quit

	case "esc":
		if m.filterInput.Value() != "" {
			m.clearFilter()
		}
		return m, nil

	case "/":
		m.filtering = true
		m.filterInput.Focus()
		return m, textinput.Blink

	case "r":
		m.loadingWT = true
		cmds := []tea.Cmd{loadWorktreesCmd(m.root)}
		if m.authed {
			m.loadingIssues = true
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
)

// threePane reports whether the terminal is wide enough for the panel layout.
func (m Model) threePane() bool { return m.width >= 110 }

func (m Model) focusPane(p int) (tea.Model, tea.Cmd) {
	m.pane = p
	m.resize() // the issues strip grows/shrinks with focus
	if p == paneClaude {
		return m, m.ensureTerm()
	}
	return m, nil
}

// ensureTerm starts (or reattaches) the claude session for the selected
// worktree, sized to the pane.
func (m *Model) ensureTerm() tea.Cmd {
	dir := m.claudeDir()
	if m.terms[dir] != nil {
		return nil
	}
	cols, rows := m.termSize()
	s, err := startTerm(dir, cols, rows)
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

// panelHeights splits the vertical space: the issues strip on top is one
// line when unfocused and half the screen when focused; claude and diff
// share what remains below.
func (m Model) panelHeights() (topH, bottomH int) {
	avail := m.height - 9 // doc frame, header + divider, summary, help
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
// worktree, or the repo root when nothing is checked out.
func (m Model) claudeDir() string {
	if ref := m.selectedRef(); ref.wt != nil && !ref.wt.Prunable {
		return ref.wt.Path
	}
	return m.root
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
	if !m.threePane() {
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
			cmds = append(cmds, loadDiffCmd(path, m.base))
		}
	}

	if dir := m.claudeDir(); dir != m.gitFor {
		m.gitFor = dir
		m.gitMode = gitModeFiles
		m.gitUnstaged, m.gitStaged, m.gitDiff = nil, nil, ""
		m.gitCol, m.gitSelU, m.gitSelS = 0, 0, 0
		m.hunks, m.commits, m.commitSel = nil, nil, 0
		cmds = append(cmds, loadGitStatusCmd(dir), loadGitLogCmd(dir))
	}
	return tea.Batch(cmds...)
}

func (m Model) keyClaude(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		return m.focusPane((m.pane + 1) % 3)
	}
	dir := m.claudeDir()
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
		return m.jumpTo(m.createdPath)
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
		m.delTarget = nil
		m.screen = scrMain
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
		m.delTarget = nil
		m.screen = scrMain
		return m, nil
	}
	return m, nil
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
			if m.threePane() && m.pane == paneClaude {
				if s := m.terms[m.claudeDir()]; s != nil {
					if up {
						s.scrollBy(3)
					} else {
						s.scrollBy(-3)
					}
				}
				return m, nil
			}
			if m.threePane() && m.pane == paneDiff {
				switch m.gitMode {
				case gitModeFiles:
					d := 1
					if up {
						d = -1
					}
					return m, m.moveGitSel(d)
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
				m.table.MoveUp(3)
			} else {
				m.table.MoveDown(3)
			}
			m.settleCursor(up)
			return m, m.syncPanes()
		case scrDetail:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionRelease {
			return m, nil
		}
		return m.handleClick(msg)
	}
	return m, nil
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
			return m.focusPane(paneClaude)
		case m.clicked(msg, "pane:diff"):
			return m.focusPane(paneDiff)
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
			return m.jumpTo(m.createdPath)
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
				m.delTarget = nil
				m.screen = scrMain
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
