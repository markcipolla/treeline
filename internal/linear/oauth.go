package linear

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// RedirectURI must match the callback URL registered on the Linear
	// OAuth application exactly.
	RedirectURI  = "http://localhost:9482/callback"
	listenAddr   = "127.0.0.1:9482"
	authorizeURL = "https://linear.app/oauth/authorize"
	tokenURL     = "https://api.linear.app/oauth/token"
)

// DefaultClientID is the client ID of the OAuth application treeline ships
// with (a public client secured by PKCE — client IDs are not secrets).
// When set, users authorize with a browser consent page and never configure
// anything. A client_id in the config file overrides it.
const DefaultClientID = "fdc7ff96ee919997f372f1ce7cf022f1"

type OAuthApp struct {
	ClientID     string
	ClientSecret string // optional: PKCE covers public clients
}

// Token is an OAuth access token plus what's needed to renew it. Linear
// access tokens expire after ~24h; the refresh token is long-lived.
type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// Fresh reports whether the access token is present and not about to expire.
func (t Token) Fresh() bool {
	return t.AccessToken != "" && (t.ExpiresAt.IsZero() || time.Until(t.ExpiresAt) > 2*time.Minute)
}

// Usable reports whether the token can produce a working session, possibly
// after a refresh.
func (t Token) Usable() bool {
	return t.AccessToken != "" || t.RefreshToken != ""
}

// Authorize runs the OAuth authorization-code flow with PKCE: starts a
// local callback server, opens the browser via open, and exchanges the code
// for a token. It blocks until authorized, ctx is done, or 3 minutes pass.
func Authorize(ctx context.Context, app OAuthApp, open func(string) error) (Token, error) {
	if app.ClientID == "" {
		return Token{}, errors.New("Linear OAuth client ID is required")
	}

	state, err := randomToken(16)
	if err != nil {
		return Token{}, err
	}
	verifier, err := randomToken(32)
	if err != nil {
		return Token{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- errors.New("OAuth state mismatch — try again")
			return
		}
		if e := q.Get("error"); e != "" {
			fmt.Fprint(w, "<html><body style=\"font-family:sans-serif\"><h2>Authorization failed</h2>You can close this tab.</body></html>")
			errCh <- errors.New("Linear authorization failed: " + e)
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- errors.New("callback received no authorization code")
			return
		}
		fmt.Fprint(w, "<html><body style=\"font-family:sans-serif\"><h2>&#10003; treeline connected to Linear</h2>You can close this tab and return to your terminal.</body></html>")
		codeCh <- code
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return Token{}, fmt.Errorf("cannot listen on %s (is another treeline auth running?): %w", listenAddr, err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	authURL := authorizeURL + "?" + url.Values{
		"client_id":             {app.ClientID},
		"redirect_uri":          {RedirectURI},
		"response_type":         {"code"},
		"scope":                 {"read"},
		"state":                 {state},
		"prompt":                {"consent"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()
	if err := open(authURL); err != nil {
		return Token{}, fmt.Errorf("could not open browser — visit this URL manually:\n%s", authURL)
	}

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return Token{}, err
	case <-time.After(3 * time.Minute):
		return Token{}, errors.New("timed out waiting for browser authorization")
	case <-ctx.Done():
		return Token{}, ctx.Err()
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {RedirectURI},
		"client_id":     {app.ClientID},
		"code_verifier": {verifier},
	}
	if app.ClientSecret != "" {
		form.Set("client_secret", app.ClientSecret)
	}
	return tokenRequest(ctx, form)
}

// Refresh exchanges a refresh token for a new access token (and possibly a
// rotated refresh token).
func Refresh(ctx context.Context, app OAuthApp, refreshToken string) (Token, error) {
	if refreshToken == "" {
		return Token{}, errors.New("no refresh token — run `treeline auth` to reconnect")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {app.ClientID},
	}
	if app.ClientSecret != "" {
		form.Set("client_secret", app.ClientSecret)
	}
	tok, err := tokenRequest(ctx, form)
	if err != nil {
		return Token{}, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

func tokenRequest(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var raw struct {
		AccessToken      string  `json:"access_token"`
		RefreshToken     string  `json:"refresh_token"`
		ExpiresIn        float64 `json:"expires_in"`
		Error            string  `json:"error"`
		ErrorDescription string  `json:"error_description"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Token{}, fmt.Errorf("token request: HTTP %d: %s", resp.StatusCode, string(body))
	}
	if raw.AccessToken == "" {
		msg := raw.ErrorDescription
		if msg == "" {
			msg = raw.Error
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return Token{}, errors.New("token request failed: " + msg)
	}
	tok := Token{AccessToken: raw.AccessToken, RefreshToken: raw.RefreshToken}
	if raw.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second)
	}
	return tok, nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
