// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/account/accounttest"
	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/logging"
)

// Fake values only. None of them is anybody's password, secret or login.
const (
	hLogin       = "bob@example.com"
	hPassword    = "fake-handler-password-one"
	hNewPassword = "fake-handler-password-two"
	hWrong       = "fake-handler-password-wrong"
)

// hStart is the clock of every handler fixture until a test moves it.
var hStart = time.Unix(1_700_000_010, 0)

// flakyStore is a MemoryStore that fails the calls a test asks it to.
type flakyStore struct {
	*accounttest.MemoryStore

	mu      sync.Mutex
	find    error
	create  error
	setHash error
}

func (s *flakyStore) failures() (find, create, setHash error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.find, s.create, s.setHash
}

func (s *flakyStore) FindByLogin(ctx context.Context, login string) (account.Record, error) {
	if find, _, _ := s.failures(); find != nil {
		return account.Record{}, find
	}

	return s.MemoryStore.FindByLogin(ctx, login)
}

func (s *flakyStore) Create(ctx context.Context, rec account.Record) (account.Record, error) {
	if _, create, _ := s.failures(); create != nil {
		return account.Record{}, create
	}

	return s.MemoryStore.Create(ctx, rec)
}

func (s *flakyStore) SetPasswordHash(ctx context.Context, id, oldHash, newHash string) error {
	if _, _, setHash := s.failures(); setHash != nil {
		return setHash
	}

	return s.MemoryStore.SetPasswordHash(ctx, id, oldHash, newHash)
}

// hFixture is the handlers over a real service and an in memory store, with
// every callback recorded.
type hFixture struct {
	t        *testing.T
	store    *flakyStore
	handlers *account.Handlers
	logs     *bytes.Buffer
	logger   *slog.Logger

	derivations atomic.Int64

	mu         sync.Mutex
	clock      time.Time
	started    []account.Identity
	startErr   error
	endedAll   []string
	endAllErr  error
	loggedOut  int
	logoutErr  error
	current    account.Identity
	hasCurrent bool
	currentErr error
	allow      func(*http.Request) error
	allowCalls int
}

func newHFixture(t *testing.T, adjust func(*account.HandlerConfig)) *hFixture {
	t.Helper()

	f := &hFixture{
		t:     t,
		store: &flakyStore{MemoryStore: accounttest.NewMemoryStore(accounttest.MemoryConfig{})},
		logs:  &bytes.Buffer{},
		clock: hStart,
	}
	f.logger = slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	hasher, err := account.NewHasher(account.HasherConfig{})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	account.SetDerive(hasher, func(password, salt []byte, iterations, memoryKiB uint32, threads uint8, keyLen uint32) []byte {
		f.derivations.Add(1)

		return account.FakeDerive(password, salt, iterations, memoryKiB, threads, keyLen)
	})

	svc, err := account.NewService(account.ServiceConfig{
		Store:  f.store,
		Hasher: hasher,
		Issuer: "Example",
		EndSessions: func(_ context.Context, id string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.endedAll = append(f.endedAll, id)

			return f.endAllErr
		},
		Logger: f.logger,
		Now:    f.now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	cfg := account.HandlerConfig{
		Service: svc,
		StartSession: func(_ context.Context, _ http.ResponseWriter, _ *http.Request, id account.Identity) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.started = append(f.started, id)

			return f.startErr
		},
		EndSession: func(context.Context, http.ResponseWriter, *http.Request) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.loggedOut++

			return f.logoutErr
		},
		CurrentIdentity: func(*http.Request) (account.Identity, bool, error) {
			f.mu.Lock()
			defer f.mu.Unlock()

			return f.current, f.hasCurrent, f.currentErr
		},
		AllowSignup: func(r *http.Request) error {
			f.mu.Lock()
			f.allowCalls++
			allow := f.allow
			f.mu.Unlock()
			if allow == nil {
				return nil
			}

			return allow(r)
		},
	}
	if adjust != nil {
		adjust(&cfg)
	}

	f.handlers, err = account.NewHandlers(cfg)
	if err != nil {
		t.Fatalf("NewHandlers: %v", err)
	}

	return f
}

func (f *hFixture) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.clock
}

func (f *hFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(d)
}

func (f *hFixture) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.started)
}

func (f *hFixture) endedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.endedAll)
}

func (f *hFixture) loggedOutCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.loggedOut
}

func (f *hFixture) allowCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.allowCalls
}

func (f *hFixture) setCurrent(id account.Identity, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current, f.hasCurrent = id, ok
}

// untouched reports that no callback ran and no password was derived.
func (f *hFixture) untouched() bool {
	return f.startedCount() == 0 && f.endedCount() == 0 && f.loggedOutCount() == 0 &&
		f.allowCount() == 0 && f.derivations.Load() == 0
}

// asJSON marshals v. A body is built from a map so a test can name a field
// the real type does not have.
func asJSON(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}

	return string(raw)
}

// call serves one request with a JSON content type, unless a modifier
// changes that, and the fixture's logger in the context.
func (f *hFixture) call(h http.Handler, method, body string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()

	return f.callReader(h, method, strings.NewReader(body), mods...)
}

// unreadable is a body that fails the test when anything reads it, for a
// handler that must answer before it looks at the body.
type unreadable struct{ t *testing.T }

func (u unreadable) Read([]byte) (int, error) {
	u.t.Errorf("the handler read the request body")

	return 0, errors.New("body must not be read")
}

