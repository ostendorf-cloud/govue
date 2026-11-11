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

// start is the clock every fixture begins at.
var start = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

// fixture is a manager on a memory store with a clock the test moves by hand,
// so nothing here sleeps.
type fixture struct {
	manager *session.Manager
	store   *session.MemoryStore
	now     time.Time
}

func newFixture(t *testing.T, adjust func(*session.Config)) *fixture {
	t.Helper()

	f := &fixture{now: start}
	clock := func() time.Time { return f.now }
	f.store = session.NewMemoryStore(clock)

	cfg := session.Config{Store: f.store, Now: clock}
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

// login starts a session and returns it together with a request carrying the
// cookie the browser would send back.
func (f *fixture) login(t *testing.T, subject string) (session.Session, *http.Request) {
	t.Helper()

	rec := httptest.NewRecorder()
	started, err := f.manager.Start(context.Background(), rec, session.Session{Subject: subject, Username: subject})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return started, withCookies(rec)
}

// read is Read with the store error asserted away, for the cases that are about
// whether there is a session rather than about an outage.
func (f *fixture) read(t *testing.T, req *http.Request) (session.Session, bool) {
	t.Helper()

	got, ok, err := f.manager.Read(context.Background(), req)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got, ok
}

// withCookies turns the Set-Cookie headers of a response into the request a
// browser would make next.
func withCookies(rec *httptest.ResponseRecorder) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	for _, cookie := range rec.Result().Cookies() {
		req.AddCookie(cookie)
	}
	return req
}

func TestNewRequiresAStore(t *testing.T) {
	_, err := session.New(session.Config{})
	if err == nil {
		t.Fatal("an empty config was accepted")
	}
	if !strings.Contains(err.Error(), "store") {
		t.Errorf("error %q does not name the missing store", err)
	}
}

func TestNewRejectsAnUnworkableConfig(t *testing.T) {
	cases := map[string]session.Config{
		"negative idle":            {Idle: -time.Minute},
		"negative lifetime":        {Lifetime: -time.Hour},
		"negative touch interval":  {TouchEvery: -time.Second},
		"touch interval over idle": {Idle: time.Minute, TouchEvery: 2 * time.Minute},
		"touch interval at idle":   {Idle: time.Minute, TouchEvery: time.Minute},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			cfg.Store = session.NewMemoryStore(nil)
			if _, err := session.New(cfg); err == nil {
				t.Error("the config was accepted")
			}
		})
	}
}

func TestNewFillsInTheDefaults(t *testing.T) {
	f := newFixture(t, nil)

	if got := f.manager.CookieName(); got != session.DefaultCookieName {
		t.Errorf("cookie name = %q, want %q", got, session.DefaultCookieName)
	}

	started, _ := f.login(t, "u1")
	if want := start.Add(session.DefaultLifetime); !started.ExpiresAt.Equal(want) {
		t.Errorf("expiry = %s, want the default lifetime %s", started.ExpiresAt, want)
	}
}

