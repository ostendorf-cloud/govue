// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/session"
)

// bearerFixture is a manager with a cookie store that counts its reads and a
// device store, both on one clock the test moves by hand.
type bearerFixture struct {
	now     time.Time
	cookies *countingStore
	devices *session.MemoryDeviceStore
	manager *session.Manager
}

func newBearerFixture(t *testing.T, withDevices bool, adjust func(*session.Config)) *bearerFixture {
	t.Helper()
	f := &bearerFixture{now: start}
	clock := func() time.Time { return f.now }
	f.cookies = &countingStore{MemoryStore: session.NewMemoryStore(clock, 100)}
	cfg := session.Config{Store: f.cookies, Now: clock}
	if withDevices {
		f.devices = session.NewMemoryDeviceStore(clock, 100)
		cfg.Devices = session.DeviceConfig{Store: f.devices}
	}
	if adjust != nil {
		adjust(&cfg)
	}
	manager, err := session.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.manager = manager
	return f
}

// login starts a cookie session for web-user and returns its cookie.
func (f *bearerFixture) login(t *testing.T) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if _, err := f.manager.Start(context.Background(), rec, session.Session{Subject: "web-user"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Start set %d cookies, want 1", len(cookies))
	}
	return cookies[0]
}

// issue starts a device session for app-user and returns its token.
func (f *bearerFixture) issue(t *testing.T) (string, session.Device) {
	t.Helper()
	token, device, err := f.manager.IssueDevice(context.Background(), "app-user", "phone", f.now)
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	return token, device
}

