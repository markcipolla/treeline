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
	// Repos remembers primary checkouts by name (e.g. "labmaster"), so
	// treeline can be launched from anywhere and offer a picker.
	Repos map[string]string `json:"repos,omitempty"`
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

func path() (string, error) {
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
		return cfg, nil
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
