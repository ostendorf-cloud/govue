package session

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"
)

// ErrNotFound is what a store returns for an id it does not hold, which is also
// what an expired, rotated or logged out session looks like from the outside.
var ErrNotFound = errors.New("session not found")

// Store keeps the session state. An implementation is safe for concurrent use
// and returns [ErrNotFound], wrapped or not, for an id it does not hold.
//
// The four methods are the whole contract, and the fourth is the one a store
// built around a key value get and set tends to forget: without a way to reach
// every session of one account, a password change and a disabled account cannot
// be enforced on the sessions that already exist.
//
// [MemoryStore] ships with this package and is correct for a single replica. An
// application running more than one, or one whose restart must not log everyone
// out, implements these four methods against something shared. That is
// deliberately the application's job: which backend, and whether a session
// surviving a deployment is wanted at all, is not something this package can
// decide for it.
type Store interface {
	// Get returns the session, or ErrNotFound. It is the caller, not the
	// store, that applies the idle timeout: a store only knows the absolute
	// expiry.
	Get(ctx context.Context, id string) (Session, error)

	// Save writes a new session or replaces the one under the same id.
	Save(ctx context.Context, s Session) error

	// Delete removes one session. Deleting an id the store does not hold is
	// not an error: a logout of an already ended session has nothing to
	// report.
	Delete(ctx context.Context, id string) error

	// DeleteSubject removes every session whose Subject matches, for a
	// password change, a disabled account or a log out everywhere.
	DeleteSubject(ctx context.Context, subject string) error
}

// memorySweepInterval is how long a [MemoryStore] may go without dropping what
// has expired.
const memorySweepInterval = time.Minute

// MemoryStore keeps sessions in the process.
//
// Expired entries are swept, so a long running process does not grow with every
// login, and an entry past its absolute expiry is already gone from [MemoryStore.Get]
// before the sweep reaches it.
//
// It is correct for exactly one replica. Two replicas each keep their own
// sessions, so a request that lands on the other one has no login, and a
// restart logs everybody out. A production deployment with more than one
// replica needs its own shared [Store]; see that interface.
type MemoryStore struct {
	mu        sync.RWMutex
	sessions  map[string]Session
	now       func() time.Time
	lastSweep time.Time
}

// NewMemoryStore returns an empty store. now is the clock; nil is [time.Now].
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{sessions: map[string]Session{}, now: now}
}

// Get returns the session, or [ErrNotFound]. An entry past its absolute expiry
// is treated as gone whether or not the sweep has removed it yet, so a late
// sweep can never hand out a session that should have ended.
func (s *MemoryStore) Get(_ context.Context, id string) (Session, error) {
	s.mu.RLock()
	found, ok := s.sessions[id]
	s.mu.RUnlock()

	// The expiry is exclusive, the same bound the Manager applies.
	if !ok || !s.now().Before(found.ExpiresAt) {
		return Session{}, ErrNotFound
	}
	return found.clone(), nil
}

// Save writes the session, and sweeps first if the last sweep is long enough
// ago. The sweep rides along on a write because that is the one path that makes
// the map grow.
func (s *MemoryStore) Save(_ context.Context, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.now().Sub(s.lastSweep) >= memorySweepInterval {
		s.sweep()
	}
	s.sessions[session.ID] = session.clone()
	return nil
}

// Delete removes one session.
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, id)
	return nil
}

// DeleteSubject removes every session of one account.
func (s *MemoryStore) DeleteSubject(_ context.Context, subject string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, session := range s.sessions {
		if session.Subject == subject {
			delete(s.sessions, id)
		}
	}
	return nil
}

// Len is how many sessions the store holds, including ones the sweep has not
// reached yet. It is for a metric and for tests.
func (s *MemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.sessions)
}

// sweep drops what is past its absolute expiry. The caller holds the write
// lock.
//
// Only the absolute expiry is applied: the idle timeout belongs to the
// [Manager] that holds the configuration, and a session idle past it is dropped
// on the read that finds it.
func (s *MemoryStore) sweep() {
	now := s.now()
	for id, session := range s.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
	s.lastSweep = now
}

// clone copies the map and the slice, so neither the caller nor the store can
// reach into what the other holds through a shared reference.
func (s Session) clone() Session {
	copied := s
	copied.Groups = slices.Clone(s.Groups)
	if s.Data != nil {
		copied.Data = maps.Clone(s.Data)
	}
	return copied
}
