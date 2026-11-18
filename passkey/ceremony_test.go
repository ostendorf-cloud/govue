// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/passkey"
	"github.com/ostendorf-cloud/govue/passkey/passkeytest"
)

// Fake values only.
const (
	testRPID   = "app.example.com"
	testOrigin = "https://app.example.com"
)

// rp is a relying party over a memory store on a clock the test moves, with
// the session of the request under the test's control.
type rp struct {
	t     *testing.T
	store *passkeytest.MemoryStore
	h     *passkey.Handlers

	mu       sync.Mutex
	clock    time.Time
	who      account.Identity
	loggedIn bool
	loginAt  time.Time
	others   int
	started  []account.Identity
}

func newRP(t *testing.T, change ...func(*passkey.Config)) *rp {
	t.Helper()
	start := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	p := &rp{t: t, store: passkeytest.NewMemoryStore(), clock: start, who: alice, loggedIn: true, loginAt: start, others: 1}
	p.store.PutAccount(alice)
	p.store.PutAccount(bob)
	cfg := passkey.Config{
		RPID:          testRPID,
		RPDisplayName: "Example App",
		Origins:       []string{testOrigin},
		Store:         p.store,
		StartSession: func(_ context.Context, _ http.ResponseWriter, _ *http.Request, id account.Identity) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.started = append(p.started, id)
			return nil
		},
		CurrentAccount: func(*http.Request) (account.Identity, time.Time, bool, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.who, p.loginAt, p.loggedIn, nil
		},
		OtherFactors: func(context.Context, account.Identity) (int, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.others, nil
		},
		Now: p.now,
	}
	for _, c := range change {
		c(&cfg)
	}
	h, err := passkey.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.h = h
	return p
}

func (p *rp) now() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clock
}

func (p *rp) advance(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock = p.clock.Add(d)
	p.loginAt = p.loginAt.Add(d)
}

func (p *rp) as(id account.Identity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.who = id
}

func (p *rp) startedSessions() []account.Identity {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.started)
}

// post sends one same origin JSON request with the cookie, if any.
func (p *rp) post(h http.Handler, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	p.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		p.t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// begin runs a begin handler and returns its body and challenge cookie.
func (p *rp) begin(h http.Handler) ([]byte, *http.Cookie) {
	p.t.Helper()
	w := p.post(h, nil, map[string]any{})
	if w.Code != http.StatusOK {
		p.t.Fatalf("begin: %d %s", w.Code, w.Body)
	}
	return w.Body.Bytes(), challengeCookie(p.t, w)
}

func challengeCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == passkey.ChallengeCookieName && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no challenge cookie in %v", w.Result().Header.Values("Set-Cookie"))
	return nil
}

// register runs a whole registration for the current account.
func (p *rp) register(a *authenticator, name string) *httptest.ResponseRecorder {
	p.t.Helper()
	body, cookie := p.begin(p.h.RegisterBegin())
	return p.post(p.h.RegisterFinish(), cookie, map[string]any{"credential": a.create(body), "name": name})
}

func (p *rp) mustRegister(a *authenticator) {
	p.t.Helper()
	if w := p.register(a, "laptop"); w.Code != http.StatusCreated {
		p.t.Fatalf("register: %d %s", w.Code, w.Body)
	}
}

// login runs a whole login with a.
func (p *rp) login(a *authenticator) *httptest.ResponseRecorder {
	p.t.Helper()
	body, cookie := p.begin(p.h.LoginBegin())
	return p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)})
}

// answer is everything a client sees of a response.
type answer struct {
	status  int
	headers http.Header
	body    string
}

func answerOf(w *httptest.ResponseRecorder) answer {
	return answer{status: w.Code, headers: w.Result().Header, body: w.Body.String()}
}

