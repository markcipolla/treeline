# 🌲 treeline

A git worktree manager TUI with Linear integration. Pick one of your Linear
issues and treeline creates a worktree on a branch named the way you want:

```
[feature|bugfix|hotfix|chore]/LMAP-142/slug-of-the-issue-title
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles) (table, viewport,
textinput, spinner, help), [Lip Gloss](https://github.com/charmbracelet/lipgloss),
and [BubbleZone](https://github.com/lrstanley/bubblezone) for mouse support.

## Install

With Homebrew:

```sh
brew install markcipolla/tap/treeline
```

Or with Go:

```sh
go install github.com/markcipolla/treeline@latest
```

or from a checkout: `go build -o treeline . && mv treeline ~/bin/` (anywhere
on your PATH). Go 1.24+ (the repo pins it via [mise](https://mise.jdx.dev) in
`mise.toml`).

## Shell integration (recommended)

The TUI can't change your shell's directory itself. Add this to `.zshrc`:

```sh
eval "$(treeline shell-init)"
```

then run `tl` instead of `treeline` — selecting a worktree with `enter`
drops you into it.

## Connect Linear

Treeline uses OAuth with PKCE. Create an OAuth application once at
[linear.app/settings/api/applications/new](https://linear.app/settings/api/applications/new)
with callback URL `http://localhost:9482/callback`, then either run:

```sh
treeline auth
```

or press `a` on the Issues tab inside the TUI. If your Linear app is a
public client the secret can be left empty (PKCE handles it).

Tokens are stored in `~/.config/treeline/config.json` (mode 0600). Linear
access tokens expire after 24 hours; treeline refreshes them automatically
using the refresh token.

## GitHub CI status

The table has a `CI` column (✓ passed, ✗ failed, ● running) for every
branch that has check runs on GitHub. It needs a token:

