package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/markcipolla/treeline/internal/linear"
)

// isolate points the config path at a temp dir. HOME is redirected too, so a
// stray legacyPath lookup can't reach the developer's real config.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// TestLoadNoFile: a missing config is not an error, it's the defaults.
func TestLoadNoFile(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.BranchTypes, defaults().BranchTypes; len(got) != len(want) {
		t.Fatalf("BranchTypes = %v, want %v", got, want)
	}
	if cfg.SlugMaxLen != defaults().SlugMaxLen {
		t.Errorf("SlugMaxLen = %d, want %d", cfg.SlugMaxLen, defaults().SlugMaxLen)
	}
}

// TestSaveLoadRoundTrip: whatever Save writes, Load reads back — tokens
// included, since losing those means re-authorizing.
func TestSaveLoadRoundTrip(t *testing.T) {
	isolate(t)
	expiry := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	off := false
	want := &Config{
		Linear: LinearConfig{
			ClientID:     "client-123",
			ClientSecret: "secret-456",
			AccessToken:  "access-789",
			RefreshToken: "refresh-abc",
			ExpiresAt:    expiry,
		},
		GitHub:          GitHubConfig{ClientID: "gh-id", Token: "gh-token"},
		BranchTypes:     []string{"feature", "spike"},
		SlugMaxLen:      32,
		PersistSessions: &off,
		Repos: map[string]RepoConfig{
			"labmaster": {Path: "/Users/x/dev/labmaster", Setup: "bin/setup", Cleanup: "bin/teardown"},
		},
	}
	if err := want.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Linear != want.Linear {
		t.Errorf("linear config = %+v, want %+v", got.Linear, want.Linear)
	}
	if got.GitHub != want.GitHub {
		t.Errorf("github config = %+v, want %+v", got.GitHub, want.GitHub)
	}
	if got.SlugMaxLen != 32 || len(got.BranchTypes) != 2 || got.BranchTypes[1] != "spike" {
		t.Errorf("branch settings = %v / %d", got.BranchTypes, got.SlugMaxLen)
	}
	if got.Persist() {
		t.Error("Persist() = true, want false (explicitly opted out)")
	}
	if r := got.Repos["labmaster"]; r.Path == "" || r.Setup != "bin/setup" || r.Cleanup != "bin/teardown" {
		t.Errorf("repo entry = %+v", r)
	}
}

// TestSavePermissions: the file holds OAuth tokens, so it must not be
// readable by anyone else.
func TestSavePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	isolate(t)
	if err := (&Config{}).Save(); err != nil {
		t.Fatal(err)
	}
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("config mode = %#o, want 0600", perm)
	}
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir mode = %#o, want 0700", perm)
	}
}

// TestLoadFillsMissingDefaults: a config written by an older version, or
// hand-edited, must not end up with an empty branch-type list or a zero slug
// length — both would break the create flow.
func TestLoadFillsMissingDefaults(t *testing.T) {
	isolate(t)
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"linear":{"access_token":"keep-me"},"branch_types":[],"slug_max_len":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BranchTypes) == 0 {
		t.Error("empty branch_types was not backfilled")
	}
	if cfg.SlugMaxLen <= 0 {
		t.Errorf("slug_max_len = %d, want the default", cfg.SlugMaxLen)
	}
	if cfg.Linear.AccessToken != "keep-me" {
		t.Errorf("access token = %q, want it preserved", cfg.Linear.AccessToken)
	}
}

// TestLoadMalformed: a corrupt config is reported, not silently replaced with
// defaults, which would look like the token had vanished.
func TestLoadMalformed(t *testing.T) {
	isolate(t)
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"linear":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load succeeded on malformed JSON")
	}
}

// TestLoadMigratesLegacyLocation: pre-0.3 configs lived at os.UserConfigDir.
// Load has to carry them forward, tokens intact, and write them to the new
// home so the migration only happens once.
func TestLoadMigratesLegacyLocation(t *testing.T) {
	isolate(t)
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	lp, err := legacyPath()
	if err != nil {
		t.Fatal(err)
	}
	if lp == p {
		// os.UserConfigDir is XDG_CONFIG_HOME on non-darwin unix, so the two
		// locations coincide there and there is nothing to migrate
		t.Skipf("legacy and current paths coincide on %s (%s)", runtime.GOOS, p)
	}

	legacy := &Config{
		Linear:      LinearConfig{AccessToken: "legacy-access", RefreshToken: "legacy-refresh"},
		BranchTypes: []string{"feature"},
		SlugMaxLen:  20,
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(lp), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lp, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Linear.AccessToken != "legacy-access" || cfg.Linear.RefreshToken != "legacy-refresh" {
		t.Fatalf("migration dropped the tokens: %+v", cfg.Linear)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("migration did not write the new location: %v", err)
	}

	// the new copy is what a second Load reads, legacy file or not
	if err := os.Remove(lp); err != nil {
		t.Fatal(err)
	}
	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if again.Linear.AccessToken != "legacy-access" {
		t.Errorf("second load lost the token: %+v", again.Linear)
	}
}

func TestPersistDefaultsOn(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		set  *bool
		want bool
	}{
		{"unset", nil, true},
		{"on", &on, true},
		{"off", &off, false},
	} {
		if got := (&Config{PersistSessions: tc.set}).Persist(); got != tc.want {
			t.Errorf("%s: Persist() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLinearTokenRoundTrip(t *testing.T) {
	expiry := time.Now().Add(time.Hour).UTC()
	var lc LinearConfig
	lc.SetToken(linear.Token{AccessToken: "a", RefreshToken: "r", ExpiresAt: expiry})
	got := lc.Token()
	if got.AccessToken != "a" || got.RefreshToken != "r" || !got.ExpiresAt.Equal(expiry) {
		t.Errorf("token round trip = %+v", got)
	}
}

// TestAppFallsBackToShippedClient: treeline's own OAuth app is used unless
// the config names one.
func TestAppFallsBackToShippedClient(t *testing.T) {
	if app := (LinearConfig{}).App(); app.ClientID != linear.DefaultClientID {
		t.Errorf("ClientID = %q, want the shipped default", app.ClientID)
	}
	own := LinearConfig{ClientID: "mine", ClientSecret: "shh"}
	if app := own.App(); app.ClientID != "mine" || app.ClientSecret != "shh" {
		t.Errorf("App() = %+v, want the config's own app", app)
	}
}

func TestPathHintHonoursXDG(t *testing.T) {
	isolate(t)
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	if hint := PathHint(); hint != p {
		t.Errorf("PathHint() = %q, want %q", hint, p)
	}
}
