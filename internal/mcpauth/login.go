package mcpauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	httpTimeout  = 30 * time.Second
	loginTimeout = 10 * time.Minute
	callbackPath = "/callback"
	clientName   = "werkz"
	maxBody      = 1 << 20
)

// checkEndpoint accepts https URLs, and http only for loopback hosts (tests and local servers).
func checkEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a valid URL", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("refusing %s: sign-in endpoints must use https", u.Redacted())
}

func isLoopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	return checkEndpoint(req.URL.String())
}

// ---------------------------------------------------------------- token endpoint

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// apply stores the response in e: a rotated refresh token replaces the old one, otherwise the old is kept.
func (t *tokenResponse) apply(e *serverEntry) {
	e.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		e.RefreshToken = t.RefreshToken
	}
	e.ExpiresAt = time.Time{}
	if t.ExpiresIn > 0 {
		e.ExpiresAt = now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	if t.Scope != "" {
		e.Scope = t.Scope
	}
}

// tokenError is an OAuth error reply. It holds only the error code and description, never a response body.
type tokenError struct {
	Status int
	Code   string
	Desc   string
}

func (e *tokenError) Error() string {
	s := fmt.Sprintf("the server's token endpoint refused the request (HTTP %d", e.Status)
	if e.Code != "" {
		s += ", " + e.Code
	}
	s += ")"
	if e.Desc != "" {
		s += ": " + e.Desc
	}
	return s
}

func (e *tokenError) terminal() bool { return e.Code == "invalid_grant" || e.Code == "invalid_client" }

func tokenRequest(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", stripURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		return nil, &tokenError{Status: resp.StatusCode, Code: clip(e.Error), Desc: clip(e.Desc)}
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return nil, errors.New("the server's token endpoint sent an unusable reply")
	}
	if tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "bearer") {
		return nil, fmt.Errorf("the server issued a %q token; only Bearer tokens are supported", clip(tr.TokenType))
	}
	return &tr, nil
}