func TestStartSetsAHostPrefixedCookie(t *testing.T) {
	f := newFixture(t, nil)

	rec := httptest.NewRecorder()
	started, err := f.manager.Start(context.Background(), rec, session.Session{Subject: "u1", Username: "simon"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%d cookies, want 1", len(cookies))
	}
	cookie := cookies[0]

	if cookie.Name != session.DefaultCookieName {
		t.Errorf("name = %q, want %q", cookie.Name, session.DefaultCookieName)
	}
	if !strings.HasPrefix(cookie.Name, "__Host-") {
		t.Errorf("name = %q, want the __Host- prefix", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Error("the cookie is readable from JavaScript")
	}
	if !cookie.Secure {
		t.Error("the cookie is sent over plain HTTP")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, want /", cookie.Path)
	}
	if cookie.Domain != "" {
		t.Errorf("Domain = %q, want none, which the __Host- prefix requires", cookie.Domain)
	}
	// Non persistent by default: the browser drops it when it closes.
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Errorf("the cookie is persistent: MaxAge = %d, Expires = %s", cookie.MaxAge, cookie.Expires)
	}

	if cookie.Value != started.ID || started.ID == "" {
		t.Errorf("cookie value %q and session id %q differ", cookie.Value, started.ID)
	}
	// Nothing but the id travels in the cookie.
	for _, leak := range []string{"simon", "u1"} {
		if strings.Contains(cookie.Value, leak) {
			t.Errorf("the cookie carries session content %q: %q", leak, cookie.Value)
		}
	}
}

func TestStartOverwritesTheCallersTimestamps(t *testing.T) {
	f := newFixture(t, nil)
	bogus := start.Add(-100 * time.Hour)

	rec := httptest.NewRecorder()
	started, err := f.manager.Start(context.Background(), rec, session.Session{
		ID: "chosen-by-the-caller", Subject: "u1",
		IssuedAt: bogus, LastSeen: bogus, ExpiresAt: start.Add(100 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if started.ID == "chosen-by-the-caller" {
		t.Error("the caller's id was kept, so a fixed session would survive the login")
	}
	if !started.IssuedAt.Equal(start) || !started.LastSeen.Equal(start) {
		t.Errorf("timestamps = %s / %s, want the clock", started.IssuedAt, started.LastSeen)
	}
	if want := start.Add(session.DefaultLifetime); !started.ExpiresAt.Equal(want) {
		t.Errorf("expiry = %s, want %s", started.ExpiresAt, want)
	}
}

func TestEveryLoginGetsANewID(t *testing.T) {
	f := newFixture(t, nil)

	seen := map[string]bool{}
	for range 50 {
		started, _ := f.login(t, "u1")
		if seen[started.ID] {
			t.Fatalf("id %q was handed out twice", started.ID)
		}
		seen[started.ID] = true
	}
}

func TestCookieNameOnPlainHTTPDropsTheHostPrefix(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) { cfg.AllowPlainHTTP = true })

	// A browser refuses a __Host- cookie without Secure, so the prefix goes
	// with the attribute rather than leaving a development run with no session
	// at all.
	if got := f.manager.CookieName(); got != "session" {
		t.Errorf("cookie name = %q, want %q", got, "session")
	}

	rec := httptest.NewRecorder()
	if _, err := f.manager.Start(context.Background(), rec, session.Session{Subject: "u1"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != "session" || cookie.Secure {
		t.Errorf("cookie = %+v, want the name %q and Secure off", cookie, "session")
	}
	// Everything else stays as strict as it was.
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" {
		t.Errorf("a plain HTTP run weakened more than Secure: %+v", cookie)
	}
}

func TestPlainHTTPAlsoDropsThePrefixFromACustomName(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) {
		cfg.CookieName = "__Host-app"
		cfg.AllowPlainHTTP = true
	})

	if got := f.manager.CookieName(); got != "app" {
		t.Errorf("cookie name = %q, want %q", got, "app")
	}
}

func TestCustomCookieNameIsKept(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) { cfg.CookieName = "__Host-app" })

	if got := f.manager.CookieName(); got != "__Host-app" {
		t.Errorf("cookie name = %q, want it kept", got)
	}
}

func TestSameSiteStrict(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) { cfg.SameSiteStrict = true })

	rec := httptest.NewRecorder()
	if _, err := f.manager.Start(context.Background(), rec, session.Session{Subject: "u1"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := rec.Result().Cookies()[0].SameSite; got != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", got)
	}
}

func TestPersistentCookieIsBoundedByTheLifetime(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) {
		cfg.Persistent = true
		cfg.Lifetime = 3 * time.Hour
	})

	rec := httptest.NewRecorder()
	if _, err := f.manager.Start(context.Background(), rec, session.Session{Subject: "u1"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := rec.Result().Cookies()[0].MaxAge; got != int((3 * time.Hour).Seconds()) {
		t.Errorf("MaxAge = %d, want the lifetime in seconds", got)
	}
}

func TestReadReturnsTheStoredSession(t *testing.T) {
	f := newFixture(t, nil)
	started, req := f.login(t, "u1")

	got, ok := f.read(t, req)
	if !ok {
		t.Fatal("the session was not found")
	}
	if got.ID != started.ID || got.Subject != "u1" || got.Username != "u1" {
		t.Errorf("session = %+v", got)
	}
}

func TestReadWithoutASession(t *testing.T) {
	f := newFixture(t, nil)
	started, _ := f.login(t, "u1")

	cases := map[string]*http.Cookie{
		"no cookie at all": nil,
		"unknown id":       {Name: session.DefaultCookieName, Value: "made-up"},
		"empty value":      {Name: session.DefaultCookieName, Value: ""},
		// The id is real, the name is not the configured one.
		"another cookie name": {Name: "session", Value: started.ID},
	}
	for name, cookie := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if cookie != nil {
				req.AddCookie(cookie)
			}
			if _, ok := f.read(t, req); ok {
				t.Error("Read reports a session")
			}
		})
	}
}

