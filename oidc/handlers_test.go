// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/session"
)

// handlerFixture is the pair of handlers against the fake provider, with a
// real session.Codec as the login store.
//
// The codec is deliberately not a stand in: the design claims a *session.Codec
// satisfies LoginStore with no adapter in between, and the only way to know
// that stays true is to wire the real one up. It is a test only import, so the
// oidc package itself still depends on nothing from session.
type handlerFixture struct {
	fake     *idp
	provider *Provider
	codec    *session.Codec
	handlers *Handlers

	// started is every identity StartSession was handed, in order.
	started []Identity
	// startErr is what StartSession returns.
	startErr error
	// logs is where the request logger writes.
	logs bytes.Buffer
}

func newHandlerFixture(t *testing.T, adjust func(*HandlerConfig)) *handlerFixture {
	t.Helper()

	f := &handlerFixture{fake: newIDP(t)}
	f.provider = provider(t, f.fake, fixed(testNow), nil)

	codec, err := session.NewCodec(session.CodecConfig{
		Key: []byte("a codec signing key of at least thirty two bytes"),
		Now: fixed(testNow),
	})
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	f.codec = codec

	cfg := HandlerConfig{
		Provider: f.provider,
		Logins:   codec,
		StartSession: func(_ context.Context, _ http.ResponseWriter, _ *http.Request, id Identity) error {
			f.started = append(f.started, id)
			return f.startErr
		},
	}
	if adjust != nil {
		adjust(&cfg)
	}
	handlers, err := NewHandlers(cfg)
	if err != nil {
		t.Fatalf("NewHandlers: %v", err)
	}
	f.handlers = handlers
	return f
}

// get runs one GET through a handler, with a logger the test can read.
func (f *handlerFixture) get(handler http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	ctx := logging.NewContext(context.Background(),
		logging.New(&f.logs, slog.LevelDebug))
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// startLogin runs Login and returns the login cookie the response carries
// together with the login signed into it.
func (f *handlerFixture) startLogin(t *testing.T, target string) (*http.Cookie, Login) {
	t.Helper()

	rec := f.get(f.handlers.Login(), target)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	cookie := loginCookieOf(t, rec, f.codec.LoginCookieName())

	var login Login
	if err := f.codec.Decode(cookie.Value, &login); err != nil {
		t.Fatalf("the login cookie does not decode: %v", err)
	}
	return cookie, login
}

// callback replays a login into the callback handler, with the fake provider
// ready to answer the token request for that login's nonce.
func (f *handlerFixture) callback(t *testing.T, cookie *http.Cookie, login Login, query url.Values) *httptest.ResponseRecorder {
	t.Helper()

	f.fake.idToken = sign(t, f.fake.key, "RS256", f.fake.kid, defaultClaims(testNow, login.Nonce))
	return f.get(f.handlers.Callback(), "/auth/callback?"+query.Encode(), cookie)
}

// loginCookieOf returns the login cookie a response set, and fails when it set
// none or cleared it.
func loginCookieOf(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()

	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			if cookie.MaxAge < 0 {
				t.Fatalf("the login cookie %q was cleared, not set", name)
			}
			if cookie.Value == "" {
				t.Fatalf("the login cookie %q carries no value", name)
			}
			return cookie
		}
	}
	t.Fatalf("the response set no %q cookie", name)
	return nil
}

// loginCookieCleared reports whether a response deleted the login cookie.
func loginCookieCleared(rec *httptest.ResponseRecorder, name string) bool {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name && cookie.MaxAge < 0 {
			return true
		}
	}
	return false
}

func TestNewHandlersNamesEveryMissingFieldAtOnce(t *testing.T) {
	_, err := NewHandlers(HandlerConfig{})
	if err == nil {
		t.Fatal("an empty config was accepted")
	}
	for _, want := range []string{"provider", "login store", "start session"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestNewHandlersAcceptsAConfigWithOnlyTheRequiredFields(t *testing.T) {
	f := newHandlerFixture(t, nil)

	// The defaults are observable: a login with no target lands on "/".
	cookie, login := f.startLogin(t, "/auth/login")
	if login.Next != "" {
		t.Errorf("next = %q, want empty", login.Next)
	}
	rec := f.callback(t, cookie, login, url.Values{"state": {login.State}, "code": {"the-code"}})
	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("location = %q, want /", got)
	}
}

func TestLoginRedirectsToTheProvider(t *testing.T) {
	f := newHandlerFixture(t, nil)

	rec := f.get(f.handlers.Login(), "/auth/login")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	// The redirect URL holds the state and the nonce of one browser's attempt,
	// so nothing may keep a copy of it.
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache control = %q, want no-store", got)
	}

	cookie := loginCookieOf(t, rec, f.codec.LoginCookieName())
	var login Login
	if err := f.codec.Decode(cookie.Value, &login); err != nil {
		t.Fatalf("the login cookie does not decode: %v", err)
	}

	parsed, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("the redirect target is not a URL: %v", err)
	}
	q := parsed.Query()
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             testClientID,
		"redirect_uri":          testRedirect,
		"state":                 login.State,
		"nonce":                 login.Nonce,
		"code_challenge":        CodeChallenge(login.Verifier),
		"code_challenge_method": "S256",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// The verifier stays in the cookie. Only its challenge is shown to the
	// provider, which is what makes an intercepted code useless.
	if strings.Contains(parsed.RawQuery, login.Verifier) {
		t.Error("the code verifier is in the authorization URL")
	}
}

