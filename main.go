package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/markcipolla/treeline/internal/branch"
	"github.com/markcipolla/treeline/internal/browser"
	"github.com/markcipolla/treeline/internal/config"
	"github.com/markcipolla/treeline/internal/github"
	"github.com/markcipolla/treeline/internal/gitx"
	"github.com/markcipolla/treeline/internal/linear"
	"github.com/markcipolla/treeline/internal/tmux"
	"github.com/markcipolla/treeline/internal/ui"
)

// version is stamped by release builds (-ldflags "-X main.version=…");
// plain `go install` builds identify as dev.
var version = "dev"

const helpText = `treeline — git worktree manager with Linear integration

usage:
  treeline              open the TUI (inside a repo, or pick a remembered one)
  treeline auth         connect to Linear via OAuth
  treeline auth github  connect to GitHub for CI status (device flow;
                        not needed if the gh CLI is installed and logged in)
  treeline rm <target>  remove a worktree by branch, issue key, or path
                        (--branch also deletes the branch; --force skips the
                        confirmations and breaks a lock held on the worktree)
  treeline sessions     list the opencode/shell sessions treeline keeps running
                        in the background (kill <name> or kill --all to stop)
  treeline shell-init   print the "tl" shell function (add to your .zshrc)
  treeline version      print version

flags:
  --cd-file <path>      write the selected worktree path to <path> on exit
                        (used by the tl shell function to cd for you)
`

const shellInit = `# treeline shell integration — add to your .zshrc:  eval "$(treeline shell-init)"
# wraps the treeline command so jumping into a worktree cd's your shell
treeline() {
  local f rc
  f="$(mktemp "${TMPDIR:-/tmp}/treeline-cd.XXXXXX")" || return
  command treeline --cd-file "$f" "$@"
  rc=$?
  if [ -s "$f" ]; then
    cd "$(cat "$f")" || rc=1
  fi
  rm -f "$f"
  return $rc
}
tl() { treeline "$@"; }
`

func main() {
	var cdFile string
	var rest []string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "--cd-file" && i+1 < len(args) {
			i++
			cdFile = args[i]
			continue
		}
		rest = append(rest, args[i])
	}

	if len(rest) > 0 {
		switch rest[0] {
		case "auth":
			if len(rest) > 1 && rest[1] == "github" {
				runAuthGitHub()
			} else {
				runAuth()
			}
		case "rm", "remove", "delete":
			runRemove(rest[1:])
		case "sessions", "session", "ls-sessions":
			runSessions(rest[1:])
		case "shell-init":
			fmt.Print(shellInit)
		case "version", "--version", "-v":
			fmt.Println("treeline " + version)
		case "help", "--help", "-h":
			fmt.Print(helpText)
		default:
			fmt.Fprintf(os.Stderr, "treeline: unknown command %q\n\n%s", rest[0], helpText)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fatal(err.Error())
	}

	root, err := gitx.RepoRoot(".")
	if err != nil {
		root = pickRepo(cfg)
	}
	if !gitx.HasCommits(root) {
		fatal("this repository has no commits yet — make an initial commit first")
	}
	rememberRepo(cfg, root)
	if err := gitx.EnsureExcluded(root); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .git/info/exclude: %v\n", err)
	}

	p := tea.NewProgram(ui.New(cfg, root), tea.WithAltScreen(), tea.WithMouseCellMotion())
	final, err := p.Run()
	if err != nil {
		fatal(err.Error())
	}

	if m, ok := final.(ui.Model); ok {
		m.Close() // detach persisted sessions, stop the rest
		if path := m.JumpPath(); path != "" {
			if cdFile != "" {
				if err := os.WriteFile(cdFile, []byte(path), 0o600); err != nil {
					fatal(err.Error())
				}
			} else {
				fmt.Println(path)
				fmt.Fprintln(os.Stderr, `tip: eval "$(treeline shell-init)" in your .zshrc, then use "tl" to cd automatically`)
			}
		}
	}
}

func runAuth() {
	cfg, err := config.Load()
	if err != nil {
		fatal(err.Error())
	}
	// Prompt for credentials only when no client ID is available — neither
	// baked into the binary nor already in the config file.
	if cfg.Linear.App().ClientID == "" {
		in := bufio.NewReader(os.Stdin)
		fmt.Println("treeline needs a Linear OAuth application.")
		fmt.Println("Create one at linear.app → Settings → API → OAuth applications,")
		fmt.Println("with callback URL: " + linear.RedirectURI)
		fmt.Println()
		fmt.Print("Client ID: ")
		cfg.Linear.ClientID = readLine(in)
		if cfg.Linear.ClientID == "" {
			fatal("a client ID is required")
		}
		fmt.Print("Client secret (leave empty for public/PKCE apps): ")
		cfg.Linear.ClientSecret = readLine(in)
		if err := cfg.Save(); err != nil {
			fatal(err.Error())
		}
	} else if cfg.Linear.ClientID != "" {
		fmt.Println("Using the client ID from " + config.PathHint() + ".")
	}

	fmt.Println("Opening your browser to authorize…")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	token, err := linear.Authorize(ctx, cfg.Linear.App(), browser.Open)
	if err != nil {
		fatal(err.Error())
	}
	cfg.Linear.SetToken(token)
	if err := cfg.Save(); err != nil {
		fatal(err.Error())
	}

	name, err := linear.NewClient(token.AccessToken).ViewerName(ctx)
	if err != nil {
		fmt.Println("✓ Token saved (could not verify: " + err.Error() + ")")
		return
	}
	fmt.Println("✓ Connected to Linear as " + name)
}

