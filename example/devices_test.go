// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/session"
	"github.com/ostendorf-cloud/govue/session/devicehttp"
)

const (
	tokenPath   = authPrefix + "/token"
	rotatePath  = authPrefix + "/token/rotate"
	devicesPath = authPrefix + "/devices"

	otherLogin    = "erin@example.com"
	otherPassword = "fake-example-password-three"
)

// newAccount signs a login up and confirms its secret over HTTP. It returns
// the TOTP secret and the cookie of the first session.
func newAccount(t *testing.T, a *app, clock *testClock, login, password string) (string, *http.Cookie) {
	t.Helper()

	w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/signup",
		body: map[string]string{"login": login, "password": password},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("signup %s = %d %s, want 201", login, w.Code, w.Body.String())
	}
	secret := signupSecret(t, w.Body.Bytes())

	clock.Add(time.Minute)
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/confirm",
		body: creds(login, password, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("confirm %s = %d %s, want 200", login, w.Code, w.Body.String())
	}
	cookie := sessionCookie(a, w)
	if cookie == nil {
		t.Fatal("confirm started no session")
	}

	return secret, cookie
}

// issueToken signs in over HTTP as an app does: credentials and a device name,
// no cookie. Each call takes the next TOTP code.
func issueToken(t *testing.T, a *app, clock *testClock, login, password, secret, name string) devicehttp.TokenResult {
	t.Helper()

	clock.Add(time.Minute)
	body := creds(login, password, totpNow(t, secret, clock))
	body["device_name"] = name
	w := a.serve(t, accountCall{method: http.MethodPost, path: tokenPath, body: body})
	if w.Code != http.StatusCreated {
		t.Fatalf("token issue = %d %s, want 201", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("token issue set a cookie: %v", w.Result().Cookies())
	}
	var res devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Token == "" || res.TokenID == "" {
		t.Fatalf("token issue body = %s, err %v", w.Body.String(), err)
	}

	return res
}

func bearerHeader(token string) string { return "Bearer " + token }

// notesWith is the status a bearer token gets from a route that needs a session.
func notesWith(t *testing.T, a *app, token string) int {
	t.Helper()

	return a.serve(t, accountCall{method: http.MethodGet, path: "/notes", authorization: bearerHeader(token)}).Code
}

func listDevices(t *testing.T, a *app, c accountCall) devicehttp.DeviceList {
	t.Helper()

	c.method, c.path = http.MethodGet, devicesPath
	w := a.serve(t, c)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", w.Code, w.Body.String())
	}
	var out devicehttp.DeviceList
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("list body = %s: %v", w.Body.String(), err)
	}

	return out
}