func (f *hFixture) callReader(h http.Handler, method string, body io.Reader, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()

	r := httptest.NewRequestWithContext(f.t.Context(), method, "/account", body)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(logging.NewContext(r.Context(), f.logger))
	for _, mod := range mods {
		mod(r)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	return w
}

func (f *hFixture) post(h http.Handler, body string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()

	return f.call(h, http.MethodPost, body, mods...)
}

// errorBody is the api error envelope.
type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func readError(t *testing.T, w *httptest.ResponseRecorder) errorBody {
	t.Helper()

	var body errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("answer %q is not the error shape: %v", w.Body.String(), err)
	}

	return body
}

// signUp signs hLogin up and returns the TOTP secret.
func (f *hFixture) signUp(login, password string) string {
	f.t.Helper()

	w := f.post(f.handlers.Signup(), asJSON(f.t, map[string]string{"login": login, "password": password}))
	if w.Code != http.StatusCreated {
		f.t.Fatalf("signup = %d %s, want 201", w.Code, w.Body.String())
	}
	var res account.SignupResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		f.t.Fatalf("signup body: %v", err)
	}

	return res.Enrolment.Secret
}

func (f *hFixture) code(secret string) string {
	f.t.Helper()

	code, err := account.TOTPCode(secret, f.now())
	if err != nil {
		f.t.Fatalf("TOTPCode: %v", err)
	}

	return code
}

func (f *hFixture) creds(login, password, code string) string {
	f.t.Helper()

	return asJSON(f.t, map[string]string{"login": login, "password": password, "code": code})
}

// register signs hLogin up and confirms the secret, then moves the clock two
// steps on so the confirming code is behind it, and returns the secret.
func (f *hFixture) register() string {
	f.t.Helper()

	secret := f.signUp(hLogin, hPassword)
	w := f.post(f.handlers.ConfirmTOTP(), f.creds(hLogin, hPassword, f.code(secret)))
	if w.Code != http.StatusOK {
		f.t.Fatalf("confirm = %d %s, want 200", w.Code, w.Body.String())
	}
	f.advance(time.Minute)

	f.mu.Lock()
	f.started = nil
	f.mu.Unlock()
	f.derivations.Store(0)

	return secret
}

func withHeader(key, value string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

func TestNewHandlersValidates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		adjust func(*account.HandlerConfig)
		want   string
	}{
		{"no service", func(c *account.HandlerConfig) { c.Service = nil }, "service is required"},
		{"no start", func(c *account.HandlerConfig) { c.StartSession = nil }, "start session is required"},
		{"no end", func(c *account.HandlerConfig) { c.EndSession = nil }, "end session is required"},
		{"no current", func(c *account.HandlerConfig) { c.CurrentIdentity = nil }, "current identity is required"},
		{"negative limit", func(c *account.HandlerConfig) { c.MaxBodyBytes = -1 }, "negative"},
		{"bad origin", func(c *account.HandlerConfig) { c.TrustedOrigins = []string{"app.example.test/"} }, "trusted origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A fresh, valid config first, then one problem.
			var cfg account.HandlerConfig
			f := newHFixture(t, func(c *account.HandlerConfig) { cfg = *c })
			_ = f
			tt.adjust(&cfg)

			_, err := account.NewHandlers(cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewHandlers = %v, want an error naming %q", err, tt.want)
			}
		})
	}

	if _, err := account.NewHandlers(account.HandlerConfig{}); err == nil ||
		!strings.Contains(err.Error(), "service is required") ||
		!strings.Contains(err.Error(), "end session is required") {
		t.Fatalf("NewHandlers(zero) = %v, want every problem at once", err)
	}
}

