// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package devicehttp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/session"
	"github.com/ostendorf-cloud/govue/session/devicehttp"
)

// Fake values only. None of them is anybody's password.
const (
	loginA    = "carol@example.com"
	loginB    = "dave@example.com"
	passwordA = "fake-example-password-one"
	passwordB = "fake-example-password-two"
	subjectA  = "subject-a"
	subjectB  = "subject-b"
	codeOK    = "123456"
	appOrigin = "https://app.example.test"
	evil      = "https://evil.example.test"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// syncBuffer is a log sink the parallel requests of one test can share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// response is one recorded answer.
type response struct {
	method, path string
	status       int
	header       http.Header
	body         string
}

// faultStore is a device store that fails the calls a test asks it to.
type faultStore struct {
	session.DeviceStore

	mu                           sync.Mutex
	create, list, lookup, revoke error
}

func (s *faultStore) fail(create, list, lookup, revoke error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.create, s.list, s.lookup, s.revoke = create, list, lookup, revoke
}

func (s *faultStore) errs() (create, list, lookup, revoke error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.create, s.list, s.lookup, s.revoke
}

func (s *faultStore) Create(ctx context.Context, d session.DeviceSession, maxPerSubject int, at time.Time) ([]string, error) {
	if err, _, _, _ := s.errs(); err != nil {
		return nil, err
	}
	return s.DeviceStore.Create(ctx, d, maxPerSubject, at)
}

func (s *faultStore) List(ctx context.Context, subject string) ([]session.DeviceSession, error) {
	if _, err, _, _ := s.errs(); err != nil {
		return nil, err
	}
	return s.DeviceStore.List(ctx, subject)
}

func (s *faultStore) Lookup(ctx context.Context, hash session.TokenHash) (session.DeviceSession, session.DeviceSlot, error) {
	if _, _, err, _ := s.errs(); err != nil {
		return session.DeviceSession{}, 0, err
	}
	return s.DeviceStore.Lookup(ctx, hash)
}

func (s *faultStore) Revoke(ctx context.Context, subject, id string) error {
	if _, _, _, err := s.errs(); err != nil {
		return err
	}
	return s.DeviceStore.Revoke(ctx, subject, id)
}

type fixture struct {
	store    *faultStore
	t        *testing.T
	clock    *clock
	manager  *session.Manager
	handlers *devicehttp.Handlers
	handler  http.Handler
	logs     *syncBuffer

	mu       sync.Mutex
	verifies int
	verifyFn func(ctx context.Context, c devicehttp.Credentials) (string, error)
	recorded []response
}

func newFixture(t *testing.T, adjust func(*devicehttp.HandlerConfig)) *fixture {
	t.Helper()
	f := &fixture{t: t, clock: &clock{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}, logs: &syncBuffer{}}
	f.store = &faultStore{DeviceStore: session.NewMemoryDeviceStore(f.clock.Now, 100)}
	manager, err := session.New(session.Config{
		Store:   session.NewMemoryStore(f.clock.Now, 100),
		Now:     f.clock.Now,
		Devices: session.DeviceConfig{Store: f.store},
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	f.manager = manager

	cfg := devicehttp.HandlerConfig{
		Sessions: manager,
		Now:      f.clock.Now,
		VerifyCredentials: func(ctx context.Context, c devicehttp.Credentials) (string, error) {
			f.mu.Lock()
			f.verifies++
			fn := f.verifyFn
			f.mu.Unlock()
			if fn != nil {
				return fn(ctx, c)
			}
			switch {
			case c.Login == loginA && c.Password == passwordA && c.Code == codeOK:
				return subjectA, nil
			case c.Login == loginB && c.Password == passwordB && c.Code == codeOK:
				return subjectB, nil
			}
			return "", devicehttp.ErrInvalidCredentials
		},
		IssueTrustedOrigins: []string{appOrigin},
	}
	if adjust != nil {
		adjust(&cfg)
	}
	h, err := devicehttp.NewHandlers(cfg)
	if err != nil {
		t.Fatalf("NewHandlers: %v", err)
	}
	f.handlers = h

	// Routes without a method, so the handler's own method check answers.
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			h.SignOut().ServeHTTP(w, r)
			return
		}
		h.Issue().ServeHTTP(w, r)
	})
	mux.Handle("/auth/token/rotate", h.Rotate())
	mux.Handle("/auth/devices", h.List())
	mux.Handle("/auth/devices/{id}", h.Revoke())
	f.handler = mux
	return f
}

// serve runs the request through the session middleware, as the chain of an
// application does, with the test's logger in the context.
func (f *fixture) serve(r *http.Request) *httptest.ResponseRecorder {
	f.t.Helper()
	logger := logging.New(f.logs, -4)
	r = r.WithContext(logging.NewContext(r.Context(), logger))
	w := httptest.NewRecorder()
	f.manager.Middleware(f.handler).ServeHTTP(w, r)
	f.mu.Lock()
	f.recorded = append(f.recorded, response{r.Method, r.URL.Path, w.Code, w.Header().Clone(), w.Body.String()})
	f.mu.Unlock()
	return w
}

type option func(*http.Request)

func bearerOf(token string) option {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func withHeader(k, v string) option { return func(r *http.Request) { r.Header.Set(k, v) } }

func crossSite() option {
	return func(r *http.Request) {
		r.Header.Set("Origin", evil)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}
}

func withCookie(c *http.Cookie) option { return func(r *http.Request) { r.AddCookie(c) } }

func (f *fixture) do(method, path string, body any, opts ...option) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			f.t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequestWithContext(f.t.Context(), method, path, reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(r)
	}
	return f.serve(r)
}

func credsA() map[string]string {
	return map[string]string{"login": loginA, "password": passwordA, "code": codeOK}
}

