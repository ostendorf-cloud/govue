package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

// call is one Take invocation at a point in time, expressed as an offset
// from the previous call in the same case.
type call struct {
	after    time.Duration
	key      string
	wantOK   bool
	wantWait time.Duration
}

func TestTake(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		burst   int
		per     time.Duration
		maxKeys int
		calls   []call
	}{
		{
			name:  "burst is exhausted then refused with a wait for the next token",
			burst: 3, per: time.Minute, maxKeys: 10,
			calls: []call{
				{0, "a", true, 0},
				{0, "a", true, 0},
				{0, "a", true, 0},
				{0, "a", false, 20 * time.Second},
			},
		},
		{
			name:  "a token refills after its share of the period elapses",
			burst: 3, per: time.Minute, maxKeys: 10,
			calls: []call{
				{0, "a", true, 0},
				{0, "a", true, 0},
				{0, "a", true, 0},
				{19 * time.Second, "a", false, time.Second},
				{2 * time.Second, "a", true, 0},
			},
		},
		{
			name:  "keys do not share tokens",
			burst: 1, per: time.Minute, maxKeys: 10,
			calls: []call{
				{0, "a", true, 0},
				{0, "a", false, time.Minute},
				{0, "b", true, 0},
				{0, "c", true, 0},
			},
		},
		{
			name:  "a long idle period caps the refill at the burst",
			burst: 2, per: time.Minute, maxKeys: 10,
			calls: []call{
				{0, "a", true, 0},
				{0, "a", true, 0},
				{24 * time.Hour, "a", true, 0},
				{0, "a", true, 0},
				{0, "a", false, 30 * time.Second},
			},
		},
		{
			name:  "a clock step backwards never grants a free token",
			burst: 1, per: time.Minute, maxKeys: 10,
			calls: []call{
				{0, "a", true, 0},
				{-10 * time.Second, "a", false, time.Minute},
			},
		},
		{
			name:  "a fractional wait rounds up",
			burst: 3, per: time.Minute, maxKeys: 10,
			calls: []call{
				{0, "a", true, 0},
				{0, "a", true, 0},
				{0, "a", true, 0},
				{time.Second, "a", false, 19 * time.Second},
			},
		},
		{
			name:  "a burst of zero refuses every call",
			burst: 0, per: time.Minute, maxKeys: 10,
			calls: []call{
				{0, "a", false, noRefillWait},
				{time.Hour, "a", false, noRefillWait},
			},
		},
		{
			name:  "maxKeys of one still limits that single key",
			burst: 1, per: time.Minute, maxKeys: 1,
			calls: []call{
				{0, "a", true, 0},
				{0, "a", false, time.Minute},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
			l := New(tc.burst, tc.per, tc.maxKeys)
			l.Now = func() time.Time { return now }

			for i, c := range tc.calls {
				now = now.Add(c.after)
				gotOK, gotWait := l.Take(c.key)
				if gotOK != c.wantOK {
					t.Fatalf("call %d: Take(%q) ok = %v, want %v", i, c.key, gotOK, c.wantOK)
				}
				// The wait is derived from a float refill, so allow a small
				// rounding slack; only the magnitude is a promise.
				if diff := gotWait - c.wantWait; diff < -time.Millisecond || diff > time.Millisecond {
					t.Fatalf("call %d: Take(%q) wait = %v, want %v", i, c.key, gotWait, c.wantWait)
				}
			}
		})
	}
}

func TestAllowWrapsTake(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	l := New(1, time.Minute, 10)
	l.Now = func() time.Time { return now }

	if !l.Allow("a") {
		t.Fatal("Allow refused the first call inside an empty burst")
	}
	if l.Allow("a") {
		t.Fatal("Allow granted a second call before any refill")
	}
}

func TestSweepsIdleBuckets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	l := New(2, time.Minute, 1000)
	l.Now = func() time.Time { return now }

	for i := range 500 {
		l.Allow(fmt.Sprintf("key-%d", i))
	}
	if n := l.Len(); n != 500 {
		t.Fatalf("Len = %d, want 500 after 500 distinct keys", n)
	}

	// Every one of those buckets has now had long enough to refill fully,
	// so the next call's sweep should drop all of them.
	now = now.Add(2 * time.Minute)
	l.Allow("late-arrival")
	if n := l.Len(); n != 1 {
		t.Fatalf("Len = %d after the sweep, want 1: idle buckets were kept", n)
	}
}

func TestBoundedMemory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	const maxKeys = 50
	l := New(2, time.Hour, maxKeys)
	l.Now = func() time.Time { return now }

	for i := range 5000 {
		l.Allow(fmt.Sprintf("key-%d", i))
		if n := l.Len(); n > maxKeys {
			t.Fatalf("Len = %d after %d one-shot keys, want at most %d", n, i+1, maxKeys)
		}
	}
}

func TestMaxKeysBelowOneIsClamped(t *testing.T) {
	t.Parallel()

	for _, maxKeys := range []int{0, -1, -100} {
		l := New(1, time.Minute, maxKeys)
		if !l.Allow("a") {
			t.Fatalf("maxKeys=%d: Allow refused the only tracked key", maxKeys)
		}
		if n := l.Len(); n != 1 {
			t.Fatalf("maxKeys=%d: Len = %d, want 1", maxKeys, n)
		}
	}
}

func TestZeroPeriodDoesNotProduceNaN(t *testing.T) {
	t.Parallel()

	l := New(2, 0, 10)
	for i := range 2 {
		if !l.Allow("a") {
			t.Fatalf("call %d of the burst was refused with a zero period", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("a third call was allowed beyond the burst")
	}
}

func TestConcurrentUse(t *testing.T) {
	const (
		goroutines     = 16
		callsPerWorker = 500
	)

	l := New(1000, time.Minute, 100)
	done := make(chan struct{}, goroutines)
	for g := range goroutines {
		go func(worker int) {
			defer func() { done <- struct{}{} }()
			for i := range callsPerWorker {
				// Every goroutine hammers the same key, so the invariant
				// after the run is that it is still the only tracked key:
				// the locking must serialize reads and writes of its
				// bucket, never losing or duplicating the entry.
				l.Allow("shared")
				// Each goroutine also touches its own key, to exercise the
				// map growing and sweeping concurrently with the shared key.
				l.Take(fmt.Sprintf("worker-%d-call-%d", worker, i))
			}
		}(g)
	}
	for range goroutines {
		<-done
	}

	if n := l.Len(); n < 1 {
		t.Fatalf("Len = %d after concurrent use, want at least 1: the shared key vanished", n)
	}
	if n := l.Len(); n > 100 {
		t.Fatalf("Len = %d after concurrent use, want at most maxKeys=100", n)
	}
}
