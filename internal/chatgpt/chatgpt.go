// Package chatgpt implements "Sign in with ChatGPT" for werkz: an OAuth sign-in (PKCE, loopback
// callback) that lets requests use the user's ChatGPT plan instead of an API key, plus storage
// and refresh of the resulting tokens.
//
// Protocol: https://developers.openai.com/siwc/token-sharing-open-source
package chatgpt

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
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AuthBase is OpenAI's account server. Tests point it at a fake server.
var AuthBase = "https://auth.openai.com"

const (
	// Resource is the API the tokens are for; it is sent on every authorize, exchange and refresh.
	Resource = "https://api.openai.com/v1"
	// PlanScope is the scope that allows requests on the user's ChatGPT plan.
	PlanScope = "chatgpt.tokens.use.direct"
	// ManageUsageURL is where users see and limit plan usage by apps.
	ManageUsageURL = "https://chatgpt.com/settings/usage"

	appName       = "werkz"
	dynamicClient = "dynamic_agent_client"
	scopes        = "openid profile email offline_access resource.invoke " + PlanScope
	callbackPath  = "/auth/callback"
	preferredPort = 1455
)

// ErrSignedOut means there is no usable sign-in: the user has to sign in again.
var ErrSignedOut = errors.New("not signed in with ChatGPT (use /login chatgpt)")

// Credentials is the stored sign-in, kept in chatgpt.json next to werkz's config (mode 0600).
// ClientID and HostID survive sign-out: OpenAI issues one client per app install and host.
type Credentials struct {
	Email             string    `json:"email,omitempty"`
	Issuer            string    `json:"issuer,omitempty"`
	Subject           string    `json:"subject,omitempty"`
	ClientID          string    `json:"client_id,omitempty"`
	HostID            string    `json:"ext_agent_host_id,omitempty"`
	IDToken           string    `json:"id_token,omitempty"`
	AccessToken       string    `json:"access_token,omitempty"`
	RefreshToken      string    `json:"refresh_token,omitempty"`
	TokenType         string    `json:"token_type,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitzero"`
	EarliestRefreshAt time.Time `json:"earliest_refresh_at,omitzero"`
	Scopes            []string  `json:"scopes,omitempty"`
	SavedAt           time.Time `json:"saved_at,omitzero"`
}

// SignedIn reports whether the credentials can be used for plan requests.
func (c *Credentials) SignedIn() bool {
	return c != nil && c.RefreshToken != "" && slices.Contains(c.Scopes, PlanScope)
}

// Path is where the sign-in is stored. Tests set WERKZ_CHATGPT_FILE.
func Path() string {
	if p := os.Getenv("WERKZ_CHATGPT_FILE"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "werkz", "chatgpt.json")
}

// Load reads the stored sign-in. A missing file is not an error (empty credentials).
func Load() (*Credentials, error) {
	c := &Credentials{}
	b, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", Path(), err)
	}
	return c, nil
}

// save writes the sign-in atomically with owner-only permissions.
func save(c *Credentials) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	c.SavedAt = time.Now().UTC()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".chatgpt-*.json") // mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

var signedIn struct {
	sync.Mutex
	mod  time.Time
	size int64
	ok   bool
}

// SignedIn reports whether a usable sign-in is stored. It is cheap to call often: the file is
// only read again when it changes.
func SignedIn() bool {
	st, err := os.Stat(Path())
	if err != nil {
		return false
	}
	signedIn.Lock()
	defer signedIn.Unlock()
	if !st.ModTime().Equal(signedIn.mod) || st.Size() != signedIn.size {
		c, err := Load()
		signedIn.ok = err == nil && c.SignedIn()
		signedIn.mod, signedIn.size = st.ModTime(), st.Size()
	}
	return signedIn.ok
}

// httpClient is used for the account server (short requests only).
var httpClient = &http.Client{Timeout: 30 * time.Second}

// ---------------------------------------------------------------- sign-in

