// Package github fetches CI status for branches and handles GitHub
// authentication: a configured token, the gh CLI's token, or OAuth device
// flow (no callback server needed).
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DefaultClientID is the client ID of the OAuth app treeline ships with for
// the device flow (public client, no secret). A github.client_id in the
// config overrides it. With the gh CLI installed none of this is needed.
const DefaultClientID = ""

type Status string

const (
	StatusNone    Status = ""
	StatusOK      Status = "success"
	StatusFail    Status = "failure"
	StatusRunning Status = "running"
)

// Token resolves an access token: the configured one wins, otherwise the gh
// CLI's stored token. Empty when neither is available.
func Token(configured string) string {
	if configured != "" {
		return configured
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// RepoFromRemote parses origin's URL into owner/repo if it's a GitHub
// remote (SSH or HTTPS).
func RepoFromRemote(root string) (string, string, bool) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", "", false
	}
	u := strings.TrimSpace(string(out))
	var path string
	if i := strings.Index(u, "github.com:"); i >= 0 {
		path = u[i+len("github.com:"):]
	} else if i := strings.Index(u, "github.com/"); i >= 0 {
		path = u[i+len("github.com/"):]
	} else {
		return "", "", false
	}
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// BranchStatuses fetches CI status for each branch concurrently.
func BranchStatuses(ctx context.Context, token, owner, repo string, branches []string) map[string]Status {
	res := make(map[string]Status, len(branches))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, b := range branches {
		if b == "" {
			continue
		}
		wg.Add(1)
		go func(b string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s := branchStatus(ctx, token, owner, repo, b)
			mu.Lock()
			res[b] = s
			mu.Unlock()
		}(b)
	}
	wg.Wait()
	return res
}

func branchStatus(ctx context.Context, token, owner, repo, branch string) Status {
	u := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s/check-runs?per_page=100",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return StatusNone
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return StatusNone
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return StatusNone // branch not pushed, no access, or no GitHub
	}
	var out struct {
		CheckRuns []checkRun `json:"check_runs"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return StatusNone
	}
	return statusFromRuns(out.CheckRuns)
}

// checkRun is one entry of a commit's check-runs listing.
type checkRun struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// statusFromRuns folds a commit's check runs into a single status: anything
// still in flight makes the whole commit running, otherwise one bad
// conclusion fails it. No runs at all means nothing to report.
func statusFromRuns(runs []checkRun) Status {
	if len(runs) == 0 {
		return StatusNone
	}
	status := StatusOK
	for _, cr := range runs {
		if cr.Status != "completed" {
			return StatusRunning
		}
		switch cr.Conclusion {
		case "failure", "timed_out", "action_required", "cancelled":
			status = StatusFail
		}
	}
	return status
}

// DeviceCode is GitHub's device-flow handshake state.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// RequestDeviceCode starts the OAuth device flow.
func RequestDeviceCode(ctx context.Context, clientID string) (*DeviceCode, error) {
	if clientID == "" {
		return nil, errors.New("no GitHub OAuth client ID configured — install the gh CLI instead, or set github.client_id in the config")
	}
	body, err := postForm(ctx, "https://github.com/login/device/code", url.Values{
		"client_id": {clientID},
		"scope":     {"repo"},
	})
	if err != nil {
		return nil, err
	}
	var dc DeviceCode
	if err := json.Unmarshal(body, &dc); err != nil || dc.UserCode == "" {
		return nil, fmt.Errorf("unexpected device-code response: %s", string(body))
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	return &dc, nil
}

// PollDeviceToken waits for the user to approve the device and returns the
// access token.
func PollDeviceToken(ctx context.Context, clientID string, dc *DeviceCode) (string, error) {
	interval := time.Duration(dc.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return "", errors.New("device code expired — try again")
		}
		body, err := postForm(ctx, "https://github.com/login/oauth/access_token", url.Values{
			"client_id":   {clientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		})
		if err != nil {
			return "", err
		}
		var out struct {
			AccessToken      string `json:"access_token"`
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return "", fmt.Errorf("unexpected token response: %s", string(body))
		}
		switch {
		case out.AccessToken != "":
			return out.AccessToken, nil
		case out.Error == "authorization_pending":
			// keep waiting
		case out.Error == "slow_down":
			interval += 5 * time.Second
		default:
			msg := out.ErrorDescription
			if msg == "" {
				msg = out.Error
			}
			return "", errors.New("GitHub authorization failed: " + msg)
		}
	}
}

func postForm(ctx context.Context, u string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