func TestHandlerSignup(t *testing.T) {
	t.Parallel()

	t.Run("creates the account and starts no session", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)

		w := f.post(f.handlers.Signup(), asJSON(t, map[string]string{"login": hLogin, "password": hPassword}))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d %s, want 201", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", got)
		}

		// The named fields are the contract, and a bare value is not.
		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("body is not a JSON object: %v", err)
		}
		enrolment, ok := raw["enrolment"].(map[string]any)
		if raw["login"] != hLogin || !ok || enrolment["secret"] == "" || enrolment["uri"] == "" {
			t.Fatalf("body = %s, want login and enrolment with secret and uri", w.Body.String())
		}
		if uri, _ := enrolment["uri"].(string); !strings.HasPrefix(uri, "otpauth://totp/") {
			t.Fatalf("uri = %q, want an otpauth uri", uri)
		}
		if f.startedCount() != 0 {
			t.Fatal("signup started a session")
		}
		if _, err := f.store.FindByLogin(t.Context(), hLogin); err != nil {
			t.Fatalf("account was not created: %v", err)
		}
	})

	failures := []struct {
		name   string
		prep   func(f *hFixture)
		body   func(t *testing.T) string
		status int
		code   string
		detail string
	}{
		{
			name:   "login that is empty",
			body:   func(t *testing.T) string { return asJSON(t, map[string]string{"login": "", "password": hPassword}) },
			status: http.StatusBadRequest, code: api.CodeInvalidArgument, detail: "login",
		},
		{
			name: "login with a colon",
			body: func(t *testing.T) string {
				return asJSON(t, map[string]string{"login": "a:b", "password": hPassword})
			},
			status: http.StatusBadRequest, code: api.CodeInvalidArgument, detail: "login",
		},
		{
			name: "password too short",
			body: func(t *testing.T) string {
				return asJSON(t, map[string]string{"login": hLogin, "password": "short"})
			},
			status: http.StatusBadRequest, code: api.CodeInvalidArgument, detail: "password",
		},
		{
			name: "password too long",
			body: func(t *testing.T) string {
				return asJSON(t, map[string]string{"login": hLogin, "password": strings.Repeat("x", account.MaxPasswordLength+1)})
			},
			status: http.StatusBadRequest, code: api.CodeInvalidArgument, detail: "password",
		},
		{
			name: "login that is taken",
			prep: func(f *hFixture) { f.signUp(hLogin, hPassword) },
			body: func(t *testing.T) string {
				return asJSON(t, map[string]string{"login": hLogin, "password": hNewPassword})
			},
			status: http.StatusConflict, code: api.CodeConflict,
		},
		{
			name: "store that fails",
			prep: func(f *hFixture) { f.store.create = errors.New("connection to db-secret-host refused") },
			body: func(t *testing.T) string {
				return asJSON(t, map[string]string{"login": hLogin, "password": hPassword})
			},
			status: http.StatusInternalServerError, code: api.CodeInternal,
		},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newHFixture(t, nil)
			if tt.prep != nil {
				tt.prep(f)
			}

			w := f.post(f.handlers.Signup(), tt.body(t))
			if w.Code != tt.status {
				t.Fatalf("status = %d %s, want %d", w.Code, w.Body.String(), tt.status)
			}
			got := readError(t, w)
			if got.Error.Code != tt.code {
				t.Fatalf("code = %q, want %q", got.Error.Code, tt.code)
			}
			if tt.detail != "" {
				if _, ok := got.Error.Details[tt.detail]; !ok {
					t.Fatalf("details = %v, want an entry for %q", got.Error.Details, tt.detail)
				}
			}
			if strings.Contains(w.Body.String(), "db-secret-host") {
				t.Fatalf("the answer carries the cause: %s", w.Body.String())
			}
			if f.startedCount() != 0 {
				t.Fatal("a failed signup started a session")
			}
		})
	}

	t.Run("busy answers 503 with Retry-After", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		w := f.post(f.handlers.Signup(), asJSON(t, map[string]string{"login": hLogin, "password": hPassword}),
			func(r *http.Request) { *r = *r.WithContext(logging.NewContext(ctx, f.logger)) })
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Fatalf("answer = %d, Retry-After %q, want 503 with Retry-After", w.Code, w.Header().Get("Retry-After"))
		}
		if _, err := f.store.FindByLogin(t.Context(), hLogin); !errors.Is(err, account.ErrNotFound) {
			t.Fatalf("a busy signup created an account: %v", err)
		}
	})

	t.Run("AllowSignup refusal stops it before any work", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.allow = func(*http.Request) error { return errors.New("invite list: secret-invite-detail") }

		// The refusal comes before the body is read: it cannot depend on it.
		w := f.callReader(f.handlers.Signup(), http.MethodPost, unreadable{t})
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d %s, want 403", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret-invite-detail") {
			t.Fatalf("the answer carries the callback's error: %s", w.Body.String())
		}
		if f.derivations.Load() != 0 {
			t.Fatal("a refused signup hashed a password")
		}
		if _, err := f.store.FindByLogin(t.Context(), hLogin); !errors.Is(err, account.ErrNotFound) {
			t.Fatalf("a refused signup created an account: %v", err)
		}
	})

	t.Run("AllowSignup api error is answered as it is", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.allow = func(*http.Request) error {
			return api.New(http.StatusTooManyRequests, api.CodeTooManyRequests, "the list is full")
		}

		w := f.post(f.handlers.Signup(), asJSON(t, map[string]string{"login": hLogin, "password": hPassword}))
		if w.Code != http.StatusTooManyRequests || readError(t, w).Error.Message != "the list is full" {
			t.Fatalf("answer = %d %s, want the callback's api error", w.Code, w.Body.String())
		}
	})

	t.Run("AllowSignup wrapped api error logs the outer error", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.allow = func(*http.Request) error {
			return fmt.Errorf("invite check for list 7: %w", api.PermissionDenied("signup is closed"))
		}

		w := f.callReader(f.handlers.Signup(), http.MethodPost, strings.NewReader(""))
		if w.Code != http.StatusForbidden || readError(t, w).Error.Message != "signup is closed" {
			t.Fatalf("answer = %d %s, want the inner api error", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "list 7") {
			t.Fatalf("the answer carries the outer error: %s", w.Body.String())
		}
		if !strings.Contains(f.logs.String(), "invite check for list 7") {
			t.Fatal("the outer error was not logged")
		}
	})

	t.Run("AllowSignup is asked once per accepted request", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)

		f.signUp(hLogin, hPassword)
		if got := f.allowCount(); got != 1 {
			t.Fatalf("AllowSignup calls = %d, want 1", got)
		}
	})
}