// Login runs the browser sign-in: it starts a loopback server, calls open with the sign-in URL
// (show is called first so the URL can be printed in case no browser opens), waits for the
// callback, exchanges the code and stores the tokens.
func Login(ctx context.Context, open func(string) error, show func(string)) (*Credentials, error) {
	prev, err := Load()
	if err != nil {
		return nil, err
	}
	if prev.HostID == "" {
		prev.HostID = "urn:uuid:" + uuid4()
		if err := save(prev); err != nil { // keep the host ID stable even if this sign-in fails
			return nil, err
		}
	}
	verifier := randString(48)
	state, nonce := randString(24), randString(24)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	ln, err := listen()
	if err != nil {
		return nil, fmt.Errorf("can't start the sign-in callback server: %w", err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d%s", ln.Addr().(*net.TCPAddr).Port, callbackPath)

	q := url.Values{}
	if prev.ClientID == "" {
		q.Set("client_id", dynamicClient)
		q.Set("agent_name_hint", appName)
	} else {
		q.Set("client_id", prev.ClientID) // id_token_hint is optional and left out: the URL is shown on screen
	}
	q.Set("ext_agent_host_id", prev.HostID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirect)
	q.Set("scope", scopes)
	q.Set("resource", Resource)
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", challenge)
	authURL := AuthBase + "/api/accounts/authorize?" + q.Encode()

	type result struct {
		code, clientID string
		err            error
	}
	done := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()
		var res result
		switch {
		case v.Get("state") != state:
			res.err = errors.New("the sign-in reply didn't match this request (state mismatch); try again")
		case v.Get("error") == "access_denied":
			res.err = errors.New("sign-in was cancelled")
		case v.Get("error") != "":
			res.err = fmt.Errorf("sign-in failed: %s %s", v.Get("error"), v.Get("error_description"))
		case v.Get("code") == "":
			res.err = errors.New("the sign-in reply had no authorization code")
		default:
			res.code, res.clientID = v.Get("code"), v.Get("client_id")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != nil {
			fmt.Fprintf(w, page, "Sign-in didn't complete", "You can close this tab and try again in werkz.")
		} else {
			fmt.Fprintf(w, page, "You're signed in to werkz", "You can close this tab and go back to your terminal.")
		}
		select {
		case done <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln) //nolint:errcheck // closed below
	defer srv.Close()

	if show != nil {
		show(authURL)
	}
	if open != nil {
		_ = open(authURL) // the URL is shown too, in case no browser opens
	}

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Minute):
		return nil, errors.New("sign-in timed out")
	}
	if res.err != nil {
		return nil, res.err
	}
	clientID := res.clientID
	if clientID == "" {
		clientID = prev.ClientID
	}
	if clientID == "" || clientID == dynamicClient {
		return nil, errors.New("OpenAI didn't issue a client ID for werkz; try signing in again")
	}

	tok, err := tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {res.code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirect},
		"resource":      {Resource},
	})
	if err != nil {
		return nil, err
	}
	if !slices.Contains(tok.scopes(), PlanScope) {
		return nil, errors.New("ChatGPT plan usage wasn't allowed for werkz; sign in again and allow it")
	}
	claims, err := verifyIDToken(ctx, tok.IDToken, clientID, nonce)
	if err != nil {
		return nil, err
	}
	c := &Credentials{
		Email: claims.Email, Issuer: claims.Issuer, Subject: claims.Subject,
		ClientID: clientID, HostID: prev.HostID,
	}
	tok.apply(c)
	if err := save(c); err != nil {
		return nil, err
	}
	return c, nil
}

const page = `<!doctype html><meta charset="utf-8"><title>werkz</title>
<body style="font:16px system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1 style="font-size:1.4rem">%s</h1><p>%s</p></body>`

// listen prefers the documented port and falls back to any free loopback port (only the port of
// the redirect URI may change between sign-ins).
func listen() (net.Listener, error) {
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort)); err == nil {
		return ln, nil
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

// ---------------------------------------------------------------- tokens

type tokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	IDToken      string          `json:"id_token"`
	TokenType    string          `json:"token_type"`
	ExpiresIn    int64           `json:"expires_in"`
	Scope        string          `json:"scope"`
	Earliest     json.RawMessage `json:"earliest_refresh_at"`
}

func (t *tokenResponse) scopes() []string { return strings.Fields(t.Scope) }

// apply copies a token response into the credentials (refresh tokens rotate: always keep the
// newest one).
func (t *tokenResponse) apply(c *Credentials) {
	c.AccessToken, c.TokenType = t.AccessToken, t.TokenType
	if t.RefreshToken != "" {
		c.RefreshToken = t.RefreshToken
	}
	if t.IDToken != "" {
		c.IDToken = t.IDToken
	}
	if s := t.scopes(); len(s) > 0 {
		c.Scopes = s
	}
	exp := t.ExpiresIn
	if exp <= 0 {
		exp = 3600
	}
	c.ExpiresAt = time.Now().Add(time.Duration(exp) * time.Second).UTC()
	c.EarliestRefreshAt = parseTime(t.Earliest)
}

// parseTime accepts a Unix timestamp (seconds) or an RFC 3339 string.
func parseTime(raw json.RawMessage) time.Time {
	s := strings.Trim(string(raw), `"`)
	if s == "" || s == "null" {
		return time.Time{}
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Unix(int64(n), 0).UTC()
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t.UTC()
}

// TokenError is an error from the account server's token endpoint.
type TokenError struct {
	Status      int
	Code        string
	Description string
}

func (e *TokenError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("ChatGPT sign-in: %s (%s)", e.Description, e.Code)
	}
	return fmt.Sprintf("ChatGPT sign-in: %s (HTTP %d)", e.Code, e.Status)
}