func TestLoginKeepsOnlyALocalNextTarget(t *testing.T) {
	cases := map[string]struct {
		next string
		want string
	}{
		"a path":                   {"/lists/7", "/lists/7"},
		"a path with a query":      {"/lists?sort=name", "/lists?sort=name"},
		"an absolute url":          {"https://evil.example.test/x", ""},
		"a protocol relative url":  {"//evil.example.test", ""},
		"a backslash host":         {`/\evil.example.test`, ""},
		"a backslash in the path":  {`/lists\7`, ""},
		"a control character":      {"/lists/\n7", ""},
		"a scheme without a slash": {"javascript:alert(1)", ""},
		"nothing at all":           {"", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newHandlerFixture(t, nil)

			target := "/auth/login?" + url.Values{"next": {tc.next}}.Encode()
			_, login := f.startLogin(t, target)
			if login.Next != tc.want {
				t.Errorf("next = %q, want %q", login.Next, tc.want)
			}
		})
	}
}

func TestLoginIgnoresTheParameterWhenNoNextIsSet(t *testing.T) {
	f := newHandlerFixture(t, func(cfg *HandlerConfig) { cfg.NoNext = true })

	_, login := f.startLogin(t, "/auth/login?next=/lists/7")
	if login.Next != "" {
		t.Errorf("next = %q, want empty: the parameter is turned off", login.Next)
	}
}

func TestLoginReadsAConfiguredNextParameter(t *testing.T) {
	f := newHandlerFixture(t, func(cfg *HandlerConfig) { cfg.NextParam = "returnTo" })

	_, login := f.startLogin(t, "/auth/login?returnTo=/lists/7&next=/elsewhere")
	if login.Next != "/lists/7" {
		t.Errorf("next = %q, want /lists/7", login.Next)
	}
}