func TestReadFailsClosedWhenTheStoreIsUnreachable(t *testing.T) {
	manager, err := session.New(session.Config{Store: failingStore{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: session.DefaultCookieName, Value: "any-id"})

	got, ok, err := manager.Read(context.Background(), req)
	if ok {
		t.Error("a store that cannot be reached produced a session")
	}
	if got.ID != "" {
		t.Errorf("session = %+v, want the zero value", got)
	}
	// Fail closed, and still say why, so the caller can answer 503 instead of
	// sending the user to the identity provider during an outage.
	if !errors.Is(err, errStore) {
		t.Errorf("Read error = %v, want the store's own error", err)
	}
}

func TestReadReportsNoErrorForAnOrdinaryLoggedOutRequest(t *testing.T) {
	f := newFixture(t, nil)

	cases := map[string]*http.Cookie{
		"no cookie at all": nil,
		"unknown id":       {Name: session.DefaultCookieName, Value: "made-up"},
	}
	for name, cookie := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if cookie != nil {
				req.AddCookie(cookie)
			}
			// No session is normal, so it is not an error: only a store that
			// could not answer is.
			_, ok, err := f.manager.Read(context.Background(), req)
			if ok {
				t.Error("Read reports a session")
			}
			if err != nil {
				t.Errorf("Read error = %v, want none", err)
			}
		})
	}
}

func TestReadReportsNoErrorForAnExpiredSession(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) { cfg.Idle = 30 * time.Minute })
	_, req := f.login(t, "u1")

	f.now = f.now.Add(time.Hour)
	_, ok, err := f.manager.Read(context.Background(), req)
	if ok {
		t.Error("the session survived the idle timeout")
	}
	// An expired session is a logged out request, not an outage.
	if err != nil {
		t.Errorf("Read error = %v, want none", err)
	}
}

func TestIdleTimeout(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) {
		cfg.Idle = 30 * time.Minute
		cfg.TouchEvery = time.Minute
	})
	_, req := f.login(t, "u1")

	f.now = f.now.Add(29 * time.Minute)
	if _, ok := f.read(t, req); !ok {
		t.Fatal("the session ended before the idle timeout")
	}

	// That read touched it, so the clock has to move the whole way again.
	f.now = f.now.Add(31 * time.Minute)
	if _, ok := f.read(t, req); ok {
		t.Error("the session survived the idle timeout")
	}
	if f.store.Len() != 0 {
		t.Error("the idle session was left in the store")
	}
}

func TestAbsoluteLifetimeEndsAnActiveSession(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) {
		cfg.Idle = time.Hour
		cfg.Lifetime = 3 * time.Hour
	})
	_, req := f.login(t, "u1")

	// Used every half hour, so the idle timeout never gets near firing.
	for range 5 {
		f.now = f.now.Add(30 * time.Minute)
		if _, ok := f.read(t, req); !ok {
			t.Fatalf("the session ended early, at %s", f.now)
		}
	}

	f.now = f.now.Add(31 * time.Minute)
	if _, ok := f.read(t, req); ok {
		t.Error("the session outlived its absolute lifetime")
	}
}

func TestBothTimeoutsAreExclusiveAtTheBoundary(t *testing.T) {
	cases := map[string]struct {
		elapsed time.Duration
		want    bool
	}{
		"just inside the idle timeout":     {29 * time.Minute, true},
		"exactly at the idle timeout":      {30 * time.Minute, false},
		"exactly at the absolute lifetime": {time.Hour, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, func(cfg *session.Config) {
				cfg.Idle = 30 * time.Minute
				cfg.Lifetime = time.Hour
			})
			_, req := f.login(t, "u1")

			f.now = f.now.Add(tc.elapsed)
			if _, ok := f.read(t, req); ok != tc.want {
				t.Errorf("after %s the session is %t, want %t", tc.elapsed, ok, tc.want)
			}
		})
	}
}

func TestReadTouchesAtMostEveryTouchEvery(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) {
		cfg.Idle = time.Hour
		cfg.TouchEvery = 5 * time.Minute
	})
	started, req := f.login(t, "u1")

	// Inside the interval the read is a read: nothing is written back.
	f.now = f.now.Add(time.Minute)
	if _, ok := f.read(t, req); !ok {
		t.Fatal("the session was lost")
	}
	if got := f.lastSeen(t, started.ID); !got.Equal(started.LastSeen) {
		t.Errorf("last use = %s, want it untouched inside the interval", got)
	}

	// Past it, the last use is written back once.
	f.now = f.now.Add(5 * time.Minute)
	touched, ok := f.read(t, req)
	if !ok {
		t.Fatal("the session was lost")
	}
	if got := f.lastSeen(t, started.ID); !got.Equal(f.now) {
		t.Errorf("last use = %s, want the current time %s", got, f.now)
	}
	if !touched.LastSeen.Equal(f.now) {
		t.Errorf("the returned session carries the stale last use %s", touched.LastSeen)
	}
}

