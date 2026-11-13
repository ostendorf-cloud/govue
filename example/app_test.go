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

	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/session"
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

// jsonKeyCount reports how many times key appears as a top-level key in
// the JSON object line, by walking the raw token stream instead of
// unmarshalling into a map: encoding/json's map decoding silently keeps
// only the last value of a duplicate key, which would hide exactly the
// bug this test exists to catch.
func jsonKeyCount(t *testing.T, line []byte, key string) int {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(line))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("reading the opening token: %v", err)
	}
	if tok != json.Delim('{') {
		t.Fatalf("log line is not a JSON object: %s", line)
	}
	count := 0
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatalf("reading a key: %v", err)
		}
		if k, ok := keyTok.(string); ok && k == key {
			count++
		}
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			t.Fatalf("reading the value for %v: %v", keyTok, err)
		}
	}
	return count
}

// accessLogLines decodes every JSON line in logs tagged "request handled",
// the access log's own message, into a generic map so a test can assert on
// whichever fields it cares about.
//
// Each line is also walked with jsonKeyCount before the decode, checking
// "request_id" appears exactly once: encoding/json's map decoding silently
// keeps only the last value of a duplicate key, so a plain Unmarshal alone
// would never notice a line carrying "request_id" twice, the exact shape
// of the bug issue #48 fixed. The token walk catches that regression even
// though the map decode behind it cannot.
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
			if got := jsonKeyCount(t, []byte(raw), "request_id"); got != 1 {
				t.Fatalf("request_id appears %d times in the access log line, want exactly 1: %s", got, raw)
			}
			lines = append(lines, parsed)
		}
	}
	return lines
}

// scrapeMetrics runs a scrape through a's admin handler, exactly as
// (*app).adminHandler mounts it on the second listener main.go runs, and
// returns the exposition text. Reading it as text, rather than importing
// prometheus here, keeps this file off the one dependency this repository
// confines to the metrics package.
func scrapeMetrics(t *testing.T, a *app) string {
	t.Helper()
	rec := httptest.NewRecorder()
	a.adminHandler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
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

// TestNonCanonicalPathBypassesTheRateLimiter pins the one case the
// catch-all does not cover: a non-canonical path never reaches the route
// chain at all, http.ServeMux answers it with its own redirect first, so
// the rate limiter never sees it. The canonical path, with the same burst
// of one, is rate limited as normal. See docs/concepts/govue.md for why
// the redirect bypassing the chain is accepted.
func TestNonCanonicalPathBypassesTheRateLimiter(t *testing.T) {
	t.Parallel()

	a, logs := newTestApp(t, 1)
	h := a.handler()

	for i := range 4 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "//app", nil))
		if rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("non-canonical request %d status = %d, want 307: never rate limited", i, rec.Code)
		}
	}

	lines := accessLogLines(t, logs)
	if len(lines) != 4 {
		t.Fatalf("got %d access log lines, want exactly 4: %s", len(lines), logs.String())
	}
	for _, line := range lines {
		if got := line["route"]; got != "GET /app" {
			t.Errorf("access log route = %v, want %q: the mux still records the real route on its own redirect", got, "GET /app")
		}
		if gotStatus, _ := line["status"].(float64); int(gotStatus) != http.StatusTemporaryRedirect {
			t.Errorf("access log status = %v, want 307", line["status"])
		}
	}

	logs.Reset()

	canonicalFirst := httptest.NewRecorder()
	h.ServeHTTP(canonicalFirst, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil))
	if canonicalFirst.Code != http.StatusOK {
		t.Fatalf("first canonical request status = %d, want 200: %s", canonicalFirst.Code, canonicalFirst.Body.String())
	}

	canonicalSecond := httptest.NewRecorder()
	h.ServeHTTP(canonicalSecond, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app", nil))
	if canonicalSecond.Code != http.StatusTooManyRequests {
		t.Fatalf("second canonical request status = %d, want 429: the limiter's one burst was never spent on the redirects above",
			canonicalSecond.Code)
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

// TestPublicHandlerNoLongerServesHealthOrMetrics pins the fix issue 85
// exists for: a request for any of the three admin paths against the public
// handler, the one httpserver.RunAdmin puts behind this application's own
// Ingress, must miss every registered route and fall through to the
// catch-all, exactly like any other unregistered path.
func TestPublicHandlerNoLongerServesHealthOrMetrics(t *testing.T) {
	t.Parallel()

	a, _ := newTestApp(t, 1000)
	h := a.handler()

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("public handler GET %s = %d, want 404: it must not be reachable through the public listener", path, rec.Code)
		}
	}
}

// TestAdminHandlerServesHealthAndMetrics is the positive half of the test
// above: the same three paths answer normally on the admin handler
// httpserver.RunAdmin puts on the second, unrouted listener.
func TestAdminHandlerServesHealthAndMetrics(t *testing.T) {
	t.Parallel()

	a, _ := newTestApp(t, 1000)
	h := a.adminHandler()

	for _, tc := range []struct {
		path string
		want int
	}{
		{"/healthz", http.StatusOK},
		{"/readyz", http.StatusOK},
		{"/metrics", http.StatusOK},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("admin handler GET %s = %d, want %d: %s", tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// TestAdminHandlerIsNeverRateLimitedOrAccessLogged is the test for the
// acceptance criterion that the admin listener's middleware is a deliberate
// choice: a burst of one is enough to refuse the second request on the
// public handler, see TestRateLimitAnswersWithTheRealRouteLabel, but a
// scraper hitting the admin handler the same number of times must never be
// refused, and must never fill the access log the public handler writes to.
func TestAdminHandlerIsNeverRateLimitedOrAccessLogged(t *testing.T) {
	t.Parallel()

	a, logs := newTestApp(t, 1)
	h := a.adminHandler()

	for i := range 5 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("scrape %d status = %d, want 200: a burst of one must not rate limit the admin listener", i, rec.Code)
		}
	}

	if lines := accessLogLines(t, logs); len(lines) != 0 {
		t.Errorf("got %d access log lines for the admin handler, want 0: %v", len(lines), lines)
	}
}

// TestAdminHandlerStillGivesEveryRequestAnID checks the middleware that did
// stay is actually wired up: Recover and RequestID are kept on the admin
// handler on purpose, so a panic there is still caught and still
// correlatable, even though AccessLog, the rate limiter, CORS, CSRF and the
// security headers are not.
func TestAdminHandlerStillGivesEveryRequestAnID(t *testing.T) {
	t.Parallel()

	a, _ := newTestApp(t, 1000)
	h := a.adminHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(middleware.RequestIDHeader) == "" {
		t.Error("the admin handler's response carries no request id")
	}
}