func TestTheCallbackFinishesTheLogin(t *testing.T) {
	f := newHandlerFixture(t, nil)
	cookie, login := f.startLogin(t, "/auth/login?next=%2Flists%2F7")

	rec := f.callback(t, cookie, login, url.Values{"state": {login.State}, "code": {"the-code"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/lists/7" {
		t.Errorf("location = %q, want the next target", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache control = %q, want no-store", got)
	}

	if len(f.started) != 1 {
		t.Fatalf("StartSession was called %d times, want 1", len(f.started))
	}
	if got := f.started[0].Subject; got != "4f2a-user" {
		t.Errorf("subject = %q, want the one from the verified id token", got)
	}
	if got := f.started[0].Email; got != "simon@example.test" {
		t.Errorf("email = %q, want the one from the verified id token", got)
	}

	// The login is spent, so the cookie is gone whatever happens next.
	if !loginCookieCleared(rec, f.codec.LoginCookieName()) {
		t.Error("the login cookie was not cleared")
	}
}

func TestTheCallbackChecksTheNextTargetAgain(t *testing.T) {
	f := newHandlerFixture(t, nil)
	cookie, login := f.startLogin(t, "/auth/login")

	// The cookie was signed by this application, but what went into it came
	// from a query string, so surviving a signature is not what makes a target
	// safe to redirect to.
	login.Next = "//evil.example.test"
	value, err := f.codec.Encode(login, DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	cookie.Value = value

	rec := f.callback(t, cookie, login, url.Values{"state": {login.State}, "code": {"the-code"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("location = %q, want /: a hostile target falls back to Success", got)
	}
}

func TestARefusedCallbackSaysNothingAboutWhy(t *testing.T) {
	// Each case is a different step failing. All of them answer through the
	// error responder, none of them starts a session, and none of them tells
	// the browser anything.
	cases := map[string]struct {
		setUp      func(*testing.T, *handlerFixture) (*http.Cookie, Login, url.Values)
		wantStatus int
	}{
		"no login cookie": {
			setUp: func(t *testing.T, f *handlerFixture) (*http.Cookie, Login, url.Values) {
				_, login := f.startLogin(t, "/auth/login")
				return nil, login, url.Values{"state": {login.State}, "code": {"the-code"}}
			},
			wantStatus: http.StatusBadRequest,
		},
		"a state that does not match": {
			setUp: func(t *testing.T, f *handlerFixture) (*http.Cookie, Login, url.Values) {
				cookie, login := f.startLogin(t, "/auth/login")
				return cookie, login, url.Values{"state": {"another browser's state"}, "code": {"the-code"}}
			},
			wantStatus: http.StatusBadRequest,
		},
		"no state at all": {
			setUp: func(t *testing.T, f *handlerFixture) (*http.Cookie, Login, url.Values) {
				cookie, login := f.startLogin(t, "/auth/login")
				return cookie, login, url.Values{"code": {"the-code"}}
			},
			wantStatus: http.StatusBadRequest,
		},
		"the provider sent an error": {
			setUp: func(t *testing.T, f *handlerFixture) (*http.Cookie, Login, url.Values) {
				cookie, login := f.startLogin(t, "/auth/login")
				return cookie, login, url.Values{
					"state":             {login.State},
					"error":             {"access_denied"},
					"error_description": {leakyDescription},
				}
			},
			wantStatus: http.StatusBadRequest,
		},
		"the provider refuses the grant": {
			setUp: func(t *testing.T, f *handlerFixture) (*http.Cookie, Login, url.Values) {
				cookie, login := f.startLogin(t, "/auth/login")
				f.fake.tokenBody = `{"error":"invalid_grant","error_description":"` + leakyDescription + `"}`
				return cookie, login, url.Values{"state": {login.State}, "code": {"the-code"}}
			},
			wantStatus: http.StatusBadRequest,
		},
		"the provider cannot be reached": {
			setUp: func(t *testing.T, f *handlerFixture) (*http.Cookie, Login, url.Values) {
				cookie, login := f.startLogin(t, "/auth/login")
				f.fake.tokenStatus = http.StatusBadGateway
				f.fake.tokenBody = "<html>" + leakyDescription + "</html>"
				return cookie, login, url.Values{"state": {login.State}, "code": {"the-code"}}
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		"the session could not be started": {
			setUp: func(t *testing.T, f *handlerFixture) (*http.Cookie, Login, url.Values) {
				cookie, login := f.startLogin(t, "/auth/login")
				f.startErr = errors.New(leakyDescription)
				return cookie, login, url.Values{"state": {login.State}, "code": {"the-code"}}
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newHandlerFixture(t, nil)
			cookie, login, query := tc.setUp(t, f)

			var cookies []*http.Cookie
			if cookie != nil {
				cookies = append(cookies, cookie)
			}
			f.fake.idToken = sign(t, f.fake.key, "RS256", f.fake.kid, defaultClaims(testNow, login.Nonce))
			rec := f.get(f.handlers.Callback(), "/auth/callback?"+query.Encode(), cookies...)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			// The one case that is allowed to have reached StartSession is the
			// one where StartSession itself failed.
			wantStarted := name == "the session could not be started"
			if started := len(f.started) > 0; started != wantStarted {
				t.Errorf("StartSession was called %d times, want %v", len(f.started), wantStarted)
			}

			body := rec.Body.String()
			for _, leak := range []string{leakyDescription, "oidc:", "invalid_grant", "access_denied"} {
				if strings.Contains(body, leak) {
					t.Errorf("the response body carries %q: %s", leak, body)
				}
			}
			// Whatever failed, the browser is left with no login to send again.
			if cookie != nil && !loginCookieCleared(rec, f.codec.LoginCookieName()) {
				t.Error("the login cookie was not cleared")
			}
		})
	}
}

// leakyDescription is a string only the provider or the application knows. It
// must turn up in the log and never in a response.
const leakyDescription = "the client secret has expired for realm apps"

func TestTheProvidersErrorReachesTheLogAndNotTheBrowser(t *testing.T) {
	f := newHandlerFixture(t, nil)
	cookie, login := f.startLogin(t, "/auth/login")

	rec := f.get(f.handlers.Callback(), "/auth/callback?"+url.Values{
		"state":             {login.State},
		"error":             {"access_denied"},
		"error_description": {leakyDescription},
	}.Encode(), cookie)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	logged := f.logs.String()
	if !strings.Contains(logged, "access_denied") || !strings.Contains(logged, leakyDescription) {
		t.Errorf("the log does not hold what the provider said: %s", logged)
	}
	if strings.Contains(rec.Body.String(), leakyDescription) {
		t.Errorf("the response body carries the provider's description: %s", rec.Body.String())
	}
}

// jar keeps what a browser would keep from a response: a cookie with a
// negative Max-Age is deleted rather than stored, which is how a clear reaches
// the next request.
func jar(kept []*http.Cookie, rec *httptest.ResponseRecorder) []*http.Cookie {
	for _, set := range rec.Result().Cookies() {
		kept = slices.DeleteFunc(kept, func(have *http.Cookie) bool { return have.Name == set.Name })
		if set.MaxAge >= 0 {
			kept = append(kept, set)
		}
	}
	return kept
}

func TestReplayingACallbackIsRefused(t *testing.T) {
	f := newHandlerFixture(t, nil)

	stored := jar(nil, f.get(f.handlers.Login(), "/auth/login"))
	if len(stored) != 1 {
		t.Fatalf("the browser stored %d cookies after the login, want 1", len(stored))
	}
	var login Login
	if err := f.codec.Decode(stored[0].Value, &login); err != nil {
		t.Fatalf("the login cookie does not decode: %v", err)
	}

	target := "/auth/callback?" + url.Values{"state": {login.State}, "code": {"the-code"}}.Encode()
	f.fake.idToken = sign(t, f.fake.key, "RS256", f.fake.kid, defaultClaims(testNow, login.Nonce))

	first := f.get(f.handlers.Callback(), target, stored...)
	if first.Code != http.StatusSeeOther {
		t.Fatalf("the first callback answered %d, want %d: %s", first.Code, http.StatusSeeOther, first.Body.String())
	}

	// The same URL again, from the same browser. The first callback deleted the
	// login cookie, so there is nothing left to finish a login with, which is
	// what single use buys.
	stored = jar(stored, first)
	if len(stored) != 0 {
		t.Fatalf("the browser still holds %d cookies after the callback, want none", len(stored))
	}

	replay := f.get(f.handlers.Callback(), target, stored...)
	if replay.Code != http.StatusBadRequest {
		t.Errorf("the replayed callback answered %d, want %d", replay.Code, http.StatusBadRequest)
	}
	if len(f.started) != 1 {
		t.Errorf("StartSession was called %d times, want 1: the replay started a second session", len(f.started))
	}
}

func TestOnlyGetAndHeadAreServed(t *testing.T) {
	f := newHandlerFixture(t, nil)

	for _, name := range []string{"login", "callback"} {
		handler := f.handlers.Login()
		if name == "callback" {
			handler = f.handlers.Callback()
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			t.Run(name+" "+method, func(t *testing.T) {
				req := httptest.NewRequestWithContext(context.Background(), method, "/auth/"+name, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
				}
				if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
					t.Errorf("allow = %q, want GET, HEAD", got)
				}
			})
		}
	}
}

func TestOnErrorReplacesTheAnswer(t *testing.T) {
	var seen error
	f := newHandlerFixture(t, func(cfg *HandlerConfig) {
		cfg.OnError = func(w http.ResponseWriter, _ *http.Request, err error) {
			seen = err
			w.WriteHeader(http.StatusTeapot)
		}
	})

	rec := f.get(f.handlers.Callback(), "/auth/callback?state=nothing")
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
	// The responder is handed the category, never the cause.
	if !errors.Is(seen, ErrLogin) {
		t.Errorf("error = %v, want ErrLogin", seen)
	}
}

func TestLocalPath(t *testing.T) {
	cases := map[string]struct {
		target string
		want   string
	}{
		"a path":                        {"/lists/7", "/lists/7"},
		"the root":                      {"/", "/"},
		"a two character path":          {"/x", "/x"},
		"a path with a query":           {"/lists?sort=name", "/lists?sort=name"},
		"a path with a fragment":        {"/lists#top", "/lists#top"},
		"an empty target":               {"", ""},
		"a relative path":               {"lists/7", ""},
		"an https url":                  {"https://evil.example.test/x", ""},
		"an uppercase scheme":           {"HTTPS://evil.example.test/x", ""},
		"a javascript url":              {"javascript:alert(1)", ""},
		"a data url":                    {"data:text/html,<script>", ""},
		"a protocol relative url":       {"//evil.example.test/x", ""},
		"three slashes":                 {"///evil.example.test", ""},
		"a backslash after the slash":   {`/\evil.example.test`, ""},
		"a backslash later in the path": {`/lists\7`, ""},
		"a newline":                     {"/lists\n/7", ""},
		"a carriage return":             {"/lists\r/7", ""},
		"a tab":                         {"/lists\t/7", ""},
		"a null byte":                   {"/lists/\x00", ""},
		"a delete character":            {"/lists/\x7f", ""},
		"a space":                       {"/lists/ 7", "/lists/ 7"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := localPath(tc.target); got != tc.want {
				t.Errorf("localPath(%q) = %q, want %q", tc.target, got, tc.want)
			}
		})
	}
}