// lastSeen is the last use the store holds for the id.
func (f *fixture) lastSeen(t *testing.T, id string) time.Time {
	t.Helper()

	stored, err := f.store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %q: %v", id, err)
	}
	return stored.LastSeen
}

func TestReadSurvivesAStoreThatCannotBeWritten(t *testing.T) {
	store := &readOnlyStore{inner: session.NewMemoryStore(func() time.Time { return start })}
	manager, err := session.New(session.Config{
		Store: store, Idle: time.Hour, TouchEvery: time.Minute,
		Now: func() time.Time { return start.Add(10 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stored := session.Session{
		ID: "s1", Subject: "u1",
		IssuedAt: start, LastSeen: start, ExpiresAt: start.Add(time.Hour),
	}
	if err := store.inner.Save(context.Background(), stored); err != nil {
		t.Fatalf("Save: %v", err)
	}
	store.refuse = true

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: session.DefaultCookieName, Value: "s1"})

	// The touch fails. The session is still valid, so the request is not.
	got, ok, err := manager.Read(context.Background(), req)
	if !ok {
		t.Fatal("a failed touch ended a valid session")
	}
	// A valid session is never reported as an outage: true always comes with a
	// nil error, whatever the write back did.
	if err != nil {
		t.Errorf("Read error = %v, want none for a valid session", err)
	}
	if !got.LastSeen.Equal(start) {
		t.Errorf("last use = %s, want the stored one when the write failed", got.LastSeen)
	}
}

func TestRotateIssuesANewID(t *testing.T) {
	f := newFixture(t, nil)
	started, req := f.login(t, "u1")

	f.now = f.now.Add(time.Minute)
	rec := httptest.NewRecorder()
	rotated, err := f.manager.Rotate(context.Background(), rec, started)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	if rotated.ID == started.ID {
		t.Error("the id did not change, so a fixed session would survive the privilege change")
	}
	if _, err := f.store.Get(context.Background(), started.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("the previous id is still in the store: %v", err)
	}
	if _, ok := f.read(t, req); ok {
		t.Error("the old cookie still works")
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != rotated.ID {
		t.Errorf("cookies = %+v, want the new id", cookies)
	}
	if _, ok := f.read(t, withCookies(rec)); !ok {
		t.Error("the new cookie does not work")
	}
}

func TestRotateRefusesASessionTheStoreNoLongerHolds(t *testing.T) {
	f := newFixture(t, nil)
	// The value a handler read a moment ago, before the account was disabled.
	stale, _ := f.login(t, "u1")

	if err := f.manager.EndAll(context.Background(), "u1"); err != nil {
		t.Fatalf("EndAll: %v", err)
	}

	rec := httptest.NewRecorder()
	rotated, err := f.manager.Rotate(context.Background(), rec, stale)
	if !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Rotate = %v, want an error wrapping ErrNotFound", err)
	}
	if rotated.ID != "" {
		t.Errorf("session = %+v, want the zero value", rotated)
	}
	// Nothing was written, so the end-all stands.
	if f.store.Len() != 0 {
		t.Errorf("the store holds %d sessions, want none after the end-all", f.store.Len())
	}
	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("cookies = %+v, want none", cookies)
	}
	if _, ok := f.read(t, withCookies(rec)); ok {
		t.Error("the ended session came back")
	}
}

func TestRotateRefusesAnExpiredSession(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) {
		cfg.Idle = time.Hour
		cfg.Lifetime = time.Hour
	})
	started, _ := f.login(t, "u1")

	// At the absolute expiry the store no longer hands the session out, so a
	// rotation cannot write it back either.
	f.now = f.now.Add(time.Hour)
	if _, err := f.manager.Rotate(context.Background(), httptest.NewRecorder(), started); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Rotate = %v, want an error wrapping ErrNotFound", err)
	}
}

