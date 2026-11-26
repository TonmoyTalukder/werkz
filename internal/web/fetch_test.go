package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func fakeResolver(m map[string]string) func(context.Context, string) ([]net.IP, error) {
	return func(_ context.Context, host string) ([]net.IP, error) {
		if ip, ok := m[host]; ok {
			return []net.IP{net.ParseIP(ip)}, nil
		}
		return nil, fmt.Errorf("no such host %s", host)
	}
}

func TestIsLocal(t *testing.T) {
	resolve := fakeResolver(map[string]string{"intranet.example": "10.0.0.5", "www.example": "93.184.216.34"})
	cases := map[string]bool{
		"http://localhost:8080/":        true,
		"http://app.localhost/":         true,
		"http://printer.local/":         true,
		"http://db.internal/":           true,
		"http://127.0.0.1/":             true,
		"http://10.1.2.3/":              true,
		"http://192.168.1.1/":           true,
		"http://172.16.0.1/":            true,
		"http://169.254.169.254/latest": true,
		"http://100.64.0.1/":            true,
		"http://0.0.0.0/":               true,
		"http://[::1]:3000/":            true,
		"http://[fd00::1]/":             true,
		"http://[fe80::1]/":             true,
		"http://[::ffff:10.0.0.1]/":     true,
		"http://224.0.0.1/":             true,
		"http://93.184.216.34/":         false,
		"http://www.example/":           false,
		"http://intranet.example/":      true,
		"http://nowhere.example/":       false,
	}
	for u, want := range cases {
		got, err := IsLocal(context.Background(), u, resolve)
		if err != nil || got != want {
			t.Errorf("IsLocal(%s) = %v, %v; want %v", u, got, err, want)
		}
	}
}

func TestFetchRejectsBadURLs(t *testing.T) {
	f := &Fetcher{}
	for u, want := range map[string]string{
		"ftp://example.com/x":         "scheme",
		"file:///etc/passwd":          "scheme",
		"http://user:pw@example.com/": "password",
		"http:///path":                "no host",
		"example.com":                 "scheme",
	} {
		if _, err := f.Fetch(context.Background(), u); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Fetch(%q) error = %v; want mention of %q", u, err, want)
		}
	}
}

