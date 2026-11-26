package chatgpt

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
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

// fakeAuth is a stand-in for OpenAI's account server.
type fakeAuth struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu           sync.Mutex
	nonce        string
	refreshCount int
	refreshErr   string // error code to return for refresh_token grants
	revoked      []string
	scope        string
	lastAuth     url.Values
}

func newFakeAuth(t *testing.T) *fakeAuth {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAuth{t: t, key: key, scope: "openid profile email offline_access resource.invoke " + PlanScope}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "jwks_uri": f.srv.URL + "/jwks",
			"revocation_endpoint": f.srv.URL + "/revoke"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kid": "k1", "kty": "RSA",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("/api/accounts/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Form.Get("resource") != Resource || r.Form.Get("client_id") != "issued-client" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_client"}`)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") == "" ||
				!strings.HasPrefix(r.Form.Get("redirect_uri"), "http://127.0.0.1:") {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_grant"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "refresh_token": "refresh-1",
				"id_token": f.idToken(f.nonce), "token_type": "Bearer", "expires_in": 3600, "scope": f.scope})
		case "refresh_token":
			if f.refreshErr != "" {
				w.WriteHeader(400)
				fmt.Fprintf(w, `{"error":%q}`, f.refreshErr)
				return
			}
			f.refreshCount++
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("access-%d", f.refreshCount+1),
				"refresh_token": fmt.Sprintf("refresh-%d", f.refreshCount+1), "token_type": "Bearer", "expires_in": 3600})
		}
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		f.mu.Unlock()
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAuth) idToken(nonce string) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	head := enc(map[string]string{"alg": "RS256", "kid": "k1"})
	body := enc(map[string]any{"iss": f.srv.URL, "sub": "user-123", "aud": "issued-client",
		"exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "email": "dev@example.com"})
	sum := sha256.Sum256([]byte(head + "." + body))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// browser plays the user: it "approves" the sign-in by calling werkz's callback.
func (f *fakeAuth) browser(u string) error {
	pu, err := url.Parse(u)
	if err != nil {
		return err
	}
	q := pu.Query()
	f.mu.Lock()
	f.nonce, f.lastAuth = q.Get("nonce"), q
	f.mu.Unlock()
	go func() {
		cb := q.Get("redirect_uri") + "?" + url.Values{"code": {"the-code"}, "state": {q.Get("state")},
			"client_id": {"issued-client"}, "scope": {f.scope}}.Encode()
		resp, err := http.Get(cb)
		if err == nil {
			resp.Body.Close()
		}
	}()
	return nil
}

func setup(t *testing.T) *fakeAuth {
	t.Setenv("WERKZ_CHATGPT_FILE", filepath.Join(t.TempDir(), "chatgpt.json"))
	f := newFakeAuth(t)
	old := AuthBase
	AuthBase = f.srv.URL
	t.Cleanup(func() { AuthBase = old })
	return f
}

func login(t *testing.T, f *fakeAuth) *Credentials {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Login(ctx, f.browser, nil)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

func TestLoginStoresVerifiedCredentials(t *testing.T) {
	f := setup(t)
	c := login(t, f)
	if c.Email != "dev@example.com" || c.Subject != "user-123" || c.ClientID != "issued-client" || !c.SignedIn() {
		t.Fatalf("credentials: %+v", c)
	}
	q := f.lastAuth
	if q.Get("client_id") != "dynamic_agent_client" || q.Get("agent_name_hint") != "werkz" ||
		q.Get("code_challenge_method") != "S256" || q.Get("resource") != Resource ||
		!strings.Contains(q.Get("scope"), PlanScope) || !strings.HasPrefix(q.Get("ext_agent_host_id"), "urn:uuid:") ||
		!strings.HasSuffix(q.Get("redirect_uri"), "/auth/callback") {
		t.Fatalf("authorize request: %v", q)
	}
	st, err := os.Stat(Path())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file: %v %v", st.Mode(), err)
	}
	if !SignedIn() {
		t.Fatal("SignedIn() should see the stored sign-in")
	}

	// The second sign-in reuses the issued client and the same host ID.
	host := c.HostID
	login(t, f)
	if f.lastAuth.Get("client_id") != "issued-client" || f.lastAuth.Get("agent_name_hint") != "" ||
		f.lastAuth.Get("ext_agent_host_id") != host || f.lastAuth.Get("id_token_hint") != "" {
		t.Fatalf("re-authorization: %v", f.lastAuth)
	}
}

func TestLoginRejectsMissingPlanScope(t *testing.T) {
	f := setup(t)
	f.scope = "openid profile email"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Login(ctx, f.browser, nil); err == nil || !strings.Contains(err.Error(), "plan usage") {
		t.Fatalf("expected a missing-scope error, got %v", err)
	}
}

func TestTokenRefreshesAndRotates(t *testing.T) {
	f := setup(t)
	login(t, f)
	var s Session
	tok, err := s.Token(context.Background())
	if err != nil || tok != "access-1" {
		t.Fatalf("fresh token: %q %v", tok, err)
	}
	c, _ := Load()
	c.ExpiresAt = time.Now().Add(-time.Minute)
	_ = save(c)
	if tok, err = s.Token(context.Background()); err != nil || tok != "access-2" {
		t.Fatalf("refreshed token: %q %v", tok, err)
	}
	if c, _ = Load(); c.RefreshToken != "refresh-2" {
		t.Fatalf("rotated refresh token not stored: %q", c.RefreshToken)
	}
	if f.refreshCount != 1 {
		t.Fatalf("refreshes: %d", f.refreshCount)
	}
}

func TestTerminalRefreshErrorSignsOut(t *testing.T) {
	f := setup(t)
	login(t, f)
	f.refreshErr = "refresh_token_reused"
	var s Session
	if err := s.ForceRefresh(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	c, _ := Load()
	if c.SignedIn() || c.ClientID != "issued-client" || c.HostID == "" {
		t.Fatalf("after a terminal refresh error the tokens go, the client and host stay: %+v", c)
	}
}

func TestLogoutRevokes(t *testing.T) {
	f := setup(t)
	login(t, f)
	if err := Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, _ := Load()
	if c.SignedIn() || c.RefreshToken != "" || c.ClientID == "" || len(f.revoked) != 1 || f.revoked[0] != "refresh-1" {
		t.Fatalf("logout: %+v revoked=%v", c, f.revoked)
	}
}

func TestBadIDTokenSignatureIsRejected(t *testing.T) {
	f := setup(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.key = other // signs with a key that isn't in the published set
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Login(ctx, f.browser, nil); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected a signature error, got %v", err)
	}
}
