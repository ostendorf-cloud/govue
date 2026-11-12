// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/web-kit/csrf"
	"github.com/ostendorf-cloud/web-kit/logging"
	"github.com/ostendorf-cloud/web-kit/session"
)

// writeKeyFile writes 32 random bytes, hex encoded, to a file under t's
// temporary directory and returns its path: exactly the shape envconf.Secret
// reads, and loadConfig's SESSION_KEY and CSRF_KEY both point at one.
func writeKeyFile(t *testing.T, name string) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("read random key bytes: %v", err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// testEnv builds the environment newTestApp loads from, with every required
// variable set to a value that validates without ever reaching a network: an
// identity provider is never contacted here, so its URLs only have to pass
// oidc.New's own shape checks.
func testEnv(t *testing.T, rateLimitBurst int) map[string]string {
	t.Helper()
	return map[string]string{
		"ADDR":               "127.0.0.1:0",
		"LOG_LEVEL":          "info",
		"SESSION_KEY":        writeKeyFile(t, "session.key"),
		"CSRF_KEY":           writeKeyFile(t, "csrf.key"),
		"OIDC_ISSUER":        "https://issuer.example.test",
		"OIDC_CLIENT_ID":     "example-client",
		"OIDC_CLIENT_SECRET": "example-client-secret",
		"OIDC_REDIRECT_URL":  "https://app.example.test/login/callback",
		"ALLOWED_ORIGINS":    "https://app.example.test",
		"RATE_LIMIT_BURST":   strconv.Itoa(rateLimitBurst),
		"RATE_LIMIT_PER":     "1m",
	}
}

// newTestApp builds an app from a fake getenv map and a logger writing to a
// buffer this test owns, so nothing it does lands in a global registry or on
// a real socket. burst is the rate limiter's burst size, which the 429 case
// needs set to exactly 1.
func newTestApp(t *testing.T, burst int) (*app, *bytes.Buffer) {
	t.Helper()

	env := testEnv(t, burst)
	cfg, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	var logs bytes.Buffer
	logger := logging.New(&logs, cfg.logLevel)

	a, err := newApp(cfg, logger)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return a, &logs
}

// startSession logs an account in directly against a's session manager,
// bypassing the OIDC flow this test never drives, and returns the cookie a
// browser would have been sent.
func startSession(t *testing.T, a *app) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if _, err := a.sessions.Start(t.Context(), rec, session.Session{Subject: "test-subject"}); err != nil {
		t.Fatalf("start session: %v", err)
	}

	res := rec.Result()
	defer func() { _ = res.Body.Close() }()

	for _, c := range res.Cookies() {
		if c.Name == a.sessions.CookieName() {
			return c
		}
	}
	t.Fatal("session.Start did not set a cookie")
	return nil
}

// accessLogLines decodes every JSON line in logs tagged "request handled",
// the access log's own message, into a generic map so a test can assert on
// whichever fields it cares about.
func accessLogLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if raw == "" {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			t.Fatalf("log line is not JSON: %v (%s)", err, raw)
		}
		if parsed["msg"] == "request handled" {
			lines = append(lines, parsed)
		}
	}
	return lines
}

// scrapeMetrics runs a scrape through a's own metrics handler, mounted bare
// on the mux exactly as (*app).handler puts it, and returns the exposition
// text. Reading it as text, rather than importing prometheus here, keeps
// this file off the one dependency this repository confines to the metrics
// package.
func scrapeMetrics(t *testing.T, a *app) string {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics scrape status = %d", rec.Code)
	}
	return rec.Body.String()
}

// metricSeries is the exposition text fragment one request/status/route
// combination produces, label order matching metrics.HTTP's own "method",
// "route", "status".
func metricSeries(name, method, route, status string) string {
	return name + `{method="` + method + `",route="` + route + `",status="` + status + `"}`
}