func TestRotateSetsTheCookieOnlyOnceTheOldIDIsGone(t *testing.T) {
	clock := func() time.Time { return start }
	store := &undeletableStore{MemoryStore: session.NewMemoryStore(clock)}
	manager, err := session.New(session.Config{Store: store, Now: clock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	started, err := manager.Start(context.Background(), httptest.NewRecorder(), session.Session{Subject: "u1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	store.refuseDelete = true

	rec := httptest.NewRecorder()
	rotated, err := manager.Rotate(context.Background(), rec, started)
	if err == nil {
		t.Fatal("a rotation whose delete failed reported success")
	}

	// Nothing is promised to the client while two ids are still valid, so the
	// caller can fail the request instead of having handed out a credential it
	// cannot withdraw.
	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("cookies = %+v, want none after a failed rotation", cookies)
	}
	// The client keeps the cookie it already had, and that session is intact.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: session.DefaultCookieName, Value: started.ID})
	if _, ok, err := manager.Read(context.Background(), req); !ok || err != nil {
		t.Errorf("the failed rotation left the caller without a session: %v", err)
	}
	// The new id is in the store, which is what saving first buys, and the
	// caller has it in the returned session to clean up or retry with.
	if rotated.ID == started.ID {
		t.Error("the returned session does not carry the new id")
	}
}

func TestRotateDoesNotExtendTheLifetime(t *testing.T) {
	f := newFixture(t, func(cfg *session.Config) {
		cfg.Idle = time.Hour
		cfg.Lifetime = 2 * time.Hour
	})
	started, _ := f.login(t, "u1")

	// Rotating every half hour would reset the absolute expiry if the expiry
	// were recomputed, and the session would then never end.
	current := started
	for range 3 {
		f.now = f.now.Add(30 * time.Minute)
		rec := httptest.NewRecorder()
		rotated, err := f.manager.Rotate(context.Background(), rec, current)
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		if !rotated.ExpiresAt.Equal(started.ExpiresAt) {
			t.Fatalf("expiry moved from %s to %s", started.ExpiresAt, rotated.ExpiresAt)
		}
		if !rotated.IssuedAt.Equal(started.IssuedAt) {
			t.Fatalf("the issue time moved to %s", rotated.IssuedAt)
		}
		current = rotated

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: session.DefaultCookieName, Value: rotated.ID})
		if _, ok := f.read(t, req); !ok {
			t.Fatalf("at %s the session had already ended", f.now)
		}
	}

	// Two hours in, the session is over however often it was rotated.
	f.now = f.now.Add(30 * time.Minute)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: session.DefaultCookieName, Value: current.ID})
	if _, ok := f.read(t, req); ok {
		t.Errorf("at %s the session outlived its lifetime", f.now)
	}
}

func TestRotateKeepsTheSessionContent(t *testing.T) {
	f := newFixture(t, nil)
	started, _ := f.login(t, "u1")
	started.Groups = []string{"admins"}
	started.Data = map[string]string{"tenant": "7"}

	rotated, err := f.manager.Rotate(context.Background(), httptest.NewRecorder(), started)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !rotated.In("admins") || rotated.Data["tenant"] != "7" {
		t.Errorf("session = %+v, want the content carried over", rotated)
	}
}

func TestEnd(t *testing.T) {
	f := newFixture(t, nil)
	started, req := f.login(t, "u1")

	rec := httptest.NewRecorder()
	if err := f.manager.End(context.Background(), rec, req); err != nil {
		t.Fatalf("End: %v", err)
	}

	if _, err := f.store.Get(context.Background(), started.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("the session is still in the store after the logout: %v", err)
	}
	if _, ok := f.read(t, req); ok {
		t.Error("the cookie still works after the logout")
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%d cookies, want 1", len(cookies))
	}
	cleared := cookies[0]
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the cookie was not cleared: %+v", cleared)
	}
	// A browser matches a deletion on the attributes, so they have to be the
	// ones the cookie was set with.
	if cleared.Name != session.DefaultCookieName || cleared.Path != "/" ||
		!cleared.HttpOnly || !cleared.Secure || cleared.SameSite != http.SameSiteLaxMode ||
		cleared.Domain != "" {
		t.Errorf("the cleared cookie's attributes differ from the set one's: %+v", cleared)
	}
}

func TestEndWithoutASessionIsNotAnError(t *testing.T) {
	f := newFixture(t, nil)

	rec := httptest.NewRecorder()
	if err := f.manager.End(context.Background(), rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/logout", nil)); err != nil {
		t.Errorf("End = %v", err)
	}
	// The cookie is cleared anyway, so a logout always leaves nothing behind.
	if cookies := rec.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Errorf("cookies = %+v, want one cleared cookie", cookies)
	}
}

