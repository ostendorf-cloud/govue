// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/frontend"
	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
	"github.com/ostendorf-cloud/govue/themeboot"
)

// upstream is the API a test proxies to: a handler behind a RoundTripper,
// so no socket is opened. It records the last request it got.
type upstream struct {
	mu      sync.Mutex
	last    *http.Request
	body    []byte
	handler http.HandlerFunc
	err     error
}

func (u *upstream) RoundTrip(req *http.Request) (*http.Response, error) {
	if u.err != nil {
		return nil, u.err
	}
	// ReverseProxy sends a nil body for a request without one.
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			return nil, err
		}
	}
	u.mu.Lock()
	u.last, u.body = req.Clone(req.Context()), body
	u.mu.Unlock()
	rec := httptest.NewRecorder()
	h := u.handler
	if h == nil {
		h = func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}
	h(rec, req)
	res := rec.Result()
	res.Request = req
	return res, nil
}

func (u *upstream) request() (*http.Request, []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.last, u.body
}

func proxyServer(t *testing.T, up *upstream, tune func(*frontend.Config)) *frontend.Server {
	t.Helper()
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.Development = true
	cfg.DevAPIProxy = "http://api:8081"
	cfg.DevAPITransport = up
	if tune != nil {
		tune(&cfg)
	}
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func send(t *testing.T, h http.Handler, method, target string, body io.Reader, header http.Header,
	remote string,
) *http.Response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, body)
	req.RemoteAddr = remote
	for name, values := range header {
		req.Header[name] = values
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestWithoutTheProxyAPIStaysA404(t *testing.T) {
	tests := map[string]func(*frontend.Config){
		"production default":        func(*frontend.Config) {},
		"development without a URL": func(c *frontend.Config) { c.Development = true },
	}
	for name, tune := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(buildOf(routes), io.Discard)
			tune(&cfg)
			s, err := frontend.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"/api/", "/api/v1/notes", "/api"} {
				res := send(t, s.Handler(), http.MethodGet, target, nil, nil, "203.0.113.7:4000")
				if got := body(t, res); res.StatusCode != http.StatusNotFound || !strings.Contains(got, "nicht gefunden") {
					t.Errorf("GET %s: status %d, want the 404 page", target, res.StatusCode)
				}
			}
			if res := send(t, s.Handler(), http.MethodPost, "/api/v1/login", nil, nil, "203.0.113.7:4000"); res.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("POST /api/v1/login: status %d, want 405", res.StatusCode)
			}
		})
	}
}

func TestDevAPIProxyNeedsTheDevelopmentFlag(t *testing.T) {
	cfg := testConfig(buildOf(routes), io.Discard)
	cfg.DevAPIProxy = "http://hunter2:8081"
	_, err := frontend.New(cfg)
	configtexttest.AssertHidden(t, err, "hunter2", "Config.DevAPIProxy is for development only and needs Config.Development")
}

func TestDevAPIProxyRefusesATargetWithoutQuotingIt(t *testing.T) {
	for _, raw := range []string{
		"http://hunter2.example.com:8081",
		"http://api.hunter2",
		"http://10.0.0.1:8081/hunter2",
		"http://192.168.1.2/hunter2",
		"http://0.0.0.0/hunter2",
		"http://[::]/hunter2",
		"http://[fe80::1%25en0]/hunter2",
		"http://localhost.:8081/hunter2",
		"ftp://localhost/hunter2",
		"hunter2",
		"localhost:8081/hunter2",
		"//localhost/hunter2",
		"http:///hunter2",
		"http://user:hunter2@localhost:8081",
		"http://localhost:8081/?hunter2",
		"http://localhost:8081/?",
		"http://localhost:8081/#hunter2",
		"http://localhost:8081/hunter2/",
		"http://localhost:8081/a/../hunter2",
		"http://localhost:8081/.hunter2",
		"http://localhost:8081/a%2fhunter2",
		"http://localhost:0/hunter2",
		"http://localhost:/hunter2",
		"http://localhost:70000/hunter2",
		"http://-hunter2-/x",
		"http://hunter2_api/x",
		"http://local host/hunter2",
		// Numbers a resolver may read as an IPv4 address.
		"http://167772161/hunter2",
		"http://2130706433/hunter2",
		"http://0x0a000001/hunter2",
		"http://0X7F000001/hunter2",
		"http://0x/hunter2",
		"http://0177/hunter2",
		"http://0x7f.1/hunter2",
		"http://0177.1/hunter2",
		"http://127.1/hunter2",
		"http://10.1/hunter2",
	} {
		t.Run(raw, func(t *testing.T) {
			cfg := testConfig(buildOf(routes), io.Discard)
			cfg.Development = true
			cfg.DevAPIProxy = raw
			_, err := frontend.New(cfg)
			configtexttest.AssertHidden(t, err, "hunter2", "Config.DevAPIProxy")
		})
	}
}