// request builds a GET with the given Authorization values and cookie.
func request(cookie *http.Cookie, auth ...string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	for _, v := range auth {
		req.Header.Add("Authorization", v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	return req
}

// junkToken has the shape of nothing this package issues.
const junkToken = "example_not_a_real_token"

func TestReadAuthenticatesABearerRequestByItsToken(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	token, device := f.issue(t)

	for name, scheme := range map[string]string{"canonical": "Bearer ", "lower case": "bearer ", "two spaces": "Bearer  "} {
		t.Run(name, func(t *testing.T) {
			got, ok, err := f.manager.Read(context.Background(), request(nil, scheme+token))
			if err != nil || !ok {
				t.Fatalf("Read = ok %v, err %v, want a session", ok, err)
			}
			if got.Subject != "app-user" || got.DeviceID != device.ID || got.Credential != session.CredentialDevice {
				t.Errorf("Read = subject %q device %q credential %v, want app-user, %q, device",
					got.Subject, got.DeviceID, got.Credential, device.ID)
			}
			if got.ID != "" {
				t.Errorf("ID = %q, want empty: a device session has no cookie session id", got.ID)
			}
			if !got.ExpiresAt.Equal(device.ExpiresAt) || !got.IssuedAt.Equal(device.CreatedAt) {
				t.Errorf("times = %v %v, want the device's %v %v", got.IssuedAt, got.ExpiresAt, device.CreatedAt, device.ExpiresAt)
			}
		})
	}
}

func TestReadMarksACookieSession(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	cookie := f.login(t)

	for name, req := range map[string]*http.Request{
		"no header":    request(cookie),
		"basic scheme": request(cookie, "Basic dXNlcjpwYXNz"),
		"empty value":  request(cookie, ""),
	} {
		t.Run(name, func(t *testing.T) {
			got, ok, err := f.manager.Read(context.Background(), req)
			if err != nil || !ok {
				t.Fatalf("Read = ok %v, err %v, want the cookie session", ok, err)
			}
			if got.Subject != "web-user" || got.Credential != session.CredentialCookie || got.DeviceID != "" {
				t.Errorf("Read = subject %q credential %v device %q, want web-user, cookie, none",
					got.Subject, got.Credential, got.DeviceID)
			}
		})
	}
}

func TestStartAndRotateMarkACookieSession(t *testing.T) {
	f := newBearerFixture(t, false, nil)
	started, err := f.manager.Start(context.Background(), httptest.NewRecorder(), session.Session{Subject: "u1", Credential: session.CredentialDevice})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if started.Credential != session.CredentialCookie {
		t.Errorf("Start credential = %v, want cookie", started.Credential)
	}
	rotated, err := f.manager.Rotate(context.Background(), httptest.NewRecorder(), started)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.Credential != session.CredentialCookie {
		t.Errorf("Rotate credential = %v, want cookie", rotated.Credential)
	}
}

// TestReadNeverFallsBackToTheCookie is the rule the CSRF boundary rests on: a
// bearer request skips the CSRF check, so it must never authenticate by the
// cookie, whatever happens to its token.
func TestReadNeverFallsBackToTheCookie(t *testing.T) {
	cases := map[string]struct {
		devices bool
		auth    func(t *testing.T, f *bearerFixture) []string
	}{
		"a junk token":   {true, func(*testing.T, *bearerFixture) []string { return []string{"Bearer " + junkToken} }},
		"an empty token": {true, func(*testing.T, *bearerFixture) []string { return []string{"Bearer"} }},
		"two headers, one valid": {true, func(t *testing.T, f *bearerFixture) []string {
			token, _ := f.issue(t)
			return []string{"Bearer " + token, "Bearer " + token}
		}},
		"two basic headers": {true, func(*testing.T, *bearerFixture) []string { return []string{"Basic a", "Basic b"} }},
		"a revoked token": {true, func(t *testing.T, f *bearerFixture) []string {
			token, device := f.issue(t)
			if err := f.manager.RevokeDevice(context.Background(), "app-user", device.ID); err != nil {
				t.Fatalf("RevokeDevice: %v", err)
			}
			return []string{"Bearer " + token}
		}},
		"an expired token": {true, func(t *testing.T, f *bearerFixture) []string {
			token, _ := f.issue(t)
			f.now = f.now.Add(session.DefaultDeviceIdle)
			return []string{"Bearer " + token}
		}},
		"an ended account": {true, func(t *testing.T, f *bearerFixture) []string {
			token, _ := f.issue(t)
			if err := f.manager.EndAll(context.Background(), "app-user"); err != nil {
				t.Fatalf("EndAll: %v", err)
			}
			return []string{"Bearer " + token}
		}},
		"a valid token without a device store": {false, func(*testing.T, *bearerFixture) []string {
			return []string{"Bearer " + junkToken}
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newBearerFixture(t, tc.devices, nil)
			cookie := f.login(t)
			auth := tc.auth(t, f)
			before := f.cookies.count()

			got, ok, err := f.manager.Read(context.Background(), request(cookie, auth...))
			if ok || err != nil || got.Subject != "" {
				t.Errorf("Read = %q ok %v err %v, want no session and no error", got.Subject, ok, err)
			}
			if f.cookies.count() != before {
				t.Error("Read asked the cookie store on a bearer request")
			}
		})
	}
}

// TestBearerWinsOverTheCookie pins the owner's decision: with both, the
// request authenticates as the token's subject and the cookie is ignored.
func TestBearerWinsOverTheCookie(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	cookie := f.login(t)
	token, _ := f.issue(t)
	before := f.cookies.count()

	got, ok, err := f.manager.Read(context.Background(), request(cookie, "Bearer "+token))
	if err != nil || !ok {
		t.Fatalf("Read = ok %v err %v", ok, err)
	}
	if got.Subject != "app-user" || got.Credential != session.CredentialDevice {
		t.Errorf("Read = %q %v, want app-user by device", got.Subject, got.Credential)
	}
	if f.cookies.count() != before {
		t.Error("Read asked the cookie store although the token authenticated")
	}
}

func TestReadReportsADeviceStoreOutage(t *testing.T) {
	f := newBearerFixture(t, false, func(cfg *session.Config) {
		cfg.Devices = session.DeviceConfig{Store: failingDeviceStore{}}
	})
	cookie := f.login(t)
	// A token reaches the store only with the right shape and checksum. A
	// failing store cannot issue one, so a working fixture does.
	g := newBearerFixture(t, true, nil)
	token, _ := g.issue(t)

	got, ok, err := f.manager.Read(context.Background(), request(cookie, "Bearer "+token))
	if ok || got.Subject != "" || !errors.Is(err, errStore) {
		t.Errorf("Read = %q ok %v err %v, want no session and the store error", got.Subject, ok, err)
	}
	if errors.Is(err, session.ErrDeviceToken) {
		t.Error("an outage is reported as a token failure")
	}
}

// serve runs a request through the middleware and reports the response and
// what the handler saw.
func serve(m *session.Manager, req *http.Request) (*httptest.ResponseRecorder, bool, session.Session, error) {
	var (
		reached bool
		seen    session.Session
		seenErr error
	)
	rec := httptest.NewRecorder()
	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		seen, _ = session.FromContext(r.Context())
		seenErr = session.ErrorFromContext(r.Context())
		w.WriteHeader(http.StatusTeapot)
	})).ServeHTTP(rec, req)
	return rec, reached, seen, seenErr
}