func TestEndAll(t *testing.T) {
	f := newFixture(t, nil)
	_, first := f.login(t, "u1")
	_, second := f.login(t, "u1")
	_, other := f.login(t, "u2")

	if err := f.manager.EndAll(context.Background(), "u1"); err != nil {
		t.Fatalf("EndAll: %v", err)
	}
	for i, req := range []*http.Request{first, second} {
		if _, ok := f.read(t, req); ok {
			t.Errorf("session %d of the account still works", i)
		}
	}
	if _, ok := f.read(t, other); !ok {
		t.Error("another account's session was ended too")
	}
}

func TestEndAllRefusesAnEmptySubject(t *testing.T) {
	f := newFixture(t, nil)
	_, req := f.login(t, "u1")

	// A session saved without a subject would otherwise make the empty string
	// a key that ends sessions.
	if err := f.manager.EndAll(context.Background(), ""); err == nil {
		t.Error("an empty subject was accepted")
	}
	if _, ok := f.read(t, req); !ok {
		t.Error("the session was ended by an empty subject")
	}
}

func TestEndAllReportsAStoreFailure(t *testing.T) {
	manager, err := session.New(session.Config{Store: failingStore{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.EndAll(context.Background(), "u1"); err == nil {
		t.Error("a store failure was swallowed")
	}
}

func TestSessionIDIsAPlainStringForCSRF(t *testing.T) {
	f := newFixture(t, nil)
	first, _ := f.login(t, "u1")
	second, _ := f.login(t, "u1")

	// csrf.Token(key, sessionID) binds a token to this id without either
	// package importing the other, which only holds while the id is a plain
	// string and two sessions of one account have different ones.
	token := func(_ []byte, sessionID string) string { return sessionID }
	if token(nil, first.ID) == "" {
		t.Error("the session id is empty")
	}
	if first.ID == second.ID {
		t.Error("two sessions of one account share an id, so they would share a CSRF token")
	}
}

func TestSessionIn(t *testing.T) {
	s := session.Session{Groups: []string{"editors", "admins"}}

	if !s.In("admins") {
		t.Error("In does not find a group the session has")
	}
	if s.In("nobody") {
		t.Error("In finds a group the session does not have")
	}
	if (session.Session{}).In("admins") {
		t.Error("In finds a group on a session without any")
	}
}

func TestNewIDIsUnguessable(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id, err := session.NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		// 32 random bytes, base64url encoded without padding.
		if len(id) != 43 {
			t.Fatalf("id %q is %d characters, want 43", id, len(id))
		}
		if seen[id] {
			t.Fatalf("id %q was handed out twice", id)
		}
		seen[id] = true
	}
}

// failingStore fails every call, standing in for a store that cannot be
// reached.
type failingStore struct{}

var errStore = errors.New("store unreachable")

func (failingStore) Get(context.Context, string) (session.Session, error) {
	return session.Session{}, errStore
}
func (failingStore) Save(context.Context, session.Session) error { return errStore }
func (failingStore) Delete(context.Context, string) error        { return errStore }
func (failingStore) DeleteSubject(context.Context, string) error { return errStore }

// readOnlyStore refuses writes once refuse is set, so a failed touch can be
// tested without a real store outage.
type readOnlyStore struct {
	inner  *session.MemoryStore
	refuse bool
}

func (s *readOnlyStore) Get(ctx context.Context, id string) (session.Session, error) {
	return s.inner.Get(ctx, id)
}

func (s *readOnlyStore) Save(ctx context.Context, sess session.Session) error {
	if s.refuse {
		return errStore
	}
	return s.inner.Save(ctx, sess)
}

func (s *readOnlyStore) Delete(ctx context.Context, id string) error {
	if s.refuse {
		return errStore
	}
	return s.inner.Delete(ctx, id)
}

func (s *readOnlyStore) DeleteSubject(ctx context.Context, subject string) error {
	if s.refuse {
		return errStore
	}
	return s.inner.DeleteSubject(ctx, subject)
}

// undeletableStore refuses Delete once refuseDelete is set, standing in for a
// store that takes a write and then cannot be reached for the delete that
// follows it.
type undeletableStore struct {
	*session.MemoryStore

	refuseDelete bool
}

func (s *undeletableStore) Delete(ctx context.Context, id string) error {
	if s.refuseDelete {
		return errStore
	}
	return s.MemoryStore.Delete(ctx, id)
}
