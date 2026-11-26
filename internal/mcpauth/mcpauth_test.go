package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake is both the MCP resource (at /mcp) and its authorization server.
type fake struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	noChallenge bool   // 401 without resource_metadata; metadata only at the well-known path
	badState    bool   // /authorize redirects with the wrong state
	issuer      string // overrides the issuer in the metadata
	tokenEP     string // overrides token_endpoint
	expiresIn   int
	registered  map[string]string // client_id -> redirect_uri
	codes       map[string]codeInfo
	access      map[string]bool
	refresh     map[string]bool
	n           int
	registers   int
	refreshes   int
	mcpCalls    int
	scopeSeen   string
}

type codeInfo struct{ challenge, redirect, resource, client string }

func newFake(t *testing.T) *fake {
	f := &fake{t: t, expiresIn: 3600, registered: map[string]string{}, codes: map[string]codeInfo{},
		access: map[string]bool{}, refresh: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", f.mcp)
	mux.HandleFunc("/.well-known/oauth-protected-resource", f.prm)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", f.prm)
	mux.HandleFunc("/.well-known/oauth-authorization-server", f.meta)
	mux.HandleFunc("/register", f.register)
	mux.HandleFunc("/authorize", f.authorize)
	mux.HandleFunc("/token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) mcpURL() string { return f.srv.URL + "/mcp" }

func (f *fake) mcp(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcpCalls++
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok != "" && f.access[tok] {
		_, _ = io.WriteString(w, `{"ok":true}`)
		return
	}
	h := `Bearer`
	if !f.noChallenge {
		h = fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource", scope="mcp"`, f.srv.URL)
	}
	w.Header().Set("WWW-Authenticate", h)
	w.WriteHeader(http.StatusUnauthorized)
}

func (f *fake) prm(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource": f.mcpURL(), "authorization_servers": []string{f.srv.URL}, "scopes_supported": []string{"mcp", "other"},
	})
}

func (f *fake) meta(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	iss, tep := f.srv.URL, f.srv.URL+"/token"
	if f.issuer != "" {
		iss = f.issuer
	}
	if f.tokenEP != "" {
		tep = f.tokenEP
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": iss, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": tep,
		"registration_endpoint": f.srv.URL + "/register", "code_challenge_methods_supported": []string{"S256"},
	})
}

