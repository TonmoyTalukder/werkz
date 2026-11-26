// Package mcpauth signs werkz in to remote (HTTP) MCP servers that use OAuth, following the MCP
// authorization spec: RFC 9728 protected resource metadata, RFC 8414 authorization server metadata
// (with an OpenID configuration fallback), RFC 7591 dynamic client registration, PKCE (S256) and RFC 8707
// resource indicators, with a loopback redirect.
package mcpauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// now is the clock; tests may replace it.
var now = time.Now

// serverEntry is the sign-in for one MCP server URL.
type serverEntry struct {
	Issuer        string    `json:"issuer,omitempty"`
	TokenEndpoint string    `json:"token_endpoint,omitempty"`
	ClientID      string    `json:"client_id,omitempty"`
	Resource      string    `json:"resource,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	AccessToken   string    `json:"access_token,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitzero"` // zero: the server gave no lifetime
}

// clientReg is a dynamically registered OAuth client, kept per authorization server issuer.
type clientReg struct {
	ClientID    string    `json:"client_id"`
	RedirectURI string    `json:"redirect_uri,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type fileData struct {
	Servers map[string]*serverEntry `json:"servers,omitempty"`
	Clients map[string]*clientReg   `json:"clients,omitempty"`
}

// Store keeps tokens and registered clients per MCP server URL in a 0600 JSON file.
type Store struct {
	path string

	mu         sync.Mutex // guards the file
	refreshMu  sync.Mutex // serialises token refreshes
	chMu       sync.Mutex
	challenges map[string]challenge // last 401 challenge seen per server URL (memory only)
}

// DefaultPath is <os.UserConfigDir()>/werkz/mcp-auth.json.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "werkz", "mcp-auth.json")
}

// OpenStore returns a store backed by path ("" means DefaultPath). The file is created on first write.
func OpenStore(path string) *Store {
	if path == "" {
		path = DefaultPath()
	}
	return &Store{path: path, challenges: map[string]challenge{}}
}

func (st *Store) load() (*fileData, error) {
	d := &fileData{}
	b, err := os.ReadFile(st.path)
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, d); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON", st.path) // never echo contents: they hold tokens
	}
	return d, nil
}

// save writes atomically with owner-only permissions.
func (st *Store) save(d *fileData) error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(st.path), ".mcp-auth-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
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
	return os.Rename(tmp.Name(), st.path)
}

// update runs fn on the file's contents and saves the result.
func (st *Store) update(fn func(*fileData)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	d, err := st.load()
	if err != nil {
		return err
	}
	fn(d)
	return st.save(d)
}

func (st *Store) entry(serverURL string) (serverEntry, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	d, err := st.load()
	if err != nil || d.Servers[serverURL] == nil {
		return serverEntry{}, false
	}
	return *d.Servers[serverURL], true
}

func (st *Store) client(issuer string) (clientReg, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	d, err := st.load()
	if err != nil || d.Clients[issuer] == nil {
		return clientReg{}, false
	}
	return *d.Clients[issuer], true
}

func (st *Store) setChallenge(serverURL string, c challenge) {
	st.chMu.Lock()
	defer st.chMu.Unlock()
	st.challenges[serverURL] = c
}

func (st *Store) getChallenge(serverURL string) (challenge, bool) {
	st.chMu.Lock()
	defer st.chMu.Unlock()
	c, ok := st.challenges[serverURL]
	return c, ok
}

// HasToken reports whether there is a stored sign-in (access or refresh token) for the server.
func (st *Store) HasToken(serverURL string) bool {
	e, ok := st.entry(serverURL)
	return ok && (e.AccessToken != "" || e.RefreshToken != "")
}

// Logout forgets the server's tokens; its client registration is kept for the next sign-in.
func Logout(serverURL string, st *Store) error {
	return st.update(func(d *fileData) { delete(d.Servers, serverURL) })
}