func sameAnswer(t *testing.T, name string, got, want answer) {
	t.Helper()
	if got.status != want.status || got.body != want.body {
		t.Fatalf("%s: got %d %s, want %d %s", name, got.status, got.body, want.status, want.body)
	}
	if len(got.headers) != len(want.headers) {
		t.Fatalf("%s: headers %v, want %v", name, got.headers, want.headers)
	}
	for k, v := range want.headers {
		if !slices.Equal(got.headers[k], v) {
			t.Fatalf("%s: header %s = %v, want %v", name, k, got.headers[k], v)
		}
	}
}

const wantRefusedLogin = `{"error":{"code":"invalid_credentials","message":"the passkey could not be verified"}}`

const wantRefusedRegistration = `{"error":{"code":"invalid_passkey","message":"the passkey could not be registered"}}`

func TestRegisterAndLogin(t *testing.T) {
	p := newRP(t)
	a := newAuthenticator(t)
	a.Count = 5

	body, cookie := p.begin(p.h.RegisterBegin())
	o := parseOptions(t, body)
	if o.PublicKey.RP.ID != testRPID || o.PublicKey.AuthenticatorSelection.ResidentKey != "required" ||
		o.PublicKey.AuthenticatorSelection.UserVerification != "required" || o.PublicKey.Attestation != "none" {
		t.Fatalf("registration options: %s", body)
	}
	handle, err := b64.DecodeString(o.PublicKey.User.ID)
	if err != nil || len(handle) != passkey.UserHandleBytes || bytes.Contains(handle, []byte(alice.ID)) {
		t.Fatalf("user handle %q is not %d random bytes", o.PublicKey.User.ID, passkey.UserHandleBytes)
	}

	w := p.post(p.h.RegisterFinish(), cookie, map[string]any{"credential": a.create(body), "name": "Laptop"})
	if w.Code != http.StatusCreated {
		t.Fatalf("register finish: %d %s", w.Code, w.Body)
	}
	want := `{"passkey":{"id":"` + b64.EncodeToString(a.CredentialID) + `","name":"Laptop","created_at":"2026-11-18T10:00:00Z","last_used_at":null}}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Fatalf("register body\n got %s\nwant %s", got, want)
	}
	creds, _ := p.store.Credentials(context.Background(), alice.ID)
	if len(creds) != 1 || creds[0].SignCount != 5 || !bytes.Equal(creds[0].UserHandle, handle) {
		t.Fatalf("stored: %+v", creds)
	}

	body, cookie = p.begin(p.h.LoginBegin())
	lo := parseOptions(t, body)
	if lo.PublicKey.UserVerification != "required" || len(lo.PublicKey.AllowCredentials) != 0 || lo.PublicKey.RPID != testRPID {
		t.Fatalf("login options: %s", body)
	}
	p.advance(time.Minute)
	a.Count = 6
	w = p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)})
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"login":"alice@example.test"}` {
		t.Fatalf("login finish: %d %s", w.Code, w.Body)
	}
	if got := p.startedSessions(); len(got) != 1 || got[0] != alice {
		t.Fatalf("sessions started: %+v", got)
	}
	creds, _ = p.store.Credentials(context.Background(), alice.ID)
	if creds[0].SignCount != 6 || !creds[0].LastUsedAt.Equal(time.Date(2026, 11, 18, 10, 1, 0, 0, time.UTC)) {
		t.Fatalf("after login: %+v", creds[0])
	}

	// A second passkey of the account reuses the handle and is excluded.
	body, _ = p.begin(p.h.RegisterBegin())
	o = parseOptions(t, body)
	if o.PublicKey.User.ID != b64.EncodeToString(handle) || len(o.PublicKey.ExcludeCredentials) != 1 ||
		o.PublicKey.ExcludeCredentials[0].ID != b64.EncodeToString(a.CredentialID) {
		t.Fatalf("second registration options: %s", body)
	}
}

func TestSyncedPasskeyWithoutCounterLogsInRepeatedly(t *testing.T) {
	p := newRP(t)
	a := newAuthenticator(t)
	p.mustRegister(a)
	for i := range 3 {
		if w := p.login(a); w.Code != http.StatusOK {
			t.Fatalf("login %d with counter zero: %d %s", i, w.Code, w.Body)
		}
	}
}

