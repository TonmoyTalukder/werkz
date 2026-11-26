package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
)

const (
	defaultUserAgent = "werkz (+https://tonmoytalukder.github.io/werkz/)"
	defaultMaxBytes  = 5 << 20
	fetchTimeout     = 30 * time.Second
	maxRedirects     = 5
	acceptHeader     = "text/html, text/markdown, text/plain, application/json;q=0.9, */*;q=0.5"
)

// Fetcher downloads web pages as readable text.
type Fetcher struct {
	LocalHost string                                                   // the one hostname the user approved on the local network ("" = none); redirects to other hosts stay refused
	Client    *http.Client                                             // nil = built per call with the safety dialer; a custom client brings its own checks
	UserAgent string                                                   // "" = defaultUserAgent
	MaxBytes  int64                                                    // body cap, 0 = 5 MB
	Resolver  func(ctx context.Context, host string) ([]net.IP, error) // nil = net.DefaultResolver
}

// Fetch gets rawURL and returns it as Markdown (HTML) or text.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	u, err := parseWebURL(rawURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	client := f.Client
	if client == nil {
		client = f.client()
		defer client.CloseIdleConnections()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	ua := f.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", acceptHeader)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	limit := f.MaxBytes
	if limit <= 0 {
		limit = defaultMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", u, err)
	}
	page := &Page{URL: resp.Request.URL.String(), Bytes: len(body)}
	if int64(len(body)) > limit {
		body = body[:limit]
		page.Bytes = len(body)
		page.Truncated = true
	}

	header := resp.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(header)
	if mediaType == "" {
		mediaType = strings.SplitN(http.DetectContentType(body), ";", 2)[0]
	}
	page.ContentType = mediaType

	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		r := decode(body, header, params["charset"], true)
		title, md, err := HTMLToMarkdownContext(ctx, r, resp.Request.URL)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", u, err)
		}
		page.Title = title
		page.Markdown = withTitle(title, md)
	case isTextType(mediaType):
		text, _ := io.ReadAll(decode(body, header, params["charset"], false))
		page.Markdown = strings.ToValidUTF8(string(text), "�")
	default:
		return nil, fmt.Errorf("%s isn't text; WebFetch reads web pages and text", mediaType)
	}
	return page, nil
}

// withTitle puts the page title on top unless the content already starts with it as a heading.
func withTitle(title, md string) string {
	if title == "" {
		return md
	}
	first, _, _ := strings.Cut(md, "\n")
	if strings.TrimSpace(strings.TrimLeft(first, "#")) == title && strings.HasPrefix(first, "#") {
		return md
	}
	return "# " + title + "\n\n" + md
}

func isTextType(t string) bool {
	switch {
	case strings.HasPrefix(t, "text/"):
		return true
	case strings.HasSuffix(t, "+json"), strings.HasSuffix(t, "+xml"):
		return true
	}
	switch t {
	case "application/json", "application/xml", "application/javascript", "application/x-javascript",
		"application/ecmascript", "application/x-ndjson", "application/yaml", "application/x-yaml":
		return true
	}
	return false
}

// decode converts the body to UTF-8: HTML honors its own <meta> charset too; other text only an
// explicit non-UTF-8 charset in the header. Unknown charsets pass through unchanged.
func decode(body []byte, header, declared string, isHTML bool) io.Reader {
	plain := bytes.NewReader(body)
	utf8 := declared == "" || strings.EqualFold(declared, "utf-8") || strings.EqualFold(declared, "utf8")
	if utf8 && !isHTML {
		return plain
	}
	if r, err := charset.NewReader(plain, header); err == nil {
		return r
	}
	return bytes.NewReader(body)
}

// parseWebURL accepts only plain http(s) URLs with a host and no embedded credentials.
func parseWebURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q: only http and https can be fetched", u.Scheme)
	}
	if u.User != nil {
		return nil, errors.New("URLs with a user name or password aren't fetched")
	}
	if u.Hostname() == "" {
		return nil, errors.New("URL has no host")
	}
	return u, nil
}

