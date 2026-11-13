// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/session"
)

// MemoryStore satisfies the interface this package publishes.
var _ session.Store = (*session.MemoryStore)(nil)

func TestMemoryStoreRoundTrip(t *testing.T) {
	store := session.NewMemoryStore(func() time.Time { return start }, 100)

	saved := session.Session{
		ID: "s1", Subject: "u1", Username: "simon",
		Groups:    []string{"admins"},
		Data:      map[string]string{"tenant": "7"},
		IssuedAt:  start,
		LastSeen:  start,
		ExpiresAt: start.Add(time.Hour),
	}
	if err := store.Save(context.Background(), saved); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "s1" || got.Subject != "u1" || got.Username != "simon" ||
		!got.In("admins") || got.Data["tenant"] != "7" || !got.ExpiresAt.Equal(saved.ExpiresAt) {
		t.Errorf("session = %+v, want what was saved", got)
	}
}

func TestMemoryStoreGetReportsNotFound(t *testing.T) {
	store := session.NewMemoryStore(nil, 100)

	if _, err := store.Get(context.Background(), "nobody"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreSaveReplaces(t *testing.T) {
	store := session.NewMemoryStore(func() time.Time { return start }, 100)
	first := session.Session{ID: "s1", Username: "before", ExpiresAt: start.Add(time.Hour)}
	second := session.Session{ID: "s1", Username: "after", ExpiresAt: start.Add(time.Hour)}

	for _, s := range []session.Session{first, second} {
		if err := store.Save(context.Background(), s); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := store.Get(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Username != "after" {
		t.Errorf("username = %q, want the replacement", got.Username)
	}
	if store.Len() != 1 {
		t.Errorf("Len = %d, want 1", store.Len())
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	store := session.NewMemoryStore(func() time.Time { return start }, 100)
	if err := store.Save(context.Background(), session.Session{ID: "s1", ExpiresAt: start.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.Delete(context.Background(), "s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(context.Background(), "s1"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	// Deleting what is not there has nothing to report.
	if err := store.Delete(context.Background(), "s1"); err != nil {
		t.Errorf("deleting an unknown id = %v", err)
	}
}

func TestMemoryStoreDeleteSubject(t *testing.T) {
	store := session.NewMemoryStore(func() time.Time { return start }, 100)
	sessions := []session.Session{
		{ID: "a", Subject: "u1", ExpiresAt: start.Add(time.Hour)},
		{ID: "b", Subject: "u1", ExpiresAt: start.Add(time.Hour)},
		{ID: "c", Subject: "u2", ExpiresAt: start.Add(time.Hour)},
	}
	for _, s := range sessions {
		if err := store.Save(context.Background(), s); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	if err := store.DeleteSubject(context.Background(), "u1"); err != nil {
		t.Fatalf("DeleteSubject: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := store.Get(context.Background(), id); !errors.Is(err, session.ErrNotFound) {
			t.Errorf("session %q of the account survived: %v", id, err)
		}
	}
	if _, err := store.Get(context.Background(), "c"); err != nil {
		t.Errorf("another account's session was deleted too: %v", err)
	}
}

func TestMemoryStoreGetIgnoresAnExpiredEntry(t *testing.T) {
	now := start
	store := session.NewMemoryStore(func() time.Time { return now }, 100)
	if err := store.Save(context.Background(), session.Session{ID: "s1", ExpiresAt: start.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Past the expiry but before any sweep could have run, the entry is still
	// in the map and must not come back out of it.
	now = start.Add(2 * time.Hour)
	if _, err := store.Get(context.Background(), "s1"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("an expired session was returned: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("Len = %d, want the unswept entry still counted", store.Len())
	}
}

func TestMemoryStoreSweepsExpiredSessions(t *testing.T) {
	now := start
	store := session.NewMemoryStore(func() time.Time { return now }, 100)

	for _, id := range []string{"a", "b", "c"} {
		if err := store.Save(context.Background(), session.Session{ID: id, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if store.Len() != 3 {
		t.Fatalf("Len = %d, want 3", store.Len())
	}

	// The next write past the sweep interval drops what has expired, so a
	// process that keeps logging people in does not keep their dead sessions.
	now = now.Add(2 * time.Hour)
	if err := store.Save(context.Background(), session.Session{ID: "fresh", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.Len() != 1 {
		t.Errorf("Len = %d after the sweep, want only the fresh session", store.Len())
	}
	if _, err := store.Get(context.Background(), "fresh"); err != nil {
		t.Errorf("the fresh session was swept too: %v", err)
	}
}

func TestMemoryStoreDoesNotSweepALiveSession(t *testing.T) {
	now := start
	store := session.NewMemoryStore(func() time.Time { return now }, 100)
	if err := store.Save(context.Background(), session.Session{ID: "long", ExpiresAt: start.Add(12 * time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	now = now.Add(2 * time.Hour)
	if err := store.Save(context.Background(), session.Session{ID: "fresh", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.Get(context.Background(), "long"); err != nil {
		t.Errorf("a session inside its lifetime was swept: %v", err)
	}
}

func TestMemoryStoreReturnsACopy(t *testing.T) {
	store := session.NewMemoryStore(func() time.Time { return start }, 100)
	original := session.Session{
		ID: "s1", Subject: "u1",
		Groups:    []string{"admins"},
		Data:      map[string]string{"tenant": "7"},
		ExpiresAt: start.Add(time.Hour),
	}
	if err := store.Save(context.Background(), original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Neither what the caller still holds nor what it got back may reach into
	// the store.
	original.Groups[0] = "changed by the caller"
	original.Data["tenant"] = "changed by the caller"

	got, err := store.Get(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Groups[0] != "admins" || got.Data["tenant"] != "7" {
		t.Fatalf("the saved session shares its map and slice with the caller: %+v", got)
	}

	got.Groups[0] = "changed by the reader"
	got.Data["tenant"] = "changed by the reader"

	again, err := store.Get(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if again.Groups[0] != "admins" || again.Data["tenant"] != "7" {
		t.Errorf("changing what Get returned changed what the store holds: %+v", again)
	}
}

func TestMemoryStoreIsSafeForConcurrentUse(t *testing.T) {
	store := session.NewMemoryStore(nil, 100)
	ctx := context.Background()

	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				id := fmt.Sprintf("w%d-%d", worker, i)
				s := session.Session{
					ID: id, Subject: fmt.Sprintf("u%d", worker),
					Groups:    []string{"admins"},
					Data:      map[string]string{"i": id},
					ExpiresAt: time.Now().Add(time.Hour),
				}
				if err := store.Save(ctx, s); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
				if _, err := store.Get(ctx, id); err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if err := store.Delete(ctx, id); err != nil {
					t.Errorf("Delete: %v", err)
					return
				}
			}
			if err := store.DeleteSubject(ctx, fmt.Sprintf("u%d", worker)); err != nil {
				t.Errorf("DeleteSubject: %v", err)
			}
		}()
	}
	wg.Wait()

	if store.Len() != 0 {
		t.Errorf("Len = %d, want everything deleted again", store.Len())
	}
}

func TestMemoryStoreBoundsMemory(t *testing.T) {
	now := start
	const maxSessions = 5
	store := session.NewMemoryStore(func() time.Time { return now }, maxSessions)

	for i := range 500 {
		id := fmt.Sprintf("s%d", i)
		s := session.Session{ID: id, ExpiresAt: now.Add(time.Hour)}
		if err := store.Save(context.Background(), s); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if n := store.Len(); n > maxSessions {
			t.Fatalf("Len = %d after %d one-shot sessions, want at most %d", n, i+1, maxSessions)
		}
	}
}

func TestMemoryStoreTouchingExistingSessionsAtTheBoundNeverEvicts(t *testing.T) {
	now := start
	const maxSessions = 5
	store := session.NewMemoryStore(func() time.Time { return now }, maxSessions)

	ids := make([]string, maxSessions)
	for i := range maxSessions {
		ids[i] = fmt.Sprintf("s%d", i)
		s := session.Session{ID: ids[i], ExpiresAt: now.Add(time.Hour)}
		if err := store.Save(context.Background(), s); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if store.Len() != maxSessions {
		t.Fatalf("Len = %d, want %d after filling the store", store.Len(), maxSessions)
	}

	// The store is at capacity. Repeatedly touching the same already-tracked
	// sessions, as Manager.Read's periodic LastSeen write does, must never
	// evict any of them: a pure update does not grow the map.
	for round := range 50 {
		for i, id := range ids {
			s := session.Session{ID: id, LastSeen: now, ExpiresAt: now.Add(time.Hour)}
			if err := store.Save(context.Background(), s); err != nil {
				t.Fatalf("round %d: Save %q: %v", round, id, err)
			}
			if n := store.Len(); n != maxSessions {
				t.Fatalf("round %d: Len = %d after touching %q, want it to stay at %d", round, n, id, maxSessions)
			}
			for _, other := range ids {
				if _, err := store.Get(context.Background(), other); err != nil {
					t.Fatalf("round %d, touch %d: session %q was evicted by a touch of %q: %v", round, i, other, id, err)
				}
			}
		}
	}
}

func TestMemoryStoreSweepGetsAChanceFromTheBoundAlone(t *testing.T) {
	now := start
	const maxSessions = 3
	store := session.NewMemoryStore(func() time.Time { return now }, maxSessions)

	// The first ever Save always sweeps, since lastSweep starts at the zero
	// time; that sets a recent lastSweep so the rest of this test is clear of
	// the time-based trigger and isolates the bound-based one.
	if err := store.Save(context.Background(), session.Session{ID: "expired1", ExpiresAt: now.Add(2 * time.Second)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(context.Background(), session.Session{ID: "expired2", ExpiresAt: now.Add(2 * time.Second)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(context.Background(), session.Session{ID: "live", ExpiresAt: now.Add(12 * time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.Len() != maxSessions {
		t.Fatalf("Len = %d, want %d after filling the store", store.Len(), maxSessions)
	}

	// Both expired entries are now past their absolute expiry, but far short
	// of memorySweepInterval: only being at the bound can give the sweep a
	// reason to look at them before the elapsed-time trigger ever would.
	now = now.Add(5 * time.Second)
	if _, err := store.Get(context.Background(), "expired1"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Get %q = %v, want ErrNotFound once it is past its expiry", "expired1", err)
	}

	// The map is at maxSessions. A new, untracked session arriving should let
	// the sweep reclaim both expired slots before any eviction is even
	// considered, rather than evicting the live session to make room.
	if err := store.Save(context.Background(), session.Session{ID: "fresh", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// With the sweep given its chance from the bound alone, both expired
	// sessions are gone and nothing had to be evicted: the store holds
	// exactly the live and the fresh session, not still pinned at maxSessions
	// by a collateral eviction.
	if got, want := store.Len(), 2; got != want {
		t.Fatalf("Len = %d, want %d: the sweep should have freed both expired slots, not evicted a live session", got, want)
	}
	if _, err := store.Get(context.Background(), "live"); err != nil {
		t.Errorf("the live session did not survive: %v", err)
	}
	if _, err := store.Get(context.Background(), "fresh"); err != nil {
		t.Errorf("the new session was not saved: %v", err)
	}
	for _, id := range []string{"expired1", "expired2"} {
		if _, err := store.Get(context.Background(), id); !errors.Is(err, session.ErrNotFound) {
			t.Errorf("Get %q = %v, want ErrNotFound", id, err)
		}
	}
}

func TestMemoryStoreMaxSessionsBelowOneIsClamped(t *testing.T) {
	now := start
	for _, maxSessions := range []int{0, -1, -100} {
		store := session.NewMemoryStore(func() time.Time { return now }, maxSessions)
		if err := store.Save(context.Background(), session.Session{ID: "s1", ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatalf("maxSessions=%d: Save: %v", maxSessions, err)
		}
		if n := store.Len(); n != 1 {
			t.Fatalf("maxSessions=%d: Len = %d, want 1", maxSessions, n)
		}
	}
}