// loginFailureCases is every cause of a refused login, each as a request body
// built against a fresh fixture.
func loginFailureCases() []struct {
	name string
	body func(f *hFixture) string
} {
	return []struct {
		name string
		body func(f *hFixture) string
	}{
		{"unknown login", func(f *hFixture) string {
			secret := f.register()

			return f.creds("nobody@example.com", hPassword, f.code(secret))
		}},
		{"wrong password", func(f *hFixture) string {
			secret := f.register()

			return f.creds(hLogin, hWrong, f.code(secret))
		}},
		{"wrong code", func(f *hFixture) string {
			f.register()

			return f.creds(hLogin, hPassword, "000000")
		}},
		{"missing code", func(f *hFixture) string {
			f.register()

			return f.creds(hLogin, hPassword, "")
		}},
		{"replayed code", func(f *hFixture) string {
			secret := f.register()
			code := f.code(secret)
			if w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, code)); w.Code != http.StatusOK {
				f.t.Fatalf("first login = %d, want 200", w.Code)
			}
			f.mu.Lock()
			f.started = nil
			f.mu.Unlock()

			return f.creds(hLogin, hPassword, code)
		}},
		{"unconfirmed secret", func(f *hFixture) string {
			secret := f.signUp(hLogin, hPassword)

			return f.creds(hLogin, hPassword, f.code(secret))
		}},
		{"disabled account", func(f *hFixture) string {
			secret := f.register()
			rec, err := f.store.FindByLogin(f.t.Context(), hLogin)
			if err != nil || !f.store.Disable(rec.ID) {
				f.t.Fatalf("disable: %v", err)
			}

			return f.creds(hLogin, hPassword, f.code(secret))
		}},
		{"empty fields", func(f *hFixture) string {
			f.register()

			return `{}`
		}},
		{"login too long", func(f *hFixture) string {
			secret := f.register()

			return f.creds(strings.Repeat("a", account.MaxLoginLength+1), hPassword, f.code(secret))
		}},
		{"login the signup refuses", func(f *hFixture) string {
			secret := f.register()

			return f.creds("a:b", hPassword, f.code(secret))
		}},
		{"password too long", func(f *hFixture) string {
			secret := f.register()

			return f.creds(hLogin, strings.Repeat("p", account.MaxPasswordLength+1), f.code(secret))
		}},
	}
}

func TestHandlerLogin(t *testing.T) {
	t.Parallel()

	t.Run("success starts one session for the identity", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.register()

		w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, f.code(secret)))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d %s, want 200", w.Code, w.Body.String())
		}
		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil || raw["login"] != hLogin {
			t.Fatalf("body = %s, want an object with the login", w.Body.String())
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.started) != 1 || f.started[0].Login != hLogin || f.started[0].ID == "" {
			t.Fatalf("started = %+v, want exactly one session for %s", f.started, hLogin)
		}
	})

	// One status and one body for every cause. The first case is the
	// reference, and every other one has to match it byte for byte.
	t.Run("every failure answers the same", func(t *testing.T) {
		t.Parallel()

		var want *httptest.ResponseRecorder
		for _, tt := range loginFailureCases() {
			f := newHFixture(t, nil)
			body := tt.body(f)

			w := f.post(f.handlers.Login(), body)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s: status = %d %s, want 401", tt.name, w.Code, w.Body.String())
			}
			if got := readError(t, w).Error.Code; got != account.CodeInvalidCredentials {
				t.Fatalf("%s: code = %q, want %q", tt.name, got, account.CodeInvalidCredentials)
			}
			if f.startedCount() != 0 {
				t.Fatalf("%s: a refused login started a session", tt.name)
			}
			if want == nil {
				want = w

				continue
			}
			if w.Body.String() != want.Body.String() {
				t.Errorf("%s: body = %s, want the reference %s", tt.name, w.Body.String(), want.Body.String())
			}
			for _, header := range []string{"Content-Type", "Cache-Control", "Retry-After", "WWW-Authenticate"} {
				if w.Header().Get(header) != want.Header().Get(header) {
					t.Errorf("%s: header %s = %q, want %q", tt.name, header, w.Header().Get(header), want.Header().Get(header))
				}
			}
		}
	})

	t.Run("busy answers 503 with Retry-After and is no login failure", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.register()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, f.code(secret)),
			func(r *http.Request) { *r = *r.WithContext(logging.NewContext(ctx, f.logger)) })
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d %s, want 503", w.Code, w.Body.String())
		}
		if got := w.Header().Get("Retry-After"); got != "2" {
			t.Fatalf("Retry-After = %q, want 2", got)
		}
		if got := readError(t, w).Error.Code; got != api.CodeUnavailable {
			t.Fatalf("code = %q, want %q", got, api.CodeUnavailable)
		}
		if f.startedCount() != 0 {
			t.Fatal("a busy login started a session")
		}
	})

	t.Run("a busy login does not consume the code", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.register()
		code := f.code(secret)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		f.post(f.handlers.Login(), f.creds(hLogin, hPassword, code),
			func(r *http.Request) { *r = *r.WithContext(logging.NewContext(ctx, f.logger)) })
		if w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, code)); w.Code != http.StatusOK {
			t.Fatalf("login after a busy answer = %d, want 200", w.Code)
		}
	})

	t.Run("a failing store answers 500 without a cause", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.register()
		f.store.find = errors.New("connection to db-secret-host refused")

		w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, f.code(secret)))
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "db-secret-host") {
			t.Fatalf("answer = %d %s, want an opaque 500", w.Code, w.Body.String())
		}
		if f.startedCount() != 0 {
			t.Fatal("a failed login started a session")
		}
	})

	t.Run("a failing StartSession answers 500 without a cause", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.register()
		f.startErr = errors.New("session store db-secret-host down")

		w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, f.code(secret)))
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "db-secret-host") {
			t.Fatalf("answer = %d %s, want an opaque 500", w.Code, w.Body.String())
		}
	})
}