func TestDevAPIProxyAcceptsLoopbackAndASingleLabel(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:8081", "http://LOCALHOST", "http://127.0.0.1:8081", "http://127.0.0.2",
		"http://[::1]:8081", "https://api", "http://api:8081", "http://site-api:8081/prefix",
		"http://api:8081/", "http://[::ffff:127.0.0.1]:8081",
		// A name with digits or a 0x prefix is still a name.
		"http://api2:8081", "http://0xapi:8081", "http://1api", "http://deadbeef",
	} {
		cfg := testConfig(buildOf(routes), io.Discard)
		cfg.Development = true
		cfg.DevAPIProxy = raw
		if _, err := frontend.New(cfg); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}
}

func TestDevAPIProxyForwardsAPIOnly(t *testing.T) {
	up := &upstream{}
	s := proxyServer(t, up, nil)
	res := send(t, s.Handler(), http.MethodPost, "/api/v1/notes?x=1&y=%C3%BC", strings.NewReader(`{"a":1}`),
		http.Header{"Content-Type": {"application/json"}}, "203.0.113.7:4000")
	if got := body(t, res); res.StatusCode != http.StatusOK || got != `{"ok":true}` {
		t.Fatalf("status %d, body %q", res.StatusCode, got)
	}
	req, gotBody := up.request()
	// An empty Host sends the target's host, which SetURL arranges.
	if req.URL.String() != "http://api:8081/api/v1/notes?x=1&y=%C3%BC" || (req.Host != "" && req.Host != "api:8081") {
		t.Errorf("upstream URL %s, Host %s", req.URL, req.Host)
	}
	if req.Method != http.MethodPost || string(gotBody) != `{"a":1}` {
		t.Errorf("upstream method %s, body %q", req.Method, gotBody)
	}

	// The site's own routes never reach the API.
	before, _ := up.request()
	for _, target := range []string{"/de/", "/api", "/apix", "/de/api/x", "/api/../de/"} {
		_ = body(t, send(t, s.Handler(), http.MethodGet, target, nil, nil, "203.0.113.7:4000"))
	}
	if after, _ := up.request(); after != before {
		t.Errorf("a site route reached the API: %s", after.URL)
	}
}

func TestDevAPIProxyPrefixIsKept(t *testing.T) {
	up := &upstream{}
	s := proxyServer(t, up, func(c *frontend.Config) { c.DevAPIProxy = "http://api:8081/prefix" })
	res := send(t, s.Handler(), http.MethodGet, "/api/v1/notes", nil, nil, "203.0.113.7:4000")
	if got := body(t, res); res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %q", res.StatusCode, got)
	}
	if req, _ := up.request(); req.URL.Path != "/prefix/api/v1/notes" {
		t.Errorf("upstream path %s", req.URL.Path)
	}
}

