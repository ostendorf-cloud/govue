// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package ratelimit implements a per-key token bucket rate limiter with a
// bounded memory footprint.
//
// A caller that keys by something attacker-controlled, such as a client
// address, must not let that key grow a map forever: a flood of distinct,
// one-shot keys would otherwise exhaust memory even though no single key ever
// came close to its limit. This package bounds the damage two ways. First, a
// bucket that has refilled all the way back to its burst size carries no
// information worth keeping, so it is a safe candidate to drop; dropping it
// just means the key starts over from a full bucket next time, exactly as if
// it had never been seen. Second, the number of tracked keys is capped at
// construction time; if the map is still full of active buckets after a
// sweep, one arbitrary bucket is evicted to make room. That can only reset an
// unlucky key's limit early, never cause a request that should be allowed to
// be denied.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// defaultMaxKeys is the floor applied when the caller passes a non-positive
// maxKeys: a limiter that tracks nothing would allow everything, which is
// never the intent of asking for a rate limiter at all.
const defaultMaxKeys = 1

// defaultPeriod replaces a non-positive per: a zero or negative period has no
// sane rate, and dividing by it would turn every bucket into NaN.
const defaultPeriod = time.Second

// sweepEvery is how long a limiter goes between sweeps of fully refilled
// buckets, even if the key count never reaches maxKeys on its own.
const sweepEvery = time.Minute

// noRefillWait is the wait handed back when the configured burst is zero, so
// the bucket never refills and there is no meaningful retry hint to compute.
const noRefillWait = sweepEvery

// bucket is one key's share of tokens as of the last time it was touched.
type bucket struct {
	tokens    float64
	touchedAt time.Time
}

// Limiter is a token bucket per key, safe for concurrent use.
//
// Each key gets its own bucket that starts full and refills over time up to
// its configured burst. Allowing an action takes one token; refusing it
// reports how long until the next token is available. Idle buckets, meaning
// ones that have refilled completely, are swept away periodically so a
// limiter keyed by many short-lived identities does not grow without bound.
type Limiter struct {
	// Now returns the current time. It defaults to time.Now and exists so
	// tests can control the clock; production callers should leave it unset.
	Now func() time.Time

	mu sync.Mutex

	burst           float64
	refillPerSecond float64
	maxKeys         int

	buckets     map[string]*bucket
	lastSweptAt time.Time
}

// New returns a Limiter that allows burst actions at once per key and
// refills one burst worth of tokens every per, tracking at most maxKeys
// distinct keys at a time.
//
// maxKeys is clamped to at least 1. per is clamped to a default of one
// second if it is zero or negative, so the rate never comes from dividing by
// zero. burst may be zero, in which case the limiter refuses every request
// for every key.
func New(burst int, per time.Duration, maxKeys int) *Limiter {
	if maxKeys < 1 {
		maxKeys = defaultMaxKeys
	}
	if per <= 0 {
		per = defaultPeriod
	}
	return &Limiter{
		Now:             time.Now,
		burst:           float64(burst),
		refillPerSecond: float64(burst) / per.Seconds(),
		maxKeys:         maxKeys,
		buckets:         make(map[string]*bucket),
	}
}

// Allow reports whether an action for key is allowed right now, consuming a
// token if so. It is a thin wrapper around Take that discards the wait hint;
// use Take directly when a denied caller needs to know how long to wait.
func (l *Limiter) Allow(key string) bool {
	allowed, _ := l.Take(key)
	return allowed
}

// Take reports whether an action for key is allowed right now. If it is, one
// token is consumed from that key's bucket. If it is not, the returned
// duration is how long until a token becomes available, suitable for a
// Retry-After response header.
func (l *Limiter) Take(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	l.maybeSweep(now)

	b, tracked := l.buckets[key]
	if !tracked {
		b = l.startBucket(key, now)
	}

	b.tokens = l.refill(b.tokens, now.Sub(b.touchedAt))
	b.touchedAt = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, l.wait(b.tokens)
}

// Len reports the number of keys currently tracked. It exists for tests and
// metrics that want to observe the bounded-memory guarantee directly.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// clock reads the current time, defaulting Now to time.Now lazily so a
// Limiter built with New always has a working clock even if a caller
// constructs one with the zero value by hand.
func (l *Limiter) clock() time.Time {
	if l.Now == nil {
		return time.Now()
	}
	return l.Now()
}

// startBucket creates a full bucket for key, evicting an existing entry
// first if the map is already at capacity. The evicted entry is arbitrary;
// map iteration order is unspecified, so this never blocks and never prefers
// one key over another. Losing a bucket early can only grant its key a fresh
// burst sooner than it would otherwise get one, never deny a request that
// should be allowed.
func (l *Limiter) startBucket(key string, now time.Time) *bucket {
	if len(l.buckets) >= l.maxKeys {
		for existing := range l.buckets {
			delete(l.buckets, existing)
			break
		}
	}
	b := &bucket{tokens: l.burst, touchedAt: now}
	l.buckets[key] = b
	return b
}

// maybeSweep drops fully refilled buckets when it is time to look, either
// because sweepEvery has passed or because the map has grown to maxKeys and
// needs the chance to shrink before Take has to evict an active key.
func (l *Limiter) maybeSweep(now time.Time) {
	if now.Sub(l.lastSweptAt) < sweepEvery && len(l.buckets) < l.maxKeys {
		return
	}
	for key, b := range l.buckets {
		if l.refill(b.tokens, now.Sub(b.touchedAt)) >= l.burst {
			delete(l.buckets, key)
		}
	}
	l.lastSweptAt = now
}

// refill projects tokens forward by elapsed, capped at burst. A negative
// elapsed, which happens if the injected clock ever steps backwards, is
// treated as no time passing rather than handed to the multiplication, so a
// clock regression can never manufacture free tokens.
func (l *Limiter) refill(tokens float64, elapsed time.Duration) float64 {
	if elapsed < 0 {
		elapsed = 0
	}
	return math.Min(l.burst, tokens+elapsed.Seconds()*l.refillPerSecond)
}

// wait turns a sub-one token balance into a duration until the next token,
// rounded up so a caller that waits exactly that long is never turned away
// again by a few nanoseconds of floating point slack.
func (l *Limiter) wait(tokens float64) time.Duration {
	if l.refillPerSecond <= 0 {
		// A zero burst never refills; there is no finite wait to compute, so
		// report a fixed, documented hold-off instead of dividing by zero.
		return noRefillWait
	}
	missing := 1 - tokens
	return time.Duration(math.Ceil(missing / l.refillPerSecond * float64(time.Second)))
}