func TestHandlerConfirmTOTP(t *testing.T) {
	t.Parallel()

	t.Run("success starts a session and the code is consumed", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.signUp(hLogin, hPassword)
		code := f.code(secret)

		w := f.post(f.handlers.ConfirmTOTP(), f.creds(hLogin, hPassword, code))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d %s, want 200", w.Code, w.Body.String())
		}
		if f.startedCount() != 1 {
			t.Fatalf("started = %d sessions, want 1", f.startedCount())
		}
		if w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, code)); w.Code != http.StatusUnauthorized {
			t.Fatalf("login with the confirming code = %d, want 401", w.Code)
		}
	})

	t.Run("no session before the second factor passed", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.signUp(hLogin, hPassword)

		for name, body := range map[string]string{
			"wrong code":     f.creds(hLogin, hPassword, "000000"),
			"wrong password": f.creds(hLogin, hWrong, f.code(secret)),
			"unknown login":  f.creds("nobody@example.com", hPassword, f.code(secret)),
		} {
			if w := f.post(f.handlers.ConfirmTOTP(), body); w.Code != http.StatusUnauthorized {
				t.Fatalf("%s: status = %d %s, want 401", name, w.Code, w.Body.String())
			}
		}
		if f.startedCount() != 0 {
			t.Fatal("a refused confirmation started a session")
		}
	})

	t.Run("a refused confirmation answers like a refused login", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.signUp(hLogin, hPassword)

		confirm := f.post(f.handlers.ConfirmTOTP(), f.creds(hLogin, hPassword, "000000"))
		login := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, "000000"))
		if confirm.Code != login.Code || confirm.Body.String() != login.Body.String() {
			t.Fatalf("confirm = %d %s, login = %d %s, want the same", confirm.Code, confirm.Body.String(), login.Code, login.Body.String())
		}
	})

	t.Run("an already confirmed secret is refused", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.register()

		if w := f.post(f.handlers.ConfirmTOTP(), f.creds(hLogin, hPassword, f.code(secret))); w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
		if f.startedCount() != 0 {
			t.Fatal("a refused confirmation started a session")
		}
	})

	t.Run("busy answers 503 and a failing StartSession 500", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.signUp(hLogin, hPassword)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		w := f.post(f.handlers.ConfirmTOTP(), f.creds(hLogin, hPassword, f.code(secret)),
			func(r *http.Request) { *r = *r.WithContext(logging.NewContext(ctx, f.logger)) })
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Fatalf("busy answer = %d, want 503 with Retry-After", w.Code)
		}

		f.startErr = errors.New("db-secret-host down")
		w = f.post(f.handlers.ConfirmTOTP(), f.creds(hLogin, hPassword, f.code(secret)))
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "db-secret-host") {
			t.Fatalf("StartSession failure = %d %s, want an opaque 500", w.Code, w.Body.String())
		}
	})
}