func credsB() map[string]string {
	return map[string]string{"login": loginB, "password": passwordB, "code": codeOK}
}

// issue signs in through the handler and returns the result.
func (f *fixture) issue(creds map[string]string, name string, opts ...option) devicehttp.TokenResult {
	f.t.Helper()
	body := map[string]string{"device_name": name}
	for k, v := range creds {
		body[k] = v
	}
	w := f.do(http.MethodPost, "/auth/token", body, opts...)
	if w.Code != http.StatusCreated {
		f.t.Fatalf("issue = %d %s, want 201", w.Code, w.Body.String())
	}
	var res devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Token == "" || res.TokenID == "" {
		f.t.Fatalf("issue body = %s, err %v", w.Body.String(), err)
	}
	return res
}

func (f *fixture) list(opts ...option) (*httptest.ResponseRecorder, devicehttp.DeviceList) {
	f.t.Helper()
	w := f.do(http.MethodGet, "/auth/devices", nil, opts...)
	var out devicehttp.DeviceList
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			f.t.Fatalf("list body = %s: %v", w.Body.String(), err)
		}
	}
	return w, out
}

func (f *fixture) cookieFor(subject string) *http.Cookie {
	f.t.Helper()
	rec := httptest.NewRecorder()
	if _, err := f.manager.Start(f.t.Context(), rec, session.Session{Subject: subject}); err != nil {
		f.t.Fatalf("Start: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		f.t.Fatalf("Start set %d cookies", len(cookies))
	}
	return cookies[0]
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("error body = %s: %v", w.Body.String(), err)
	}
	return env.Error.Code
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d: %s", w.Code, want, w.Body.String())
	}
}

func wantNoStore(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
		t.Errorf("Cache-Control = %q, want one no-store", got)
	}
}

func TestIssueReturnsTheTokenOnceAndItAuthenticates(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	w := f.do(http.MethodPost, "/auth/token", map[string]string{
		"login": loginA, "password": passwordA, "code": codeOK, "device_name": "Pixel",
	})
	wantStatus(t, w, http.StatusCreated)
	wantNoStore(t, w)
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q", got)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Errorf("Set-Cookie = %v, want none", w.Result().Cookies())
	}
	var res devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("body: %v", err)
	}
	if !res.ExpiresAt.Equal(f.clock.Now().Add(session.DefaultDeviceLifetime)) {
		t.Errorf("expires_at = %s", res.ExpiresAt)
	}
	d, err := f.manager.AuthenticateDevice(t.Context(), res.Token)
	if err != nil {
		t.Fatalf("the issued token does not authenticate: %v", err)
	}
	if d.ID != res.TokenID || d.Subject != subjectA || d.Name != "Pixel" {
		t.Errorf("device = %+v, token id %q", d, res.TokenID)
	}

	// The body has exactly the documented keys.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &keys); err != nil {
		t.Fatalf("keys: %v", err)
	}
	for _, k := range []string{"token", "token_id", "expires_at"} {
		delete(keys, k)
	}
	if len(keys) != 0 {
		t.Errorf("unexpected keys in the issue body: %v", keys)
	}

	// A second sign in is a second device with another token.
	second := f.issue(credsA(), "Tablet")
	if second.Token == res.Token || second.TokenID == res.TokenID {
		t.Error("two issues share a token or an id")
	}
}

func TestIssueRefusalsAreOneBodyPerCause(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	var bodies []string
	for _, c := range []map[string]string{
		{"login": loginA, "password": "fake-wrong-password", "code": codeOK},
		{"login": "nobody@example.com", "password": passwordA, "code": codeOK},
		{"login": loginA, "password": passwordA, "code": "000000"},
		{"login": loginA, "password": passwordA},
		{},
	} {
		w := f.do(http.MethodPost, "/auth/token", c)
		wantStatus(t, w, http.StatusUnauthorized)
		wantNoStore(t, w)
		if got := errorCode(t, w); got != devicehttp.CodeInvalidCredentials {
			t.Errorf("code = %q", got)
		}
		bodies = append(bodies, w.Body.String())
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Errorf("refusal bodies differ:\n%s\n%s", bodies[0], b)
		}
	}
	devices, err := f.manager.ListDevices(t.Context(), subjectA)
	if err != nil || len(devices) != 0 {
		t.Fatalf("devices after refusals = %v, %v", devices, err)
	}
}

func TestIssueMapsTheCallbackErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	f.verifyFn = func(context.Context, devicehttp.Credentials) (string, error) {
		return "", fmt.Errorf("the account table at db-internal.example.test failed: %w", devicehttp.ErrBusy)
	}
	w := f.do(http.MethodPost, "/auth/token", credsA())
	wantStatus(t, w, http.StatusServiceUnavailable)
	wantNoStore(t, w)
	if w.Header().Get("Retry-After") == "" {
		t.Error("a busy answer carries no Retry-After")
	}

	f.verifyFn = func(context.Context, devicehttp.Credentials) (string, error) {
		return "", errors.New("the account table at db-internal.example.test failed")
	}
	w = f.do(http.MethodPost, "/auth/token", credsA())
	wantStatus(t, w, http.StatusInternalServerError)
	wantNoStore(t, w)
	if strings.Contains(w.Body.String(), "db-internal") {
		t.Errorf("a cause reached the client: %s", w.Body.String())
	}

	f.verifyFn = func(context.Context, devicehttp.Credentials) (string, error) { return "", nil }
	w = f.do(http.MethodPost, "/auth/token", credsA())
	wantStatus(t, w, http.StatusInternalServerError)
}

