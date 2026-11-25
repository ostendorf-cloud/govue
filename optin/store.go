// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package optin

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Key identifies a record. The key of an entry is HMAC-SHA-256 of the
// address in lower case, the key of a send counter HMAC-SHA-256 of the
// mailbox (see the package doc), each under its own key derived from
// Config.Secret. It is pseudonymous, not anonymous: whoever holds the secret
// can compute it from an address.
type Key [32]byte

// TokenHash is the SHA-256 of a confirmation token under a domain separator.
// The token itself is never stored.
type TokenHash [32]byte

// State is the state of a record.
type State uint8

// The states of a record. StateNone is never stored: [Store.Update] deletes a
// record whose function leaves it in StateNone.
const (
	StateNone State = iota
	StatePending
	StateConfirmed
	// StateCounter is the send counter of one mailbox, not an entry of the
	// list. It holds no address.
	StateCounter
)

// String returns the lower case name of the state.
func (s State) String() string {
	switch s {
	case StateNone:
		return "none"
	case StatePending:
		return "pending"
	case StateConfirmed:
		return "confirmed"
	case StateCounter:
		return "counter"
	}
	return "unknown"
}

// Entry is one record of the store. Which fields are set depends on the
// state:
//
//   - pending: Key, Address, Language, ConsentVersion, Created and
//     ConfirmHash.
//   - confirmed: Key, Address, Language, ConsentVersion, Created and
//     Confirmed. The token hash is zero.
//   - counter: Key, Created (the start of the counting period), Sends and
//     LastSent. No address.
type Entry struct {
	Key   Key
	State State
	// Address is the address as the visitor typed it, trimmed.
	Address string
	// Language is the page language the request came from, a hint for the
	// site's mail text. It may be empty.
	Language string
	// ConsentVersion is Config.ConsentVersion at the time of the request.
	ConsentVersion string
	// Created is the start of the pending period of an entry, or of the
	// counting period of a counter. A resend keeps it.
	Created time.Time
	// Confirmed is the time of the confirmation.
	Confirmed time.Time
	// ConfirmHash is the hash of the newest confirmation token.
	ConfirmHash TokenHash
	// Sends counts the confirmation mails to one mailbox in the counting
	// period. Counter only.
	Sends int
	// LastSent is the time of the last confirmation mail to the mailbox.
	// Counter only.
	LastSent time.Time
}

// ErrNotFound is returned by [Store.Get] and [Store.FindByConfirmHash] when
// nothing matches.
var ErrNotFound = errors.New("optin: entry not found")

// ErrStoreFull is returned by [MemoryStore.Update] when a new record would
// pass MaxEntries.
var ErrStoreFull = errors.New("optin: store is full")

// Store keeps the records. The package holds every rule, the store only has
// to keep each method atomic. A SQL adapter in a product implements it on
// its own table, see the package doc for a schema. Every method must be safe
// for concurrent use. An error text must not carry an address or a token,
// because the handler logs it.
type Store interface {
	// Get returns the record of key, or ErrNotFound.
	Get(ctx context.Context, key Key) (Entry, error)
	// Update loads the record of key, calls fn with it and writes the
	// result, as one atomic step against every other method, see the package
	// doc for SQL. A missing record is passed with only Key set and State
	// StateNone. When fn returns an error nothing is written and Update
	// returns that error unchanged. When fn leaves the state at StateNone the
	// record is deleted. fn must not call the store. Update may call fn more
	// than once, when it retries a transaction: only the last call counts, so
	// fn starts from what it is given every time.
	Update(ctx context.Context, key Key, fn func(*Entry) error) error
	// FindByConfirmHash returns the key of the pending entry whose
	// ConfirmHash is h, or ErrNotFound. The handler checks the hash again
	// under Update, so a stale answer is safe.
	FindByConfirmHash(ctx context.Context, h TokenHash) (Key, error)
	// DeleteUnconfirmedBefore deletes every pending entry whose Created is at
	// or before cutoff, and every counter whose Created is at or before
	// cutoff and whose LastSent is at or before lastSentCutoff, and returns
	// how many it deleted. It never touches a confirmed entry. The second
	// time keeps a counter whose last mail is recent, so the gap between two
	// mails also holds when a counting period ends.
	DeleteUnconfirmedBefore(ctx context.Context, cutoff, lastSentCutoff time.Time) (int, error)
	// DeleteConfirmedBefore deletes every confirmed entry whose Confirmed is
	// at or before cutoff and returns how many it deleted. It never touches a
	// pending entry or a counter.
	DeleteConfirmedBefore(ctx context.Context, cutoff time.Time) (int, error)
	// EachConfirmed calls fn with every confirmed entry, in any order, and
	// stops at the first error of fn, which it returns. fn can be slow, it
	// sends a mail: the store must not hold a lock or a transaction open
	// while it runs.
	EachConfirmed(ctx context.Context, fn func(Entry) error) error
}