func runAuthGitHub() {
	cfg, err := config.Load()
	if err != nil {
		fatal(err.Error())
	}
	if tok := github.Token(cfg.GitHub.Token); tok != "" && cfg.GitHub.Token == "" {
		fmt.Println("The gh CLI is already logged in — treeline uses its token automatically.")
		return
	}
	clientID := cfg.GitHub.ClientID
	if clientID == "" {
		clientID = github.DefaultClientID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	dc, err := github.RequestDeviceCode(ctx, clientID)
	if err != nil {
		fatal(err.Error())
	}
	fmt.Println("Enter this code at " + dc.VerificationURI)
	fmt.Println()
	fmt.Println("    " + dc.UserCode)
	fmt.Println()
	_ = browser.Open(dc.VerificationURI)
	fmt.Println("Waiting for approval…")
	token, err := github.PollDeviceToken(ctx, clientID, dc)
	if err != nil {
		fatal(err.Error())
	}
	cfg.GitHub.Token = token
	if err := cfg.Save(); err != nil {
		fatal(err.Error())
	}
	fmt.Println("✓ GitHub connected — CI status will show in the table.")
}

// runRemove deletes a worktree from the CLI: treeline rm <target>.
func runRemove(args []string) {
	var delBranch, force bool
	var target string
	for _, a := range args {
		switch a {
		case "--branch", "-b":
			delBranch = true
		case "--force", "-f":
			force = true
		default:
			if target != "" {
				fatal("rm takes a single target")
			}
			target = a
		}
	}
	if target == "" {
		fatal("usage: treeline rm <branch | issue-key | path> [--branch] [--force]")
	}
	root, err := gitx.RepoRoot(".")
	if err != nil {
		fatal("treeline rm must be run inside the repository")
	}
	wts, err := gitx.List(root)
	if err != nil {
		fatal(err.Error())
	}

	var wt *gitx.Worktree
	for i := range wts {
		w := &wts[i]
		if w.Branch == target || w.Path == target || filepath.Base(w.Path) == target {
			wt = w
			break
		}
		for _, seg := range strings.Split(w.Branch, "/") {
			if k := branch.ParseIssueKey(seg); k != "" && strings.EqualFold(k, target) {
				wt = w
				break
			}
		}
		if wt != nil {
			break
		}
	}
	if wt == nil {
		fatal("no worktree matches " + target + " — see them in the treeline TUI or `git worktree list`")
	}
	if wt.IsPrimary {
		fatal("refusing to remove the primary checkout at " + wt.Path)
	}
	tmux.KillDir(wt.Path) // don't leave a background agent in a deleted worktree

	switch {
	case wt.Prunable:
		// directory is already gone; drop the stale registration
		if err := gitx.Prune(root); err != nil {
			fatal(err.Error())
		}
	default:
		if wt.Dirty && !force {
			fmt.Printf("%s has uncommitted changes — remove and discard them? [y/N] ", wt.Branch)
			if !confirm() {
				fatal("aborted")
			}
		}
		removeAskingAboutLock(root, *wt, force)
	}
	fmt.Println("✓ removed worktree " + wt.Path)

	if delBranch && wt.Branch != "" {
		if err := gitx.DeleteBranch(root, wt.Branch); err != nil {
			fatal(err.Error())
		}
		fmt.Println("✓ deleted branch " + wt.Branch)
	}
}

// runSessions lists the sessions treeline keeps alive on its own tmux server
// after it quits — a detached opencode is otherwise invisible — and stops them
// on request.
func runSessions(args []string) {
	if !tmux.Available() {
		fatal("tmux is not installed, so sessions are not persisted — install tmux to keep opencode running between launches")
	}
	if len(args) > 0 {
		if args[0] != "kill" && args[0] != "rm" {
			fatal("usage: treeline sessions [kill <name> | kill --all]")
		}
		killSession(args[1:])
		return
	}
	sessions, err := tmux.List()
	if err != nil {
		fatal(err.Error())
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions running — treeline starts them when you open the opencode or shell pane")
		return
	}

	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	for _, s := range sessions {
		state := "detached"
		if s.Attached {
			state = "attached"
		}
		fmt.Printf("  %-34s  %-8s  %6s  %s\n", s.Name, state, age(s.Created), s.Dir)
	}
	fmt.Println()
	fmt.Println("attach outside treeline:  tmux -L " + tmux.Socket + " attach -t <name>")
	fmt.Println("stop one:                 treeline sessions kill <name>")
}

// killSession ends one session by name (any unambiguous fragment will do) or
// every one of them.
func killSession(args []string) {
	if len(args) != 1 {
		fatal("usage: treeline sessions kill <name> | treeline sessions kill --all")
	}
	if args[0] == "--all" || args[0] == "-a" {
		if err := tmux.KillAll(); err != nil {
			fatal(err.Error())
		}
		fmt.Println("✓ stopped every session")
		return
	}
	sessions, err := tmux.List()
	if err != nil {
		fatal(err.Error())
	}
	var matches []string
	for _, s := range sessions {
		if s.Name == args[0] {
			matches = []string{s.Name}
			break
		}
		if strings.Contains(s.Name, args[0]) {
			matches = append(matches, s.Name)
		}
	}
	switch len(matches) {
	case 0:
		fatal("no session matches " + args[0] + " — see them with `treeline sessions`")
	case 1:
	default:
		fatal("several sessions match " + args[0] + ": " + strings.Join(matches, ", "))
	}
	if err := tmux.Kill(matches[0]); err != nil {
		fatal(err.Error())
	}
	fmt.Println("✓ stopped " + matches[0])
}

// age renders how long a session has been up, at one unit of precision.
func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}