func TestHandlerChangePassword(t *testing.T) {
	t.Parallel()

	change := func(f *hFixture, current, next string) *httptest.ResponseRecorder {
		return f.post(f.handlers.ChangePassword(), asJSON(f.t, map[string]string{"current_password": current, "new_password": next}))
	}
	loggedIn := func(f *hFixture) {
		rec, err := f.store.FindByLogin(f.t.Context(), hLogin)
		if err != nil {
			f.t.Fatalf("find: %v", err)
		}
		f.setCurrent(account.Identity{ID: rec.ID, Login: rec.Login}, true)
	}

	t.Run("outcome one, changed and every session ended", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.register()
		loggedIn(f)

		w := change(f, hPassword, hNewPassword)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d %s, want 200", w.Code, w.Body.String())
		}
		var res account.PasswordChangeResult
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || !res.PasswordChanged || !res.SessionsEnded {
			t.Fatalf("body = %s, want password_changed and sessions_ended true", w.Body.String())
		}
		if f.endedCount() != 1 {
			t.Fatalf("EndSessions calls = %d, want 1", f.endedCount())
		}
		if f.startedCount() != 0 {
			t.Fatal("a password change started a session")
		}
	})

	t.Run("outcome two, changed and the sessions not ended", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.register()
		loggedIn(f)
		f.endAllErr = errors.New("session store db-secret-host down")

		w := change(f, hPassword, hNewPassword)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d %s, want 200", w.Code, w.Body.String())
		}
		var res account.PasswordChangeResult
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || !res.PasswordChanged || res.SessionsEnded {
			t.Fatalf("body = %s, want password_changed true and sessions_ended false", w.Body.String())
		}
		if strings.Contains(w.Body.String(), "db-secret-host") {
			t.Fatalf("the answer carries the cause: %s", w.Body.String())
		}
		if !strings.Contains(f.logs.String(), "ending the sessions failed") {
			t.Fatal("the failure to end the sessions was not logged")
		}
	})

	t.Run("outcome three, stale", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.register()
		loggedIn(f)
		f.store.setHash = account.ErrNotFound

		w := change(f, hPassword, hNewPassword)
		if w.Code != http.StatusConflict || readError(t, w).Error.Code != account.CodeStale {
			t.Fatalf("answer = %d %s, want 409 stale", w.Code, w.Body.String())
		}
		if f.endedCount() != 0 {
			t.Fatal("a stale change ended sessions")
		}
	})

	t.Run("outcome four, unknown", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.register()
		loggedIn(f)
		f.store.setHash = errors.New("driver: context deadline at db-secret-host")

		w := change(f, hPassword, hNewPassword)
		if w.Code != http.StatusInternalServerError || readError(t, w).Error.Code != account.CodePasswordChangeUnknown {
			t.Fatalf("answer = %d %s, want 500 password_change_unknown", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "db-secret-host") {
			t.Fatalf("the answer carries the cause: %s", w.Body.String())
		}
		if f.endedCount() != 1 {
			t.Fatalf("EndSessions calls = %d, want 1: the service ends them anyway", f.endedCount())
		}
	})

	t.Run("refusals end no session", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name    string
			current string
			next    string
			status  int
			code    string
			detail  string
		}{
			{"wrong current password", hWrong, hNewPassword, http.StatusForbidden, account.CodeInvalidCredentials, ""},
			{"new password too short", hPassword, "short", http.StatusBadRequest, api.CodeInvalidArgument, "new_password"},
			{"new password too long", hPassword, strings.Repeat("n", account.MaxPasswordLength+1), http.StatusBadRequest, api.CodeInvalidArgument, "new_password"},
		}
		for _, tt := range tests {
			f := newHFixture(t, nil)
			f.register()
			loggedIn(f)

			w := change(f, tt.current, tt.next)
			got := readError(t, w)
			if w.Code != tt.status || got.Error.Code != tt.code {
				t.Fatalf("%s: answer = %d %s, want %d %s", tt.name, w.Code, w.Body.String(), tt.status, tt.code)
			}
			if tt.detail != "" {
				if _, ok := got.Error.Details[tt.detail]; !ok {
					t.Fatalf("%s: details = %v, want %q", tt.name, got.Error.Details, tt.detail)
				}
			}
			if f.endedCount() != 0 || f.startedCount() != 0 {
				t.Fatalf("%s: a refused change ended or started a session", tt.name)
			}
		}
	})

	t.Run("busy answers 503", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.register()
		loggedIn(f)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		w := f.post(f.handlers.ChangePassword(), asJSON(t, map[string]string{"current_password": hPassword, "new_password": hNewPassword}),
			func(r *http.Request) { *r = *r.WithContext(logging.NewContext(ctx, f.logger)) })
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Fatalf("answer = %d, want 503 with Retry-After", w.Code)
		}
		if f.endedCount() != 0 {
			t.Fatal("a busy change ended sessions")
		}
	})

	t.Run("no session answers 401 and reads no body", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.register()

		w := f.callReader(f.handlers.ChangePassword(), http.MethodPost, unreadable{t})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
		if f.endedCount() != 0 || f.derivations.Load() != 0 {
			t.Fatal("an anonymous change did work")
		}
	})

	t.Run("a session store that fails answers 503", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.register()
		f.currentErr = errors.New("db-secret-host down")

		w := change(f, hPassword, hNewPassword)
		if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "db-secret-host") {
			t.Fatalf("answer = %d %s, want an opaque 503", w.Code, w.Body.String())
		}
	})

	t.Run("the new password logs in afterwards", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		secret := f.register()
		loggedIn(f)
		if w := change(f, hPassword, hNewPassword); w.Code != http.StatusOK {
			t.Fatalf("change = %d, want 200", w.Code)
		}

		if w := f.post(f.handlers.Login(), f.creds(hLogin, hPassword, f.code(secret))); w.Code != http.StatusUnauthorized {
			t.Fatalf("login with the old password = %d, want 401", w.Code)
		}
		if w := f.post(f.handlers.Login(), f.creds(hLogin, hNewPassword, f.code(secret))); w.Code != http.StatusOK {
			t.Fatalf("login with the new password = %d, want 200", w.Code)
		}
	})
}

func TestHandlerLogout(t *testing.T) {
	t.Parallel()

	t.Run("ends the session once and answers 204", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)

		w := f.post(f.handlers.Logout(), "")
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("answer = %d %q, want an empty 204", w.Code, w.Body.String())
		}
		if f.loggedOutCount() != 1 {
			t.Fatalf("EndSession calls = %d, want 1", f.loggedOutCount())
		}
	})

	t.Run("a failure answers 500 without a cause", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)
		f.logoutErr = errors.New("db-secret-host down")

		w := f.post(f.handlers.Logout(), "")
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "db-secret-host") {
			t.Fatalf("answer = %d %s, want an opaque 500", w.Code, w.Body.String())
		}
	})
}

// handlerCase is one handler with a valid body, for the tests every handler
// shares.
type handlerCase struct {
	name string
	get  func(h *account.Handlers) http.Handler
	body func(t *testing.T) string
}

func allHandlers() []handlerCase {
	creds := func(t *testing.T) string {
		return asJSON(t, map[string]string{"login": hLogin, "password": hPassword, "code": "000000"})
	}

	return []handlerCase{
		{"signup", (*account.Handlers).Signup, func(t *testing.T) string {
			return asJSON(t, map[string]string{"login": hLogin, "password": hPassword})
		}},
		{"confirm", (*account.Handlers).ConfirmTOTP, creds},
		{"login", (*account.Handlers).Login, creds},
		{"change password", (*account.Handlers).ChangePassword, func(t *testing.T) string {
			return asJSON(t, map[string]string{"current_password": hPassword, "new_password": hNewPassword})
		}},
		{"logout", (*account.Handlers).Logout, func(*testing.T) string { return "" }},
	}
}