func TestHandlerLabelsEveryCaseByItsRealRoute(t *testing.T) {
	t.Parallel()

	// A generous burst: this test is about the route label, not the rate
	// limiter, and a shared app across subtests must not run into it.
	a, logs := newTestApp(t, 1000)
	h := a.handler()

	sessionCookie := startSession(t, a)

	for _, tc := range []struct {
		name       string
		request    func() *http.Request
		wantStatus int
		wantRoute  string
	}{
		{
			name: "a normal 200 on a real route",
			request: func() *http.Request {
				return httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil)
			},
			wantStatus: http.StatusOK,
			wantRoute:  "GET /app",
		},
		{
			name: "a 403 from the csrf protector on an unsafe method with no token",
			request: func() *http.Request {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/notes",
					strings.NewReader(`{"text":"hello"}`))
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(sessionCookie)
				return req
			},
			wantStatus: http.StatusForbidden,
			wantRoute:  "POST /notes",
		},
		{
			name: "an unregistered path matches the catch-all",
			request: func() *http.Request {
				return httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/this-path-was-never-registered", nil)
			},
			wantStatus: http.StatusNotFound,
			wantRoute:  "/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.request())

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			lines := accessLogLines(t, logs)
			if len(lines) != 1 {
				t.Fatalf("got %d access log lines, want exactly 1: %s", len(lines), logs.String())
			}
			line := lines[0]
			if got := line["route"]; got != tc.wantRoute {
				t.Errorf("access log route = %v, want %q", got, tc.wantRoute)
			}
			if gotStatus, _ := line["status"].(float64); int(gotStatus) != tc.wantStatus {
				t.Errorf("access log status = %v, want %d", line["status"], tc.wantStatus)
			}
			if got := line["method"]; got != tc.request().Method {
				t.Errorf("access log method = %v, want %q", got, tc.request().Method)
			}

			body := scrapeMetrics(t, a)
			series := metricSeries("http_requests_total", tc.request().Method, tc.wantRoute, strconv.Itoa(tc.wantStatus))
			if !strings.Contains(body, series) {
				t.Errorf("metrics scrape is missing %s:\n%s", series, body)
			}
		})
	}
}

// TestCSRFProtectedWriteSucceedsWithTheRealToken is the happy path the 403
// cases above do not cover on their own: every one of them refuses a
// request, so a broken Expected function, one that never actually calls
// csrf.Token, or a protector wired to the wrong key would still leave this
// suite green. This proves the real token, derived the same way a rendered
// page would derive it, is accepted, and that the handler behind the
// protector actually ran rather than only the middleware passing.
func TestCSRFProtectedWriteSucceedsWithTheRealToken(t *testing.T) {
	t.Parallel()

	a, logs := newTestApp(t, 1000)
	h := a.handler()

	sessionCookie := startSession(t, a)

	// The token a real page would send: read the session back the same way
	// csrf.New's Expected function does in (*app) newApp, then derive its
	// token with the same call that function makes.
	readBack := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	readBack.AddCookie(sessionCookie)
	s, ok, err := a.sessions.Read(t.Context(), readBack)
	if err != nil || !ok {
		t.Fatalf("read back the session that was just started: ok=%v err=%v", ok, err)
	}
	token := csrf.Token(a.cfg.csrfKey, s.ID)

	logs.Reset()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/notes", strings.NewReader(`{"text":"a real note"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrf.HeaderName, token)
	// The origin half of the check still has to pass on its own merits:
	// same-origin is what a same site form post or fetch call actually
	// sends.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(sessionCookie)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 with a valid token: %s", rec.Code, rec.Body.String())
	}

	lines := accessLogLines(t, logs)
	if len(lines) != 1 {
		t.Fatalf("got %d access log lines, want exactly 1: %s", len(lines), logs.String())
	}
	line := lines[0]
	if got := line["route"]; got != "POST /notes" {
		t.Errorf("access log route = %v, want %q", got, "POST /notes")
	}
	if gotStatus, _ := line["status"].(float64); int(gotStatus) != http.StatusCreated {
		t.Errorf("access log status = %v, want 201", line["status"])
	}

	body := scrapeMetrics(t, a)
	series := metricSeries("http_requests_total", http.MethodPost, "POST /notes", "201")
	if !strings.Contains(body, series) {
		t.Errorf("metrics scrape is missing %s:\n%s", series, body)
	}

	// The handler behind the protector actually ran: the note is readable
	// back, not only accepted by the middleware chain in front of it.
	list := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/notes", nil)
	list.AddCookie(sessionCookie)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list notes status = %d, want 200: %s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "a real note") {
		t.Errorf("the created note is missing from the list: %s", listRec.Body.String())
	}
}