// client builds an HTTP client whose connections all go through the safety dialer. There is no
// proxy: it would connect on our behalf and bypass the address check.
func (f *Fetcher) client() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         f.dial,
			TLSHandshakeTimeout: 10 * time.Second,
			ForceAttemptHTTP2:   true,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to %q refused: only http and https can be fetched", req.URL.Scheme)
			}
			return nil // the address is checked by the dialer when the redirect connects
		},
	}
}

// dial resolves the host itself, refuses local addresses unless allowed, and connects to the IP it
// checked. Resolving once and dialing that IP is what stops DNS rebinding.
func (f *Fetcher) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	approved := f.LocalHost != "" && strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(f.LocalHost, "."))
	if !approved && isLocalName(host) {
		return nil, localErr(host)
	}
	ips, err := lookup(ctx, f.Resolver, host)
	if err != nil {
		return nil, err
	}
	if !approved {
		for _, ip := range ips {
			if isLocalIP(ip) {
				return nil, localErr(host)
			}
		}
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func localErr(host string) error {
	return fmt.Errorf("refusing to connect to %s: it is on the local network", host)
}

// lookup resolves host; IP literals skip the resolver.
func lookup(ctx context.Context, resolve func(context.Context, string) ([]net.IP, error), host string) ([]net.IP, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []net.IP{net.IP(a.WithZone("").AsSlice())}, nil
	}
	var ips []net.IP
	var err error
	if resolve != nil {
		ips, err = resolve(ctx, host)
	} else {
		ips, err = net.DefaultResolver.LookupIP(ctx, "ip", host)
	}
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses found for %s", host)
	}
	return ips, nil
}

// IsLocal reports whether a URL's host is on the local network: localhost, *.localhost, *.local,
// *.internal, or any resolved address that is loopback, private, link-local (including cloud
// metadata), CGNAT, unspecified or multicast. Unresolvable hosts are not local.
func IsLocal(ctx context.Context, rawURL string, resolve func(context.Context, string) ([]net.IP, error)) (bool, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false, fmt.Errorf("invalid URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return false, errors.New("URL has no host")
	}
	if isLocalName(host) {
		return true, nil
	}
	ips, err := lookup(ctx, resolve, host)
	if err != nil {
		return false, nil //nolint:nilerr // an unresolvable host can't be reached, so it isn't local
	}
	for _, ip := range ips {
		if isLocalIP(ip) {
			return true, nil
		}
	}
	return false, nil
}

func isLocalName(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	return h == "localhost" || strings.HasSuffix(h, ".localhost") ||
		strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal")
}

var (
	cgnat     = netip.MustParsePrefix("100.64.0.0/10")
	thisNet   = netip.MustParsePrefix("0.0.0.0/8")
	reserved  = netip.MustParsePrefix("240.0.0.0/4")
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour = netip.MustParsePrefix("2002::/16")
)

func isLocalIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true // not an address we can judge: refuse
	}
	return localAddr(a)
}

func localAddr(a netip.Addr) bool {
	a = a.WithZone("").Unmap() // an IPv4 address written as IPv6 (::ffff:10.0.0.1) is judged as IPv4
	if a.Is4() {
		return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
			a.IsMulticast() || a.IsUnspecified() || cgnat.Contains(a) || thisNet.Contains(a) || reserved.Contains(a)
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	// IPv6 forms that carry an IPv4 address: judge the IPv4 address inside.
	b := a.As16()
	switch {
	case nat64.Contains(a): // 64:ff9b::/96, last 32 bits
		return localAddr(netip.AddrFrom4([4]byte(b[12:16])))
	case sixToFour.Contains(a): // 2002::/16, bits 16-48
		return localAddr(netip.AddrFrom4([4]byte(b[2:6])))
	case [12]byte(b[:12]) == [12]byte{}: // IPv4-compatible ::a.b.c.d
		return localAddr(netip.AddrFrom4([4]byte(b[12:16])))
	}
	return false
}

// Paginate returns up to limit characters of md starting at the character (rune) offset, and the
// offset to continue from (0 when the end was reached). Counting runes never splits a character.
func Paginate(md string, offset, limit int) (chunk string, next int) {
	r := []rune(md)
	offset = min(max(offset, 0), len(r))
	if offset >= len(r) {
		return "", 0
	}
	if limit <= 0 || offset+limit >= len(r) {
		return string(r[offset:]), 0
	}
	return string(r[offset : offset+limit]), offset + limit
}
