// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package optin

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_Update(t *testing.T) {
	t.Parallel()
	key := Key{1}
	hash := TokenHash{2}
	at := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	errStop := errors.New("stop")

	t.Run("a missing entry arrives with its key and no state", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryStore(0)
		var seen Entry
		if err := s.Update(t.Context(), key, func(e *Entry) error { seen = *e; return nil }); err != nil {
			t.Fatal(err)
		}
		if seen != (Entry{Key: key}) || s.Len() != 0 {
			t.Fatalf("seen %+v, len %d", seen, s.Len())
		}
	})
	t.Run("an error writes nothing", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryStore(0)
		err := s.Update(t.Context(), key, func(e *Entry) error { e.State = StatePending; return errStop })
		if !errors.Is(err, errStop) || s.Len() != 0 {
			t.Fatalf("err %v, len %d", err, s.Len())
		}
	})
	t.Run("the hash index follows the state", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryStore(0)
		ctx := t.Context()
		mustUpdate(t, s, key, func(e *Entry) { *e = Entry{State: StatePending, ConfirmHash: hash, Created: at} })
		if got, err := s.FindByConfirmHash(ctx, hash); err != nil || got != key {
			t.Fatalf("pending: %v %v", got, err)
		}
		mustUpdate(t, s, key, func(e *Entry) { e.State, e.ConfirmHash = StateConfirmed, TokenHash{} })
		if _, err := s.FindByConfirmHash(ctx, hash); !errors.Is(err, ErrNotFound) {
			t.Fatalf("confirmed: %v", err)
		}
		mustUpdate(t, s, key, func(e *Entry) { e.State = StateNone })
		if _, err := s.Get(ctx, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted: %v", err)
		}
	})
	t.Run("a full store refuses a new key and keeps an old one", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryStore(1)
		mustUpdate(t, s, key, func(e *Entry) { e.State = StateConfirmed })
		err := s.Update(t.Context(), Key{9}, func(e *Entry) error { e.State = StatePending; return nil })
		if !errors.Is(err, ErrStoreFull) {
			t.Fatalf("got %v", err)
		}
		mustUpdate(t, s, key, func(e *Entry) { e.State = StateConfirmed })
		// Deleting a missing key needs no room.
		mustUpdate(t, s, Key{9}, func(*Entry) {})
	})
	t.Run("a cancelled context is refused", func(t *testing.T) {
		t.Parallel()
		s := NewMemoryStore(0)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := s.Update(ctx, key, func(*Entry) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
}

func mustUpdate(t *testing.T, s *MemoryStore, key Key, fn func(*Entry)) {
	t.Helper()
	if err := s.Update(t.Context(), key, func(e *Entry) error { fn(e); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryStore_DeleteUnconfirmedBefore(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore(0)
	at := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	mustUpdate(t, s, Key{1}, func(e *Entry) { *e = Entry{State: StatePending, Created: at, ConfirmHash: TokenHash{1}} })
	mustUpdate(t, s, Key{2}, func(e *Entry) {
		*e = Entry{State: StatePending, Created: at.Add(time.Second), ConfirmHash: TokenHash{2}}
	})
	mustUpdate(t, s, Key{3}, func(e *Entry) {
		*e = Entry{State: StateConfirmed, Created: at.Add(-time.Hour), Confirmed: at.Add(-time.Hour)}
	})
	mustUpdate(t, s, Key{4}, func(e *Entry) { *e = Entry{State: StateCounter, Created: at, Sends: 1} })
	mustUpdate(t, s, Key{5}, func(e *Entry) { *e = Entry{State: StateCounter, Created: at.Add(time.Second), Sends: 1} })
	n, err := s.DeleteUnconfirmedBefore(t.Context(), at)
	if err != nil || n != 2 {
		t.Fatalf("got %d %v", n, err)
	}
	if _, err := s.FindByConfirmHash(t.Context(), TokenHash{1}); !errors.Is(err, ErrNotFound) {
		t.Fatal("the hash of a purged entry is still found")
	}
	for _, k := range []Key{{2}, {3}, {5}} {
		if _, err := s.Get(t.Context(), k); err != nil {
			t.Errorf("key %d: %v", k[0], err)
		}
	}
}

func TestMemoryStore_DeleteConfirmedBefore(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore(0)
	at := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	mustUpdate(t, s, Key{1}, func(e *Entry) { *e = Entry{State: StateConfirmed, Confirmed: at} })
	mustUpdate(t, s, Key{2}, func(e *Entry) { *e = Entry{State: StateConfirmed, Confirmed: at.Add(time.Second)} })
	mustUpdate(t, s, Key{3}, func(e *Entry) {
		*e = Entry{State: StatePending, Created: at.Add(-time.Hour), ConfirmHash: TokenHash{3}}
	})
	mustUpdate(t, s, Key{4}, func(e *Entry) { *e = Entry{State: StateCounter, Created: at.Add(-time.Hour)} })
	n, err := s.DeleteConfirmedBefore(t.Context(), at)
	if err != nil || n != 1 {
		t.Fatalf("got %d %v", n, err)
	}
	if _, err := s.Get(t.Context(), Key{1}); !errors.Is(err, ErrNotFound) {
		t.Fatal("the entry confirmed at the cutoff is still there")
	}
	for _, k := range []Key{{2}, {3}, {4}} {
		if _, err := s.Get(t.Context(), k); err != nil {
			t.Errorf("key %d: %v", k[0], err)
		}
	}
}

// TestRequest_concurrentRequestsSendOneMail pins that the per address limit
// holds under concurrent requests for one address.
func TestRequest_concurrentRequestsSendOneMail(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	const racers = 16
	bodies := make([][]byte, racers)
	for i := range bodies {
		bodies[i] = jsonBody(t, map[string]any{"email": visitor, "consent": true, "pow": e.solve(t)})
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			serve(t, e.h.Request(), http.MethodPost, "/x", bodies[i])
		}()
	}
	close(start)
	wg.Wait()
	e.wait(t)
	if n := len(e.sender.to(visitor)); n != 1 {
		t.Fatalf("%d mails, want 1", n)
	}
}

// TestConfirm_concurrentUsesConfirmOnce pins single use under concurrency.
func TestConfirm_concurrentUsesConfirmOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	token := tokenAfter(t, e.sender.to(visitor)[0].Body, confirmPage)
	const racers = 16
	codes := make([]int, racers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i] = e.confirm(t, token).Code
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d confirmations succeeded, want 1", ok)
	}
}
