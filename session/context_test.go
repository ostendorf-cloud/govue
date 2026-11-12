// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/session"
)

// countingStore counts the reads, which is how the one read per request is
// proven: nothing else about a Manager says how often it went to the store.
type countingStore struct {
	*session.MemoryStore

	mu   sync.Mutex
	gets int
}

func (s *countingStore) Get(ctx context.Context, id string) (session.Session, error) {
	s.mu.Lock()
	s.gets++
	s.mu.Unlock()
	return s.MemoryStore.Get(ctx, id)
}

func (s *countingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

// served runs one request through the middleware and the handler and reports
// whether the handler was reached.
func served(manager *session.Manager, req *http.Request, handler http.HandlerFunc) bool {
	reached := false
	wrapped := manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		handler(w, r)
	}))
	wrapped.ServeHTTP(httptest.NewRecorder(), req)
	return reached
}

func TestMiddlewareReadsTheSessionOncePerRequest(t *testing.T) {
	f := newFixture(t, nil)
	counting := &countingStore{MemoryStore: f.store}
	manager, err := session.New(session.Config{Store: counting, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	started, err := manager.Start(context.Background(), rec, session.Session{Subject: "u1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	before := counting.count()

	var ids []string
	reached := served(manager, withCookies(rec), func(_ http.ResponseWriter, r *http.Request) {
		// Two readers, which is the ordinary case: a handler and whatever it
		// calls, or a csrf.Expected closure next to it.
		for range 2 {
			got, ok := session.FromContext(r.Context())
			if !ok {
				t.Error("the context carries no session")
			}
			ids = append(ids, got.ID)
		}
	})
	if !reached {
		t.Fatal("the request never reached the handler")
	}

	if got := counting.count() - before; got != 1 {
		t.Errorf("the store saw %d reads, want 1", got)
	}
	for _, id := range ids {
		if id != started.ID {
			t.Errorf("the context carries session %q, want %q", id, started.ID)
		}
	}
}

func TestFromContextReportsNoSessionForARequestWithoutACookie(t *testing.T) {
	f := newFixture(t, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	reached := served(f.manager, req, func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := session.FromContext(r.Context()); ok {
			t.Error("a request without a cookie looks logged in")
		}
		if err := session.ErrorFromContext(r.Context()); err != nil {
			t.Errorf("error = %v, want nil: a logged out request is not an outage", err)
		}
	})
	if !reached {
		t.Error("the request never reached the handler")
	}
}

func TestFromContextFailsClosedWhenTheStoreIsUnreachable(t *testing.T) {
	manager, err := session.New(session.Config{Store: failingStore{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: manager.CookieName(), Value: "an id the store cannot be asked about"})

	reached := served(manager, req, func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := session.FromContext(r.Context()); ok {
			t.Error("a store outage looks logged in")
		}
		// The distinction Read draws is what the context keeps: a handler that
		// cares answers 503 rather than redirecting to the provider.
		if err := session.ErrorFromContext(r.Context()); !errors.Is(err, errStore) {
			t.Errorf("error = %v, want the store's", err)
		}
	})
	if !reached {
		t.Error("the request never reached the handler")
	}
}

func TestFromContextReportsNoSessionWithoutTheMiddleware(t *testing.T) {
	// A handler mounted without the middleware has to look logged out, so
	// forgetting to mount it fails closed.
	if _, ok := session.FromContext(context.Background()); ok {
		t.Error("a bare context carries a session")
	}
	if err := session.ErrorFromContext(context.Background()); err != nil {
		t.Errorf("error = %v, want nil", err)
	}
}

func TestMiddlewareDoesNotRefuseARequest(t *testing.T) {
	// The middleware is not an authorisation gate: a public route behind the
	// same chain must not turn into an error because a cookie could not be
	// read.
	cases := map[string]func(*testing.T) (*session.Manager, *http.Request){
		"no cookie": func(t *testing.T) (*session.Manager, *http.Request) {
			f := newFixture(t, nil)
			return f.manager, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		},
		"an id the store does not hold": func(t *testing.T) (*session.Manager, *http.Request) {
			f := newFixture(t, nil)
			return f.manager, withCookie(f.manager.CookieName(), "an id nothing holds")
		},
		"a store outage": func(t *testing.T) (*session.Manager, *http.Request) {
			manager, err := session.New(session.Config{Store: failingStore{}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			return manager, withCookie(manager.CookieName(), "an id the store cannot be asked about")
		},
		"a session that is there": func(t *testing.T) (*session.Manager, *http.Request) {
			f := newFixture(t, nil)
			_, req := f.login(t, "u1")
			return f.manager, req
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			manager, req := build(t)

			rec := httptest.NewRecorder()
			manager.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			})).ServeHTTP(rec, req)

			if rec.Code != http.StatusTeapot {
				t.Errorf("status = %d, want %d: the middleware answered instead of the handler",
					rec.Code, http.StatusTeapot)
			}
		})
	}
}

func TestTheContextHoldsTheSessionAsItWasAtTheStartOfTheRequest(t *testing.T) {
	f := newFixture(t, nil)
	_, req := f.login(t, "u1")

	var fromContext, rotated session.Session
	reached := served(f.manager, req, func(w http.ResponseWriter, r *http.Request) {
		before, ok := session.FromContext(r.Context())
		if !ok {
			t.Fatal("the context carries no session")
		}

		var err error
		rotated, err = f.manager.Rotate(r.Context(), w, before)
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		fromContext, _ = session.FromContext(r.Context())
	})
	if !reached {
		t.Fatal("the request never reached the handler")
	}

	// Rotate returned the new session; the context still holds the one the
	// request arrived with, which is what the doc comment promises.
	if fromContext.ID == rotated.ID {
		t.Error("the context followed the rotation")
	}
}