// TestDeviceTokensOverHTTP runs the whole life of a device through the real
// chain: sign up, confirm, token issue, an authenticated call, rotation, the
// old token inside the grace and after it, list and revoke.
func TestDeviceTokensOverHTTP(t *testing.T) {
	t.Parallel()
	a, clock := newAccountApp(t, true, 100)

	secret, cookie := newAccount(t, a, clock, acctLogin, acctPassword)
	otherSecret, _ := newAccount(t, a, clock, otherLogin, otherPassword)

	// Issue. The token is in the answer, the answer is never cached, and no
	// cookie moves.
	phone := issueToken(t, a, clock, acctLogin, acctPassword, secret, "Pixel")
	issuedAt := clock.Now()
	if !phone.ExpiresAt.Equal(issuedAt.Add(session.DefaultDeviceLifetime)) {
		t.Errorf("expires_at = %s", phone.ExpiresAt)
	}

	// An authenticated call with the bearer token, with no CSRF token and from
	// another site, as an app sends it. It lands under the device's subject.
	if got := notesWith(t, a, phone.Token); got != http.StatusOK {
		t.Fatalf("GET /notes with the token = %d, want 200", got)
	}
	w := a.serve(t, accountCall{
		method: http.MethodPost, path: "/notes", body: map[string]string{"text": "from the app"},
		authorization: bearerHeader(phone.Token), site: "cross-site", origin: evilOrigin,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /notes with the token = %d %s, want 201", w.Code, w.Body.String())
	}
	if got := notesStatus(t, a, cookie); got != http.StatusOK {
		t.Fatalf("the cookie session of the same account = %d", got)
	}
	cookieNotes := a.serve(t, accountCall{method: http.MethodGet, path: "/notes", cookie: cookie}).Body.String()
	if !strings.Contains(cookieNotes, "from the app") {
		t.Errorf("the note of the app is not under the account's subject: %s", cookieNotes)
	}

	// A rotation inside the minimum interval is refused, and keeps the token.
	rotate := func(token, nonce string) *httptest.ResponseRecorder {
		return a.serve(t, accountCall{
			method: http.MethodPost, path: rotatePath, body: map[string]string{"nonce": nonce},
			authorization: bearerHeader(token), site: "cross-site", origin: evilOrigin,
		})
	}
	if w := rotate(phone.Token, "nonce-one"); w.Code != http.StatusConflict {
		t.Fatalf("early rotate = %d %s, want 409", w.Code, w.Body.String())
	}
	if got := notesWith(t, a, phone.Token); got != http.StatusOK {
		t.Fatalf("the token after a refused rotation = %d", got)
	}

	// Rotation: a new token, the same id and expiry.
	clock.Add(session.DefaultDeviceMinRotateInterval + time.Minute)
	w = rotate(phone.Token, "nonce-two")
	if w.Code != http.StatusOK {
		t.Fatalf("rotate = %d %s, want 200", w.Code, w.Body.String())
	}
	var rotated devicehttp.TokenResult
	if err := json.Unmarshal(w.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.Token == phone.Token || rotated.TokenID != phone.TokenID || !rotated.ExpiresAt.Equal(phone.ExpiresAt) {
		t.Fatalf("rotation result = %+v, first = %+v", rotated, phone)
	}

	// The first use of the new token confirms it. The old one stays valid for
	// the grace, however many requests are in flight.
	if got := notesWith(t, a, rotated.Token); got != http.StatusOK {
		t.Fatalf("the new token = %d, want 200", got)
	}
	clock.Add(time.Minute)
	for range 2 {
		if got := notesWith(t, a, phone.Token); got != http.StatusOK {
			t.Fatalf("the old token inside the grace = %d, want 200", got)
		}
	}

	// List, with the token and with the cookie. The cookie is a minute older
	// than the grace allows to forget: refresh by reading it through notes.
	for name, c := range map[string]accountCall{
		"bearer": {authorization: bearerHeader(rotated.Token)},
		"cookie": {cookie: cookie},
	} {
		out := listDevices(t, a, c)
		if len(out.Devices) != 1 || out.Devices[0].ID != phone.TokenID || out.Devices[0].Name != "Pixel" {
			t.Errorf("%s list = %+v", name, out.Devices)
		}
		if !out.Devices[0].ExpiresAt.Equal(phone.ExpiresAt) {
			t.Errorf("%s list: the stored expiry moved to %s, want %s", name, out.Devices[0].ExpiresAt, phone.ExpiresAt)
		}
		if got := out.Devices[0].Current; got != (name == "bearer") {
			t.Errorf("%s list: current = %v", name, got)
		}
	}

	// Revoke. The web session ends the watch, a device ends the tablet, and an
	// id of another account answers like an unknown one.
	watch := issueToken(t, a, clock, acctLogin, acctPassword, secret, "Watch")
	tablet := issueToken(t, a, clock, acctLogin, acctPassword, secret, "Tablet")
	foreign := issueToken(t, a, clock, otherLogin, otherPassword, otherSecret, "Erin phone")

	revoke := func(id string, c accountCall) *httptest.ResponseRecorder {
		c.method, c.path = http.MethodDelete, devicesPath+"/"+id
		return a.serve(t, c)
	}
	if w := revoke(watch.TokenID, accountCall{cookie: cookie}); w.Code != http.StatusForbidden {
		t.Fatalf("revoke by cookie without the CSRF token = %d, want 403", w.Code)
	}
	if got := notesWith(t, a, watch.Token); got != http.StatusOK {
		t.Fatalf("the watch after a refused revoke = %d", got)
	}
	if w := revoke(watch.TokenID, accountCall{cookie: cookie, token: tokenOf(t, a, cookie)}); w.Code != http.StatusNoContent {
		t.Fatalf("revoke by cookie = %d %s, want 204", w.Code, w.Body.String())
	}
	if got := notesWith(t, a, watch.Token); got != http.StatusUnauthorized {
		t.Fatalf("the revoked watch = %d, want 401", got)
	}
	if w := revoke(tablet.TokenID, accountCall{authorization: bearerHeader(rotated.Token)}); w.Code != http.StatusNoContent {
		t.Fatalf("revoke by device = %d %s, want 204", w.Code, w.Body.String())
	}
	if got := notesWith(t, a, tablet.Token); got != http.StatusUnauthorized {
		t.Fatalf("the revoked tablet = %d, want 401", got)
	}
	other := revoke(foreign.TokenID, accountCall{authorization: bearerHeader(rotated.Token)})
	unknown := revoke(strings.Repeat("u", len(foreign.TokenID)), accountCall{authorization: bearerHeader(rotated.Token)})
	// The request id is the one field that differs by request.
	noID := func(w *httptest.ResponseRecorder) string {
		var env map[string]map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("error body %s: %v", w.Body.String(), err)
		}
		delete(env["error"], "request_id")
		out, _ := json.Marshal(env)

		return string(out)
	}
	if other.Code != http.StatusNotFound || other.Code != unknown.Code || noID(other) != noID(unknown) {
		t.Fatalf("another account's id = %d %s, unknown id = %d %s", other.Code, other.Body, unknown.Code, unknown.Body)
	}
	if got := notesWith(t, a, foreign.Token); got != http.StatusOK {
		t.Fatalf("the other account's device after the attempt = %d, want 200", got)
	}

	// After the grace the old token is reuse: 401, and the whole device goes,
	// the holder of the new token with it.
	clock.Add(session.DefaultDeviceGrace)
	w = a.serve(t, accountCall{method: http.MethodGet, path: "/notes", authorization: bearerHeader(phone.Token)})
	if w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the old token after the grace = %d %q, want 401 no-store", w.Code, w.Header().Get("Cache-Control"))
	}
	if got := notesWith(t, a, rotated.Token); got != http.StatusUnauthorized {
		t.Fatalf("the new token after the reuse = %d, want 401", got)
	}
	// The cookie session of the account is untouched by the device's end.
	if got := notesStatus(t, a, cookie); got != http.StatusOK {
		t.Errorf("the cookie session after the reuse = %d, want 200", got)
	}
	if out := listDevices(t, a, accountCall{cookie: cookie}); len(out.Devices) != 0 {
		t.Errorf("devices after the reuse = %+v", out.Devices)
	}

	// The device signs itself out, and a second sign out is a 401.
	last := issueToken(t, a, clock, acctLogin, acctPassword, secret, "Phone again")
	signOut := func(token string) *httptest.ResponseRecorder {
		return a.serve(t, accountCall{method: http.MethodDelete, path: tokenPath, authorization: bearerHeader(token)})
	}
	if w := signOut(last.Token); w.Code != http.StatusNoContent {
		t.Fatalf("sign out = %d %s, want 204", w.Code, w.Body.String())
	}
	if got := notesWith(t, a, last.Token); got != http.StatusUnauthorized {
		t.Fatalf("the token after the sign out = %d, want 401", got)
	}
	if w := signOut(last.Token); w.Code != http.StatusUnauthorized {
		t.Fatalf("second sign out = %d, want 401", w.Code)
	}
}