func TestMiddlewareRefusesAFailedBearerRequest(t *testing.T) {
	// RFC 6750 section 3.1: a request without a token gets the bare
	// challenge, a token that fails gets invalid_token.
	const invalid = `Bearer error="invalid_token"`
	cases := map[string]struct {
		auth      []string
		challenge string
	}{
		"a junk token":           {[]string{"Bearer " + junkToken}, invalid},
		"bearer without a token": {[]string{"Bearer"}, "Bearer"},
		"bearer and spaces only": {[]string{"Bearer   "}, "Bearer"},
		"two headers":            {[]string{"Bearer a", "Bearer b"}, invalid},
		"two basic headers":      {[]string{"Basic a", "Basic b"}, invalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newBearerFixture(t, true, nil)
			cookie := f.login(t)
			before := f.cookies.count()

			rec, reached, _, _ := serve(f.manager, request(cookie, tc.auth...))
			if reached {
				t.Error("the handler ran for a bearer request whose token failed")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != tc.challenge {
				t.Errorf("WWW-Authenticate = %q, want %q", got, tc.challenge)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
				t.Errorf("Set-Cookie = %q, want none: the cookie is ignored, not cleared", got)
			}
			if f.cookies.count() != before {
				t.Error("the middleware asked the cookie store on a bearer request")
			}
		})
	}
}

func TestMiddlewarePassesAValidBearerRequest(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	cookie := f.login(t)
	token, device := f.issue(t)

	rec, reached, seen, _ := serve(f.manager, request(cookie, "Bearer "+token))
	if !reached || rec.Code != http.StatusTeapot {
		t.Fatalf("reached %v status %d, want the handler", reached, rec.Code)
	}
	if seen.Subject != "app-user" || seen.Credential != session.CredentialDevice || seen.DeviceID != device.ID {
		t.Errorf("context = %q %v %q, want app-user by device %q", seen.Subject, seen.Credential, seen.DeviceID, device.ID)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on every bearer response", got)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q, want none on success", got)
	}
}

func TestMiddlewareLeavesACookieRequestAlone(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	cookie := f.login(t)

	for name, req := range map[string]*http.Request{
		"a cookie":           request(cookie),
		"a cookie and basic": request(cookie, "Basic dXNlcjpwYXNz"),
		"nothing":            request(nil),
	} {
		t.Run(name, func(t *testing.T) {
			rec, reached, seen, _ := serve(f.manager, req)
			if !reached || rec.Code != http.StatusTeapot {
				t.Fatalf("reached %v status %d, want the handler", reached, rec.Code)
			}
			if req.Header.Get("Cookie") != "" && (seen.Subject != "web-user" || seen.Credential != session.CredentialCookie) {
				t.Errorf("context = %q %v, want web-user by cookie", seen.Subject, seen.Credential)
			}
			if got := rec.Header().Get("Cache-Control"); got != "" {
				t.Errorf("Cache-Control = %q, want none set by the middleware on a cookie request", got)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("WWW-Authenticate = %q, want none", got)
			}
		})
	}
}

