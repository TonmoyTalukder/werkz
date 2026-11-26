package mcpauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNeedsAuth means the server wants a sign-in (401 with no usable token); errors.Is works through wrapping.
var ErrNeedsAuth = errors.New("this server needs you to sign in")

// needsAuthError carries the server's WWW-Authenticate details for Login; it matches ErrNeedsAuth.
type needsAuthError struct{ ch challenge }

func (*needsAuthError) Error() string        { return ErrNeedsAuth.Error() }
func (*needsAuthError) Is(target error) bool { return target == ErrNeedsAuth }

// expirySkew is how long before expiry a token is treated as expired.
const expirySkew = 30 * time.Second

// challenge is what a 401's WWW-Authenticate: Bearer header says.
type challenge struct {
	ResourceMetadata string
	Scope            string
	Error            string
}

// Transport adds the server's access token (Authorization: Bearer) to requests for serverURL, refreshes it
// with the refresh token when expired or on a 401 (once, then retries the request), and returns ErrNeedsAuth
// when there is no usable token and the server answers 401. Requests without a token are still sent.
func Transport(serverURL string, base http.RoundTripper, st *Store) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	u, _ := url.Parse(serverURL)
	return &transport{server: serverURL, origin: u, base: base, st: st}
}

type transport struct {
	server string
	origin *url.URL
	base   http.RoundTripper
	st     *Store
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Never hand the token to another host (e.g. after a redirect).
	if t.origin == nil || !strings.EqualFold(req.URL.Scheme, t.origin.Scheme) || !strings.EqualFold(req.URL.Host, t.origin.Host) {
		return t.base.RoundTrip(req)
	}
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil { // keep the body so a retry can resend it
		b, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
		req.Body, _ = req.GetBody()
	}
	send := func(tok string) (*http.Response, error) {
		r := req.Clone(req.Context())
		if req.GetBody != nil {
			r.Body, _ = req.GetBody()
		}
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		return t.base.RoundTrip(r)
	}
	hc := &http.Client{Transport: t.base, Timeout: httpTimeout, CheckRedirect: checkRedirect}

	tok, err := t.st.accessToken(req.Context(), hc, t.server)
	if err != nil {
		return nil, err
	}
	resp, err := send(tok)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	ch := parseChallenge(resp.Header.Values("WWW-Authenticate"))
	t.st.setChallenge(t.server, ch)
	drain(resp)
	if tok == "" {
		return nil, fmt.Errorf("%s: %w", t.server, &needsAuthError{ch})
	}
	// A token was sent and refused: refresh once, then retry.
	tok2, err := t.st.refresh(req.Context(), hc, t.server, tok)
	if err != nil {
		return nil, err
	}
	if tok2 == "" {
		return nil, fmt.Errorf("%s: %w", t.server, &needsAuthError{ch})
	}
	resp, err = send(tok2)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch = parseChallenge(resp.Header.Values("WWW-Authenticate"))
		t.st.setChallenge(t.server, ch)
		drain(resp)
		return nil, fmt.Errorf("%s: %w", t.server, &needsAuthError{ch})
	}
	return resp, nil
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
}

// accessToken returns a usable access token for the server, refreshing an expired one. "" means none.
func (st *Store) accessToken(ctx context.Context, hc *http.Client, serverURL string) (string, error) {
	e, ok := st.entry(serverURL)
	if !ok {
		return "", nil
	}
	if e.AccessToken != "" && usable(e) {
		return e.AccessToken, nil
	}
	if e.RefreshToken == "" {
		return "", nil
	}
	return st.refresh(ctx, hc, serverURL, e.AccessToken)
}

func usable(e serverEntry) bool {
	return e.ExpiresAt.IsZero() || now().Add(expirySkew).Before(e.ExpiresAt)
}

// refresh exchanges the refresh token for a new access token. stale is the access token the caller found
// unusable; if another caller already replaced it, that newer token is returned instead of refreshing again.
// "" with a nil error means there is nothing to refresh with (or the server rejected the refresh token, in
// which case the stored tokens are dropped).
func (st *Store) refresh(ctx context.Context, hc *http.Client, serverURL, stale string) (string, error) {
	st.refreshMu.Lock()
	defer st.refreshMu.Unlock()
	e, ok := st.entry(serverURL)
	if !ok {
		return "", nil
	}
	if e.AccessToken != "" && e.AccessToken != stale && usable(e) {
		return e.AccessToken, nil
	}
	if e.RefreshToken == "" {
		return "", nil
	}
	if err := checkEndpoint(e.TokenEndpoint); err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {e.RefreshToken},
		"client_id":     {e.ClientID},
	}
	if e.Resource != "" {
		form.Set("resource", e.Resource)
	}
	tr, err := tokenRequest(ctx, hc, e.TokenEndpoint, form)
	if err != nil {
		var te *tokenError
		if errors.As(err, &te) && te.terminal() {
			_ = st.update(func(d *fileData) { delete(d.Servers, serverURL) })
			return "", nil
		}
		return "", err
	}
	err = st.update(func(d *fileData) {
		cur := d.Servers[serverURL]
		if cur == nil {
			return
		}
		tr.apply(cur)
	})
	if err != nil {
		return "", err
	}
	return tr.AccessToken, nil
}

// parseChallenge reads the Bearer challenge out of WWW-Authenticate header values.
func parseChallenge(values []string) challenge {
	var c challenge
	for _, v := range values {
		s := strings.TrimSpace(v)
		if len(s) < 6 || !strings.EqualFold(s[:6], "bearer") || (len(s) > 6 && s[6] != ' ' && s[6] != '\t') {
			continue
		}
		s = s[6:]
		for {
			s = strings.TrimLeft(s, " \t,")
			eq := strings.IndexByte(s, '=')
			if eq <= 0 || strings.ContainsAny(s[:eq], " \t,") {
				break
			}
			key := strings.ToLower(s[:eq])
			s = s[eq+1:]
			var val string
			if strings.HasPrefix(s, `"`) {
				var sb strings.Builder
				i := 1
				for ; i < len(s) && s[i] != '"'; i++ {
					if s[i] == '\\' && i+1 < len(s) {
						i++
					}
					sb.WriteByte(s[i])
				}
				val, s = sb.String(), s[min(i+1, len(s)):]
			} else {
				end := strings.IndexAny(s, ", \t")
				if end < 0 {
					end = len(s)
				}
				val, s = s[:end], s[end:]
			}
			switch key {
			case "resource_metadata":
				c.ResourceMetadata = val
			case "scope":
				c.Scope = val
			case "error":
				c.Error = val
			}
		}
		if c != (challenge{}) {
			return c
		}
	}
	return c
}