// TestDevAPIProxyNeverForwardsASpoofedClient sends every header a client
// could use to claim another address. The API sees the address the trusted
// resolver found, and nothing else.
func TestDevAPIProxyNeverForwardsASpoofedClient(t *testing.T) {
	spoof := http.Header{
		"X-Forwarded-For":   {"6.6.6.6"},
		"X-Forwarded-Host":  {"evil.example"},
		"X-Forwarded-Proto": {"https"},
		"Forwarded":         {"for=6.6.6.6;host=evil.example;proto=https"},
		"X-Real-Ip":         {"6.6.6.6"},
		"Via":               {"1.1 evil.example"},
	}
	t.Run("no trusted proxy", func(t *testing.T) {
		up := &upstream{}
		s := proxyServer(t, up, nil)
		_ = body(t, send(t, s.Handler(), http.MethodGet, "/api/v1/me", nil, spoof, "203.0.113.7:4000"))
		req, _ := up.request()
		assertForwarded(t, req, "203.0.113.7")
	})
	t.Run("behind a trusted proxy", func(t *testing.T) {
		up := &upstream{}
		s := proxyServer(t, up, func(c *frontend.Config) {
			c.ClientIP = clientip.NewResolver([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
		})
		header := spoof.Clone()
		header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.9")
		_ = body(t, send(t, s.Handler(), http.MethodGet, "/api/v1/me", nil, header, "10.0.0.1:4000"))
		req, _ := up.request()
		assertForwarded(t, req, "198.51.100.9")
	})
}

func assertForwarded(t *testing.T, req *http.Request, client string) {
	t.Helper()
	if got := req.Header.Values("X-Forwarded-For"); len(got) != 1 || got[0] != client {
		t.Errorf("X-Forwarded-For = %q, want only %s", got, client)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "example.com" {
		t.Errorf("X-Forwarded-Host = %q, want the request's own host", got)
	}
	if got := req.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q, want http", got)
	}
	for _, name := range []string{"Forwarded", "X-Real-Ip", "Via"} {
		if req.Header.Get(name) != "" {
			t.Errorf("%s reached the API: %q", name, req.Header.Get(name))
		}
	}
	for name, values := range req.Header {
		if strings.Contains(strings.Join(values, ","), "6.6.6.6") || strings.Contains(strings.Join(values, ","), "evil") {
			t.Errorf("a spoofed value reached the API in %s: %q", name, values)
		}
	}
}

// TestDevAPIProxyLeaksNoHeader covers both directions: the hop by hop
// headers a client names stay with the frontend, the API's copies of the
// security headers are replaced by the frontend's, and its hop by hop
// headers do not reach the browser.
func TestDevAPIProxyLeaksNoHeader(t *testing.T) {
	up := &upstream{handler: func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Content-Security-Policy", "default-src *")
		h.Set("X-Frame-Options", "ALLOWALL")
		h.Set("Strict-Transport-Security", "max-age=0")
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		h.Set("X-Request-Id", "from-the-api")
		h.Set("Connection", "X-Upstream-Hop")
		h.Set("X-Upstream-Hop", "leak")
		h.Set("Set-Cookie", "session=new; Path=/; HttpOnly; Secure; SameSite=Lax")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}}
	s := proxyServer(t, up, nil)
	res := send(t, s.Handler(), http.MethodGet, "/api/v1/me", nil, http.Header{
		"Connection":    {"X-Client-Hop"},
		"X-Client-Hop":  {"leak"},
		"Te":            {"trailers"},
		"Cookie":        {"session=abc"},
		"Authorization": {"Bearer t"},
	}, "203.0.113.7:4000")
	_ = body(t, res)

	req, _ := up.request()
	if req.Header.Get("X-Client-Hop") != "" || req.Header.Get("Connection") != "" {
		t.Errorf("a hop by hop header reached the API: %v", req.Header)
	}
	if req.Header.Get("Cookie") != "session=abc" || req.Header.Get("Authorization") != "Bearer t" {
		t.Errorf("the API's own credentials did not reach it: %v", req.Header)
	}

	if res.StatusCode != http.StatusCreated || res.Header.Get("Set-Cookie") == "" {
		t.Errorf("status %d, Set-Cookie %q: the API's answer did not pass", res.StatusCode, res.Header.Get("Set-Cookie"))
	}
	if res.Header.Get("X-Upstream-Hop") != "" {
		t.Error("a hop by hop header of the API reached the browser")
	}
	for name, want := range map[string]string{
		"X-Frame-Options":              "DENY",
		"Strict-Transport-Security":    "max-age=31536000; includeSubDomains; preload",
		"Cross-Origin-Resource-Policy": "same-origin",
	} {
		if got := res.Header.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want only the frontend's %q", name, got, want)
		}
	}
	if got := res.Header.Values("Content-Security-Policy"); len(got) != 1 || !strings.Contains(got[0], themeboot.Hash()) {
		t.Errorf("Content-Security-Policy = %q, want only the frontend's", got)
	}
	if got := res.Header.Values("X-Request-Id"); len(got) != 1 || got[0] == "from-the-api" {
		t.Errorf("X-Request-Id = %q, want only the frontend's", got)
	}
}

func TestDevAPIProxyFailureIsOneOpaque502(t *testing.T) {
	up := &upstream{err: errors.New("dial tcp api:8081: secret-cause")}
	var logs bytes.Buffer
	cfg := testConfig(buildOf(routes), &logs)
	cfg.Development = true
	cfg.DevAPIProxy = "http://api:8081"
	cfg.DevAPITransport = up
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := send(t, s.Handler(), http.MethodGet, "/api/v1/me", nil, nil, "203.0.113.7:4000")
	got := body(t, res)
	if res.StatusCode != http.StatusBadGateway || strings.TrimSpace(got) != "Bad Gateway" {
		t.Errorf("status %d, body %q, want an opaque 502", res.StatusCode, got)
	}
	if res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("Content-Security-Policy") == "" {
		t.Error("the 502 lacks the security headers")
	}
	if strings.Contains(logs.String(), "secret-cause") || strings.Contains(logs.String(), "api:8081") {
		t.Errorf("the log holds the cause: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "dev api proxy failed") {
		t.Errorf("the failure was not logged: %s", logs.String())
	}
}

func TestDevAPIProxyBoundsTheBody(t *testing.T) {
	up := &upstream{}
	s := proxyServer(t, up, nil)
	big := strings.NewReader(strings.Repeat("x", frontend.DevAPIProxyMaxBody+1))
	res := send(t, s.Handler(), http.MethodPost, "/api/v1/notes", big, nil, "203.0.113.7:4000")
	if _ = body(t, res); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", res.StatusCode)
	}
}