// terminal reports refresh errors after which the stored sign-in can't be used any more.
func (e *TokenError) terminal() bool {
	switch e.Code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired",
		"refresh_token_invalidated", "refresh_token_reused":
		return true
	}
	return false
}

func tokenRequest(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", AuthBase+"/api/accounts/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error       any    `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(b, &e)
		te := &TokenError{Status: resp.StatusCode, Description: e.Description}
		switch v := e.Error.(type) {
		case string:
			te.Code = v
		case map[string]any:
			te.Code, _ = v["code"].(string)
			if m, _ := v["message"].(string); te.Description == "" {
				te.Description = m
			}
		}
		if te.Code == "" {
			te.Code = http.StatusText(resp.StatusCode)
		}
		return nil, te
	}
	var t tokenResponse
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("unexpected reply from the ChatGPT token endpoint: %w", err)
	}
	if t.AccessToken == "" {
		return nil, errors.New("the ChatGPT token endpoint returned no access token")
	}
	return &t, nil
}

// Session hands out access tokens for plan requests, refreshing them when needed. Refreshes are
// serialized within this process and, with a file lock, across werkz windows: refresh tokens
// rotate, so two concurrent refreshes would sign the user out.
type Session struct {
	mu sync.Mutex
}

// Token returns a valid access token and the signed-in credentials.
func (s *Session) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := Load()
	if err != nil {
		return "", err
	}
	if !c.SignedIn() {
		return "", ErrSignedOut
	}
	if fresh(c) {
		return c.AccessToken, nil
	}
	unlock, err := lockFile(Path() + ".lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	if c, err = Load(); err != nil { // another window may have refreshed meanwhile
		return "", err
	}
	if !c.SignedIn() {
		return "", ErrSignedOut
	}
	if fresh(c) {
		return c.AccessToken, nil
	}
	if err := refresh(ctx, c); err != nil {
		return "", err
	}
	return c.AccessToken, nil
}

// ForceRefresh refreshes now (after the API rejected the access token).
func (s *Session) ForceRefresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockFile(Path() + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	c, err := Load()
	if err != nil {
		return err
	}
	if !c.SignedIn() {
		return ErrSignedOut
	}
	return refresh(ctx, c)
}

// fresh reports whether the access token is good for at least two more minutes. A token that
// is close to expiry but may not be refreshed yet (earliest_refresh_at) is used as it is.
func fresh(c *Credentials) bool {
	if c.AccessToken == "" {
		return false
	}
	now := time.Now()
	if now.Add(2 * time.Minute).Before(c.ExpiresAt) {
		return true
	}
	return now.Before(c.EarliestRefreshAt) && now.Before(c.ExpiresAt)
}

func refresh(ctx context.Context, c *Credentials) error {
	tok, err := tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.ClientID},
		"refresh_token": {c.RefreshToken},
		"resource":      {Resource},
	})
	var te *TokenError
	if errors.As(err, &te) && te.terminal() {
		clearTokens(c)
		_ = save(c)
		return fmt.Errorf("%w: your ChatGPT sign-in expired or was revoked", ErrSignedOut)
	}
	if err != nil {
		return err // temporary: keep the credentials
	}
	tok.apply(c)
	return save(c)
}

func clearTokens(c *Credentials) {
	c.AccessToken, c.RefreshToken, c.TokenType = "", "", ""
	c.ExpiresAt, c.EarliestRefreshAt = time.Time{}, time.Time{}
	c.Scopes = nil
}

// Logout revokes the refresh token and removes the tokens. The client ID, host ID and the last
// ID token are kept for the next sign-in. On a network failure the tokens are kept, so the
// revocation can be retried.
func Logout(ctx context.Context) error {
	c, err := Load()
	if err != nil {
		return err
	}
	if c.RefreshToken != "" {
		d, err := discover(ctx)
		if err != nil {
			return fmt.Errorf("can't reach OpenAI to sign out (%w); try again", err)
		}
		if d.Revocation != "" {
			form := url.Values{"token": {c.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {c.ClientID}}
			req, err := http.NewRequestWithContext(ctx, "POST", d.Revocation, strings.NewReader(form.Encode()))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := httpClient.Do(req)
			if err != nil {
				return fmt.Errorf("can't reach OpenAI to sign out (%w); try again", err)
			}
			resp.Body.Close()
			if resp.StatusCode >= 500 {
				return fmt.Errorf("OpenAI couldn't sign you out (HTTP %d); try again", resp.StatusCode)
			}
		}
	}
	clearTokens(c)
	return save(c)
}

// ---------------------------------------------------------------- helpers

func randString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