func TestChallengeCookie(t *testing.T) {
	for _, plain := range []bool{false, true} {
		p := newRP(t, func(c *passkey.Config) { c.AllowPlainHTTP = plain })
		w := p.post(p.h.LoginBegin(), nil, map[string]any{})
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("plain %t: cookies %v", plain, cookies)
		}
		c := cookies[0]
		name := passkey.ChallengeCookieName
		if plain {
			name = passkey.PlainChallengeCookieName
		}
		if c.Name != name || !c.HttpOnly || c.Secure == plain || c.SameSite != http.SameSiteStrictMode ||
			c.Path != "/" || c.Domain != "" || c.MaxAge != 300 || len(c.Value) != 43 {
			t.Fatalf("plain %t: cookie %+v", plain, c)
		}
		if strings.Contains(w.Body.String(), c.Value) {
			t.Fatal("the challenge id is in the body")
		}

		f := p.post(p.h.LoginFinish(), c, map[string]any{"credential": json.RawMessage(`{}`)})
		cleared := f.Result().Cookies()
		if len(cleared) != 1 || cleared[0].Name != name || cleared[0].MaxAge != -1 {
			t.Fatalf("plain %t: a failed finish did not clear the cookie: %v", plain, cleared)
		}
	}
}

// TestLoginRefusalsAreOneAnswer is the identical answer of every refused
// login: status, headers and body, the cleared cookie included.
func TestLoginRefusalsAreOneAnswer(t *testing.T) {
	type attempt func(p *rp, a *authenticator) *httptest.ResponseRecorder

	withAssertion := func(change func(a *authenticator)) attempt {
		return func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			change(a)
			return p.login(a)
		}
	}

	tests := []struct {
		name string
		run  attempt
	}{
		{"no challenge cookie", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			body, _ := p.begin(p.h.LoginBegin())
			return p.post(p.h.LoginFinish(), nil, map[string]any{"credential": a.get(body)})
		}},
		{"unknown challenge id", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			body, cookie := p.begin(p.h.LoginBegin())
			cookie.Value = strings.Repeat("A", 43)
			return p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)})
		}},
		{"reused challenge", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			body, cookie := p.begin(p.h.LoginBegin())
			if w := p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)}); w.Code != http.StatusOK {
				t.Fatalf("first finish: %d %s", w.Code, w.Body)
			}
			return p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)})
		}},
		{"expired challenge", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			body, cookie := p.begin(p.h.LoginBegin())
			p.advance(passkey.DefaultChallengeTTL)
			return p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)})
		}},
		{"registration challenge", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			body, cookie := p.begin(p.h.RegisterBegin())
			return p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)})
		}},
		{"overlapping ceremony", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			first, _ := p.begin(p.h.LoginBegin())
			_, second := p.begin(p.h.LoginBegin())
			// The browser holds the second cookie, the prompt answers the first.
			return p.post(p.h.LoginFinish(), second, map[string]any{"credential": a.get(first)})
		}},
		{"wrong origin", withAssertion(func(a *authenticator) { a.Origin = "https://evil.example.org" })},
		{"sibling origin", withAssertion(func(a *authenticator) { a.Origin = "https://other.app.example.com" })},
		{"wrong rp id hash", withAssertion(func(a *authenticator) { a.RPID = "example.com" })},
		{"user not verified", withAssertion(func(a *authenticator) { a.Flags &^= passkeytest.FlagUserVerified })},
		{"user not present", withAssertion(func(a *authenticator) { a.Flags &^= passkeytest.FlagUserPresent })},
		{"sign count went back", withAssertion(func(a *authenticator) { a.Count = 4 })},
		{"sign count stood still", withAssertion(func(a *authenticator) { a.Count = 5 })},
		{"sign count dropped to zero", withAssertion(func(a *authenticator) { a.Count = 0 })},
		{"unknown credential", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			stranger := newAuthenticator(t)
			stranger.UserHandle = a.UserHandle
			return p.login(stranger)
		}},
		{"unknown user handle", withAssertion(func(a *authenticator) { a.UserHandle = bytes.Repeat([]byte{7}, 32) })},
		{"no user handle", withAssertion(func(a *authenticator) { a.UserHandle = nil })},
		{"credential of another account", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			p.as(bob)
			other := newAuthenticator(t)
			p.mustRegister(other)
			// Alice's key and id under bob's handle.
			a.UserHandle = other.UserHandle
			return p.login(a)
		}},
		{"bad signature", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			forger := newAuthenticator(t)
			forger.CredentialID, forger.UserHandle, forger.Count = a.CredentialID, a.UserHandle, 6
			return p.login(forger)
		}},
		{"disabled account", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			p.store.Disable(alice.ID)
			return p.login(a)
		}},
		{"malformed credential", func(p *rp, _ *authenticator) *httptest.ResponseRecorder {
			_, cookie := p.begin(p.h.LoginBegin())
			return p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": json.RawMessage(`{"id":"x"}`)})
		}},
	}

	var reference *answer
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newRP(t)
			a := newAuthenticator(t)
			a.Count = 5
			p.mustRegister(a)
			a.Count = 6

			got := answerOf(tt.run(p, a))
			if got.status != http.StatusUnauthorized || strings.TrimSpace(got.body) != wantRefusedLogin {
				t.Fatalf("got %d %s, want 401 %s", got.status, got.body, wantRefusedLogin)
			}
			if reference == nil {
				reference = &got
			}
			sameAnswer(t, tt.name, got, *reference)

			wantStarted := 0
			if tt.name == "reused challenge" {
				wantStarted = 1
			}
			if n := len(p.startedSessions()); n != wantStarted {
				t.Fatalf("%d sessions started, want %d", n, wantStarted)
			}
		})
	}
}