// countingReader yields n bytes of x and counts what the proxy took.
type countingReader struct {
	left int64
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.left == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), c.left)
	for i := range p[:n] {
		p[i] = 'x'
	}
	c.left -= n
	c.read += n
	return int(n), nil
}

func TestDevAPIProxyRaisedBodyLimit(t *testing.T) {
	const raised = 4 << 20
	tests := []struct {
		name   string
		limit  int64
		size   int64
		status int
	}{
		{"above the default limit", 0, frontend.DevAPIProxyMaxBody + 1, http.StatusRequestEntityTooLarge},
		{"at the default limit", 0, frontend.DevAPIProxyMaxBody, http.StatusOK},
		{"below a raised limit", raised, 3 << 20, http.StatusOK},
		{"at a raised limit", raised, raised, http.StatusOK},
		{"above a raised limit", raised, raised + 1, http.StatusRequestEntityTooLarge},
		{"above the default under a lowered limit", 1024, 1025, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			up := &upstream{}
			s := proxyServer(t, up, func(c *frontend.Config) { c.DevAPIMaxBody = tc.limit })
			res := send(t, s.Handler(), http.MethodPost, "/api/v1/notes",
				bytes.NewReader(bytes.Repeat([]byte("x"), int(tc.size))), nil, "203.0.113.7:4000")
			_ = body(t, res)
			if res.StatusCode != tc.status {
				t.Errorf("status %d, want %d", res.StatusCode, tc.status)
			}
			if tc.status == http.StatusOK {
				if _, got := up.request(); int64(len(got)) != tc.size {
					t.Errorf("upstream got %d bytes, want %d", len(got), tc.size)
				}
			}
		})
	}
}

// TestDevAPIProxyStopsReadingAtTheLimit proves the proxy streams: a body far
// above the limit is cut off, never read to its end.
func TestDevAPIProxyStopsReadingAtTheLimit(t *testing.T) {
	t.Parallel()
	const limit = 64 << 10
	s := proxyServer(t, &upstream{}, func(c *frontend.Config) { c.DevAPIMaxBody = limit })
	src := &countingReader{left: 64 << 20}
	res := send(t, s.Handler(), http.MethodPost, "/api/v1/notes", src, nil, "203.0.113.7:4000")
	_ = body(t, res)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", res.StatusCode)
	}
	if src.read > limit+(64<<10) {
		t.Errorf("proxy read %d bytes of a body over the %d limit", src.read, limit)
	}
}

func TestDevAPIMaxBodyValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		limit   int64
		wantErr bool
	}{
		{"zero is the default", 0, false},
		{"one byte", 1, false},
		{"the upper bound", frontend.DevAPIProxyMaxBodyLimit, false},
		{"negative", -1, true},
		{"above the upper bound", frontend.DevAPIProxyMaxBodyLimit + 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig(buildOf(routes), io.Discard)
			cfg.DevAPIMaxBody = tc.limit
			_, err := frontend.New(cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("New: err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "Config.DevAPIMaxBody") {
				t.Errorf("the error does not name the field: %v", err)
			}
		})
	}
}

func TestDevAPIProxyIsLoggedByPatternAndNeverCounted(t *testing.T) {
	up := &upstream{handler: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><title>api page</title>")
	}}
	fake := &fakeUmami{}
	views := newViews(t, fake, 16)
	var logs bytes.Buffer
	cfg := testConfig(buildOf(routes), &logs)
	cfg.Development = true
	cfg.DevAPIProxy = "http://api:8081"
	cfg.DevAPITransport = up
	cfg.Analytics = views
	s, err := frontend.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, visit(t, s.Handler(), "/api/v1/page/secret-id", nil))
	closeViews(t, views)
	if got := fake.urls(t); len(got) != 0 {
		t.Errorf("a proxied answer was counted: %v", got)
	}
	var route any
	for _, raw := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(raw, &entry) == nil && entry["route"] != nil {
			route = entry["route"]
		}
	}
	if route != "/api/" || strings.Contains(logs.String(), "secret-id") {
		t.Errorf("access log route = %v, want the pattern /api/ and no path", route)
	}
}