// DefaultMaxEntries bounds a [MemoryStore] built with a zero size.
const DefaultMaxEntries = 100_000

// MemoryStore is a [Store] in process memory. It is correct for one process
// and loses every record when the process ends. It suits tests and a site
// that accepts that loss. It is safe for concurrent use.
type MemoryStore struct {
	mu      sync.Mutex
	max     int
	entries map[Key]Entry
	byHash  map[TokenHash]Key
}

// NewMemoryStore returns an empty store that holds at most maxEntries
// records, counters included. Zero or less means [DefaultMaxEntries].
func NewMemoryStore(maxEntries int) *MemoryStore {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	return &MemoryStore{max: maxEntries, entries: map[Key]Entry{}, byHash: map[TokenHash]Key{}}
}

// Get implements [Store].
func (m *MemoryStore) Get(ctx context.Context, key Key) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

// Update implements [Store].
func (m *MemoryStore) Update(ctx context.Context, key Key, fn func(*Entry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, exists := m.entries[key]
	e := old
	if !exists {
		e = Entry{Key: key}
	}
	if err := fn(&e); err != nil {
		return err
	}
	e.Key = key
	if !exists && e.State != StateNone && len(m.entries) >= m.max {
		return ErrStoreFull
	}
	if exists && old.State == StatePending {
		delete(m.byHash, old.ConfirmHash)
	}
	if e.State == StateNone {
		delete(m.entries, key)
		return nil
	}
	m.entries[key] = e
	if e.State == StatePending {
		m.byHash[e.ConfirmHash] = key
	}
	return nil
}

// FindByConfirmHash implements [Store].
func (m *MemoryStore) FindByConfirmHash(ctx context.Context, h TokenHash) (Key, error) {
	if err := ctx.Err(); err != nil {
		return Key{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.byHash[h]
	if !ok {
		return Key{}, ErrNotFound
	}
	return key, nil
}

// DeleteUnconfirmedBefore implements [Store].
func (m *MemoryStore) DeleteUnconfirmedBefore(ctx context.Context, cutoff, lastSentCutoff time.Time) (int, error) {
	return m.deleteWhere(ctx, func(e Entry) bool {
		switch e.State {
		case StatePending:
			return !e.Created.After(cutoff)
		case StateCounter:
			return !e.Created.After(cutoff) && !e.LastSent.After(lastSentCutoff)
		}
		return false
	})
}

// DeleteConfirmedBefore implements [Store].
func (m *MemoryStore) DeleteConfirmedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	return m.deleteWhere(ctx, func(e Entry) bool {
		return e.State == StateConfirmed && !e.Confirmed.After(cutoff)
	})
}

func (m *MemoryStore) deleteWhere(ctx context.Context, match func(Entry) bool) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for key, e := range m.entries {
		if match(e) {
			if e.State == StatePending {
				delete(m.byHash, e.ConfirmHash)
			}
			delete(m.entries, key)
			n++
		}
	}
	return n, nil
}

// EachConfirmed implements [Store]. It copies the confirmed entries under the
// lock and calls fn after releasing it.
func (m *MemoryStore) EachConfirmed(ctx context.Context, fn func(Entry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	list := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		if e.State == StateConfirmed {
			list = append(list, e)
		}
	}
	m.mu.Unlock()
	for _, e := range list {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

// Len returns the number of records, counters included.
func (m *MemoryStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
