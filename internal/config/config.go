// Package config loads and saves treeline's configuration at
// ~/.config/treeline/config.json (written with 0600 since it holds tokens).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/markcipolla/treeline/internal/linear"
)

type Config struct {
	Linear      LinearConfig `json:"linear"`
	GitHub      GitHubConfig `json:"github"`
	BranchTypes []string     `json:"branch_types"`
	SlugMaxLen  int          `json:"slug_max_len"`
	// PersistSessions keeps the agent and shell panes running on treeline's
	// own tmux server, so quitting treeline detaches from them instead of
	// killing them and the next launch picks them back up. Unset means on
	// wherever tmux is installed; set it to false to opt out.
	PersistSessions *bool `json:"persist_sessions,omitempty"`
	// FileIcons draws file-type icons in the ide pane's explorer, tabs and
	// search results. The glyphs come from the Nerd Font private use area, so
	// a terminal without one shows boxes: unset means on, set it to false to
	// get the plain tree back.
	FileIcons *bool `json:"file_icons,omitempty"`
	// Repos remembers primary checkouts by name (e.g. "labmaster"), so
	// treeline can be launched anywhere, list every repo's worktrees, and
	// offer a repo picker when creating one.
	Repos map[string]RepoConfig `json:"repos,omitempty"`
}

// Persist reports whether embedded sessions should be tmux-backed.
func (c *Config) Persist() bool {
	return c.PersistSessions == nil || *c.PersistSessions
}

// Icons reports whether the ide pane draws file-type icons.
func (c *Config) Icons() bool {
	return c.FileIcons == nil || *c.FileIcons
}

// RepoConfig is a registered repository: its primary checkout plus optional
// lifecycle hooks, each run with `sh -c` inside the worktree. Setup runs
// after a worktree is created (e.g. assign a port, create a database);
// Cleanup runs before one is removed (e.g. drop the database).
type RepoConfig struct {
	Path    string `json:"path"`
	Setup   string `json:"setup,omitempty"`
	Cleanup string `json:"cleanup,omitempty"`
	// SetupPane runs Setup in a visible pane beside the others instead of
	// in the background, so a script that starts a dev server has somewhere
	// to keep running.
	SetupPane bool `json:"setup_pane,omitempty"`
}

// UnmarshalJSON accepts both the current object form and the legacy plain
// path string.
func (r *RepoConfig) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		r.Path = s
		return nil
	}
	type plain RepoConfig
	return json.Unmarshal(b, (*plain)(r))
}

// GitHubConfig is only needed when the gh CLI isn't installed: a personal
// token, or a client ID for the OAuth device flow.
type GitHubConfig struct {
	ClientID string `json:"client_id,omitempty"`
	Token    string `json:"token,omitempty"`
}

type LinearConfig struct {
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
}

func (l LinearConfig) Token() linear.Token {
	return linear.Token{
		AccessToken:  l.AccessToken,
		RefreshToken: l.RefreshToken,
		ExpiresAt:    l.ExpiresAt,
	}
}

func (l *LinearConfig) SetToken(t linear.Token) {
	l.AccessToken = t.AccessToken
	l.RefreshToken = t.RefreshToken
	l.ExpiresAt = t.ExpiresAt
}

// App returns the OAuth application to authorize against: the user's own
// client_id from the config when set, otherwise the one treeline ships with.
func (l LinearConfig) App() linear.OAuthApp {
	id := l.ClientID
	if id == "" {
		id = linear.DefaultClientID
	}
	return linear.OAuthApp{ClientID: id, ClientSecret: l.ClientSecret}
}

func defaults() *Config {
	return &Config{
		BranchTypes: []string{"feature", "bugfix", "hotfix", "chore"},
		SlugMaxLen:  48,
	}
}

// path is ~/.config/treeline/config.json (XDG_CONFIG_HOME respected).
func path() (string, error) {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "treeline", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "treeline", "config.json"), nil
}

// legacyPath is the pre-0.3 location (os.UserConfigDir — on macOS that is
// ~/Library/Application Support); Load migrates it forward once.
func legacyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "treeline", "config.json"), nil
}

// PathHint returns the config path for display in messages, best-effort.
func PathHint() string {
	p, err := path()
	if err != nil {
		return "~/.config/treeline/config.json"
	}
	return p
}

func Load() (*Config, error) {
	cfg := defaults()
	p, err := path()
	if err != nil {
		return cfg, nil
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		// migrate the pre-0.3 location forward once
		if lp, lerr := legacyPath(); lerr == nil && lp != p {
			if ldata, lerr := os.ReadFile(lp); lerr == nil {
				data, err = ldata, nil
				if uerr := json.Unmarshal(data, cfg); uerr == nil {
					_ = cfg.Save() // write to the new home
				}
			}
		}
		if data == nil {
			return cfg, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	if len(cfg.BranchTypes) == 0 {
		cfg.BranchTypes = defaults().BranchTypes
	}
	if cfg.SlugMaxLen <= 0 {
		cfg.SlugMaxLen = defaults().SlugMaxLen
	}
	return cfg, nil
}

func (c *Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o600)
}
