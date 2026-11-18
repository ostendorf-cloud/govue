// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package accounttest

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/ostendorf-cloud/govue/account"
)

// MemoryConfig configures a [MemoryStore].
type MemoryConfig struct {
	// FoldCase makes logins case insensitive: a login that differs from a
	// stored one only in letter case finds that account and conflicts with
	// it. The record keeps the login as it was created.
	FoldCase bool
}

// MemoryStore is an [account.Store] in memory, for tests of code built on the
// account package. One mutex guards every call, so each conditional write
// compares and writes as one step, the way a conditional UPDATE does. It
// passes [Run].
//
// It is a test fake, not a production store: it keeps nothing across a
// restart and never shrinks.
type MemoryStore struct {
	foldCase bool

	mu       sync.Mutex
	next     int
	byKey    map[string]*account.Record
	byID     map[string]*account.Record
	disabled map[string]bool
}

var _ account.Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore(cfg MemoryConfig) *MemoryStore {
	return &MemoryStore{
		foldCase: cfg.FoldCase,
		byKey:    map[string]*account.Record{},
		byID:     map[string]*account.Record{},
		disabled: map[string]bool{},
	}
}

// Disable holds the account with id back from login: FindByLogin answers
// account.ErrNotFound for it from now on, the way a consumer's adapter
// refuses a disabled account. It reports whether such an account exists.
func (s *MemoryStore) Disable(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byID[id]; !ok {
		return false
	}

	s.disabled[id] = true

	return true
}

// key is the map key of a login.
func (s *MemoryStore) key(login string) string {
	if s.foldCase {
		return strings.ToLower(login)
	}

	return login
}

// FindByLogin implements [account.Store].
func (s *MemoryStore) FindByLogin(_ context.Context, login string) (account.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.byKey[s.key(login)]
	if !ok || s.disabled[rec.ID] {
		return account.Record{}, account.ErrNotFound
	}

	return *rec, nil
}

// Create implements [account.Store].
func (s *MemoryStore) Create(_ context.Context, rec account.Record) (account.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.key(rec.Login)
	if _, taken := s.byKey[key]; taken {
		return account.Record{}, account.ErrConflict
	}

	s.next++
	rec.ID = "memory-" + strconv.Itoa(s.next)
	rec.TOTPCounter = 0

	stored := rec
	s.byKey[key] = &stored
	s.byID[rec.ID] = &stored

	return rec, nil
}

// AdvanceTOTPCounter implements [account.Store].
func (s *MemoryStore) AdvanceTOTPCounter(_ context.Context, id string, counter int64) error {
	return s.update(id, func(rec *account.Record) bool {
		if rec.TOTPCounter <= 0 || counter <= rec.TOTPCounter {
			return false
		}

		rec.TOTPCounter = counter

		return true
	})
}

// ConfirmTOTP implements [account.Store].
func (s *MemoryStore) ConfirmTOTP(_ context.Context, id, secret string, counter int64) error {
	return s.update(id, func(rec *account.Record) bool {
		if rec.TOTPSecret == "" || rec.TOTPSecret != secret || rec.TOTPCounter != 0 || counter <= 0 {
			return false
		}

		rec.TOTPCounter = counter

		return true
	})
}

// ReplaceTOTPSecret implements [account.Store].
func (s *MemoryStore) ReplaceTOTPSecret(_ context.Context, id, secret string) error {
	return s.update(id, func(rec *account.Record) bool {
		if rec.TOTPCounter != 0 {
			return false
		}

		rec.TOTPSecret = secret

		return true
	})
}

// SetPasswordHash implements [account.Store].
func (s *MemoryStore) SetPasswordHash(_ context.Context, id, oldHash, newHash string) error {
	return s.update(id, func(rec *account.Record) bool {
		if rec.PasswordHash == "" || rec.PasswordHash != oldHash {
			return false
		}

		rec.PasswordHash = newHash

		return true
	})
}

// update applies change to the account with id under the lock, and answers
// account.ErrNotFound when there is no such account or change declines.
func (s *MemoryStore) update(id string, change func(*account.Record) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.byID[id]
	if !ok || !change(rec) {
		return account.ErrNotFound
	}

	return nil
}