// rememberRepo records the primary checkout in the config so treeline can be
// launched from anywhere later. Worktrees under .worktrees are not primaries.
func rememberRepo(cfg *config.Config, root string) {
	if strings.Contains(root, string(filepath.Separator)+".worktrees"+string(filepath.Separator)) {
		return
	}
	name := filepath.Base(root)
	entry := cfg.Repos[name]
	if entry.Path == root {
		return
	}
	if cfg.Repos == nil {
		cfg.Repos = map[string]config.RepoConfig{}
	}
	entry.Path = root // keep any setup/cleanup scripts the entry carries
	cfg.Repos[name] = entry
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save config: %v\n", err)
	}
}

// pickRepo is used when treeline is launched outside a git repository: offer
// the remembered primary checkouts.
func pickRepo(cfg *config.Config) string {
	names := make([]string, 0, len(cfg.Repos))
	for n, rc := range cfg.Repos {
		if _, err := gitx.RepoRoot(rc.Path); err == nil {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		fatal("not inside a git repository — run treeline from inside a repo once so it can remember it")
	}
	if len(names) == 1 {
		fmt.Println("Using " + names[0] + " (" + cfg.Repos[names[0]].Path + ")")
		return cfg.Repos[names[0]].Path
	}
	fmt.Println("Not inside a git repository. Pick one:")
	for i, n := range names {
		fmt.Printf("  %d) %-16s %s\n", i+1, n, cfg.Repos[n].Path)
	}
	fmt.Printf("Choose [1-%d]: ", len(names))
	in := bufio.NewReader(os.Stdin)
	choice, err := strconv.Atoi(readLine(in))
	if err != nil || choice < 1 || choice > len(names) {
		fatal("no repository chosen")
	}
	return cfg.Repos[names[choice-1]].Path
}

// removeAskingAboutLock removes a worktree, asking first when a lock has to be
// broken — git needs a second --force for that and refuses outright without
// one, and whatever holds the lock (a agent session working in there, say)
// loses the directory under it.
func removeAskingAboutLock(root string, wt gitx.Worktree, force bool) {
	unlock := force
	if wt.Locked && !force {
		fmt.Printf("%s is locked by %s\n", wt.Branch, lockLabel(wt.LockReason))
		fmt.Print("force remove anyway? [y/N] ")
		if !confirm() {
			fatal("aborted")
		}
		unlock = true
	}
	err := gitx.Remove(root, wt.Path, wt.Dirty, unlock)
	if errors.Is(err, gitx.ErrLocked) && !unlock {
		// the lock was taken after the worktree list was read
		fmt.Println(err.Error())
		fmt.Print("force remove anyway? [y/N] ")
		if !confirm() {
			fatal("aborted")
		}
		err = gitx.Remove(root, wt.Path, wt.Dirty, true)
	}
	if err != nil {
		fatal(err.Error())
	}
}

// confirm reads a yes/no answer, defaulting to no.
func confirm() bool {
	switch readLine(bufio.NewReader(os.Stdin)) {
	case "y", "Y", "yes":
		return true
	}
	return false
}

// lockLabel describes a lock, standing in for a missing reason.
func lockLabel(reason string) string {
	if reason == "" {
		return "another process (no reason given)"
	}
	return reason
}

func readLine(in *bufio.Reader) string {
	s, _ := in.ReadString('\n')
	return strings.TrimSpace(s)
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "treeline: "+msg)
	os.Exit(1)
}