func TestClonedAuthenticatorIsRefusedAfterTheOriginal(t *testing.T) {
	p := newRP(t)
	a := newAuthenticator(t)
	a.Count = 10
	p.mustRegister(a)
	copied := a.clone()

	a.Count = 11
	if w := p.login(a); w.Code != http.StatusOK {
		t.Fatalf("original: %d %s", w.Code, w.Body)
	}
	copied.Count = 11
	if w := p.login(copied); w.Code != http.StatusUnauthorized {
		t.Fatalf("copy with a used counter: %d %s, want 401", w.Code, w.Body)
	}
	creds, _ := p.store.Credentials(context.Background(), alice.ID)
	if creds[0].SignCount != 11 {
		t.Fatalf("the refused login moved the counter to %d", creds[0].SignCount)
	}
}

func TestOverlappingCeremonyFailsDeterministically(t *testing.T) {
	p := newRP(t)
	a := newAuthenticator(t)
	p.mustRegister(a)

	first, _ := p.begin(p.h.LoginBegin())
	second, cookie := p.begin(p.h.LoginBegin())
	if w := p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(first)}); w.Code != http.StatusUnauthorized {
		t.Fatalf("finish of the replaced ceremony: %d, want 401", w.Code)
	}
	// The finish took the second challenge and cleared the cookie, so the
	// second ceremony cannot finish either. The person starts again.
	if w := p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(second)}); w.Code != http.StatusUnauthorized {
		t.Fatalf("finish of the second ceremony after the first took it: %d, want 401", w.Code)
	}
	if w := p.login(a); w.Code != http.StatusOK {
		t.Fatalf("a fresh ceremony: %d %s", w.Code, w.Body)
	}
}

