// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a settable clock for tests.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newMemory(t *testing.T, maxEntries int, c *clock) *MemoryChallengeStore {
	t.Helper()
	s, err := NewMemoryChallengeStore(MemoryChallengeConfig{MaxEntries: maxEntries, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMemoryChallengeStore(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	live := Challenge{Ceremony: CeremonyLogin, Data: []byte("x"), ExpiresAt: c.Now().Add(time.Minute)}

	tests := []struct {
		name    string
		run     func(s *MemoryChallengeStore) error
		wantErr error
	}{
		{"take once", func(s *MemoryChallengeStore) error {
			_, err := s.Take(ctx, "a")
			return err
		}, nil},
		{"take twice", func(s *MemoryChallengeStore) error {
			if _, err := s.Take(ctx, "a"); err != nil {
				return err
			}
			_, err := s.Take(ctx, "a")
			return err
		}, ErrNotFound},
		{"unknown id", func(s *MemoryChallengeStore) error {
			_, err := s.Take(ctx, "b")
			return err
		}, ErrNotFound},
		{"expired", func(s *MemoryChallengeStore) error {
			c.Add(time.Minute)
			defer c.Add(-time.Minute)
			_, err := s.Take(ctx, "a")
			return err
		}, ErrNotFound},
		{"expired is gone after the refusal", func(s *MemoryChallengeStore) error {
			c.Add(time.Minute)
			_, _ = s.Take(ctx, "a")
			c.Add(-time.Minute)
			_, err := s.Take(ctx, "a")
			return err
		}, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newMemory(t, 0, c)
			if err := s.Put(ctx, "a", live); err != nil {
				t.Fatal(err)
			}
			if err := tt.run(s); !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestMemoryChallengeStoreBounded(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	s := newMemory(t, 3, c)
	for i := range 5 {
		if err := s.Put(ctx, fmt.Sprint(i), Challenge{ExpiresAt: c.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if s.Len() != 3 {
		t.Fatalf("Len: got %d, want 3", s.Len())
	}
	for i, want := range []error{ErrNotFound, ErrNotFound, nil, nil, nil} {
		if _, err := s.Take(ctx, fmt.Sprint(i)); !errors.Is(err, want) {
			t.Fatalf("Take %d: got %v, want %v, the oldest go first", i, err, want)
		}
	}
}

func TestMemoryChallengeStoreDropsExpiredFirst(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	s := newMemory(t, 2, c)
	_ = s.Put(ctx, "old", Challenge{ExpiresAt: c.Now().Add(time.Second)})
	_ = s.Put(ctx, "live", Challenge{ExpiresAt: c.Now().Add(time.Hour)})
	c.Add(2 * time.Second)
	_ = s.Put(ctx, "new", Challenge{ExpiresAt: c.Now().Add(time.Hour)})
	if _, err := s.Take(ctx, "live"); err != nil {
		t.Fatalf("a live challenge was dropped while an expired one was held: %v", err)
	}
}

func TestMemoryChallengeStoreRefusesBadInput(t *testing.T) {
	if _, err := NewMemoryChallengeStore(MemoryChallengeConfig{MaxEntries: -1}); err == nil {
		t.Fatal("a negative MaxEntries was accepted")
	}
	s := newMemory(t, 0, newClock())
	if err := s.Put(context.Background(), "", Challenge{}); err == nil {
		t.Fatal("an empty id was accepted")
	}
}

func TestMemoryChallengeStoreTakeRacesOnce(t *testing.T) {
	ctx := context.Background()
	c := newClock()
	s := newMemory(t, 0, c)
	for round := range 200 {
		id := fmt.Sprint(round)
		_ = s.Put(ctx, id, Challenge{ExpiresAt: c.Now().Add(time.Minute)})
		var ok atomic.Int32
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				<-start
				if _, err := s.Take(ctx, id); err == nil {
					ok.Add(1)
				}
			})
		}
		close(start)
		wg.Wait()
		if ok.Load() != 1 {
			t.Fatalf("round %d: %d racing takes succeeded, want 1", round, ok.Load())
		}
	}
}

func TestChallengeIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id, err := newChallengeID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 43 || seen[id] {
			t.Fatalf("id %q is not 32 fresh bytes in base64url", id)
		}
		seen[id] = true
	}
}
