// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/web-kit/session"
)

// loginFixture is a codec whose clock the test moves by hand.
type loginFixture struct {
	codec *session.Codec
	now   time.Time
}

// codecKey is long enough for session.MinKeyBytes. A real one is generated.
const codecKey = "a codec signing key of at least thirty two bytes"

func newLoginFixture(t *testing.T, adjust func(*session.CodecConfig)) *loginFixture {
	t.Helper()

	f := &loginFixture{now: start}
	cfg := session.CodecConfig{
		Key: []byte(codecKey),
		Now: func() time.Time { return f.now },
	}
	if adjust != nil {
		adjust(&cfg)
	}
	codec, err := session.NewCodec(cfg)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	f.codec = codec
	return f
}

// inFlightLogin stands in for an oidc.Login: the state of a login that has not
// happened yet. This package must not import that one, so the shape is
// restated here.
type inFlightLogin struct {
	State string `json:"st"`
	Next  string `json:"nx,omitempty"`
}

// set signs a login into a cookie and returns the response it was written to.
func (f *loginFixture) set(t *testing.T, login inFlightLogin) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	if err := f.codec.SetLoginCookie(rec, login); err != nil {
		t.Fatalf("SetLoginCookie: %v", err)
	}
	return rec
}

// read is ReadLoginCookie with the response it cleared the cookie on handed
// back, because every test here cares about both.
func (f *loginFixture) read(req *http.Request, into *inFlightLogin) (*httptest.ResponseRecorder, error) {
	rec := httptest.NewRecorder()
	return rec, f.codec.ReadLoginCookie(rec, req, into)
}

// onlyCookie returns the one cookie a response is expected to carry.
func onlyCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("the response carries %d cookies, want 1", len(cookies))
	}
	return cookies[0]
}

// withCookie is a request carrying one cookie by name and value, for the
// values a signed round trip cannot produce.
func withCookie(name, value string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: name, Value: value})
	return req
}

// noCookie is a request a browser with nothing stored would make.
func noCookie() *http.Request {
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
}

func TestNewCodecFillsInTheLoginDefaults(t *testing.T) {
	f := newLoginFixture(t, nil)

	if got := f.codec.LoginCookieName(); got != session.DefaultLoginCookieName {
		t.Errorf("login cookie name = %q, want %q", got, session.DefaultLoginCookieName)
	}

	// The default lifetime is observable through the expiry inside the value:
	// good just before it, gone at it.
	req := withCookies(f.set(t, inFlightLogin{State: "s"}))
	f.now = start.Add(session.DefaultLoginLifetime - time.Second)
	var back inFlightLogin
	if _, err := f.read(req, &back); err != nil {
		t.Errorf("a login just under the default lifetime was refused: %v", err)
	}

	req = withCookies(f.set(t, inFlightLogin{State: "s"}))
	f.now = f.now.Add(session.DefaultLoginLifetime)
	if _, err := f.read(req, &back); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("error = %v, want ErrInvalid past the default lifetime", err)
	}
}

func TestNewCodecRefusesANonPositiveLoginLifetime(t *testing.T) {
	_, err := session.NewCodec(session.CodecConfig{
		Key:           []byte(codecKey),
		LoginLifetime: -time.Minute,
	})
	if err == nil {
		t.Fatal("a negative login lifetime was accepted")
	}
	if !strings.Contains(err.Error(), "login lifetime") {
		t.Errorf("error %q does not name the login lifetime", err)
	}
}

func TestNewCodecNamesEveryProblemAtOnce(t *testing.T) {
	_, err := session.NewCodec(session.CodecConfig{Key: []byte("short"), LoginLifetime: -time.Minute})
	if err == nil {
		t.Fatal("a broken config was accepted")
	}
	for _, want := range []string{"codec key", "login lifetime"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestSetLoginCookieAttributes(t *testing.T) {
	f := newLoginFixture(t, nil)

	cookie := onlyCookie(t, f.set(t, inFlightLogin{State: "s"}))
	if cookie.Name != session.DefaultLoginCookieName {
		t.Errorf("name = %q, want %q", cookie.Name, session.DefaultLoginCookieName)
	}
	if cookie.Value == "" {
		t.Error("the cookie carries no value")
	}
	if cookie.Path != "/" {
		t.Errorf("path = %q, want /", cookie.Path)
	}
	if cookie.Domain != "" {
		t.Errorf("domain = %q, want none: the cookie belongs to this host alone", cookie.Domain)
	}
	if !cookie.HttpOnly {
		t.Error("the cookie is readable by a script")
	}
	if !cookie.Secure {
		t.Error("the cookie is not Secure")
	}
	// Lax and not Strict: the provider redirects the browser back to the
	// callback, and that is a cross site top level navigation.
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("same site = %v, want Lax", cookie.SameSite)
	}
	// No Max-Age: the lifetime that counts is the one inside the signed value.
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Errorf("the cookie carries a lifetime: max age %d, expires %s", cookie.MaxAge, cookie.Expires)
	}
}

func TestPlainHTTPDropsTheHostPrefixAndSecureFromTheLoginCookie(t *testing.T) {
	f := newLoginFixture(t, func(cfg *session.CodecConfig) { cfg.AllowPlainHTTP = true })

	if got, want := f.codec.LoginCookieName(), "login"; got != want {
		t.Errorf("login cookie name = %q, want %q", got, want)
	}

	cookie := onlyCookie(t, f.set(t, inFlightLogin{State: "s"}))
	if cookie.Name != "login" {
		t.Errorf("name = %q, want login without the __Host- prefix", cookie.Name)
	}
	if cookie.Secure {
		t.Error("the cookie is Secure on a plain HTTP run, so a browser would never send it back")
	}
}