func TestRateLimitAnswersWithTheRealRouteLabel(t *testing.T) {
	t.Parallel()

	a, logs := newTestApp(t, 1)
	h := a.handler()

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}

	logs.Reset()
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}

	lines := accessLogLines(t, logs)
	if len(lines) != 1 {
		t.Fatalf("got %d access log lines, want exactly 1: %s", len(lines), logs.String())
	}
	line := lines[0]
	if got := line["route"]; got != "GET /app" {
		t.Errorf("access log route = %v, want %q: the rate limiter must run inside the route chain, not the outer one", got, "GET /app")
	}
	if gotStatus, _ := line["status"].(float64); int(gotStatus) != http.StatusTooManyRequests {
		t.Errorf("access log status = %v, want 429", line["status"])
	}

	body := scrapeMetrics(t, a)
	series := metricSeries("http_requests_total", http.MethodGet, "GET /app", "429")
	if !strings.Contains(body, series) {
		t.Errorf("metrics scrape is missing %s:\n%s", series, body)
	}
}

func TestNonceReachesTheHandlerThatRenderedUnderIt(t *testing.T) {
	t.Parallel()

	a, _ := newTestApp(t, 1000)
	h := a.handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	csp := rec.Header().Get("Content-Security-Policy")
	start := strings.Index(csp, "'nonce-")
	if start == -1 {
		t.Fatalf("no nonce in the Content-Security-Policy header: %s", csp)
	}
	start += len("'nonce-")
	end := strings.Index(csp[start:], "'")
	if end == -1 {
		t.Fatalf("the nonce source is not closed in the header: %s", csp)
	}
	nonce := csp[start : start+end]
	if nonce == "" {
		t.Fatal("the nonce in the header is empty")
	}

	if !strings.Contains(rec.Body.String(), `nonce="`+nonce+`"`) {
		t.Errorf("the handler's inline script does not carry the header's own nonce: body=%s header=%s", rec.Body.String(), csp)
	}
}

// TestAccessLogNeverRecordsTheUnmatchedRoute pins the constraint this
// repository's two chains exist to satisfy: a middleware that answers a
// request itself must never be logged against the bounded "unmatched"
// label, whatever request drove it. A future change that moves one of
// these middlewares back into the single outer chain fails this test
// loudly, instead of only showing up as a quieter route label in
// production.
func TestAccessLogNeverRecordsTheUnmatchedRoute(t *testing.T) {
	t.Parallel()

	a, logs := newTestApp(t, 1000)
	h := a.handler()
	sessionCookie := startSession(t, a)

	noToken := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/notes", strings.NewReader(`{"text":"hello"}`))
	noToken.Header.Set("Content-Type", "application/json")
	noToken.AddCookie(sessionCookie)

	for _, req := range []*http.Request{
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil),
		noToken,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/this-path-was-never-registered", nil),
	} {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	limited, limitedLogs := newTestApp(t, 1)
	limitedHandler := limited.handler()
	for range 2 {
		limitedHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil))
	}

	for _, buf := range []*bytes.Buffer{logs, limitedLogs} {
		for _, line := range accessLogLines(t, buf) {
			if line["route"] == "unmatched" {
				t.Fatalf("an access log line recorded the unmatched route: %v", line)
			}
		}
	}
}