// TestMiddlewarePassesADeviceStoreOutage keeps the outage apart from a token
// failure: the handler answers it, as for a cookie store outage.
func TestMiddlewarePassesADeviceStoreOutage(t *testing.T) {
	g := newBearerFixture(t, true, nil)
	token, _ := g.issue(t)
	f := newBearerFixture(t, false, func(cfg *session.Config) {
		cfg.Devices = session.DeviceConfig{Store: failingDeviceStore{}}
	})

	rec, reached, seen, seenErr := serve(f.manager, request(nil, "Bearer "+token))
	if !reached || !errors.Is(seenErr, errStore) || seen.Subject != "" {
		t.Errorf("reached %v err %v subject %q, want the handler with the outage and no session", reached, seenErr, seen.Subject)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestOnBearerRefusalAnswersTheRefusal(t *testing.T) {
	var called int
	f := newBearerFixture(t, true, func(cfg *session.Config) {
		cfg.OnBearerRefusal = func(w http.ResponseWriter, _ *http.Request) {
			called++
			if w.Header().Get("WWW-Authenticate") != `Bearer error="invalid_token"` || w.Header().Get("Cache-Control") != "no-store" {
				t.Error("the headers are not set before the refusal is answered")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
		}
	})

	rec, reached, _, _ := serve(f.manager, request(nil, "Bearer "+junkToken))
	if reached || called != 1 || rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("reached %v called %d status %d type %q, want the custom refusal",
			reached, called, rec.Code, rec.Header().Get("Content-Type"))
	}
}

// TestANoOpRefusalIsStill401 pins that a refusal callback which writes
// nothing cannot turn the refusal into an empty 200.
func TestANoOpRefusalIsStill401(t *testing.T) {
	for name, refuse := range map[string]func(http.ResponseWriter, *http.Request){
		"writes nothing":       func(http.ResponseWriter, *http.Request) {},
		"only sets a header":   func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("X-Example", "1") },
		"drops the challenge":  func(w http.ResponseWriter, _ *http.Request) { w.Header().Del("WWW-Authenticate") },
		"drops the cache rule": func(w http.ResponseWriter, _ *http.Request) { w.Header().Del("Cache-Control") },
		"answers 200":          func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
		"writes a body only":   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) },
		"drops both and writes": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Del("WWW-Authenticate")
			w.Header().Del("Cache-Control")
			w.WriteHeader(http.StatusUnauthorized)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBearerFixture(t, true, func(cfg *session.Config) { cfg.OnBearerRefusal = refuse })
			rec, reached, _, _ := serve(f.manager, request(nil, "Bearer "+junkToken))
			if reached {
				t.Error("the handler ran")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer error="invalid_token"` {
				t.Errorf("WWW-Authenticate = %q", got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

// TestWithoutADeviceStoreABearerRequestIsAnonymous is the escape hatch for a
// product whose own gateway or JWT uses the Authorization header: no 401, no
// session, and the cookie is still never read.
func TestWithoutADeviceStoreABearerRequestIsAnonymous(t *testing.T) {
	for name, auth := range map[string][]string{
		"a gateway token": {"Bearer an-api-gateway-jwt"},
		"no token":        {"Bearer"},
		"two headers":     {"Bearer a", "Bearer b"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBearerFixture(t, false, nil)
			cookie := f.login(t)
			before := f.cookies.count()

			rec, reached, seen, seenErr := serve(f.manager, request(cookie, auth...))
			if !reached || rec.Code != http.StatusTeapot {
				t.Fatalf("reached %v status %d, want the handler", reached, rec.Code)
			}
			if seen.Subject != "" || seen.Credential != session.CredentialNone || seenErr != nil {
				t.Errorf("context = %q %v %v, want anonymous", seen.Subject, seen.Credential, seenErr)
			}
			if f.cookies.count() != before {
				t.Error("the cookie store was asked on a bearer request")
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("WWW-Authenticate = %q, want none", got)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
				t.Errorf("Set-Cookie = %q, want none", got)
			}
		})
	}
}

// TestNoStoreWinsOverTheHandler pins that no handler behind the middleware
// can make a response to a bearer request cacheable, whichever way it
// writes, while a cookie response keeps the handler's own header.
func TestNoStoreWinsOverTheHandler(t *testing.T) {
	const immutable = "public, max-age=31536000, immutable"
	writes := map[string]http.HandlerFunc{
		"WriteHeader": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", immutable)
			w.WriteHeader(http.StatusOK)
		},
		"Write only": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", immutable)
			_, _ = w.Write([]byte("body"))
		},
		"Flush": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", immutable)
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("Flush: %v", err)
			}
		},
		"nothing written": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", immutable)
		},
	}
	for name, handler := range writes {
		t.Run(name, func(t *testing.T) {
			f := newBearerFixture(t, true, nil)
			token, _ := f.issue(t)
			cookie := f.login(t)

			rec := httptest.NewRecorder()
			f.manager.Middleware(handler).ServeHTTP(rec, request(nil, "Bearer "+token))
			if got := rec.Result().Header.Get("Cache-Control"); got != "no-store" {
				t.Errorf("bearer Cache-Control = %q, want no-store", got)
			}

			rec = httptest.NewRecorder()
			f.manager.Middleware(handler).ServeHTTP(rec, request(cookie))
			if got := rec.Result().Header.Get("Cache-Control"); got != immutable {
				t.Errorf("cookie Cache-Control = %q, want the handler's %q", got, immutable)
			}
		})
	}
}

// TestStartAndRotateRefuseABearerRequest pins that no handler behind the
// middleware mints a cookie session on a bearer request.
func TestStartAndRotateRefuseABearerRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		devices bool
		two     bool
	}{
		"with a device store":         {true, false},
		"without one":                 {false, false},
		"two headers without a store": {false, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBearerFixture(t, tc.devices, nil)
			auth := []string{"Bearer an-api-gateway-jwt"}
			if tc.devices {
				token, _ := f.issue(t)
				auth = []string{"Bearer " + token}
			}
			if tc.two {
				auth = append(auth, "Bearer another")
			}
			cookie := f.login(t)
			cookieSession, ok, err := f.manager.Read(context.Background(), request(cookie))
			if err != nil || !ok {
				t.Fatalf("Read: %v %v", ok, err)
			}
			sessionsBefore := f.cookies.Len()

			rec := httptest.NewRecorder()
			f.manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := f.manager.Start(r.Context(), w, session.Session{Subject: "u1"}); !errors.Is(err, session.ErrBearerRequest) {
					t.Errorf("Start = %v, want ErrBearerRequest", err)
				}
				if _, err := f.manager.Rotate(r.Context(), w, cookieSession); !errors.Is(err, session.ErrBearerRequest) {
					t.Errorf("Rotate = %v, want ErrBearerRequest", err)
				}
			})).ServeHTTP(rec, request(nil, auth...))
			if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
				t.Errorf("Set-Cookie = %q, want none", got)
			}
			if f.cookies.Len() != sessionsBefore {
				t.Errorf("cookie store holds %d sessions, want %d", f.cookies.Len(), sessionsBefore)
			}
		})
	}

	// A cookie request behind the middleware still starts a session.
	f := newBearerFixture(t, true, nil)
	rec := httptest.NewRecorder()
	f.manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := f.manager.Start(r.Context(), w, session.Session{Subject: "u1"}); err != nil {
			t.Errorf("Start on a cookie request = %v", err)
		}
	})).ServeHTTP(rec, request(nil))
	if len(rec.Header().Values("Set-Cookie")) != 1 {
		t.Error("Start on a cookie request set no cookie")
	}
}

// TestADeviceSessionHasNoGroups pins what Session.In documents: a device
// session carries no groups, so a deny list check passes it.
func TestADeviceSessionHasNoGroups(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	token, _ := f.issue(t)
	s, ok, err := f.manager.Read(context.Background(), request(nil, "Bearer "+token))
	if err != nil || !ok {
		t.Fatalf("Read: %v %v", ok, err)
	}
	if s.Groups != nil || s.In("admin") || s.Username != "" || s.Name != "" || s.Email != "" || s.Data != nil {
		t.Errorf("device session = %+v, want no groups and no account attributes", s)
	}
}

// TestEndReportsAFailedRevoke pins that a sign out the store did not carry
// out is not reported as done.
func TestEndReportsAFailedRevoke(t *testing.T) {
	h := newHooked(t)
	token := h.issue(t)
	h.store.revoke = func(context.Context, string, string) error { return errStore }

	err := h.manager.End(context.Background(), httptest.NewRecorder(), request(nil, "Bearer "+token))
	if !errors.Is(err, errStore) {
		t.Errorf("End = %v, want the store error", err)
	}
}

// TestRotateRefusesEitherDeviceMarker pins both halves of the Rotate guard.
func TestRotateRefusesEitherDeviceMarker(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	s, ok, err := f.manager.Read(context.Background(), request(f.login(t)))
	if err != nil || !ok {
		t.Fatalf("Read: %v %v", ok, err)
	}
	for name, mark := range map[string]func(*session.Session){
		"credential only": func(s *session.Session) { s.Credential = session.CredentialDevice },
		"device id only":  func(s *session.Session) { s.DeviceID = "a-device" },
	} {
		t.Run(name, func(t *testing.T) {
			marked := s
			mark(&marked)
			rec := httptest.NewRecorder()
			if _, err := f.manager.Rotate(context.Background(), rec, marked); err == nil {
				t.Error("Rotate = nil, want a refusal")
			}
			if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
				t.Errorf("Set-Cookie = %q, want none", got)
			}
		})
	}
}

// deviceIDStore returns every session with a DeviceID set, as a store that
// persisted one would.
type deviceIDStore struct{ *session.MemoryStore }

func (s deviceIDStore) Get(ctx context.Context, id string) (session.Session, error) {
	got, err := s.MemoryStore.Get(ctx, id)
	got.DeviceID = "a-stored-device-id"
	got.Credential = session.CredentialDevice
	return got, err
}

// TestACookieSessionNeverCarriesADeviceID pins that what a store holds does
// not decide the credential of a cookie session.
func TestACookieSessionNeverCarriesADeviceID(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) {
		cfg.Store = deviceIDStore{session.NewMemoryStore(cfg.Now, 100)}
	})
	_, req := f.login(t, "u1")
	s, ok := f.read(t, req)
	if !ok || s.DeviceID != "" || s.Credential != session.CredentialCookie {
		t.Errorf("Read = ok %v device %q credential %v, want a plain cookie session", ok, s.DeviceID, s.Credential)
	}
}

func TestEndSignsTheDeviceOutAndLeavesTheCookie(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	cookie := f.login(t)
	token, _ := f.issue(t)

	rec := httptest.NewRecorder()
	if err := f.manager.End(context.Background(), rec, request(cookie, "Bearer "+token)); err != nil {
		t.Fatalf("End: %v", err)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("Set-Cookie = %q, want none", got)
	}
	if _, err := f.manager.AuthenticateDevice(context.Background(), token); !errors.Is(err, session.ErrDeviceToken) {
		t.Errorf("AuthenticateDevice after End = %v, want ErrDeviceToken", err)
	}
	if _, ok, _ := f.manager.Read(context.Background(), request(cookie)); !ok {
		t.Error("End on a bearer request ended the cookie session")
	}
}

func TestEndRefusesAFailedBearerRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		devices bool
		auth    []string
		want    error
	}{
		"a junk token":            {true, []string{"Bearer " + junkToken}, session.ErrDeviceToken},
		"two headers":             {true, []string{"Bearer a", "Bearer b"}, session.ErrDeviceToken},
		"without a device store":  {false, []string{"Bearer " + junkToken}, session.ErrDevicesDisabled},
		"two without a store":     {false, []string{"Bearer a", "Bearer b"}, session.ErrDevicesDisabled},
		"bearer without a token":  {true, []string{"Bearer"}, session.ErrDeviceToken},
		"two headers, both basic": {true, []string{"Basic a", "Basic b"}, session.ErrDeviceToken},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBearerFixture(t, tc.devices, nil)
			cookie := f.login(t)
			before := f.cookies.count()

			rec := httptest.NewRecorder()
			err := f.manager.End(context.Background(), rec, request(cookie, tc.auth...))
			if !errors.Is(err, tc.want) {
				t.Errorf("End = %v, want %v", err, tc.want)
			}
			if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
				t.Errorf("Set-Cookie = %q, want none", got)
			}
			if f.cookies.count() != before {
				t.Error("End asked the cookie store on a bearer request")
			}
			if _, ok, _ := f.manager.Read(context.Background(), request(cookie)); !ok {
				t.Error("the cookie session is gone")
			}
		})
	}
}

func TestRotateRefusesADeviceSession(t *testing.T) {
	f := newBearerFixture(t, true, nil)
	token, _ := f.issue(t)
	s, ok, err := f.manager.Read(context.Background(), request(nil, "Bearer "+token))
	if err != nil || !ok {
		t.Fatalf("Read = ok %v err %v", ok, err)
	}

	rec := httptest.NewRecorder()
	if _, err := f.manager.Rotate(context.Background(), rec, s); err == nil {
		t.Fatal("Rotate of a device session = nil, want an error: it would mint a cookie session from a token")
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("Set-Cookie = %q, want none", got)
	}
	if n := f.cookies.Len(); n != 0 {
		t.Errorf("cookie store holds %d sessions, want 0", n)
	}
}

func TestReadLoginCookieIgnoresTheCookieOnABearerRequest(t *testing.T) {
	f := newLoginFixture(t, nil)
	set := f.set(t, inFlightLogin{State: "s"})
	cookie := onlyCookie(t, set)

	for name, auth := range map[string][]string{
		"bearer":      {"Bearer " + junkToken},
		"two headers": {"Bearer a", "Bearer b"},
	} {
		t.Run(name, func(t *testing.T) {
			req := request(cookie, auth...)
			var got inFlightLogin
			rec, err := f.read(req, &got)
			if !errors.Is(err, session.ErrInvalid) {
				t.Errorf("ReadLoginCookie = %v, want ErrInvalid", err)
			}
			if got != (inFlightLogin{}) {
				t.Errorf("decoded %+v, want nothing", got)
			}
			if v := rec.Header().Values("Set-Cookie"); len(v) != 0 {
				t.Errorf("Set-Cookie = %q, want none on a bearer request", v)
			}
		})
	}

	// The same cookie still works on a cookie request.
	var got inFlightLogin
	if _, err := f.read(request(cookie), &got); err != nil || got.State != "s" {
		t.Errorf("ReadLoginCookie on a cookie request = %+v, %v", got, err)
	}
}

func TestCredentialString(t *testing.T) {
	for c, want := range map[session.Credential]string{
		session.CredentialNone:   "none",
		session.CredentialCookie: "cookie",
		session.CredentialDevice: "device",
		session.Credential(42):   "unknown",
	} {
		if got := c.String(); got != want {
			t.Errorf("Credential(%d) = %q, want %q", int(c), got, want)
		}
	}
	var zero session.Credential
	if zero != session.CredentialNone {
		t.Error("the zero Credential is not CredentialNone")
	}
}