func TestPlainHTTPAlsoDropsThePrefixFromACustomLoginName(t *testing.T) {
	f := newLoginFixture(t, func(cfg *session.CodecConfig) {
		cfg.LoginCookieName = "__Host-catalogue-login"
		cfg.AllowPlainHTTP = true
	})

	if got, want := f.codec.LoginCookieName(), "catalogue-login"; got != want {
		t.Errorf("login cookie name = %q, want %q", got, want)
	}
}

func TestCustomLoginCookieNameIsKept(t *testing.T) {
	f := newLoginFixture(t, func(cfg *session.CodecConfig) { cfg.LoginCookieName = "__Host-catalogue-login" })

	if got, want := f.codec.LoginCookieName(), "__Host-catalogue-login"; got != want {
		t.Errorf("login cookie name = %q, want %q", got, want)
	}
}

func TestALoginSurvivesTheCookieRoundTrip(t *testing.T) {
	f := newLoginFixture(t, nil)
	login := inFlightLogin{State: "the state", Next: "/lists/7"}

	req := withCookies(f.set(t, login))
	var back inFlightLogin
	if _, err := f.read(req, &back); err != nil {
		t.Fatalf("ReadLoginCookie: %v", err)
	}
	if back != login {
		t.Errorf("the login came back as %+v, want %+v", back, login)
	}
}

func TestReadLoginCookieAlwaysClearsTheCookie(t *testing.T) {
	cases := map[string]struct {
		request func(*loginFixture, *testing.T) *http.Request
		wantErr bool
	}{
		"a successful read": {
			request: func(f *loginFixture, t *testing.T) *http.Request {
				return withCookies(f.set(t, inFlightLogin{State: "s"}))
			},
		},
		"a value that does not verify": {
			request: func(f *loginFixture, _ *testing.T) *http.Request {
				return withCookie(f.codec.LoginCookieName(), "not-a-signed-value.nope")
			},
			wantErr: true,
		},
		"no cookie at all": {
			request: func(*loginFixture, *testing.T) *http.Request { return noCookie() },
			wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLoginFixture(t, nil)

			var back inFlightLogin
			rec, err := f.read(tc.request(f, t), &back)
			if tc.wantErr && err == nil {
				t.Fatal("the read was accepted")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ReadLoginCookie: %v", err)
			}

			// Single use is the point: the login is good for one callback,
			// whether that callback worked or not.
			if cookie := onlyCookie(t, rec); cookie.MaxAge != -1 {
				t.Errorf("max age = %d, want -1: the login cookie was not cleared", cookie.MaxAge)
			}
		})
	}
}

func TestReadLoginCookieRefusesEveryFailureAsErrInvalid(t *testing.T) {
	f := newLoginFixture(t, nil)
	signed := onlyCookie(t, f.set(t, inFlightLogin{State: "s"})).Value

	// One character of the signed payload changed, which is as little as a
	// browser can do to it.
	tampered := []byte(signed)
	if tampered[5] == 'A' {
		tampered[5] = 'B'
	} else {
		tampered[5] = 'A'
	}

	cases := map[string]*http.Request{
		"no cookie at all":  noCookie(),
		"an empty cookie":   withCookie(f.codec.LoginCookieName(), ""),
		"a tampered value":  withCookie(f.codec.LoginCookieName(), string(tampered)),
		"a signature alone": withCookie(f.codec.LoginCookieName(), ".signature"),
		"another cookie":    withCookie("__Host-something-else", signed),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			var back inFlightLogin
			if _, err := f.read(req, &back); !errors.Is(err, session.ErrInvalid) {
				t.Errorf("error = %v, want one that is ErrInvalid", err)
			}
		})
	}
}

func TestALoginExpiresAfterItsLifetime(t *testing.T) {
	f := newLoginFixture(t, func(cfg *session.CodecConfig) { cfg.LoginLifetime = 5 * time.Minute })
	req := withCookies(f.set(t, inFlightLogin{State: "s"}))

	// The expiry is exclusive, the same bound both session timeouts apply.
	f.now = start.Add(5 * time.Minute)

	var back inFlightLogin
	if _, err := f.read(req, &back); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("error = %v, want one that is ErrInvalid: the login is past its lifetime", err)
	}
}

func TestTheClearedLoginCookieMatchesTheSetOne(t *testing.T) {
	f := newLoginFixture(t, nil)
	set := onlyCookie(t, f.set(t, inFlightLogin{State: "s"}))

	rec := httptest.NewRecorder()
	f.codec.ClearLoginCookie(rec)
	gone := onlyCookie(t, rec)

	// A browser matches a deletion against the attributes of what it stored, so
	// a clear differing in one of them leaves the cookie in place.
	if gone.Name != set.Name {
		t.Errorf("name = %q, want %q", gone.Name, set.Name)
	}
	if gone.Path != set.Path {
		t.Errorf("path = %q, want %q", gone.Path, set.Path)
	}
	if gone.Domain != set.Domain {
		t.Errorf("domain = %q, want %q", gone.Domain, set.Domain)
	}
	if gone.Secure != set.Secure {
		t.Errorf("secure = %v, want %v", gone.Secure, set.Secure)
	}
	if gone.HttpOnly != set.HttpOnly {
		t.Errorf("http only = %v, want %v", gone.HttpOnly, set.HttpOnly)
	}
	if gone.SameSite != set.SameSite {
		t.Errorf("same site = %v, want %v", gone.SameSite, set.SameSite)
	}
	if gone.MaxAge != -1 {
		t.Errorf("max age = %d, want -1", gone.MaxAge)
	}
	if gone.Value != "" {
		t.Errorf("the cleared cookie still carries %q", gone.Value)
	}
}