// stripURL unwraps a *url.Error so the message doesn't repeat the request URL (it may carry secrets).
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func clip(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// ---------------------------------------------------------------- discovery

type protectedResource struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type asMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	ScopesSupported               []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// getJSON fetches a metadata document; found is false for a 404.
func getJSON(ctx context.Context, hc *http.Client, rawURL string, v any) (found bool, err error) {
	if err := checkEndpoint(rawURL); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return false, stripURL(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s answered HTTP %d", urlHost(rawURL), resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(v); err != nil {
		return false, fmt.Errorf("%s sent invalid metadata", urlHost(rawURL))
	}
	return true, nil
}

func urlHost(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return "the server"
}

// wellKnown builds origin/.well-known/<suffix>[/path] for base (RFC 8615 / 9728 / 8414 path insertion).
func wellKnown(base *url.URL, suffix string, withPath bool) string {
	u := url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/.well-known/" + suffix}
	if p := strings.TrimRight(base.Path, "/"); withPath && p != "" {
		u.Path += p
	}
	return u.String()
}

// covers reports whether the protected resource identifier names serverURL (same origin, path prefix).
func covers(resource, serverURL string) bool {
	r, err1 := url.Parse(resource)
	s, err2 := url.Parse(serverURL)
	if err1 != nil || err2 != nil || !strings.EqualFold(r.Scheme, s.Scheme) || !strings.EqualFold(r.Host, s.Host) {
		return false
	}
	rp, sp := strings.TrimRight(r.Path, "/"), strings.TrimRight(s.Path, "/")
	return sp == rp || strings.HasPrefix(sp, rp+"/")
}

// probe asks the server for its WWW-Authenticate challenge when this process hasn't seen a 401 yet.
func probe(ctx context.Context, hc *http.Client, serverURL string) challenge {
	body := `{"jsonrpc":"2.0","id":0,"method":"ping"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, strings.NewReader(body))
	if err != nil {
		return challenge{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return challenge{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return challenge{}
	}
	return parseChallenge(resp.Header.Values("WWW-Authenticate"))
}

func discoverResource(ctx context.Context, hc *http.Client, serverURL string, ch challenge) (*protectedResource, error) {
	su, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	var urls []string
	if ch.ResourceMetadata != "" {
		urls = append(urls, ch.ResourceMetadata)
	}
	urls = append(urls, wellKnown(su, "oauth-protected-resource", true))
	if p := wellKnown(su, "oauth-protected-resource", false); p != urls[len(urls)-1] {
		urls = append(urls, p)
	}
	var firstErr error
	for _, u := range urls {
		var pr protectedResource
		found, err := getJSON(ctx, hc, u, &pr)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !found {
			continue
		}
		if !covers(pr.Resource, serverURL) {
			return nil, errors.New("the server's protected-resource metadata is for a different resource; refusing to sign in")
		}
		return &pr, nil
	}
	return nil, firstErr // nil: no metadata published
}

func discoverAS(ctx context.Context, hc *http.Client, issuer string) (*asMetadata, error) {
	iu, err := url.Parse(issuer)
	if err != nil || iu.Host == "" {
		return nil, fmt.Errorf("%q is not a valid authorization server", issuer)
	}
	urls := []string{
		wellKnown(iu, "oauth-authorization-server", true),
		wellKnown(iu, "openid-configuration", true),
		strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration",
	}
	var firstErr error
	for _, u := range urls {
		var md asMetadata
		found, err := getJSON(ctx, hc, u, &md)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !found {
			continue
		}
		if md.Issuer != issuer { // RFC 8414 section 3.3
			return nil, errors.New("the authorization server's metadata names a different issuer than it was fetched from; refusing to sign in")
		}
		for _, ep := range []string{md.AuthorizationEndpoint, md.TokenEndpoint} {
			if err := checkEndpoint(ep); err != nil {
				return nil, err
			}
		}
		if md.RegistrationEndpoint != "" {
			if err := checkEndpoint(md.RegistrationEndpoint); err != nil {
				return nil, err
			}
		}
		if len(md.CodeChallengeMethodsSupported) > 0 && !contains(md.CodeChallengeMethodsSupported, "S256") {
			return nil, errors.New("the authorization server doesn't support PKCE S256")
		}
		return &md, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, errors.New("couldn't find the authorization server's metadata")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- registration

func register(ctx context.Context, hc *http.Client, endpoint, redirect string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                clientName,
		"redirect_uris":              []string{redirect},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("client registration failed: %w", stripURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	var out struct {
		ClientID string `json:"client_id"`
		Error    string `json:"error"`
		Desc     string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("the server refused to register werkz (HTTP %d %s %s)", resp.StatusCode, clip(out.Error), clip(out.Desc))
	}
	if out.ClientID == "" {
		return "", errors.New("the server's registration reply had no client_id")
	}
	return out.ClientID, nil
}

// ---------------------------------------------------------------- login

const resultPage = `<!doctype html><meta charset="utf-8"><title>werkz</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<body style="font:16px system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1 style="font-size:1.4rem">%s</h1><p>%s</p></body>`

// Login runs the browser sign-in for serverURL and stores the tokens. open receives the authorization URL
// (it may open a browser or print it; an error from it doesn't stop the flow, which waits for the callback).
func Login(ctx context.Context, serverURL string, st *Store, open func(authURL string) error) error {
	if err := checkEndpoint(serverURL); err != nil {
		return err
	}
	hc := &http.Client{Timeout: httpTimeout, CheckRedirect: checkRedirect}

	ch, ok := st.getChallenge(serverURL)
	if !ok {
		ch = probe(ctx, hc, serverURL)
	}

	issuer := ""
	var scopes []string
	pr, err := discoverResource(ctx, hc, serverURL, ch)
	if err != nil {
		return fmt.Errorf("finding the server's sign-in details: %w", err)
	}
	if pr != nil && len(pr.AuthorizationServers) > 0 {
		issuer, scopes = pr.AuthorizationServers[0], pr.ScopesSupported
	} else if su, err := url.Parse(serverURL); err == nil { // older servers: the MCP server is its own authorization server
		issuer = su.Scheme + "://" + su.Host
	}
	md, err := discoverAS(ctx, hc, issuer)
	if err != nil {
		return fmt.Errorf("finding the authorization server's details: %w", err)
	}
	if len(scopes) == 0 {
		scopes = md.ScopesSupported
	}
	scope := ch.Scope
	if scope == "" {
		scope = strings.Join(scopes, " ")
	}

	ln, redirect, reg, err := listen(st, issuer)
	if err != nil {
		return fmt.Errorf("can't start the sign-in callback server: %w", err)
	}
	defer ln.Close()

	clientID := reg.ClientID
	if clientID == "" {
		if md.RegistrationEndpoint == "" {
			return errors.New("this server doesn't support automatic client registration, so werkz can't sign in to it")
		}
		if clientID, err = register(ctx, hc, md.RegistrationEndpoint, redirect); err != nil {
			return err
		}
		err = st.update(func(d *fileData) {
			if d.Clients == nil {
				d.Clients = map[string]*clientReg{}
			}
			d.Clients[issuer] = &clientReg{ClientID: clientID, RedirectURI: redirect, CreatedAt: now().UTC()}
		})
		if err != nil {
			return err
		}
	}

	verifier := randString(64)
	state := randString(32)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"resource":              {serverURL},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	authURL, err := withQuery(md.AuthorizationEndpoint, q)
	if err != nil {
		return err
	}

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		first := false
		once.Do(func() { first = true })
		if !first {
			http.Error(w, "this sign-in link was already used", http.StatusGone)
			return
		}
		v := r.URL.Query()
		var res result
		switch {
		case subtle.ConstantTimeCompare([]byte(v.Get("state")), []byte(state)) != 1:
			res.err = errors.New("the sign-in reply didn't match this request (state mismatch); try again")
		case v.Get("iss") != "" && v.Get("iss") != issuer:
			res.err = errors.New("the sign-in reply came from a different authorization server; try again")
		case v.Get("error") == "access_denied":
			res.err = errors.New("sign-in was cancelled")
		case v.Get("error") != "":
			res.err = fmt.Errorf("sign-in failed: %s %s", clip(v.Get("error")), clip(v.Get("error_description")))
		case v.Get("code") == "":
			res.err = errors.New("the sign-in reply had no authorization code")
		default:
			res.code = v.Get("code")
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		if res.err != nil {
			fmt.Fprintf(w, resultPage, "Sign-in didn't complete", html.EscapeString(res.err.Error())+" You can close this tab and try again in werkz.")
		} else {
			fmt.Fprintf(w, resultPage, "You're signed in to werkz", "You can close this tab and go back to your terminal.")
		}
		done <- res
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln) //nolint:errcheck // stops when the listener is closed on return
	defer srv.Close()

	if open != nil {
		_ = open(authURL) // the caller can show the URL too, in case no browser opens
	}

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(loginTimeout):
		return errors.New("sign-in timed out")
	}
	if res.err != nil {
		return res.err
	}

	tr, err := tokenRequest(ctx, hc, md.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {res.code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirect},
		"resource":      {serverURL},
	})
	if err != nil {
		var te *tokenError
		if errors.As(err, &te) && te.Code == "invalid_client" { // the stored registration is no good: register again next time
			_ = st.update(func(d *fileData) { delete(d.Clients, issuer) })
		}
		return err
	}
	e := &serverEntry{Issuer: issuer, TokenEndpoint: md.TokenEndpoint, ClientID: clientID, Resource: serverURL}
	tr.apply(e)
	return st.update(func(d *fileData) {
		if d.Servers == nil {
			d.Servers = map[string]*serverEntry{}
		}
		d.Servers[serverURL] = e
	})
}