func TestIssueChecksMethodBodyAndOriginBeforeTheCredentials(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *devicehttp.HandlerConfig) { c.MaxBodyBytes = 256 })

	good := `{"login":"` + loginA + `","password":"` + passwordA + `","code":"` + codeOK + `"}`
	cases := []struct {
		name   string
		method string
		body   any
		opts   []option
		want   int
	}{
		{"GET", http.MethodGet, nil, nil, http.StatusMethodNotAllowed},
		{"PUT", http.MethodPut, good, nil, http.StatusMethodNotAllowed},
		{"PATCH", http.MethodPatch, good, nil, http.StatusMethodNotAllowed},
		{"form content type", http.MethodPost, good, []option{withHeader("Content-Type", "application/x-www-form-urlencoded")}, http.StatusUnsupportedMediaType},
		{"text content type", http.MethodPost, good, []option{withHeader("Content-Type", "text/plain")}, http.StatusUnsupportedMediaType},
		{"unknown field", http.MethodPost, `{"login":"a","password":"b","code":"c","admin":true}`, nil, http.StatusBadRequest},
		{"second value", http.MethodPost, good + good, nil, http.StatusBadRequest},
		{"not an object", http.MethodPost, `["a"]`, nil, http.StatusBadRequest},
		{"too large", http.MethodPost, `{"login":"` + strings.Repeat("a", 300) + `"}`, nil, http.StatusRequestEntityTooLarge},
		{"cross site", http.MethodPost, good, []option{crossSite()}, http.StatusForbidden},
		{"name too long", http.MethodPost, `{"login":"` + loginA + `","device_name":"` + strings.Repeat("n", session.MaxDeviceNameBytes+1) + `"}`, nil, http.StatusBadRequest},
		{"name with a control character", http.MethodPost, `{"login":"` + loginA + `","device_name":"a\u0007b"}`, nil, http.StatusBadRequest},
		{"name with a newline", http.MethodPost, `{"login":"` + loginA + `","device_name":"a\nb"}`, nil, http.StatusBadRequest},
		{"name with a format character", http.MethodPost, `{"login":"` + loginA + `","device_name":"a\u202eb"}`, nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := f.verifyCount()
			w := f.do(tc.method, "/auth/token", tc.body, tc.opts...)
			wantStatus(t, w, tc.want)
			wantNoStore(t, w)
			if f.verifyCount() != before {
				t.Error("the credentials were checked before the request was refused")
			}
			if tc.want == http.StatusMethodNotAllowed && w.Header().Get("Allow") != http.MethodPost {
				t.Errorf("Allow = %q", w.Header().Get("Allow"))
			}
		})
	}
	if devices, _ := f.manager.ListDevices(t.Context(), subjectA); len(devices) != 0 {
		t.Fatalf("a refused request left %d devices", len(devices))
	}
}

func (f *fixture) verifyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.verifies
}

func TestIssueAcceptsATrustedOriginAndRefusesOthers(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	trusted := func(r *http.Request) {
		r.Header.Set("Origin", appOrigin)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}
	w := f.do(http.MethodPost, "/auth/token", credsA(), trusted)
	wantStatus(t, w, http.StatusCreated)

	w = f.do(http.MethodPost, "/auth/token", credsA(), crossSite())
	wantStatus(t, w, http.StatusForbidden)
}

func TestIssueDeviceNamePolicy(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *devicehttp.HandlerConfig) {
		c.DeviceName = func(requested string) (string, error) {
			switch requested {
			case "":
				return "Unnamed device", nil
			case "forbidden":
				return "", errors.New("not allowed")
			case "grows":
				return strings.Repeat("n", session.MaxDeviceNameBytes+1), nil
			}
			return requested, nil
		}
	})

	f.issue(credsA(), "")
	f.issue(credsA(), "Pixel")
	// A policy that refuses is a 400. A policy that returns a name the session
	// package refuses is its own bug: an opaque 500. Both are answered before
	// the credentials are checked, so no code is consumed.
	for name, want := range map[string]int{"forbidden": http.StatusBadRequest, "grows": http.StatusInternalServerError} {
		before := f.verifyCount()
		w := f.do(http.MethodPost, "/auth/token", map[string]string{"login": loginA, "password": passwordA, "code": codeOK, "device_name": name})
		wantStatus(t, w, want)
		wantNoStore(t, w)
		if f.verifyCount() != before {
			t.Errorf("%s: the credentials were checked", name)
		}
		if strings.Contains(w.Body.String(), "policy") {
			t.Errorf("%s: a cause reached the client: %s", name, w.Body.String())
		}
	}
	devices, err := f.manager.ListDevices(t.Context(), subjectA)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, d := range devices {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Pixel", "Unnamed device"}) {
		t.Errorf("names = %v", names)
	}
}

// The handler checks a name before the credentials, and the session package
// checks it again. The two must agree, or a name that passes here is refused
// after the credentials are known to be right.
func TestIssueNameRulesMatchTheSessionPackage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	names := []string{
		"", "Pixel 9", "Simon's phone", "\u65e5\u672c\u8a9e", strings.Repeat("n", session.MaxDeviceNameBytes),
		strings.Repeat("n", session.MaxDeviceNameBytes+1), "tab\there", "a\x00b", "a\x7fb", "a\u200bb",
		"a\u202eb", "a\ufeffb", "a\u0085b", "\u00a0nbsp", "emoji \U0001F4F1",
		strings.Repeat("\u00e9", session.MaxDeviceNameBytes/2), strings.Repeat("\u00e9", session.MaxDeviceNameBytes/2+1),
	}
	for _, name := range names {
		_, _, sessionErr := f.manager.IssueDevice(t.Context(), "parity-subject", name, f.clock.Now())
		raw, _ := json.Marshal(map[string]string{"login": loginA, "password": passwordA, "code": codeOK, "device_name": name})
		w := f.do(http.MethodPost, "/auth/token", string(raw))
		handlerOK := w.Code == http.StatusCreated
		if handlerOK != (sessionErr == nil) {
			t.Errorf("name %q: handler status %d, session error %v", name, w.Code, sessionErr)
		}
	}
}