func TestEveryHandlerRefusesACrossOriginRequest(t *testing.T) {
	t.Parallel()

	refusals := map[string]func(*http.Request){
		"Sec-Fetch-Site cross-site": withHeader("Sec-Fetch-Site", "cross-site"),
		"Sec-Fetch-Site same-site":  withHeader("Sec-Fetch-Site", "same-site"),
		"Origin of another host":    withHeader("Origin", "https://evil.example"),
		"Origin null":               withHeader("Origin", "null"),
	}

	for _, hc := range allHandlers() {
		for name, mod := range refusals {
			for _, session := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s, %s, session %t", hc.name, name, session), func(t *testing.T) {
					t.Parallel()
					f := newHFixture(t, nil)
					f.setCurrent(account.Identity{ID: "acct-1", Login: hLogin}, session)

					w := f.post(hc.get(f.handlers), hc.body(t), mod)
					if w.Code != http.StatusForbidden {
						t.Fatalf("status = %d %s, want 403", w.Code, w.Body.String())
					}
					if !f.untouched() {
						t.Fatal("a refused cross origin request reached a callback or hashed a password")
					}
				})
			}
		}
	}
}

func TestEveryHandlerAcceptsItsOwnOriginAndATrustedOne(t *testing.T) {
	t.Parallel()

	accepted := map[string]func(*http.Request){
		"Sec-Fetch-Site same-origin": withHeader("Sec-Fetch-Site", "same-origin"),
		"Sec-Fetch-Site none":        withHeader("Sec-Fetch-Site", "none"),
		"Origin of this host":        withHeader("Origin", "http://example.com"),
		"no browser header":          func(*http.Request) {},
		"trusted origin":             withHeader("Origin", "https://app.example.test"),
	}

	for _, hc := range allHandlers() {
		for name, mod := range accepted {
			t.Run(hc.name+", "+name, func(t *testing.T) {
				t.Parallel()
				f := newHFixture(t, func(c *account.HandlerConfig) { c.TrustedOrigins = []string{"https://app.example.test"} })
				f.setCurrent(account.Identity{ID: "acct-1", Login: hLogin}, true)

				w := f.post(hc.get(f.handlers), hc.body(t), mod)
				if w.Code == http.StatusForbidden && readError(t, w).Error.Message == "cross origin request refused" {
					t.Fatalf("the origin check refused it: %s", w.Body.String())
				}
			})
		}
	}
}

func TestEveryHandlerAnswersPOSTOnly(t *testing.T) {
	t.Parallel()

	for _, hc := range allHandlers() {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
			t.Run(hc.name+" "+method, func(t *testing.T) {
				t.Parallel()
				f := newHFixture(t, nil)
				f.setCurrent(account.Identity{ID: "acct-1", Login: hLogin}, true)

				w := f.call(hc.get(f.handlers), method, hc.body(t))
				if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
					t.Fatalf("answer = %d, Allow %q, want 405 with Allow POST", w.Code, w.Header().Get("Allow"))
				}
				if !f.untouched() {
					t.Fatal("a refused method reached a callback or hashed a password")
				}
			})
		}
	}
}

