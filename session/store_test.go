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

	"github.com/ostendorf-cloud/web-kit/session"
)

// MemoryStore satisfies the interface this package publishes.
var _ session.Store = (*session.MemoryStore)(nil)

func TestMemoryStoreRoundTrip(t *testing.T) {
	store := session.NewMemoryStore(func() time.Time { return start })

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
	store := session.NewMemoryStore(nil)

	if _, err := store.Get(context.Background(), "nobody"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreSaveReplaces(t *testing.T) {
	store := session.NewMemoryStore(func() time.Time { return start })
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
	store := session.NewMemoryStore(func() time.Time { return start })
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
	store := session.NewMemoryStore(func() time.Time { return start })
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
	store := session.NewMemoryStore(func() time.Time { return now })
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
	store := session.NewMemoryStore(func() time.Time { return now })

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
	store := session.NewMemoryStore(func() time.Time { return now })
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
	store := session.NewMemoryStore(func() time.Time { return start })
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
	store := session.NewMemoryStore(nil)
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