func TestIssueIgnoresABearerTokenAndAuthenticatesByTheCredentialsOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a := f.issue(credsA(), "A phone")

	// A valid token and no credentials is no sign in.
	w := f.do(http.MethodPost, "/auth/token", map[string]string{"device_name": "x"}, bearerOf(a.Token))
	wantStatus(t, w, http.StatusUnauthorized)
	if got := errorCode(t, w); got != devicehttp.CodeInvalidCredentials {
		t.Errorf("code = %q", got)
	}
	devices, _ := f.manager.ListDevices(t.Context(), subjectA)
	if len(devices) != 1 {
		t.Fatalf("devices of A = %d, want 1", len(devices))
	}

	// A valid token and the credentials of B issue for B, never for A.
	b := f.issue(credsB(), "B phone", bearerOf(a.Token))
	d, err := f.manager.AuthenticateDevice(t.Context(), b.Token)
	if err != nil || d.Subject != subjectB {
		t.Fatalf("token of B belongs to %q, err %v", d.Subject, err)
	}

	// A cookie of A neither helps nor is read.
	w = f.do(http.MethodPost, "/auth/token", map[string]string{"device_name": "x"}, withCookie(f.cookieFor(subjectA)))
	wantStatus(t, w, http.StatusUnauthorized)

	// A junk token is the middleware's 401, before the handler.
	w = f.do(http.MethodPost, "/auth/token", credsA(), bearerOf("example_junk_token"))
	wantStatus(t, w, http.StatusUnauthorized)
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("no WWW-Authenticate on a refused bearer request")
	}
}

func TestIssueTakesTheCheckTimeBeforeTheVerification(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	// A password change lands during a slow verification. The device must not
	// be issued, because the check read the old credential.
	f.verifyFn = func(ctx context.Context, _ devicehttp.Credentials) (string, error) {
		f.clock.Add(time.Second)
		if err := f.manager.EndAll(ctx, subjectA); err != nil {
			t.Errorf("EndAll: %v", err)
		}
		f.clock.Add(time.Second)
		return subjectA, nil
	}
	w := f.do(http.MethodPost, "/auth/token", credsA())
	wantStatus(t, w, http.StatusConflict)
	wantNoStore(t, w)
	if got := errorCode(t, w); got != devicehttp.CodeStale {
		t.Errorf("code = %q", got)
	}
	if devices, _ := f.manager.ListDevices(t.Context(), subjectA); len(devices) != 0 {
		t.Fatalf("a device outlived the password change: %v", devices)
	}
}

func TestRotateIssuesANewTokenAndKeepsIDAndExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	first := f.issue(credsA(), "Pixel")

	// The minimum interval has not passed since the issue.
	w := f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "nonce-one"}, bearerOf(first.Token))
	wantStatus(t, w, http.StatusConflict)
	wantNoStore(t, w)
	if got := errorCode(t, w); got != devicehttp.CodeRotationRefused {
		t.Errorf("code = %q", got)
	}
	if strings.Contains(w.Body.String(), first.Token) {
		t.Error("a refusal echoes the token")
	}

	f.clock.Add(session.DefaultDeviceMinRotateInterval + time.Minute)
	w = f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "nonce-two"}, bearerOf(first.Token))
	wantStatus(t, w, http.StatusOK)
	wantNoStore(t, w)
	if len(w.Result().Cookies()) != 0 {
		t.Errorf("Set-Cookie = %v", w.Result().Cookies())
	}
	var next devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if next.Token == "" || next.Token == first.Token {
		t.Fatal("rotation returned no new token")
	}
	if next.TokenID != first.TokenID {
		t.Errorf("token id moved: %q to %q", first.TokenID, next.TokenID)
	}
	if !next.ExpiresAt.Equal(first.ExpiresAt) {
		t.Errorf("expires_at moved: %s to %s", first.ExpiresAt, next.ExpiresAt)
	}
	// Both tokens work: the old one is current until the new one is used.
	if _, err := f.manager.AuthenticateDevice(t.Context(), first.Token); err != nil {
		t.Errorf("the old token stopped working: %v", err)
	}
	// What the store holds, not what the answer says, keeps its expiry.
	_, listed := f.list(bearerOf(first.Token))
	if len(listed.Devices) != 1 || !listed.Devices[0].ExpiresAt.Equal(first.ExpiresAt) {
		t.Errorf("the stored expiry moved: %+v, want %s", listed.Devices, first.ExpiresAt)
	}
}

func TestRotateNeedsABearerTokenAndNeverACookie(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	tok := f.issue(credsA(), "Pixel")
	f.clock.Add(2 * time.Hour)
	body := map[string]string{"nonce": "nonce-one"}

	for name, opts := range map[string][]option{
		"neither":          nil,
		"cookie only":      {withCookie(f.cookieFor(subjectA))},
		"junk bearer":      {bearerOf("example_junk_token")},
		"junk with cookie": {bearerOf("example_junk_token"), withCookie(f.cookieFor(subjectA))},
		"basic":            {withHeader("Authorization", "Basic dXNlcjpwYXNz")},
		"two headers": {func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer "+tok.Token)
			r.Header.Add("Authorization", "Bearer "+tok.Token)
		}},
		"empty bearer":       {withHeader("Authorization", "Bearer")},
		"token in the body":  nil,
		"token in the query": nil,
	} {
		t.Run(name, func(t *testing.T) {
			path := "/auth/token/rotate"
			b := any(body)
			switch name {
			case "token in the body":
				b = map[string]string{"nonce": "nonce-one", "token": tok.Token}
			case "token in the query":
				path += "?access_token=" + tok.Token
			}
			w := f.do(http.MethodPost, path, b, opts...)
			wantNoStore(t, w)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), `"token"`) && w.Code == http.StatusOK {
				t.Fatal("a token was issued")
			}
		})
	}
	// None of them rotated anything: the token still rotates once.
	w := f.do(http.MethodPost, "/auth/token/rotate", body, bearerOf(tok.Token))
	wantStatus(t, w, http.StatusOK)
}