// TestDeviceTokensEndWithAPasswordChange pins that the password change of the
// account handlers ends the device sessions too, and that the old password
// issues nothing afterwards.
func TestDeviceTokensEndWithAPasswordChange(t *testing.T) {
	t.Parallel()
	a, clock := newAccountApp(t, true, 100)

	secret, cookie := newAccount(t, a, clock, acctLogin, acctPassword)
	phone := issueToken(t, a, clock, acctLogin, acctPassword, secret, "Pixel")
	tablet := issueToken(t, a, clock, acctLogin, acctPassword, secret, "Tablet")
	otherSecret, _ := newAccount(t, a, clock, otherLogin, otherPassword)
	foreign := issueToken(t, a, clock, otherLogin, otherPassword, otherSecret, "Erin phone")
	for _, tok := range []string{phone.Token, tablet.Token, foreign.Token} {
		if got := notesWith(t, a, tok); got != http.StatusOK {
			t.Fatalf("a token before the change = %d, want 200", got)
		}
	}

	w := a.serve(t, accountCall{
		method: http.MethodPost, path: accountPrefix + "/password", cookie: cookie, token: tokenOf(t, a, cookie),
		body: map[string]string{"current_password": acctPassword, "new_password": acctNew},
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sessions_ended":true`) {
		t.Fatalf("password change = %d %s, want 200 with every session ended", w.Code, w.Body.String())
	}

	for name, tok := range map[string]string{"phone": phone.Token, "tablet": tablet.Token} {
		if got := notesWith(t, a, tok); got != http.StatusUnauthorized {
			t.Errorf("%s after the change = %d, want 401", name, got)
		}
	}
	if got := notesWith(t, a, foreign.Token); got != http.StatusOK {
		t.Errorf("another account's device after the change = %d, want 200", got)
	}

	// The old password issues nothing, the new one does.
	clock.Add(time.Minute)
	w = a.serve(t, accountCall{
		method: http.MethodPost, path: tokenPath,
		body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("issue with the old password = %d, want 401", w.Code)
	}
	fresh := issueToken(t, a, clock, acctLogin, acctNew, secret, "Pixel")
	if got := notesWith(t, a, fresh.Token); got != http.StatusOK {
		t.Fatalf("the token of the new password = %d, want 200", got)
	}
}

// The origins of ALLOWED_ORIGINS are web origins: the CSRF protector trusts
// them and the CORS policy sends credentials to them. An app origin must never
// be one of them, so the issue route does not trust them either. Without app
// origins the CSRF protector lets such an origin through, and the handler's own
// check refuses. With app origins the protector refuses first, see
// TestIssueFromAnUnlistedOriginIsRefusedByTheProtector.
func TestDeviceTokenIssueDoesNotTrustTheWebOrigins(t *testing.T) {
	t.Parallel()
	a, clock := newAccountApp(t, true, 100)
	secret, _ := newAccount(t, a, clock, acctLogin, acctPassword)

	clock.Add(time.Minute)
	w := a.serve(t, accountCall{
		method: http.MethodPost, path: tokenPath, site: "cross-site", origin: a.cfg.allowedOrigins[0],
		body: creds(acctLogin, acctPassword, totpNow(t, secret, clock)),
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("issue from a web origin = %d %s, want 403", w.Code, w.Body.String())
	}
}

// The example maps the answers of Service.Login to the two errors devicehttp
// knows: a busy hasher is a 503 with Retry-After, never a refused sign in.
func TestMapLoginError(t *testing.T) {
	t.Parallel()

	if got := mapLoginError(fmt.Errorf("login: %w", account.ErrInvalidCredentials)); !errors.Is(got, devicehttp.ErrInvalidCredentials) {
		t.Errorf("invalid credentials map to %v", got)
	}
	if got := mapLoginError(fmt.Errorf("login: %w", account.ErrHashingBusy)); !errors.Is(got, devicehttp.ErrBusy) {
		t.Errorf("a busy hasher maps to %v", got)
	}
	other := errors.New("store down")
	if got := mapLoginError(other); !errors.Is(got, other) || errors.Is(got, devicehttp.ErrInvalidCredentials) || errors.Is(got, devicehttp.ErrBusy) {
		t.Errorf("another error maps to %v", got)
	}

	// Through the handler, ErrBusy is a 503 with Retry-After.
	h, err := devicehttp.NewHandlers(devicehttp.HandlerConfig{
		Sessions: mustSessions(t),
		VerifyCredentials: func(context.Context, devicehttp.Credentials) (string, error) {
			return "", mapLoginError(account.ErrHashingBusy)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tokenPath, strings.NewReader(`{"login":"a"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Issue().ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("busy = %d, Retry-After %q, want 503 with one", w.Code, w.Header().Get("Retry-After"))
	}
}

func mustSessions(t *testing.T) *session.Manager {
	t.Helper()
	m, err := session.New(session.Config{
		Store:   session.NewMemoryStore(nil, 10),
		Devices: session.DeviceConfig{Store: session.NewMemoryDeviceStore(nil, 10)},
	})
	if err != nil {
		t.Fatal(err)
	}

	return m
}

// TestDeviceTokenIssueIsRateLimited pins the 429 the package documentation
// asks every product to have in front of the issue route.
func TestDeviceTokenIssueIsRateLimited(t *testing.T) {
	t.Parallel()
	const burst = 3
	a, _ := newAccountApp(t, false, burst)

	var last *httptest.ResponseRecorder
	for i := range burst + 1 {
		last = a.serve(t, accountCall{
			method: http.MethodPost, path: tokenPath,
			body: creds(acctLogin, "fake-wrong-password-"+strconv.Itoa(i), "000000"),
		})
		if i < burst && last.Code != http.StatusUnauthorized {
			t.Fatalf("request %d = %d, want 401", i, last.Code)
		}
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d = %d %s, want 429", burst, last.Code, last.Body.String())
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("the 429 carries no Retry-After")
	}
	// The stricter limit is on the issue route only: a bearer route is not
	// held back by it.
	if got := notesStatus(t, a, nil); got != http.StatusUnauthorized {
		t.Errorf("an ordinary route = %d, want its own answer", got)
	}
}

// tokenCredential is one way a request authenticates, or fails to, for the
// contract of the token handlers.
type tokenCredential struct {
	name   string
	cookie bool
	auth   func(f tokenFixtures) []string
	// kind is what the shared predicate and the device store make of it:
	// none (no bearer request), valid (a token that authenticates) or refused
	// (a token that fails, or two headers).
	kind string
}

const (
	kindNone    = "none"
	kindValid   = "valid"
	kindRefused = "refused"
)

type tokenFixtures struct {
	cookie    *http.Cookie
	csrfToken string
	token     string
	tablet    session.Device
	tabletTok string
}

func tokenCredentials() []tokenCredential {
	bearerOnly := func(f tokenFixtures) []string { return []string{"Bearer " + f.token} }
	return []tokenCredential{
		{"neither", false, func(tokenFixtures) []string { return nil }, kindNone},
		{"cookie only", true, func(tokenFixtures) []string { return nil }, kindNone},
		{"cookie and basic", true, func(tokenFixtures) []string { return []string{"Basic dXNlcjpwYXNz"} }, kindNone},
		{"bearer only", false, bearerOnly, kindValid},
		{"bearer and cookie", true, bearerOnly, kindValid},
		{"junk bearer", false, func(tokenFixtures) []string { return []string{"Bearer example_junk_token"} }, kindRefused},
		{"junk bearer and cookie", true, func(tokenFixtures) []string { return []string{"Bearer example_junk_token"} }, kindRefused},
		{"two headers and cookie", true, func(f tokenFixtures) []string {
			return []string{"Bearer " + f.token, "Bearer " + f.token}
		}, kindRefused},
	}
}

// tokenExpected is the contract of ADR 0001 for the token handlers, written as
// rules. handler is issue, rotate, signout, list or revoke.
func tokenExpected(handler string, c tokenCredential, cross, withCSRF bool) int {
	switch c.kind {
	case kindRefused:
		// A token that fails is the middleware's one 401, on every route, and
		// the cookie is never a second chance.
		return http.StatusUnauthorized
	case kindValid:
		if handler == "issue" && cross {
			// Issue always checks the origin: it carries no bearer token.
			return http.StatusForbidden
		}
		return map[string]int{
			"issue": http.StatusCreated, "rotate": http.StatusOK, "signout": http.StatusNoContent,
			"list": http.StatusOK, "revoke": http.StatusNoContent,
		}[handler]
	}
	// Not a bearer request: the CSRF protector checks every unsafe one.
	if handler != "list" {
		if cross || (c.cookie && !withCSRF) {
			return http.StatusForbidden
		}
	}
	switch handler {
	case "issue":
		return http.StatusCreated
	case "list":
		if c.cookie {
			return http.StatusOK
		}
	case "revoke":
		// The cookie session belongs to another subject than the device.
		if c.cookie {
			return http.StatusNotFound
		}
	}

	return http.StatusUnauthorized
}

// TestDeviceTokenHandlersContract sends the cross product of credential, site
// and CSRF token to each token handler through the real route chain and pins
// every outcome and its effect. ADR 0001 is the spec.
func TestDeviceTokenHandlersContract(t *testing.T) {
	a, clock := newAccountApp(t, true, 1000)
	secret, _ := newAccount(t, a, clock, acctLogin, acctPassword)
	h := a.handler()

	for _, handler := range []string{"issue", "rotate", "signout", "list", "revoke"} {
		for _, cred := range tokenCredentials() {
			for _, cross := range []bool{false, true} {
				for _, withCSRF := range []bool{false, true} {
					if !cred.cookie && withCSRF {
						continue
					}
					name := handler + "/" + cred.name
					if cross {
						name += "/cross site"
					}
					if withCSRF {
						name += "/csrf token"
					}
					t.Run(name, func(t *testing.T) {
						runTokenContractCase(t, a, clock, h, secret, handler, cred, cross, withCSRF)
					})
				}
			}
		}
	}
}

func runTokenContractCase(t *testing.T, a *app, clock *testClock, h http.Handler, secret, handler string, cred tokenCredential, cross, withCSRF bool) {
	t.Helper()

	// Fresh credentials for every case: a sign out or a revocation ends them.
	rec := httptest.NewRecorder()
	s, err := a.sessions.Start(t.Context(), rec, session.Session{Subject: webSubject})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Start set %d cookies", len(cookies))
	}
	// A second apart, so the cap of devices per account, which ends the least
	// recently seen one, never ends a fixture of this case.
	clock.Add(time.Second)
	token, _, err := a.sessions.IssueDevice(t.Context(), appSubject, "phone", clock.Now())
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	clock.Add(time.Second)
	tabletTok, tablet, err := a.sessions.IssueDevice(t.Context(), appSubject, "tablet", clock.Now())
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	f := tokenFixtures{cookie: cookies[0], csrfToken: csrf.Token(a.cfg.csrfKey, s.ID), token: token, tablet: tablet, tabletTok: tabletTok}

	method, path := http.MethodGet, devicesPath
	var body string
	switch handler {
	case "issue":
		method, path = http.MethodPost, tokenPath
		clock.Add(time.Minute)
		raw, _ := json.Marshal(creds(acctLogin, acctPassword, totpNow(t, secret, clock)))
		body = string(raw)
	case "rotate":
		method, path, body = http.MethodPost, rotatePath, `{"nonce":"contract-nonce"}`
		// The minimum interval since the issue has to be over.
		clock.Add(session.DefaultDeviceMinRotateInterval + time.Minute)
	case "signout":
		method, path = http.MethodDelete, tokenPath
	case "revoke":
		method, path = http.MethodDelete, devicesPath+"/"+tablet.ID
	}

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cross {
		req.Header.Set("Origin", evilOrigin)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
	} else {
		req.Header.Set("Origin", sameOrigin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if withCSRF {
		req.Header.Set(csrf.HeaderName, f.csrfToken)
	}
	if cred.cookie {
		req.AddCookie(f.cookie)
	}
	for _, v := range cred.auth(f) {
		req.Header.Add("Authorization", v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	want := tokenExpected(handler, cred, cross, withCSRF)
	if w.Code != want {
		t.Fatalf("status = %d, want %d: %s", w.Code, want, w.Body.String())
	}
	if got := w.Header().Values("Cache-Control"); len(got) != 1 || got[0] != "no-store" {
		t.Errorf("Cache-Control = %q, want one no-store", got)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Errorf("Set-Cookie = %v, want none", got)
	}
	if cred.kind == kindRefused && w.Header().Get("WWW-Authenticate") != challengeInvalid {
		t.Errorf("WWW-Authenticate = %q", w.Header().Get("WWW-Authenticate"))
	}
	for _, secretValue := range []string{f.token, f.tabletTok} {
		if strings.Contains(w.Body.String(), secretValue) {
			t.Error("the body echoes a token")
		}
	}

	alive := func(tok string) bool {
		_, err := a.sessions.AuthenticateDevice(t.Context(), tok)
		return err == nil
	}
	switch handler {
	case "issue":
		if want != http.StatusCreated {
			return
		}
		var res devicehttp.TokenResult
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Token == "" {
			t.Fatalf("body = %s, err %v", w.Body.String(), err)
		}
		d, err := a.sessions.AuthenticateDevice(t.Context(), res.Token)
		if err != nil || d.Subject == appSubject || d.Subject == webSubject {
			t.Errorf("the issued device belongs to %q, err %v: it must belong to the credentials", d.Subject, err)
		}
	case "rotate":
		if want == http.StatusOK {
			var res devicehttp.TokenResult
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Token == "" || res.Token == f.token {
				t.Fatalf("body = %s, err %v", w.Body.String(), err)
			}
		} else if strings.Contains(w.Body.String(), `"token"`) {
			t.Error("a refused rotation carries a token")
		}
	case "signout":
		if alive(f.token) != (want != http.StatusNoContent) {
			t.Errorf("token alive = %v after status %d", alive(f.token), want)
		}
		if !alive(f.tabletTok) {
			t.Error("the other device of the subject ended")
		}
	case "revoke":
		if alive(f.tabletTok) != (want != http.StatusNoContent) {
			t.Errorf("target alive = %v after status %d", alive(f.tabletTok), want)
		}
	case "list":
		if want == http.StatusOK {
			var out devicehttp.DeviceList
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			wantDevices := 2
			if cred.kind != kindValid {
				// The cookie session is the web subject, which has none.
				wantDevices = 0
			}
			if len(out.Devices) < wantDevices {
				t.Errorf("devices = %d, want at least %d", len(out.Devices), wantDevices)
			}
			for _, d := range out.Devices {
				if cred.kind != kindValid && (d.ID == f.tablet.ID) {
					t.Error("the cookie of the web subject lists a device of the app subject")
				}
			}
		}
	}
}