func TestRegistrationRefusals(t *testing.T) {
	type attempt func(p *rp, a *authenticator) *httptest.ResponseRecorder

	finishWith := func(change func(p *rp, a *authenticator)) attempt {
		return func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			body, cookie := p.begin(p.h.RegisterBegin())
			change(p, a)
			return p.post(p.h.RegisterFinish(), cookie, map[string]any{"credential": a.create(body)})
		}
	}

	tests := []struct {
		name   string
		run    attempt
		status int
		body   string
	}{
		{"no challenge cookie", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			body, _ := p.begin(p.h.RegisterBegin())
			return p.post(p.h.RegisterFinish(), nil, map[string]any{"credential": a.create(body)})
		}, http.StatusBadRequest, wantRefusedRegistration},
		{"reused challenge", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			body, cookie := p.begin(p.h.RegisterBegin())
			if w := p.post(p.h.RegisterFinish(), cookie, map[string]any{"credential": a.create(body)}); w.Code != http.StatusCreated {
				t.Fatalf("first finish: %d %s", w.Code, w.Body)
			}
			b := newAuthenticator(t)
			return p.post(p.h.RegisterFinish(), cookie, map[string]any{"credential": b.create(body)})
		}, http.StatusBadRequest, wantRefusedRegistration},
		{"expired challenge", finishWith(func(p *rp, _ *authenticator) {
			p.advance(passkey.DefaultChallengeTTL)
			p.mu.Lock()
			p.loginAt = p.clock
			p.mu.Unlock()
		}), http.StatusBadRequest, wantRefusedRegistration},
		{
			"challenge of another account", finishWith(func(p *rp, _ *authenticator) { p.as(bob) }),
			http.StatusBadRequest, wantRefusedRegistration,
		},
		{"login challenge", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			reg, _ := p.begin(p.h.RegisterBegin())
			_, cookie := p.begin(p.h.LoginBegin())
			return p.post(p.h.RegisterFinish(), cookie, map[string]any{"credential": a.create(reg)})
		}, http.StatusBadRequest, wantRefusedRegistration},
		{
			"wrong origin", finishWith(func(_ *rp, a *authenticator) { a.Origin = "https://evil.example.org" }),
			http.StatusBadRequest, wantRefusedRegistration,
		},
		{
			"wrong rp id hash", finishWith(func(_ *rp, a *authenticator) { a.RPID = "example.com" }),
			http.StatusBadRequest, wantRefusedRegistration,
		},
		{
			"user not verified", finishWith(func(_ *rp, a *authenticator) { a.Flags &^= passkeytest.FlagUserVerified }),
			http.StatusBadRequest, wantRefusedRegistration,
		},
		{"credential id taken", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			p.mustRegister(a)
			p.as(bob)
			return p.register(a.clone(), "")
		}, http.StatusBadRequest, wantRefusedRegistration},
		{"stale authentication at finish", finishWith(func(p *rp, _ *authenticator) {
			p.mu.Lock()
			p.loginAt = p.clock.Add(-passkey.DefaultReauthWindow - time.Second)
			p.mu.Unlock()
		}), http.StatusForbidden, `{"error":{"code":"reauthentication_required","message":"log in again to change your passkeys"}}`},
		{"no session at finish", finishWith(func(p *rp, _ *authenticator) {
			p.mu.Lock()
			p.loggedIn = false
			p.mu.Unlock()
		}), http.StatusUnauthorized, `{"error":{"code":"unauthenticated","message":"log in first"}}`},
		{"name too long", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			return p.register(a, strings.Repeat("ä", passkey.MaxNameLength+1))
		}, http.StatusBadRequest, `{"error":{"code":"invalid_argument","message":"the name is too long","details":{"name":"too_long"}}}`},
		{"name not printable", func(p *rp, a *authenticator) *httptest.ResponseRecorder {
			return p.register(a, "a\x00b")
		}, http.StatusBadRequest, `{"error":{"code":"invalid_argument","message":"the name is not valid","details":{"name":"invalid"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newRP(t)
			a := newAuthenticator(t)
			w := tt.run(p, a)
			if w.Code != tt.status || strings.TrimSpace(w.Body.String()) != tt.body {
				t.Fatalf("got %d %s, want %d %s", w.Code, w.Body, tt.status, tt.body)
			}
			if tt.name == "challenge of another account" || tt.name == "credential id taken" {
				if creds, _ := p.store.Credentials(context.Background(), bob.ID); len(creds) != 0 {
					t.Fatalf("bob got a passkey: %+v", creds)
				}
			}
		})
	}
}

func TestRegisterBeginNeedsRecentLogin(t *testing.T) {
	p := newRP(t)
	p.mu.Lock()
	p.loginAt = p.clock.Add(-passkey.DefaultReauthWindow - time.Second)
	p.mu.Unlock()
	w := p.post(p.h.RegisterBegin(), nil, map[string]any{})
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), passkey.CodeReauthenticationRequired) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("a refused begin set a challenge cookie")
	}

	p.mu.Lock()
	p.loggedIn = false
	p.mu.Unlock()
	if w := p.post(p.h.RegisterBegin(), nil, map[string]any{}); w.Code != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", w.Code)
	}
}

func TestCeremonyRoutesCheckMethodOriginAndBody(t *testing.T) {
	p := newRP(t)
	for name, h := range map[string]http.Handler{
		"register begin": p.h.RegisterBegin(), "register finish": p.h.RegisterFinish(),
		"login begin": p.h.LoginBegin(), "login finish": p.h.LoginFinish(),
	} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s by GET: %d", name, w.Code)
		}

		r = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s cross site: %d, cookies %v", name, w.Code, w.Result().Cookies())
		}
	}

	w := p.post(p.h.LoginBegin(), nil, map[string]any{"login": "alice"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("login begin with a field: %d", w.Code)
	}
}

// TestCrossOriginRefusalKeepsTheChallenge shows a refused cross site finish
// cannot burn a person's pending challenge: the origin check runs first.
func TestCrossOriginRefusalKeepsTheChallenge(t *testing.T) {
	p := newRP(t)
	a := newAuthenticator(t)
	p.mustRegister(a)
	body, cookie := p.begin(p.h.LoginBegin())

	raw, _ := json.Marshal(map[string]any{"credential": a.get(body)})
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example.org")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	p.h.LoginFinish().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross origin finish: %d", w.Code)
	}
	if w := p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)}); w.Code != http.StatusOK {
		t.Fatalf("same origin finish after the refusal: %d %s", w.Code, w.Body)
	}
}

func TestStoreFailureIsNoRefusal(t *testing.T) {
	p := newRP(t)
	a := newAuthenticator(t)
	p.mustRegister(a)
	broken := &failingStore{MemoryStore: p.store, err: errors.New("database down")}
	h, err := passkey.New(passkey.Config{
		RPID: testRPID, RPDisplayName: "Example App", Origins: []string{testOrigin}, Store: broken,
		StartSession: func(context.Context, http.ResponseWriter, *http.Request, account.Identity) error { return nil },
		CurrentAccount: func(*http.Request) (account.Identity, time.Time, bool, error) {
			return alice, p.now(), true, nil
		},
		OtherFactors: func(context.Context, account.Identity) (int, error) { return 1, nil },
		Now:          p.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.h = h
	if w := p.login(a); w.Code != http.StatusInternalServerError {
		t.Fatalf("login over a failing store: %d %s, want 500", w.Code, w.Body)
	}
}

// barrierStore lets n lookups read the credentials before any of them goes
// on, so racing logins all see the same stored counter.
type barrierStore struct {
	*passkeytest.MemoryStore
	arrived sync.WaitGroup
}

func (s *barrierStore) FindByUserHandle(ctx context.Context, h []byte) (account.Identity, []passkey.Credential, error) {
	id, creds, err := s.MemoryStore.FindByUserHandle(ctx, h)
	s.arrived.Done()
	s.arrived.Wait()
	return id, creds, err
}

// TestRacingClonesWinOnce runs two logins of one credential with the same
// counter at once. Both read the stored counter before either writes, so only
// the compare and set of UpdateCredential tells them apart.
func TestRacingClonesWinOnce(t *testing.T) {
	for round := range 20 {
		p := newRP(t)
		a := newAuthenticator(t)
		a.Count = 5
		p.mustRegister(a)
		b := &barrierStore{MemoryStore: p.store}
		b.arrived.Add(2)
		h, err := passkey.New(passkey.Config{
			RPID: testRPID, RPDisplayName: "Example App", Origins: []string{testOrigin}, Store: b,
			StartSession: func(context.Context, http.ResponseWriter, *http.Request, account.Identity) error { return nil },
			CurrentAccount: func(*http.Request) (account.Identity, time.Time, bool, error) {
				return alice, p.now(), true, nil
			},
			OtherFactors: func(context.Context, account.Identity) (int, error) { return 1, nil },
			Now:          p.now,
		})
		if err != nil {
			t.Fatal(err)
		}
		p.h = h

		copied := a.clone()
		a.Count, copied.Count = 6, 6
		bodies := make([][]byte, 2)
		cookies := make([]*http.Cookie, 2)
		for i := range 2 {
			bodies[i], cookies[i] = p.begin(p.h.LoginBegin())
		}
		codes := make([]int, 2)
		var wg sync.WaitGroup
		for i, auth := range []*authenticator{a, copied} {
			assertion := auth.get(bodies[i])
			wg.Go(func() {
				codes[i] = p.post(p.h.LoginFinish(), cookies[i], map[string]any{"credential": assertion}).Code
			})
		}
		wg.Wait()
		ok := 0
		for _, c := range codes {
			if c == http.StatusOK {
				ok++
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d of 2 racing logins with one counter got through, codes %v", round, ok, codes)
		}
	}
}

// TestRacingFirstRegistrationsKeepOneHandle begins two first registrations
// for one account from two browsers. Each begin makes a new handle. Exactly
// one finish succeeds, whichever comes first, the other gets the one refusal,
// and a new begin then offers the stored handle.
func TestRacingFirstRegistrationsKeepOneHandle(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		for round := range 20 {
			p := newRP(t)
			bodies := make([][]byte, 2)
			cookies := make([]*http.Cookie, 2)
			auths := []*authenticator{newAuthenticator(t), newAuthenticator(t)}
			for i := range 2 {
				bodies[i], cookies[i] = p.begin(p.h.RegisterBegin())
			}
			if parseOptions(t, bodies[0]).PublicKey.User.ID == parseOptions(t, bodies[1]).PublicKey.User.ID {
				t.Fatal("two first begins offered the same handle, the race is not set up")
			}
			creds := []json.RawMessage{auths[0].create(bodies[0]), auths[1].create(bodies[1])}

			codes := make([]int, 2)
			finish := func(i int) {
				codes[i] = p.post(p.h.RegisterFinish(), cookies[i], map[string]any{"credential": creds[i]}).Code
			}
			if concurrent {
				var wg sync.WaitGroup
				for i := range 2 {
					wg.Go(func() { finish(i) })
				}
				wg.Wait()
			} else {
				finish(0)
				finish(1)
			}
			slices.Sort(codes)
			if !slices.Equal(codes, []int{http.StatusCreated, http.StatusBadRequest}) {
				t.Fatalf("concurrent %t round %d: codes %v, want one 201 and one 400", concurrent, round, codes)
			}

			stored, _ := p.store.Credentials(context.Background(), alice.ID)
			if len(stored) != 1 {
				t.Fatalf("concurrent %t round %d: %d passkeys stored", concurrent, round, len(stored))
			}
			body, _ := p.begin(p.h.RegisterBegin())
			if parseOptions(t, body).PublicKey.User.ID != b64.EncodeToString(stored[0].UserHandle) {
				t.Fatalf("concurrent %t round %d: the next begin does not offer the stored handle", concurrent, round)
			}
		}
	}
}

// handleBlindStore is a store that misses the one handle per account guard
// of Store.AddCredential: it accepts any handle for an account.
type handleBlindStore struct {
	*passkeytest.MemoryStore
	mu    sync.Mutex
	extra []passkey.Credential
}

func (s *handleBlindStore) AddCredential(ctx context.Context, c passkey.Credential) error {
	if err := s.MemoryStore.AddCredential(ctx, c); !errors.Is(err, passkey.ErrConflict) {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extra = append(s.extra, c)
	return nil
}

func (s *handleBlindStore) Credentials(ctx context.Context, accountID string) ([]passkey.Credential, error) {
	creds, err := s.MemoryStore.Credentials(ctx, accountID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.extra {
		if c.AccountID == accountID {
			creds = append(creds, c)
		}
	}
	return creds, err
}

// TestRegistrationRefusesASecondHandleOverAStoreWithoutTheGuard shows the
// handler's own check: over a store that accepts any handle, the second of
// two first registrations is still refused.
func TestRegistrationRefusesASecondHandleOverAStoreWithoutTheGuard(t *testing.T) {
	p := newRP(t)
	blind := &handleBlindStore{MemoryStore: p.store}
	p = newRP(t, func(c *passkey.Config) { c.Store = blind })
	blind.PutAccount(alice)

	first, firstCookie := p.begin(p.h.RegisterBegin())
	second, secondCookie := p.begin(p.h.RegisterBegin())
	a, b := newAuthenticator(t), newAuthenticator(t)
	if w := p.post(p.h.RegisterFinish(), firstCookie, map[string]any{"credential": a.create(first)}); w.Code != http.StatusCreated {
		t.Fatalf("first finish: %d %s", w.Code, w.Body)
	}
	w := p.post(p.h.RegisterFinish(), secondCookie, map[string]any{"credential": b.create(second)})
	if w.Code != http.StatusBadRequest || strings.TrimSpace(w.Body.String()) != wantRefusedRegistration {
		t.Fatalf("second finish with another handle: %d %s, want the one refusal", w.Code, w.Body)
	}
}

// expiryBlindChallenges is a ChallengeStore that never checks ExpiresAt, so
// only the handler's own recheck stands between an old challenge and a login.
type expiryBlindChallenges struct {
	mu sync.Mutex
	m  map[string]passkey.Challenge
}

func (s *expiryBlindChallenges) Put(_ context.Context, id string, c passkey.Challenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = c
	return nil
}

func (s *expiryBlindChallenges) Take(_ context.Context, id string) (passkey.Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[id]
	if !ok {
		return passkey.Challenge{}, passkey.ErrNotFound
	}
	delete(s.m, id)
	return c, nil
}

func TestHandlerRechecksExpiryOfAStoreThatDoesNot(t *testing.T) {
	p := newRP(t, func(c *passkey.Config) {
		c.Challenges = &expiryBlindChallenges{m: map[string]passkey.Challenge{}}
	})
	a := newAuthenticator(t)
	p.mustRegister(a)

	body, cookie := p.begin(p.h.LoginBegin())
	p.advance(passkey.DefaultChallengeTTL)
	w := p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)})
	if w.Code != http.StatusUnauthorized || strings.TrimSpace(w.Body.String()) != wantRefusedLogin {
		t.Fatalf("expired challenge from an expiry blind store: %d %s, want the one refusal", w.Code, w.Body)
	}

	body, cookie = p.begin(p.h.LoginBegin())
	p.advance(passkey.DefaultChallengeTTL - time.Second)
	if w := p.post(p.h.LoginFinish(), cookie, map[string]any{"credential": a.get(body)}); w.Code != http.StatusOK {
		t.Fatalf("a challenge one second before expiry: %d %s", w.Code, w.Body)
	}
}

type failingStore struct {
	*passkeytest.MemoryStore
	err error
}

func (s *failingStore) FindByUserHandle(context.Context, []byte) (account.Identity, []passkey.Credential, error) {
	return account.Identity{}, nil, s.err
}