func TestRotateChecksMethodAndNonce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	tok := f.issue(credsA(), "Pixel")
	f.clock.Add(2 * time.Hour)

	w := f.do(http.MethodGet, "/auth/token/rotate", nil, bearerOf(tok.Token))
	wantStatus(t, w, http.StatusMethodNotAllowed)
	wantNoStore(t, w)
	for name, body := range map[string]any{
		"empty nonce":   map[string]string{"nonce": ""},
		"nonce spaces":  map[string]string{"nonce": "has space"},
		"unknown field": `{"nonce":"n1","extra":1}`,
		"long nonce":    map[string]string{"nonce": strings.Repeat("n", session.MaxDeviceNonceBytes+1)},
	} {
		w := f.do(http.MethodPost, "/auth/token/rotate", body, bearerOf(tok.Token))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
	// No body is no JSON, whatever the token.
	w = f.do(http.MethodPost, "/auth/token/rotate", nil, bearerOf(tok.Token))
	wantStatus(t, w, http.StatusUnsupportedMediaType)
	// Cross site is no reason to refuse a bearer request.
	w = f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "n1"}, bearerOf(tok.Token), crossSite())
	wantStatus(t, w, http.StatusOK)
}

// The grace test of ADR 0001: two concurrent requests with the same retired
// token inside the window both succeed. The same token after the window
// revokes the whole device, the holder of the new token included.
func TestRotationGraceAndReuseDetection(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	old := f.issue(credsA(), "Pixel")
	f.clock.Add(session.DefaultDeviceMinRotateInterval + time.Minute)

	w := f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "n1"}, bearerOf(old.Token))
	wantStatus(t, w, http.StatusOK)
	var next devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	// The first use of the new token confirms it and retires the old one.
	if got, _ := f.list(bearerOf(next.Token)); got.Code != http.StatusOK {
		t.Fatalf("list with the new token = %d", got.Code)
	}

	f.clock.Add(time.Minute)
	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/devices", nil)
			r.Header.Set("Authorization", "Bearer "+old.Token)
			statuses[i] = f.serve(r).Code
		})
	}
	wg.Wait()
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusOK {
		t.Fatalf("two concurrent requests with the retired token inside the grace = %v, want both 200", statuses)
	}
	// A rotation with the retired token is refused and revokes nothing.
	w = f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "n2"}, bearerOf(old.Token))
	wantStatus(t, w, http.StatusConflict)
	if got, _ := f.list(bearerOf(next.Token)); got.Code != http.StatusOK {
		t.Fatalf("the new token after a refused rotation = %d", got.Code)
	}

	// After the grace the retired token is reuse: 401, and the device is gone.
	f.clock.Add(session.DefaultDeviceGrace)
	w, _ = f.list(bearerOf(old.Token))
	wantStatus(t, w, http.StatusUnauthorized)
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("no challenge on a reused token")
	}
	wantNoStore(t, w)
	if got, _ := f.list(bearerOf(next.Token)); got.Code != http.StatusUnauthorized {
		t.Fatalf("the legitimate holder after the reuse = %d, want 401", got.Code)
	}
	devices, err := f.manager.ListDevices(t.Context(), subjectA)
	if err != nil || len(devices) != 0 {
		t.Fatalf("devices after the reuse = %v, %v", devices, err)
	}
	if !strings.Contains(f.logs.String(), "reused") {
		t.Error("the reuse event was not logged")
	}
}

func TestConcurrentRotationsOfOneTokenNeverFailTheToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	tok := f.issue(credsA(), "Pixel")
	f.clock.Add(2 * time.Hour)

	statuses := make([]int, 4)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Go(func() {
			body := fmt.Sprintf(`{"nonce":"nonce-%d"}`, i)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/token/rotate", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+tok.Token)
			statuses[i] = f.serve(r).Code
		})
	}
	wg.Wait()
	ok := 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Errorf("status %d among %v", s, statuses)
		}
	}
	if ok == 0 {
		t.Errorf("no rotation succeeded: %v", statuses)
	}
	if _, err := f.manager.AuthenticateDevice(t.Context(), tok.Token); err != nil {
		t.Errorf("the token fails after concurrent rotations: %v", err)
	}
}

func TestListShowsTheCallersDevicesOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a1 := f.issue(credsA(), "Pixel")
	f.clock.Add(time.Minute)
	a2 := f.issue(credsA(), "Tablet")
	b1 := f.issue(credsB(), "Other phone")

	w, got := f.list(bearerOf(a1.Token))
	wantStatus(t, w, http.StatusOK)
	wantNoStore(t, w)
	if len(got.Devices) != 2 {
		t.Fatalf("devices = %+v, want 2", got.Devices)
	}
	ids := map[string]devicehttp.DeviceInfo{}
	for _, d := range got.Devices {
		ids[d.ID] = d
	}
	if ids[a1.TokenID].Name != "Pixel" || !ids[a1.TokenID].Current {
		t.Errorf("first device = %+v", ids[a1.TokenID])
	}
	if ids[a2.TokenID].Name != "Tablet" || ids[a2.TokenID].Current {
		t.Errorf("second device = %+v", ids[a2.TokenID])
	}
	if _, ok := ids[b1.TokenID]; ok {
		t.Error("another account's device is listed")
	}
	if ids[a1.TokenID].CreatedAt.IsZero() || ids[a1.TokenID].LastSeen.IsZero() || !ids[a1.TokenID].ExpiresAt.Equal(a1.ExpiresAt) {
		t.Errorf("times = %+v", ids[a1.TokenID])
	}

	// The documented keys and nothing else.
	var raw struct {
		Devices []map[string]json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{"created_at", "current", "expires_at", "id", "last_seen", "name"}
	for _, d := range raw.Devices {
		keys := make([]string, 0, len(d))
		for k := range d {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, want) {
			t.Errorf("device keys = %v, want %v", keys, want)
		}
	}
	for _, tok := range []string{a1.Token, a2.Token, b1.Token} {
		if strings.Contains(w.Body.String(), tok) {
			t.Error("the list carries a token")
		}
	}

	// A cookie session of A lists the same two, none of them current.
	w, got = f.list(withCookie(f.cookieFor(subjectA)))
	wantStatus(t, w, http.StatusOK)
	if len(got.Devices) != 2 || got.Devices[0].Current || got.Devices[1].Current {
		t.Errorf("cookie list = %+v", got.Devices)
	}

	// An account without devices gets an empty array, never null.
	w, _ = f.list(withCookie(f.cookieFor("subject-without-devices")))
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"devices":[]`) {
		t.Errorf("empty list = %s", w.Body.String())
	}
}

func TestListNeedsAuthenticationAndTheRightMethod(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.issue(credsA(), "Pixel")

	w, _ := f.list()
	wantStatus(t, w, http.StatusUnauthorized)
	wantNoStore(t, w)
	w, _ = f.list(bearerOf("example_junk_token"))
	wantStatus(t, w, http.StatusUnauthorized)
	w, _ = f.list(bearerOf("example_junk_token"), withCookie(f.cookieFor(subjectA)))
	wantStatus(t, w, http.StatusUnauthorized)

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodHead} {
		w := f.do(m, "/auth/devices", nil, withCookie(f.cookieFor(subjectA)))
		wantStatus(t, w, http.StatusMethodNotAllowed)
	}
}

func TestRevokeEndsOneDeviceOfTheCaller(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a1 := f.issue(credsA(), "Pixel")
	a2 := f.issue(credsA(), "Tablet")

	w := f.do(http.MethodDelete, "/auth/devices/"+a2.TokenID, nil, bearerOf(a1.Token))
	wantStatus(t, w, http.StatusNoContent)
	wantNoStore(t, w)
	if _, err := f.manager.AuthenticateDevice(t.Context(), a2.Token); !errors.Is(err, session.ErrDeviceToken) {
		t.Errorf("the revoked token authenticates: %v", err)
	}
	if _, err := f.manager.AuthenticateDevice(t.Context(), a1.Token); err != nil {
		t.Errorf("the other device is gone: %v", err)
	}

	// A cookie session revokes too, here the calling device's neighbour.
	a3 := f.issue(credsA(), "Watch")
	w = f.do(http.MethodDelete, "/auth/devices/"+a3.TokenID, nil, withCookie(f.cookieFor(subjectA)))
	wantStatus(t, w, http.StatusNoContent)

	// A second revoke of the same id is an unknown id.
	w = f.do(http.MethodDelete, "/auth/devices/"+a3.TokenID, nil, bearerOf(a1.Token))
	wantStatus(t, w, http.StatusNotFound)
}

func TestRevokeAnswersAnotherSubjectsDeviceLikeAnUnknownID(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a := f.issue(credsA(), "A phone")
	b := f.issue(credsB(), "B phone")

	other := f.do(http.MethodDelete, "/auth/devices/"+b.TokenID, nil, bearerOf(a.Token))
	unknown := f.do(http.MethodDelete, "/auth/devices/"+strings.Repeat("x", len(b.TokenID)), nil, bearerOf(a.Token))
	wantStatus(t, other, http.StatusNotFound)
	if other.Code != unknown.Code || other.Body.String() != unknown.Body.String() {
		t.Errorf("other subject: %d %s\nunknown: %d %s", other.Code, other.Body, unknown.Code, unknown.Body)
	}
	for _, h := range []string{"Content-Type", "Cache-Control", "Retry-After", "WWW-Authenticate"} {
		if other.Header().Get(h) != unknown.Header().Get(h) {
			t.Errorf("header %s differs: %q and %q", h, other.Header().Get(h), unknown.Header().Get(h))
		}
	}
	// With a cookie session too.
	other = f.do(http.MethodDelete, "/auth/devices/"+b.TokenID, nil, withCookie(f.cookieFor(subjectA)))
	wantStatus(t, other, http.StatusNotFound)

	if _, err := f.manager.AuthenticateDevice(t.Context(), b.Token); err != nil {
		t.Fatalf("B's device was revoked by A: %v", err)
	}
	// The empty and the odd ids are unknown ids as well.
	for _, id := range []string{"%20", "..", "a%2Fb", strings.Repeat("z", 5000)} {
		w := f.do(http.MethodDelete, "/auth/devices/"+id, nil, bearerOf(a.Token))
		if w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently && w.Code != http.StatusTemporaryRedirect {
			t.Errorf("id %q = %d", id, w.Code)
		}
	}
}

func TestRevokeChecksAuthenticationMethodAndOrigin(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a := f.issue(credsA(), "A phone")
	a2 := f.issue(credsA(), "Second")

	w := f.do(http.MethodDelete, "/auth/devices/"+a2.TokenID, nil)
	wantStatus(t, w, http.StatusUnauthorized)
	w = f.do(http.MethodDelete, "/auth/devices/"+a2.TokenID, nil, bearerOf("example_junk_token"), withCookie(f.cookieFor(subjectA)))
	wantStatus(t, w, http.StatusUnauthorized)
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
		w = f.do(m, "/auth/devices/"+a2.TokenID, nil, bearerOf(a.Token))
		wantStatus(t, w, http.StatusMethodNotAllowed)
	}
	// A cross site request that rides the cookie is refused by the origin
	// check. A bearer request is not ambient and passes.
	w = f.do(http.MethodDelete, "/auth/devices/"+a2.TokenID, nil, withCookie(f.cookieFor(subjectA)), crossSite())
	wantStatus(t, w, http.StatusForbidden)
	if _, err := f.manager.AuthenticateDevice(t.Context(), a2.Token); err != nil {
		t.Fatalf("a refused request revoked the device: %v", err)
	}
	w = f.do(http.MethodDelete, "/auth/devices/"+a2.TokenID, nil, bearerOf(a.Token), crossSite())
	wantStatus(t, w, http.StatusNoContent)
}

func TestSignOutEndsTheCallingDeviceOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	a1 := f.issue(credsA(), "Pixel")
	a2 := f.issue(credsA(), "Tablet")
	b := f.issue(credsB(), "Other")

	w := f.do(http.MethodDelete, "/auth/token", nil, bearerOf(a1.Token))
	wantStatus(t, w, http.StatusNoContent)
	wantNoStore(t, w)
	if len(w.Result().Cookies()) != 0 {
		t.Errorf("Set-Cookie = %v", w.Result().Cookies())
	}
	if _, err := f.manager.AuthenticateDevice(t.Context(), a1.Token); !errors.Is(err, session.ErrDeviceToken) {
		t.Errorf("the signed out token authenticates: %v", err)
	}
	for name, tok := range map[string]string{"second device": a2.Token, "other account": b.Token} {
		if _, err := f.manager.AuthenticateDevice(t.Context(), tok); err != nil {
			t.Errorf("%s was ended: %v", name, err)
		}
	}
	// A second sign out with the same token is a 401, not a success.
	w = f.do(http.MethodDelete, "/auth/token", nil, bearerOf(a1.Token))
	wantStatus(t, w, http.StatusUnauthorized)
}

func TestSignOutNeedsABearerTokenAndNeverTouchesTheCookie(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	tok := f.issue(credsA(), "Pixel")
	cookie := f.cookieFor(subjectA)

	for name, opts := range map[string][]option{
		"neither":          nil,
		"cookie only":      {withCookie(cookie)},
		"junk":             {bearerOf("example_junk_token")},
		"junk with cookie": {bearerOf("example_junk_token"), withCookie(cookie)},
		"basic":            {withHeader("Authorization", "Basic dXNlcjpwYXNz")},
	} {
		w := f.do(http.MethodDelete, "/auth/token", nil, opts...)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, w.Code)
		}
		wantNoStore(t, w)
		if len(w.Result().Cookies()) != 0 {
			t.Errorf("%s: Set-Cookie = %v", name, w.Result().Cookies())
		}
	}
	// The cookie session is alive, and so is the device.
	if w, _ := f.list(withCookie(cookie)); w.Code != http.StatusOK {
		t.Errorf("the cookie session was ended: %d", w.Code)
	}
	if _, err := f.manager.AuthenticateDevice(t.Context(), tok.Token); err != nil {
		t.Errorf("the device was ended: %v", err)
	}
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		w := f.do(m, "/auth/token", nil, bearerOf(tok.Token))
		wantStatus(t, w, http.StatusMethodNotAllowed)
	}
}

// hashForms is every spelling of the hash of a token that a careless log line
// or body could carry.
func hashForms(token string) []string {
	sum := sha256.Sum256([]byte(token))
	// A structured logger writes a byte array as a JSON array of numbers.
	asJSON, _ := json.Marshal(sum)
	return []string{
		string(asJSON),
		hex.EncodeToString(sum[:]),
		strings.ToUpper(hex.EncodeToString(sum[:])),
		base64.StdEncoding.EncodeToString(sum[:]),
		base64.RawStdEncoding.EncodeToString(sum[:]),
		base64.URLEncoding.EncodeToString(sum[:]),
		base64.RawURLEncoding.EncodeToString(sum[:]),
		fmt.Sprint(sum),
		fmt.Sprintf("%v", sum[:]),
	}
}

func TestNoTokenOrHashReachesALogAnErrorBodyOrAList(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	var tokens []string
	first := f.issue(credsA(), "Pixel")
	tokens = append(tokens, first.Token)
	second := f.issue(credsB(), "Other")
	tokens = append(tokens, second.Token)

	f.clock.Add(2 * time.Hour)
	w := f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "n1"}, bearerOf(first.Token))
	wantStatus(t, w, http.StatusOK)
	var rotated devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	tokens = append(tokens, rotated.Token)

	// Every failure class, with the tokens in the request.
	f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "n1"}, bearerOf(first.Token))
	f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "bad nonce"}, bearerOf(first.Token))
	f.do(http.MethodPost, "/auth/token/rotate", `{"nonce":"n","token":"`+first.Token+`"}`, bearerOf(first.Token))
	f.do(http.MethodPost, "/auth/token", `{"login":"a","token":"`+first.Token+`"}`, bearerOf(first.Token))
	f.do(http.MethodPost, "/auth/token", map[string]string{"login": loginA, "password": "fake-wrong-password"}, bearerOf(first.Token))
	f.do(http.MethodGet, "/auth/devices", nil, bearerOf(first.Token))
	f.do(http.MethodGet, "/auth/devices", nil, bearerOf(rotated.Token))
	f.do(http.MethodDelete, "/auth/devices/"+second.TokenID, nil, bearerOf(first.Token))
	f.do(http.MethodDelete, "/auth/devices/"+first.TokenID, nil, bearerOf(second.Token))
	f.do(http.MethodPut, "/auth/devices", nil, bearerOf(first.Token))
	f.do(http.MethodDelete, "/auth/token", nil, bearerOf(second.Token))
	f.do(http.MethodGet, "/auth/devices", nil, bearerOf(second.Token))
	// A reuse after the grace, which logs the event.
	f.clock.Add(session.DefaultDeviceGrace + time.Minute)
	f.do(http.MethodGet, "/auth/devices", nil, bearerOf(rotated.Token))
	f.do(http.MethodGet, "/auth/devices", nil, bearerOf(first.Token))
	f.do(http.MethodGet, "/auth/devices", nil, bearerOf(rotated.Token))

	logs := f.logs.String()
	if logs == "" {
		t.Fatal("the test logged nothing, so it proves nothing")
	}
	for _, tok := range tokens {
		forbidden := append([]string{tok}, hashForms(tok)...)
		for _, needle := range forbidden {
			if strings.Contains(logs, needle) {
				t.Errorf("the log carries %q", needle[:min(len(needle), 12)]+"...")
			}
		}
	}
	for _, needle := range []string{"Authorization", "authorization", "Bearer "} {
		if strings.Contains(logs, needle) {
			t.Errorf("the log carries %q", needle)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.recorded {
		success := (rec.method == http.MethodPost && rec.path == "/auth/token" && rec.status == http.StatusCreated) ||
			(rec.method == http.MethodPost && rec.path == "/auth/token/rotate" && rec.status == http.StatusOK)
		var own string
		if success {
			var res devicehttp.TokenResult
			if err := json.Unmarshal([]byte(rec.body), &res); err != nil {
				t.Fatalf("success body %s: %v", rec.body, err)
			}
			own = res.Token
			if strings.Count(rec.body, own) != 1 {
				t.Errorf("the token is in the body %d times, want once", strings.Count(rec.body, own))
			}
		}
		for _, tok := range tokens {
			if tok == own {
				continue
			}
			for _, needle := range append([]string{tok}, hashForms(tok)...) {
				if strings.Contains(rec.body, needle) {
					t.Errorf("%s %s (%d) carries a token or hash", rec.method, rec.path, rec.status)
				}
				for k, vs := range rec.header {
					if strings.Contains(strings.Join(vs, ","), needle) {
						t.Errorf("%s %s header %s carries a token or hash", rec.method, rec.path, k)
					}
				}
			}
		}
	}
}

func TestEveryAnswerIsNoStoreJSON(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	tok := f.issue(credsA(), "Pixel")
	f.do(http.MethodPost, "/auth/token/rotate", map[string]string{"nonce": "n1"}, bearerOf(tok.Token))
	f.list(bearerOf(tok.Token))
	f.do(http.MethodDelete, "/auth/devices/unknown", nil, bearerOf(tok.Token))
	f.do(http.MethodPut, "/auth/token", nil)
	f.do(http.MethodPost, "/auth/token", `{`)
	f.do(http.MethodDelete, "/auth/token", nil, bearerOf(tok.Token))

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.recorded {
		if got := rec.header.Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
			t.Errorf("%s %s (%d): Cache-Control = %q", rec.method, rec.path, rec.status, got)
		}
		if rec.status != http.StatusNoContent && !strings.HasPrefix(rec.header.Get("Content-Type"), "application/json") {
			t.Errorf("%s %s (%d): Content-Type = %q", rec.method, rec.path, rec.status, rec.header.Get("Content-Type"))
		}
		if len(rec.header.Values("Set-Cookie")) != 0 {
			t.Errorf("%s %s (%d): sets a cookie", rec.method, rec.path, rec.status)
		}
	}
}

// A refused trusted origin stays out of the error, and so does the error of
// the standard library, which quotes it.
func TestNewHandlersKeepsACredentialOutOfTheError(t *testing.T) {
	t.Parallel()

	for _, origin := range []string{
		"s3cr3tTOKEN",
		"https://user:s3cr3tTOKEN@app.example.test/",
		"https://app.example.test/?token=s3cr3tTOKEN",
		"https://user:s3cr3tTOKEN@app example.test\x7f",
	} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()

			_, err := devicehttp.NewHandlers(devicehttp.HandlerConfig{
				IssueTrustedOrigins: []string{"https://app.example.test", origin},
			})
			configtexttest.AssertHidden(t, err, "s3cr3tTOKEN", "trusted origin 2")
		})
	}
}

func TestNewHandlersNamesEveryProblem(t *testing.T) {
	t.Parallel()

	_, err := devicehttp.NewHandlers(devicehttp.HandlerConfig{MaxBodyBytes: -1, IssueTrustedOrigins: []string{"not an origin/"}})
	if err == nil {
		t.Fatal("an empty configuration is accepted")
	}
	for _, want := range []string{"sessions", "verify", "max body", "trusted origin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestHandlersAnswer401WithoutTheMiddleware(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	tok := f.issue(credsA(), "Pixel")
	f.clock.Add(2 * time.Hour)

	// Mounted without session.Manager.Middleware, the handlers that read the
	// session from the context look logged out. Rotate and sign out check the
	// token themselves.
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/devices", nil)
	r.Header.Set("Authorization", "Bearer "+tok.Token)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	wantStatus(t, w, http.StatusUnauthorized)
	wantNoStore(t, w)

	r = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/token/rotate", strings.NewReader(`{"nonce":"n1"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+tok.Token)
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	wantStatus(t, w, http.StatusOK)
}