func serveBody(t *testing.T, contentType, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestFetchHTML(t *testing.T) {
	var gotAccept, gotUA string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept, gotUA = r.Header.Get("Accept"), r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<title>Hi</title><main><p>Hello <a href="/x">there</a></p></main>`)
	}))
	defer s.Close()
	page, err := (&Fetcher{LocalHost: "127.0.0.1"}).Fetch(context.Background(), s.URL+"/a")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Hi\n\nHello [there](" + s.URL + "/x)"
	if page.Markdown != want || page.Title != "Hi" || page.ContentType != "text/html" {
		t.Errorf("page = %+v", page)
	}
	if !strings.HasPrefix(gotAccept, "text/html") || !strings.Contains(gotUA, "werkz") {
		t.Errorf("headers: Accept=%q UA=%q", gotAccept, gotUA)
	}
}

func TestFetchTitleNotRepeated(t *testing.T) {
	s := serveBody(t, "text/html", `<title>Hi</title><main><h1>Hi</h1><p>body</p></main>`)
	page, err := (&Fetcher{LocalHost: "127.0.0.1"}).Fetch(context.Background(), s.URL)
	if err != nil || page.Markdown != "# Hi\n\nbody" {
		t.Errorf("got %q, %v", page.Markdown, err)
	}
}

func TestFetchLatin1(t *testing.T) {
	s := serveBody(t, "text/plain; charset=iso-8859-1", "caf\xe9")
	page, err := (&Fetcher{LocalHost: "127.0.0.1"}).Fetch(context.Background(), s.URL)
	if err != nil || page.Markdown != "café" {
		t.Errorf("got %q, %v", page.Markdown, err)
	}
}

func TestFetchRefusesLocalByDefault(t *testing.T) {
	s := serveBody(t, "text/plain", "secret")
	_, err := (&Fetcher{}).Fetch(context.Background(), s.URL)
	if err == nil || !strings.Contains(err.Error(), "local network") {
		t.Errorf("error = %v; want a local network refusal", err)
	}
}

func TestFetchRedirectToLocalRefusedAtDial(t *testing.T) {
	secret := serveBody(t, "text/plain", "secret")
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(secret.URL, "http://"))
	// "public.test" looks public until it resolves; the dialer must catch the 127.0.0.1 behind it.
	f := &Fetcher{Resolver: fakeResolver(map[string]string{"public.test": "127.0.0.1"})}
	_, err := f.Fetch(context.Background(), "http://public.test:"+port+"/")
	if err == nil || !strings.Contains(err.Error(), "local network") {
		t.Errorf("error = %v; want a local network refusal", err)
	}
}

func TestFetchRedirectRules(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/ftp":
			w.Header().Set("Location", "ftp://example.com/x")
			w.WriteHeader(http.StatusFound)
		case "/ok":
			http.Redirect(w, r, "/final", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "done")
		}
	}))
	defer s.Close()
	f := &Fetcher{LocalHost: "127.0.0.1"}
	if _, err := f.Fetch(context.Background(), s.URL+"/loop"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("loop error = %v", err)
	}
	if _, err := f.Fetch(context.Background(), s.URL+"/ftp"); err == nil || !strings.Contains(err.Error(), "only http and https") {
		t.Errorf("ftp redirect error = %v", err)
	}
	page, err := f.Fetch(context.Background(), s.URL+"/ok")
	if err != nil || page.URL != s.URL+"/final" {
		t.Errorf("page = %+v, err = %v", page, err)
	}
}

func TestFetchBodyCap(t *testing.T) {
	s := serveBody(t, "text/plain", strings.Repeat("x", 100))
	page, err := (&Fetcher{LocalHost: "127.0.0.1", MaxBytes: 10}).Fetch(context.Background(), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Truncated || page.Bytes != 10 || page.Markdown != strings.Repeat("x", 10) {
		t.Errorf("page = %+v", page)
	}
}

func TestFetchNotText(t *testing.T) {
	s := serveBody(t, "application/pdf", "%PDF-1.4")
	_, err := (&Fetcher{LocalHost: "127.0.0.1"}).Fetch(context.Background(), s.URL)
	if err == nil || !strings.Contains(err.Error(), "application/pdf isn't text") {
		t.Errorf("error = %v", err)
	}
}

func TestFetchHTTPError(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	defer s.Close()
	_, err := (&Fetcher{LocalHost: "127.0.0.1"}).Fetch(context.Background(), s.URL)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404 Not Found") {
		t.Errorf("error = %v", err)
	}
}

func TestPaginate(t *testing.T) {
	md := "héllo wörld ✓✓✓"
	chunk, next := Paginate(md, 0, 7)
	if chunk != "héllo w" || next != 7 {
		t.Errorf("first = %q, %d", chunk, next)
	}
	var all strings.Builder
	for off := 0; ; {
		c, n := Paginate(md, off, 4)
		if !utf8.ValidString(c) {
			t.Fatalf("invalid UTF-8 chunk %q", c)
		}
		all.WriteString(c)
		if n == 0 {
			break
		}
		off = n
	}
	if all.String() != md {
		t.Errorf("pages joined = %q", all.String())
	}
	if c, n := Paginate(md, 100, 5); c != "" || n != 0 {
		t.Errorf("past the end = %q, %d", c, n)
	}
	if c, n := Paginate(md, -3, 0); c != md || n != 0 {
		t.Errorf("whole = %q, %d", c, n)
	}
}

func TestLocalHostScopedToApprovedHost(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "other") }))
	defer other.Close()
	_, otherPort, _ := net.SplitHostPort(strings.TrimPrefix(other.URL, "http://"))
	var self string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/meta":
			http.Redirect(w, r, "http://metadata.test/", http.StatusFound)
		case "/lan":
			http.Redirect(w, r, "http://lan.test:"+otherPort+"/", http.StatusFound)
		case "/lo":
			http.Redirect(w, r, "http://localhost:"+otherPort+"/", http.StatusFound)
		case "/same":
			http.Redirect(w, r, self+"/final", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "done")
		}
	}))
	defer s.Close()
	self = s.URL
	f := &Fetcher{LocalHost: "127.0.0.1", Resolver: fakeResolver(map[string]string{
		"metadata.test": "169.254.169.254", "lan.test": "192.168.1.5", "localhost": "127.0.0.1"})}
	for _, p := range []string{"/meta", "/lan", "/lo"} {
		if _, err := f.Fetch(context.Background(), s.URL+p); err == nil || !strings.Contains(err.Error(), "local network") {
			t.Errorf("%s: error = %v; want a local network refusal", p, err)
		}
	}
	if page, err := f.Fetch(context.Background(), s.URL+"/same"); err != nil || page.Markdown != "done" {
		t.Errorf("same-host redirect: %+v, %v", page, err)
	}
	// Case-insensitive match of the approved name.
	f.LocalHost = "LOCALHOST"
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	if _, err := f.Fetch(context.Background(), "http://localhost:"+port+"/"); err != nil {
		t.Errorf("approved localhost: %v", err)
	}
}

func TestIsLocalIPForms(t *testing.T) {
	local := []string{
		"fe80::1%eth0", "::ffff:10.0.0.1", "64:ff9b::a00:1", "64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe",
		"2002:7f00:1::", "2002:a9fe:a9fe::1", "2002:c0a8:101::", "::10.0.0.1", "::127.0.0.1", "::169.254.169.254",
		"0.1.2.3", "fd00::1", "::1", "::",
	}
	for _, s := range local {
		ips, err := lookup(context.Background(), nil, s)
		if err != nil || !isLocalIP(ips[0]) {
			t.Errorf("%s should be local (%v)", s, err)
		}
	}
	for _, s := range []string{"93.184.216.34", "2606:4700::1111", "64:ff9b::5db8:d822", "2002:5db8:d822::1", "::ffff:93.184.216.34"} {
		ips, err := lookup(context.Background(), nil, s)
		if err != nil || isLocalIP(ips[0]) {
			t.Errorf("%s should not be local (%v)", s, err)
		}
	}
	for _, u := range []string{"http://[fe80::1%25eth0]/", "http://[64:ff9b::a00:1]/", "http://[2002:a00:1::]/"} {
		if got, err := IsLocal(context.Background(), u, nil); err != nil || !got {
			t.Errorf("IsLocal(%s) = %v, %v", u, got, err)
		}
	}
}