func (f *fake) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string   `json:"client_name"`
		Redirect []string `json:"redirect_uris"`
		Grants   []string `json:"grant_types"`
		Auth     string   `json:"token_endpoint_auth_method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Name != "werkz" || req.Auth != "none" || len(req.Redirect) != 1 || len(req.Grants) != 2 ||
		!strings.HasPrefix(req.Redirect[0], "http://127.0.0.1:") || !strings.HasSuffix(req.Redirect[0], "/callback") {
		http.Error(w, `{"error":"invalid_client_metadata"}`, 400)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registers++
	id := fmt.Sprintf("client-%d", f.registers)
	f.registered[id] = req.Redirect[0]
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]string{"client_id": id})
}

func (f *fake) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" ||
		f.registered[q.Get("client_id")] != q.Get("redirect_uri") || q.Get("resource") != f.mcpURL() || len(q.Get("code_challenge")) < 43 {
		http.Error(w, "bad authorize request", 400)
		return
	}
	f.scopeSeen = q.Get("scope")
	f.n++
	code := fmt.Sprintf("code-%d", f.n)
	f.codes[code] = codeInfo{q.Get("code_challenge"), q.Get("redirect_uri"), q.Get("resource"), q.Get("client_id")}
	state := q.Get("state")
	if f.badState {
		state = "forged"
	}
	http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(state), http.StatusFound)
}

func (f *fake) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(code string) {
		w.WriteHeader(400)
		// The body deliberately carries a secret-looking field; errors must never echo it.
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "refresh_token": "LEAKED-SECRET"})
	}
	if r.PostForm.Get("resource") != f.mcpURL() {
		fail("invalid_target")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		ci, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != ci.challenge || ci.redirect != r.PostForm.Get("redirect_uri") ||
			ci.client != r.PostForm.Get("client_id") || len(r.PostForm.Get("code_verifier")) < 43 {
			fail("invalid_grant")
			return
		}
	case "refresh_token":
		rt := r.PostForm.Get("refresh_token")
		if !f.refresh[rt] {
			fail("invalid_grant")
			return
		}
		delete(f.refresh, rt)
		f.refreshes++
	default:
		fail("unsupported_grant_type")
		return
	}
	f.n++
	at, rt := fmt.Sprintf("access-%d", f.n), fmt.Sprintf("refresh-%d", f.n)
	f.access[at], f.refresh[rt] = true, true
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": at, "refresh_token": rt, "token_type": "Bearer", "expires_in": f.expiresIn})
}

// browser performs the GET on the authorize URL like a browser would, following redirects to the callback.
func browser(t *testing.T) func(string) error {
	return func(u string) error {
		resp, err := http.Get(u)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
}

func newStore(t *testing.T) *Store {
	return OpenStore(filepath.Join(t.TempDir(), "cfg", "mcp-auth.json"))
}

func login(t *testing.T, f *fake, st *Store) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return Login(ctx, f.mcpURL(), st, browser(t))
}

func get(t *testing.T, rt http.RoundTripper, u string) (*http.Response, error) {
	c := &http.Client{Transport: rt, Timeout: 10 * time.Second}
	return c.Get(u)
}

func TestLoginAndTransport(t *testing.T) {
	f, st := newFake(t), newStore(t)
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	if !st.HasToken(f.mcpURL()) {
		t.Fatal("no token stored")
	}
	if f.scopeSeen != "mcp" {
		t.Errorf("scope = %q, want mcp (from WWW-Authenticate)", f.scopeSeen)
	}
	resp, err := get(t, Transport(f.mcpURL(), nil, st), f.mcpURL())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d, token not sent", resp.StatusCode)
	}
}

func TestLoginWithoutChallengeUsesWellKnown(t *testing.T) {
	f, st := newFake(t), newStore(t)
	f.noChallenge = true
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	if f.scopeSeen != "mcp other" {
		t.Errorf("scope = %q, want scopes_supported", f.scopeSeen)
	}
}

func TestFileModes(t *testing.T) {
	f, st := newFake(t), newStore(t)
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(st.path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(st.path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", di.Mode().Perm())
	}
}

func TestExpiredTokenRefreshes(t *testing.T) {
	f, st := newFake(t), newStore(t)
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	old, _ := st.entry(f.mcpURL())
	if err := st.update(func(d *fileData) { d.Servers[f.mcpURL()].ExpiresAt = now().Add(-time.Minute) }); err != nil {
		t.Fatal(err)
	}
	resp, err := get(t, Transport(f.mcpURL(), nil, st), f.mcpURL())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || f.refreshes != 1 {
		t.Fatalf("status %d, refreshes %d", resp.StatusCode, f.refreshes)
	}
	e, _ := st.entry(f.mcpURL())
	if e.AccessToken == old.AccessToken || e.RefreshToken == old.RefreshToken {
		t.Error("tokens were not rotated in the store")
	}
}

func TestRejectedTokenRefreshesOnceThenRetries(t *testing.T) {
	f, st := newFake(t), newStore(t)
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	e, _ := st.entry(f.mcpURL())
	f.mu.Lock()
	delete(f.access, e.AccessToken) // revoked server-side, still looks valid locally
	f.mcpCalls = 0
	f.mu.Unlock()
	body := strings.NewReader(`{"x":1}`)
	req, _ := http.NewRequest("POST", f.mcpURL(), body)
	resp, err := (&http.Client{Transport: Transport(f.mcpURL(), nil, st)}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || f.refreshes != 1 || f.mcpCalls != 2 {
		t.Fatalf("status %d refreshes %d calls %d", resp.StatusCode, f.refreshes, f.mcpCalls)
	}
}

func TestRefreshFailureNeedsAuthAndLeaksNothing(t *testing.T) {
	f, st := newFake(t), newStore(t)
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	e, _ := st.entry(f.mcpURL())
	f.mu.Lock()
	f.access, f.refresh = map[string]bool{}, map[string]bool{}
	f.mu.Unlock()
	_, err := get(t, Transport(f.mcpURL(), nil, st), f.mcpURL())
	if !errors.Is(err, ErrNeedsAuth) {
		t.Fatalf("err = %v, want ErrNeedsAuth", err)
	}
	for _, secret := range []string{e.AccessToken, e.RefreshToken, "LEAKED-SECRET"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks %q: %v", secret, err)
		}
	}
	if st.HasToken(f.mcpURL()) {
		t.Error("dead tokens were kept")
	}
}

func TestNoTokenNeedsAuth(t *testing.T) {
	f, st := newFake(t), newStore(t)
	_, err := get(t, Transport(f.mcpURL(), nil, st), f.mcpURL())
	if !errors.Is(err, ErrNeedsAuth) {
		t.Fatalf("err = %v, want ErrNeedsAuth", err)
	}
	if c, ok := st.getChallenge(f.mcpURL()); !ok || c.Scope != "mcp" || c.ResourceMetadata == "" {
		t.Errorf("challenge not kept: %+v", c)
	}
	// And Login can use it.
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
}

func TestOpenServerWorksWithoutToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("sent an Authorization header with no token")
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	resp, err := get(t, Transport(srv.URL, nil, newStore(t)), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestTokenNotSentToOtherHosts(t *testing.T) {
	f, st := newFake(t), newStore(t)
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("token leaked to another host")
		}
	}))
	defer other.Close()
	resp, err := get(t, Transport(f.mcpURL(), nil, st), other.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestLogoutKeepsRegistration(t *testing.T) {
	f, st := newFake(t), newStore(t)
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	if err := Logout(f.mcpURL(), st); err != nil {
		t.Fatal(err)
	}
	if st.HasToken(f.mcpURL()) {
		t.Fatal("token survived logout")
	}
	if err := login(t, f, st); err != nil {
		t.Fatal(err)
	}
	if f.registers != 1 {
		t.Errorf("registered %d times, want the stored registration reused", f.registers)
	}
	if !st.HasToken(f.mcpURL()) {
		t.Error("second login stored no token")
	}
}

func TestWrongStateRejected(t *testing.T) {
	f, st := newFake(t), newStore(t)
	f.badState = true
	err := login(t, f, st)
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("err = %v, want state mismatch", err)
	}
	if st.HasToken(f.mcpURL()) {
		t.Error("token stored after a state mismatch")
	}
}

func TestIssuerMismatchRejected(t *testing.T) {
	f, st := newFake(t), newStore(t)
	f.issuer = "https://evil.example"
	err := login(t, f, st)
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("err = %v, want issuer mismatch", err)
	}
	if f.registers != 0 {
		t.Error("registered with a server whose metadata was rejected")
	}
}

func TestInsecureEndpointRejected(t *testing.T) {
	f, st := newFake(t), newStore(t)
	f.tokenEP = "http://example.com/token"
	err := login(t, f, st)
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v, want https refusal", err)
	}
	for u, ok := range map[string]bool{
		"https://a.example/x": true, "http://127.0.0.1:1/x": true, "http://localhost/x": true,
		"http://[::1]:5/x": true, "http://a.example/x": false, "ftp://127.0.0.1/x": false, "/relative": false,
	} {
		if (checkEndpoint(u) == nil) != ok {
			t.Errorf("checkEndpoint(%q) ok=%v, want %v", u, !ok, ok)
		}
	}
}

func TestLoginStopsOnContext(t *testing.T) {
	f, st := newFake(t), newStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := Login(ctx, f.mcpURL(), st, func(string) error { return errors.New("no browser") })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallbackAcceptsOnlyOne(t *testing.T) {
	f, st := newFake(t), newStore(t)
	var second int
	open := func(u string) error {
		resp, err := http.Get(u)
		if err != nil {
			return err
		}
		redirect := resp.Request.URL.String() // the callback URL that was hit
		resp.Body.Close()
		r2, err := http.Get(redirect)
		if err == nil {
			second = r2.StatusCode
			r2.Body.Close()
		}
		return nil
	}
	if err := Login(context.Background(), f.mcpURL(), st, open); err != nil {
		t.Fatal(err)
	}
	if second == http.StatusOK {
		t.Error("callback accepted twice")
	}
}

func TestParseChallenge(t *testing.T) {
	c := parseChallenge([]string{`Basic realm="x"`, `Bearer realm="a", resource_metadata="https://h/.well-known/oauth-protected-resource", scope="read write", error=insufficient_scope`})
	want := challenge{ResourceMetadata: "https://h/.well-known/oauth-protected-resource", Scope: "read write", Error: "insufficient_scope"}
	if c != want {
		t.Errorf("got %+v", c)
	}
	if c := parseChallenge([]string{"Bearer"}); c != (challenge{}) {
		t.Errorf("bare Bearer: %+v", c)
	}
}

func TestVerifierStrength(t *testing.T) {
	a, b := randString(64), randString(64)
	if len(a) < 43 || a == b {
		t.Errorf("weak verifier %q", a)
	}
}
