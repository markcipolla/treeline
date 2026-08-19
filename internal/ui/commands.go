package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/branch"
	"github.com/markcipolla/treeline/internal/browser"
	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/github"
	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
)

type worktreesMsg struct {
	wts []gitx.Worktree
	err error
}

type issuesMsg struct {
	viewer linear.Viewer
	issues []linear.Issue
	err    error
}

type issueFetchedMsg struct {
	issue *linear.Issue
	err   error
}

type createdMsg struct {
	path       string
	branchName string
	err        error
}

type removedMsg struct {
	err error
}

type authDoneMsg struct {
	token linear.Token
	err   error
}

type ciMsg struct {
	statuses map[string]github.Status
}

type deviceCodeMsg struct {
	dc  *github.DeviceCode
	err error
}

type ghTokenMsg struct {
	token string
	err   error
}

// tokenMu serializes token refreshes: several tea.Cmds may call Linear
// concurrently and only one of them should refresh and save.
var tokenMu sync.Mutex

// freshToken returns a valid access token, refreshing (and persisting the
// rotated token) when the stored one is expired or about to expire.
func freshToken(cfg *config.Config) (string, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	tok := cfg.Linear.Token()
	if tok.Fresh() {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		return "", errors.New("Linear session expired — press a (or run `treeline auth`) to reconnect")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	newTok, err := linear.Refresh(ctx, cfg.Linear.App(), tok.RefreshToken)
	if err != nil {
		return "", err
	}
	cfg.Linear.SetToken(newTok)
	if err := cfg.Save(); err != nil {
		return "", err
	}
	return newTok.AccessToken, nil
}

type diffMsg struct {
	path string // worktree the diff belongs to
	diff string
	err  error
}

func loadDiffCmd(path, base string) tea.Cmd {
	return func() tea.Msg {
		d, err := gitx.Diff(path, base)
		return diffMsg{path: path, diff: d, err: err}
	}
}

func loadWorktreesCmd(root string) tea.Cmd {
	return func() tea.Msg {
		wts, err := gitx.List(root)
		return worktreesMsg{wts: wts, err: err}
	}
}

func loadIssuesCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		token, err := freshToken(cfg)
		if err != nil {
			return issuesMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		viewer, issues, err := linear.NewClient(token).AssignedIssues(ctx)
		return issuesMsg{viewer: viewer, issues: issues, err: err}
	}
}

type searchMsg struct {
	seq    int // matches the keystroke sequence that fired the search
	issues []linear.Issue
	err    error
}

// searchDebounceMsg fires shortly after a keystroke; the search only runs if
// no newer keystroke has bumped the sequence since.
type searchDebounceMsg struct{ seq int }

func searchDebounceCmd(seq int) tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
		return searchDebounceMsg{seq: seq}
	})
}

func searchIssuesCmd(cfg *config.Config, raw string, seq int) tea.Cmd {
	return func() tea.Msg {
		token, err := freshToken(cfg)
		if err != nil {
			return searchMsg{seq: seq, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		issues, err := linear.NewClient(token).SearchIssues(ctx, linear.ParseSearchQuery(raw))
		return searchMsg{seq: seq, issues: issues, err: err}
	}
}

func fetchIssueCmd(cfg *config.Config, key string) tea.Cmd {
	return func() tea.Msg {
		token, err := freshToken(cfg)
		if err != nil {
			return issueFetchedMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		is, err := linear.NewClient(token).Issue(ctx, key)
		return issueFetchedMsg{issue: is, err: err}
	}
}

func createWorktreeCmd(root, name, base string) tea.Cmd {
	return func() tea.Msg {
		if err := branch.ValidateRef(name); err != nil {
			return createdMsg{err: err}
		}
		dir := filepath.Join(".worktrees", branch.DirFor(name))
		abs := filepath.Join(root, dir)
		if _, err := os.Stat(abs); err == nil {
			return createdMsg{err: errPathExists(dir)}
		}
		var err error
		if gitx.BranchExists(root, name) {
			err = gitx.Add(root, dir, name, "", false)
		} else {
			err = gitx.Add(root, dir, name, base, true)
		}
		if err != nil {
			return createdMsg{err: err}
		}
		return createdMsg{path: abs, branchName: name}
	}
}

type pathExistsError string

func (e pathExistsError) Error() string { return string(e) + " already exists" }

func errPathExists(dir string) error { return pathExistsError(dir) }

func removeWorktreeCmd(root string, wt gitx.Worktree, deleteBranch bool) tea.Cmd {
	return func() tea.Msg {
		if wt.Prunable {
			// the directory is already gone; drop the stale registration
			if err := gitx.Prune(root); err != nil {
				return removedMsg{err: err}
			}
		} else if err := gitx.Remove(root, wt.Path, wt.Dirty); err != nil {
			return removedMsg{err: err}
		}
		if deleteBranch && wt.Branch != "" {
			if err := gitx.DeleteBranch(root, wt.Branch); err != nil {
				return removedMsg{err: err}
			}
		}
		return removedMsg{}
	}
}

func authCmd(ctx context.Context, app linear.OAuthApp) tea.Cmd {
	return func() tea.Msg {
		token, err := linear.Authorize(ctx, app, browser.Open)
		return authDoneMsg{token: token, err: err}
	}
}

// openBrowser is a small alias so UI code doesn't import browser directly.
var openBrowser = browser.Open

func loadCICmd(token, owner, repo string, branches []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return ciMsg{statuses: github.BranchStatuses(ctx, token, owner, repo, branches)}
	}
}

func requestDeviceCodeCmd(ctx context.Context, clientID string) tea.Cmd {
	return func() tea.Msg {
		dc, err := github.RequestDeviceCode(ctx, clientID)
		return deviceCodeMsg{dc: dc, err: err}
	}
}

func pollDeviceTokenCmd(ctx context.Context, clientID string, dc *github.DeviceCode) tea.Cmd {
	return func() tea.Msg {
		token, err := github.PollDeviceToken(ctx, clientID, dc)
		return ghTokenMsg{token: token, err: err}
	}
}
