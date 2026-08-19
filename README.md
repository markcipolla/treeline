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
| `d` | row with worktree | remove worktree (`y` keep branch, `b` delete branch too) |
| `/` | — | filter the table |
| `r` | — | refresh issues, worktrees, and CI |
| `a` | — | connect / reconnect Linear |
| `g` | — | connect GitHub (CI) |
| `q` / `ctrl+c` | — | quit |

The mouse works too: click buttons and branch-type options; scroll the
table and detail view with the wheel.

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

## Config

`~/.config/treeline/config.json`:

```json
{
  "linear": { "client_id": "…", "client_secret": "…", "access_token": "…" },
  "branch_types": ["feature", "bugfix", "hotfix", "chore"],
  "slug_max_len": 48
}
```

`branch_types` controls the type-picker options; `slug_max_len` caps the
slug generated from issue titles.
