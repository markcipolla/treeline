// Package update occasionally asks GitHub for treeline's latest release, so
// an install that has gone stale hears about the new one. The check never
// blocks: it runs in the background and caches what it learns, and the
// notice shown on the way out comes from the cache a previous run left.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/markcipolla/treeline/internal/config"
)

// every is how long a cached answer is trusted before the next check.
const every = 24 * time.Hour

const latestURL = "https://api.github.com/repos/markcipolla/treeline/releases/latest"

// cache is ~/.config/treeline/update.json: the last release we saw and when
// we last asked.
type cache struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

// Check refreshes the cache in the background when the last answer is a day
// old. It returns immediately, and the goroutine dies with the process: a
// session too short to finish the request simply asks again next time.
func Check(current string) {
	if current == "dev" {
		return // unreleased build — nothing meaningful to compare against
	}
	if time.Since(read().Checked) < every {
		return
	}
	go func() {
		c := read()
		if latest, err := fetchLatest(); err == nil {
			c.Latest = latest
		}
		// Stamped either way, so being offline doesn't retry every launch.
		c.Checked = time.Now()
		write(c)
	}()
}

// Notice returns the upgrade message when the release we last saw is newer
// than the running version, otherwise "".
func Notice(current string) string {
	latest := read().Latest
	if !newer(latest, current) {
		return ""
	}
	return fmt.Sprintf("a newer treeline is out (%s, you have %s) — upgrade with: brew install markcipolla/tap/treeline",
		latest, current)
}

// newer compares dotted numeric versions, accepting both tag ("v0.17.5") and
// stamped ("0.17.5") forms. A non-numeric field ends the comparison, so
// pre-release builds never nag.
func newer(latest, current string) bool {
	l, c := fields(latest), fields(current)
	if len(c) == 0 {
		return false // a dev build has no version to be behind
	}
	for i := 0; i < len(l) && i < len(c); i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return len(l) > len(c)
}

func fields(v string) []int {
	var out []int
	for _, f := range strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".") {
		n, err := strconv.Atoi(f)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

func fetchLatest() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: %s", resp.Status)
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	return out.TagName, nil
}

func cachePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "update.json"), nil
}

func read() cache {
	var c cache
	p, err := cachePath()
	if err != nil {
		return c
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c) // a corrupt cache just means checking again
	return c
}

func write(c cache) {
	p, err := cachePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, append(data, '\n'), 0o600)
}