- If the [gh CLI](https://cli.github.com) is installed and logged in,
  treeline uses its token automatically — nothing to do.
- Otherwise press `g` in the TUI or run `treeline auth github` — OAuth
  device flow: enter a short code at github.com/login/device, no callback
  or secret involved.

## Usage

Run `treeline` (or `tl`) inside any git repository. One table shows your
Linear issues grouped by status (active work first), with each issue's
worktree, git state, and CI status inline; worktrees that don't belong to
an issue are grouped at the bottom.

| Key | On | Action |
| --- | --- | --- |
| `enter` | row with worktree | jump into worktree (quit + cd with `tl`) |
| `enter` | issue without worktree | create worktree for it |
| `v` | issue row | issue details (scrollable) |
| `n` | — | manual entry: issue key or free-form branch |
| `d` | row with worktree | remove worktree (`y` keep branch, `b` delete branch too; locked worktrees ask again, see [below](#locked-worktrees)) |
| `/` | — | filter the table |
| `r` | — | refresh issues, worktrees, and CI |
| `a` | — | connect / reconnect Linear |
| `g` | — | connect GitHub (CI) |
| `q` / `ctrl+c` | — | quit |

### Locked worktrees

A worktree can be locked — claude locks the one it is working in, recording
a reason like `claude session encapsulated-shimmying-sparrow (pid 65825)`.
Git then refuses to remove it, and needs a second `--force` to be talked out
of it. The remove modal names the lock before you commit to anything, and if
git refuses anyway it asks a second time with the buttons relabelled
`force remove` / `force remove + delete branch`, defaulting to cancel.
Forcing pulls the directory out from under whatever holds the lock, so a
claude session still working in there loses its files.

`treeline rm` does the same from the shell: it prints the lock reason and
asks, and `--force` answers yes to both that and the uncommitted-changes
prompt.

The mouse works too: click buttons and branch-type options; scroll the
table and detail view with the wheel.

`ctrl+q` cycles the panes: issues, claude, git, shell. The claude and shell
panes run in the selected worktree and survive quitting — see
[Background sessions](#background-sessions).

### Layouts

How the panes are arranged follows the width of the terminal:

| Width   | Layout                                                            |
| ------- | ----------------------------------------------------------------- |
| < 110   | the issues table on its own                                       |
| 110–179 | issues as a strip on top, claude beside the git pane over a shell |
| 180–239 | three columns: issues, claude, git over the shell                 |
| ≥ 240   | four columns: the shell gets one of its own                       |

In the stacked layout the issues strip collapses to a single line when
another pane has focus and grows back to half the screen when you `ctrl+q`
into it. As a column it stays full height, so the cards are always in view,
and it widens when you focus it — far enough to show every column of the
grid, never so far that the work panes stop being usable. A narrow issues
column drops the cells it can do without — ASSIGNEE, then REPO, then
WORKTREE and PRIORITY — to keep titles readable.

Every panel draws its own border and they sit flush, so the seam between two
of them is two lines thick. Sharing one line reads tighter, but then a rule
inside one panel — the issues grid's dividers — has to T-join the border of
the panel beside it, and a focused panel's highlight runs into its
neighbour's edge.

### The git pane

Files mode puts the unstaged and staged lists side by side with the selected
file's diff underneath. Each of the three scrolls on its own: the wheel
moves whichever one the pointer is over, so reading down a long diff doesn't
drag the file lists with it. Without a mouse, `pgup`/`pgdn` (or
`shift+↑`/`shift+↓`) scroll the preview while `↑`/`↓` keep moving between
files.

| Key | Action |
| --- | --- |
| `↑` `↓` | move between files (the list follows the cursor) |
| `pgup` `pgdn` | scroll the diff preview, leaving the cursor put |
| `tab` `←` `→` | switch between unstaged and staged |
| `space` | stage / unstage the file |
| `enter` | hunk-by-hunk staging |
| `l` / `b` | commit log / branch diff vs the base |
| `c` | commit form (`ctrl+g` drafts a message with claude) |

`l` opens the commit log, which is laid out the same way: the commits on top
and the selected one's message and patch — diffstat first — underneath. The
wheel scrolls whichever of the two the pointer is over, `↑`/`↓` walk the
commits, `pgup`/`pgdn` (or `shift+↑`/`shift+↓`) scroll the patch, `r`
reloads and `b` switches to the whole branch diff. A merge is shown against
its first parent.

Drag with the mouse to select text anywhere in the pane — diff lines, file
names, log entries — and releasing copies it to the clipboard, the same as
in the claude and shell panes. A plain click still selects a file, or a
commit in the log.

### Where worktrees live

Inside the repo under `.worktrees/` (added to `.git/info/exclude`
automatically, so nothing shows up in `git status`):

```
myrepo/
├── .worktrees/
│   ├── LMAP-142-fix-login-redirect/   ← branch feature/LMAP-142/fix-login-redirect
│   └── LMAP-150-add-billing/
└── src/
```

New branches start from `origin`'s default branch when it's known, otherwise
`HEAD`. If the branch already exists it's checked out as-is.

## Background sessions

The claude and shell panes keep running after you quit. Where tmux is
installed, treeline starts them on a tmux server of its own — a dedicated
socket, its own config, no status bar and no prefix key — so quitting only
detaches: the claude in a worktree keeps its context, a dev server in the
shell pane keeps serving, and the next launch attaches straight back to
them. Switching between worktrees inside treeline works the same way, one
session per worktree per pane. Panes backed this way are marked `· tmux` in
the pane title.

Sessions outlive treeline, so there is a command to see and stop them:

```sh
treeline sessions                  # name, attached/detached, age, directory
treeline sessions kill LMAP-142    # any unambiguous fragment of the name
treeline sessions kill --all
```

To work in one full-screen instead of in the pane:

```sh
tmux -L treeline attach -t <name>          # detach again from another shell:
tmux -L treeline detach-client -s <name>   # (treeline's server has no prefix key)
```

Removing a worktree stops its sessions — nothing is left running in a
directory that no longer exists. Without tmux installed, panes behave as
they always did: the programs are killed when treeline exits. Set
`"persist_sessions": false` in the config to opt out.

## Config

`~/.config/treeline/config.json`:

```json
{
  "linear": { "client_id": "…", "client_secret": "…", "access_token": "…" },
  "branch_types": ["feature", "bugfix", "hotfix", "chore"],
  "slug_max_len": 48,
  "persist_sessions": true
}
```

### Repos

Treeline works across multiple repositories: register each one (in-app via
`,` settings, or in the config) and every repo's worktrees appear in the
list; creating a worktree asks which repo it belongs to. Each repo can have
lifecycle hooks, run with `sh -c` inside the worktree with
`TREELINE_REPO`, `TREELINE_WORKTREE`, `TREELINE_BRANCH`, and
`TREELINE_ISSUE` in the environment:

```json
"repos": {
  "labmaster": {
    "path": "/Users/you/dev/labmaster",
    "setup": "./scripts/worktree-setup.sh",
    "setup_pane": true,
    "cleanup": "./scripts/worktree-teardown.sh"
  },
  "conductor": { "path": "/Users/you/dev/conductor" }
}
```

`setup` runs after a worktree is created (assign a port, create a
database); `cleanup` runs before one is removed (drop the database). By
default `setup` runs in the background; `setup_pane` (the "show setup in a
pane" checkbox in settings) runs it in a tab of the shell pane instead, so
a script that starts a dev server has somewhere to keep running — the tab
scrolls, takes keystrokes, and persists over tmux like the claude and
shell panes. The shell pane is tabbed either way: `ctrl+t` opens extra
shell tabs, `ctrl+←`/`ctrl+→` (or a click) switch between them, and each
tab is its own tmux session. The config lives at
`~/.config/treeline/config.json`.

`branch_types` controls the type-picker options; `slug_max_len` caps the
slug generated from issue titles; `persist_sessions` (default on wherever
tmux is installed) keeps the claude and shell panes alive between launches —
see [Background sessions](#background-sessions).
