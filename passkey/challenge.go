// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Ceremony names what a challenge was issued for. A challenge of one
// ceremony never finishes the other.
type Ceremony string

// The two ceremonies.
const (
	CeremonyRegistration Ceremony = "registration"
	CeremonyLogin        Ceremony = "login"
)

// Challenge is one pending ceremony as the [ChallengeStore] holds it.
type Challenge struct {
	// Ceremony is what the challenge was issued for.
	Ceremony Ceremony

	// AccountID is the account a registration challenge belongs to. A
	// registration finishes only for the same account. It is empty for a
	// login, which binds to the browser through the cookie instead.
	AccountID string

	// Data is the ceremony state in a form only this package reads, the
	// challenge bytes among it. It holds no secret beyond the challenge
	// itself, which the browser saw.
	Data []byte

	// ExpiresAt is when the challenge stops being usable.
	ExpiresAt time.Time
}

// ChallengeStore keeps the challenges between a begin and a finish. Every
// challenge is single use: Take returns it and deletes it in one step, so a
// second finish with the same id finds nothing.
//
// The id is 32 random bytes in base64url, made by this package and sent to
// the browser in an HttpOnly cookie only. A store that persists challenges,
// to share them between replicas, may hash the id before it uses it as a
// key. It must keep Take atomic across replicas: a read followed by a delete
// lets two finishes with one id both pass.
//
// An implementation is safe for concurrent use.
type ChallengeStore interface {
	// Put stores c under id. An id is never reused.
	Put(ctx context.Context, id string, c Challenge) error

	// Take returns the challenge stored under id and deletes it, in one step.
	// A missing id, an id taken before and an expired challenge return
	// ErrNotFound.
	Take(ctx context.Context, id string) (Challenge, error)
}

// DefaultMaxChallenges bounds a [MemoryChallengeStore] when
// [MemoryChallengeConfig.MaxEntries] is zero.
const DefaultMaxChallenges = 10000

// MemoryChallengeConfig configures a [MemoryChallengeStore].
type MemoryChallengeConfig struct {
	// MaxEntries bounds how many challenges are held. When it is reached,
	// expired challenges are dropped first, then the oldest live one. Zero
	// is [DefaultMaxChallenges].
	MaxEntries int

	// Now is the clock. Nil is time.Now.
	Now func() time.Time
}

// MemoryChallengeStore is the default [ChallengeStore]: a map in memory,
// bounded, for one process. An application with several replicas behind a
// load balancer without sticky sessions needs a shared store instead, or a
// finish may land on a replica that never saw the begin.
//
// When it is full it drops the oldest challenge, so a flood of begin calls
// cannot fill memory. The cost is that such a flood can push out a real
// challenge before it finishes, and the person starts again. The begin routes
// need a per client rate limit for that reason. Refusing new challenges when
// full would let the same flood stop every login for a whole challenge
// lifetime instead.
type MemoryChallengeStore struct {
	max int
	now func() time.Time

	mu sync.Mutex
	// head and tail are the oldest and the newest entry, a doubly linked
	// list in the order the challenges were made.
	head, tail *memoryChallenge
	byID       map[string]*memoryChallenge
}

type memoryChallenge struct {
	id         string
	challenge  Challenge
	prev, next *memoryChallenge
}

var _ ChallengeStore = (*MemoryChallengeStore)(nil)

// NewMemoryChallengeStore returns an empty store. A negative MaxEntries is
// an error.
func NewMemoryChallengeStore(cfg MemoryChallengeConfig) (*MemoryChallengeStore, error) {
	if cfg.MaxEntries < 0 {
		return nil, fmt.Errorf("passkey: memory challenge store: max entries %d is negative", cfg.MaxEntries)
	}
	if cfg.MaxEntries == 0 {
		cfg.MaxEntries = DefaultMaxChallenges
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &MemoryChallengeStore{
		max:  cfg.MaxEntries,
		now:  cfg.Now,
		byID: map[string]*memoryChallenge{},
	}, nil
}

// Put stores c under id.
func (s *MemoryChallengeStore) Put(_ context.Context, id string, c Challenge) error {
	if id == "" {
		return errors.New("passkey: memory challenge store: empty id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.byID[id]; ok {
		s.remove(e)
	}
	s.dropExpired()
	for len(s.byID) >= s.max {
		s.remove(s.head)
	}

	e := &memoryChallenge{id: id, challenge: c, prev: s.tail}
	if s.tail != nil {
		s.tail.next = e
	} else {
		s.head = e
	}
	s.tail = e
	s.byID[id] = e

	return nil
}

// Take returns the challenge under id and deletes it.
func (s *MemoryChallengeStore) Take(_ context.Context, id string) (Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.byID[id]
	if !ok {
		return Challenge{}, ErrNotFound
	}
	s.remove(e)

	if !s.now().Before(e.challenge.ExpiresAt) {
		return Challenge{}, ErrNotFound
	}

	return e.challenge, nil
}

// Len returns how many challenges are held, expired ones included until the
// next Put drops them. It exists for tests.
func (s *MemoryChallengeStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.byID)
}

// dropExpired removes expired challenges from the front. Challenges are
// stored in the order they were made and usually with one lifetime, so the
// expired ones sit at the front. One with a shorter lifetime behind a longer
// one waits until it reaches the front or is taken, and Take refuses it.
func (s *MemoryChallengeStore) dropExpired() {
	now := s.now()
	for s.head != nil && !now.Before(s.head.challenge.ExpiresAt) {
		s.remove(s.head)
	}
}

// remove unlinks e and forgets its id.
func (s *MemoryChallengeStore) remove(e *memoryChallenge) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		s.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		s.tail = e.prev
	}
	e.prev, e.next = nil, nil
	delete(s.byID, e.id)
}

// newChallengeID returns 32 random bytes in base64url without padding.
func newChallengeID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("passkey: challenge id: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