// listen binds 127.0.0.1 (never another interface), preferring the port of the stored registration so its
// redirect URI stays valid, else any free port. reg is the stored registration for the issuer, if any.
func listen(st *Store, issuer string) (ln net.Listener, redirect string, reg clientReg, err error) {
	reg, _ = st.client(issuer)
	if reg.ClientID != "" {
		if u, perr := url.Parse(reg.RedirectURI); perr == nil && u.Hostname() == "127.0.0.1" && u.Port() != "" {
			ln, _ = net.Listen("tcp", "127.0.0.1:"+u.Port()) // busy: fall back below
		}
	}
	if ln == nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, "", reg, err
		}
		if reg.ClientID != "" { // can't reuse the old redirect URI: register fresh
			reg = clientReg{}
		}
	}
	redirect = "http://127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port) + callbackPath
	return ln, redirect, reg, nil
}

func withQuery(base string, q url.Values) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	existing := u.Query()
	for k, v := range q {
		existing[k] = v
	}
	u.RawQuery = existing.Encode()
	return u.String(), nil
}

// randString returns n random URL-safe characters (n >= 43 for a PKCE verifier).
func randString(n int) string {
	b := make([]byte, (n*3+3)/4)
	if _, err := rand.Read(b); err != nil {
		panic("mcpauth: no randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}