func TestEveryBodyHandlerIsStrict(t *testing.T) {
	t.Parallel()

	bodyHandlers := allHandlers()[:4]
	bad := []struct {
		name   string
		body   func(valid string) string
		mod    func(*http.Request)
		status int
	}{
		{"no content type", func(v string) string { return v }, func(r *http.Request) { r.Header.Del("Content-Type") }, http.StatusUnsupportedMediaType},
		{"text plain", func(v string) string { return v }, withHeader("Content-Type", "text/plain"), http.StatusUnsupportedMediaType},
		{"a form", func(v string) string { return v }, withHeader("Content-Type", "application/x-www-form-urlencoded"), http.StatusUnsupportedMediaType},
		{"json with a charset is fine but text json is not", func(v string) string { return v }, withHeader("Content-Type", "text/json"), http.StatusUnsupportedMediaType},
		{"an unknown field", func(v string) string { return strings.Replace(v, "{", `{"admin":true,`, 1) }, nil, http.StatusBadRequest},
		{"two values", func(v string) string { return v + v }, nil, http.StatusBadRequest},
		{"a value after the object", func(v string) string { return v + ` 1` }, nil, http.StatusBadRequest},
		{"an empty body", func(string) string { return "" }, nil, http.StatusBadRequest},
		{"a bare string", func(string) string { return `"hello"` }, nil, http.StatusBadRequest},
		{"an array", func(string) string { return `[]` }, nil, http.StatusBadRequest},
		{"broken json", func(v string) string { return v[:len(v)-1] }, nil, http.StatusBadRequest},
		{"a field of the wrong type", func(string) string { return `{"login":5,"password":5,"code":5,"current_password":5,"new_password":5}` }, nil, http.StatusBadRequest},
		{"a content type that only starts with application/json", func(v string) string { return v }, withHeader("Content-Type", "application/jsonx"), http.StatusUnsupportedMediaType},
		{"a content type with a malformed parameter", func(v string) string { return v }, withHeader("Content-Type", "application/json; charset"), http.StatusUnsupportedMediaType},
		{"a body over the limit", func(v string) string {
			return strings.Replace(v, "{", `{"login":"`+strings.Repeat("x", int(account.DefaultMaxBodyBytes))+`",`, 1)
		}, nil, http.StatusRequestEntityTooLarge},
	}

	for _, hc := range bodyHandlers {
		for _, tt := range bad {
			t.Run(hc.name+", "+tt.name, func(t *testing.T) {
				t.Parallel()
				f := newHFixture(t, nil)
				f.setCurrent(account.Identity{ID: "acct-1", Login: hLogin}, true)

				var mods []func(*http.Request)
				if tt.mod != nil {
					mods = append(mods, tt.mod)
				}
				w := f.post(hc.get(f.handlers), tt.body(hc.body(t)), mods...)
				// The change password body has no login field, so the oversize
				// case there is an unknown field before it is too large.
				if tt.status == http.StatusRequestEntityTooLarge && hc.name == "change password" {
					if w.Code != http.StatusRequestEntityTooLarge && w.Code != http.StatusBadRequest {
						t.Fatalf("status = %d, want 413 or 400", w.Code)
					}
				} else if w.Code != tt.status {
					t.Fatalf("status = %d %s, want %d", w.Code, w.Body.String(), tt.status)
				}
				if f.derivations.Load() != 0 || f.startedCount() != 0 || f.endedCount() != 0 {
					t.Fatal("a malformed request reached the service")
				}
			})
		}
	}

	t.Run("application/json with a charset is accepted", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, nil)

		w := f.post(f.handlers.Signup(), allHandlers()[0].body(t), withHeader("Content-Type", "application/json; charset=utf-8"))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d %s, want 201", w.Code, w.Body.String())
		}
	})

	// The default is 16 KiB exactly, written as a literal so a change of the
	// constant shows here. A body of that size passes the limit, one byte
	// more does not. The long login is refused later, as a bad login.
	t.Run("the default limit is 16384 bytes", func(t *testing.T) {
		t.Parallel()

		sized := func(n int) string {
			const prefix, suffix = `{"login":"`, `","password":"x"}`

			return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix
		}

		f := newHFixture(t, nil)
		if body := sized(16384); len(body) != 16384 {
			t.Fatalf("test body is %d bytes", len(body))
		}
		if w := f.post(f.handlers.Signup(), sized(16384)); w.Code == http.StatusRequestEntityTooLarge {
			t.Fatalf("a body of 16384 bytes was refused as too large: %s", w.Body.String())
		}
		if w := f.post(f.handlers.Signup(), sized(16385)); w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("a body of 16385 bytes = %d, want 413", w.Code)
		}
	})

	t.Run("a custom limit applies", func(t *testing.T) {
		t.Parallel()
		f := newHFixture(t, func(c *account.HandlerConfig) { c.MaxBodyBytes = 64 })

		w := f.post(f.handlers.Signup(), asJSON(t, map[string]string{"login": hLogin, "password": strings.Repeat("p", 100)}))
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", w.Code)
		}
		if got := readError(t, w).Error.Code; got != api.CodeRequestTooLarge {
			t.Fatalf("code = %q, want %q", got, api.CodeRequestTooLarge)
		}
	})
}

// A password, a code, a secret or an enrolment URI never reaches the log, on
// the way through and on the way out of every failure.
func TestNoSecretReachesTheLog(t *testing.T) {
	t.Parallel()

	f := newHFixture(t, nil)
	secret := f.signUp(hLogin, hPassword)
	code := f.code(secret)
	rec, err := f.store.FindByLogin(t.Context(), hLogin)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	uri, err := account.TOTPProvisioningURI("Example", hLogin, secret)
	if err != nil {
		t.Fatalf("uri: %v", err)
	}

	f.post(f.handlers.ConfirmTOTP(), f.creds(hLogin, hPassword, code))
	f.advance(time.Minute)
	f.post(f.handlers.Login(), f.creds(hLogin, hWrong, f.code(secret)))
	f.post(f.handlers.Login(), f.creds(hLogin, hPassword, f.code(secret)))
	f.post(f.handlers.Login(), `{"login":"x","password":"`+hWrong+`","unknown":1}`)
	f.setCurrent(account.Identity{ID: rec.ID, Login: rec.Login}, true)
	f.post(f.handlers.ChangePassword(), asJSON(t, map[string]string{"current_password": hPassword, "new_password": hNewPassword}))
	f.endAllErr = errors.New("end failed")
	f.store.setHash = errors.New("write failed")
	f.post(f.handlers.ChangePassword(), asJSON(t, map[string]string{"current_password": hNewPassword, "new_password": hPassword + "-again"}))
	f.signUp("carol@example.com", hNewPassword)
	f.store.create = errors.New("create failed")
	f.post(f.handlers.Signup(), asJSON(t, map[string]string{"login": "other@example.com", "password": hWrong}))

	logs := f.logs.String()
	if logs == "" {
		t.Fatal("the fixture logged nothing, so the check proves nothing")
	}
	for name, value := range map[string]string{
		"password": hPassword, "new password": hNewPassword, "wrong password": hWrong,
		"secret": secret, "uri": uri, "code": code, "hash": rec.PasswordHash,
	} {
		if strings.Contains(logs, value) {
			t.Errorf("the log carries the %s", name)
		}
	}
}
