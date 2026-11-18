// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package accounttest_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ostendorf-cloud/govue/account"
)

// flaw is one way a store breaks the contract.
type flaw int

const (
	// The conditional writes read, then write in a second step.
	nonAtomicCreate flaw = iota
	nonAtomicAdvance
	nonAtomicConfirm

	// A guard of a conditional write is missing.
	advanceUnconfirmed
	advanceSameCounter
	advanceAnyCounter
	confirmIgnoresSecret
	confirmEmptySecret
	confirmTwice
	confirmZeroCounter
	replaceConfirmed

	// Create keeps what it must ignore, or overwrites a taken login.
	createKeepsID
	createKeepsCounter
	createOverwrites

	// Letter case is handled differently by Create and FindByLogin.
	foldInCreateOnly
	foldEverywhere

	// A write reaches further than it should.
	setPasswordResetsCounter
	setPasswordAllAccounts
	setPasswordUnknownOK

	// The compare and set of a password hash is missing or split.
	nonAtomicSetPassword
	setPasswordUnconditional
	setPasswordOnEmpty

	// FindByLogin answers an unknown login with a plain error.
	findOtherError

	// Disable does not stop a login.
	disableNoop
)

// broken is one broken store and the checks that must catch it.
type broken struct {
	name      string
	flaws     []flaw
	foldsCase bool
	mustFail  []string
}

func brokenStores() []broken {
	return []broken{
		{name: "non atomic create", flaws: []flaw{nonAtomicCreate}, mustFail: []string{"Create/ConcurrentSameLoginCreatesOne"}},
		{name: "non atomic create folding case", flaws: []flaw{nonAtomicCreate, foldEverywhere}, foldsCase: true, mustFail: []string{"Login/ConcurrentCaseVariants"}},
		{name: "non atomic advance", flaws: []flaw{nonAtomicAdvance}, mustFail: []string{"AdvanceTOTPCounter/ConcurrentSameCounterOnce", "AdvanceTOTPCounter/ConcurrentNeverMovesBack"}},
		{name: "non atomic confirm", flaws: []flaw{nonAtomicConfirm}, mustFail: []string{"ConfirmTOTP/ConcurrentOnce"}},
		{name: "advance on an unconfirmed secret", flaws: []flaw{advanceUnconfirmed}, mustFail: []string{"AdvanceTOTPCounter/RefusesUnconfirmed", "Create/IgnoresIDAndCounter"}},
		{name: "advance to the same counter", flaws: []flaw{advanceSameCounter}, mustFail: []string{"AdvanceTOTPCounter/OnlyForward", "ConfirmTOTP/Guards"}},
		{name: "advance to any counter", flaws: []flaw{advanceAnyCounter}, mustFail: []string{"AdvanceTOTPCounter/OnlyForward"}},
		{name: "confirm ignores the secret", flaws: []flaw{confirmIgnoresSecret}, mustFail: []string{"ConfirmTOTP/Guards", "ReplaceTOTPSecret/OnlyUnconfirmed"}},
		{name: "confirm without a secret", flaws: []flaw{confirmEmptySecret}, mustFail: []string{"ConfirmTOTP/Guards"}},
		{name: "confirm twice", flaws: []flaw{confirmTwice}, mustFail: []string{"ConfirmTOTP/Guards"}},
		{name: "confirm with counter zero", flaws: []flaw{confirmZeroCounter}, mustFail: []string{"ConfirmTOTP/Guards"}},
		{name: "replace a confirmed secret", flaws: []flaw{replaceConfirmed}, mustFail: []string{"ReplaceTOTPSecret/OnlyUnconfirmed"}},
		{name: "create keeps the caller id", flaws: []flaw{createKeepsID}, mustFail: []string{"Create/IgnoresIDAndCounter"}},
		{name: "create keeps the caller counter", flaws: []flaw{createKeepsCounter}, mustFail: []string{"Create/IgnoresIDAndCounter"}},
		{name: "create overwrites a taken login", flaws: []flaw{createOverwrites}, mustFail: []string{"Create/RefusesTakenLogin", "Create/ConcurrentSameLoginCreatesOne"}},
		{name: "folds case in create only", flaws: []flaw{foldInCreateOnly}, foldsCase: true, mustFail: []string{"Login/CaseHandling"}},
		{name: "folds case without saying so", flaws: []flaw{foldEverywhere}, mustFail: []string{"Login/CaseHandling", "Login/ConcurrentCaseVariants"}},
		{name: "says it folds case but does not", foldsCase: true, mustFail: []string{"Login/CaseHandling", "Login/ConcurrentCaseVariants"}},
		{name: "set password resets the counter", flaws: []flaw{setPasswordResetsCounter}, mustFail: []string{"SetPasswordHash/OnlyTheHash"}},
		{name: "set password on every account", flaws: []flaw{setPasswordAllAccounts}, mustFail: []string{"Writes/StayInTheirAccount"}},
		{name: "set password of an unknown id", flaws: []flaw{setPasswordUnknownOK}, mustFail: []string{"SetPasswordHash/OnlyTheHash"}},
		{name: "non atomic set password", flaws: []flaw{nonAtomicSetPassword}, mustFail: []string{"SetPasswordHash/ConcurrentSameOldHashOnce"}},
		{name: "unconditional set password", flaws: []flaw{setPasswordUnconditional}, mustFail: []string{"SetPasswordHash/RefusesAStaleHash", "SetPasswordHash/RefusesAnAccountWithoutAPassword", "SetPasswordHash/ConcurrentSameOldHashOnce"}},
		{name: "set password on an account without one", flaws: []flaw{setPasswordOnEmpty}, mustFail: []string{"SetPasswordHash/RefusesAnAccountWithoutAPassword"}},
		{name: "unknown login is a plain error", flaws: []flaw{findOtherError}, mustFail: []string{"FindByLogin/UnknownLogin"}},
		{name: "disable does nothing", flaws: []flaw{disableNoop}, mustFail: []string{"Disable/CannotLogIn"}},
	}
}

// brokenStore is a map store with configurable flaws. Every map access holds
// the mutex, so a flaw is a broken contract and never a data race the race
// detector would report instead.
type brokenStore struct {
	flaws map[flaw]bool

	mu       sync.Mutex
	next     int
	byKey    map[string]account.Record
	disabled map[string]bool
}

var _ account.Store = (*brokenStore)(nil)

func newBrokenStore(flaws []flaw) *brokenStore {
	s := &brokenStore{
		flaws:    map[flaw]bool{},
		byKey:    map[string]account.Record{},
		disabled: map[string]bool{},
	}
	for _, f := range flaws {
		s.flaws[f] = true
	}

	return s
}

// window is the gap a non atomic write leaves between its read and its
// write, long enough for every racing call to read the same old value.
const window = 5 * time.Millisecond

func (s *brokenStore) findKey(login string) string {
	if s.flaws[foldEverywhere] {
		return strings.ToLower(login)
	}

	return login
}

func (s *brokenStore) createKey(login string) string {
	if s.flaws[foldEverywhere] || s.flaws[foldInCreateOnly] {
		return strings.ToLower(login)
	}

	return login
}

func (s *brokenStore) disable(id string) {
	if s.flaws[disableNoop] {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.disabled[id] = true
}

func (s *brokenStore) FindByLogin(_ context.Context, login string) (account.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.byKey[s.findKey(login)]
	if !ok || s.disabled[rec.ID] {
		if s.flaws[findOtherError] {
			return account.Record{}, errors.New("broken: no rows")
		}

		return account.Record{}, account.ErrNotFound
	}

	return rec, nil
}

func (s *brokenStore) Create(_ context.Context, rec account.Record) (account.Record, error) {
	key := s.createKey(rec.Login)

	s.mu.Lock()
	_, taken := s.byKey[key]
	s.mu.Unlock()

	if taken && !s.flaws[createOverwrites] {
		return account.Record{}, account.ErrConflict
	}

	if s.flaws[nonAtomicCreate] {
		time.Sleep(window)
	} else {
		s.mu.Lock()
		defer s.mu.Unlock()

		if _, taken := s.byKey[key]; taken && !s.flaws[createOverwrites] {
			return account.Record{}, account.ErrConflict
		}
	}

	return s.insert(key, rec), nil
}

// insert stores rec under key. It locks only when the caller does not hold
// the lock, which a non atomic create does not.
func (s *brokenStore) insert(key string, rec account.Record) account.Record {
	if s.flaws[nonAtomicCreate] {
		s.mu.Lock()
		defer s.mu.Unlock()
	}

	s.next++
	if !s.flaws[createKeepsID] || rec.ID == "" {
		rec.ID = "broken-" + strconv.Itoa(s.next)
	}
	if !s.flaws[createKeepsCounter] {
		rec.TOTPCounter = 0
	}

	s.byKey[key] = rec

	return rec
}

func (s *brokenStore) AdvanceTOTPCounter(_ context.Context, id string, counter int64) error {
	return s.update(id, s.flaws[nonAtomicAdvance], func(rec *account.Record) bool {
		switch {
		case s.flaws[advanceAnyCounter]:
		case rec.TOTPCounter <= 0 && !s.flaws[advanceUnconfirmed]:
			return false
		case s.flaws[advanceSameCounter] && counter < rec.TOTPCounter:
			return false
		case !s.flaws[advanceSameCounter] && counter <= rec.TOTPCounter:
			return false
		}

		rec.TOTPCounter = counter

		return true
	})
}

func (s *brokenStore) ConfirmTOTP(_ context.Context, id, secret string, counter int64) error {
	return s.update(id, s.flaws[nonAtomicConfirm], func(rec *account.Record) bool {
		if rec.TOTPSecret == "" && !s.flaws[confirmEmptySecret] {
			return false
		}
		if rec.TOTPSecret != secret && !s.flaws[confirmIgnoresSecret] {
			return false
		}
		if rec.TOTPCounter != 0 && !s.flaws[confirmTwice] {
			return false
		}
		if counter <= 0 && !s.flaws[confirmZeroCounter] {
			return false
		}

		rec.TOTPCounter = counter

		return true
	})
}

func (s *brokenStore) ReplaceTOTPSecret(_ context.Context, id, secret string) error {
	return s.update(id, false, func(rec *account.Record) bool {
		if rec.TOTPCounter != 0 && !s.flaws[replaceConfirmed] {
			return false
		}

		rec.TOTPSecret = secret

		return true
	})
}

func (s *brokenStore) SetPasswordHash(_ context.Context, id, oldHash, newHash string) error {
	if s.flaws[setPasswordAllAccounts] {
		s.mu.Lock()
		defer s.mu.Unlock()

		for key, rec := range s.byKey {
			rec.PasswordHash = newHash
			s.byKey[key] = rec
		}

		return nil
	}

	err := s.update(id, s.flaws[nonAtomicSetPassword], func(rec *account.Record) bool {
		if !s.flaws[setPasswordUnconditional] {
			if rec.PasswordHash == "" && !s.flaws[setPasswordOnEmpty] {
				return false
			}
			if rec.PasswordHash != oldHash {
				return false
			}
		}

		rec.PasswordHash = newHash
		if s.flaws[setPasswordResetsCounter] {
			rec.TOTPCounter = 0
		}

		return true
	})
	if err != nil && s.flaws[setPasswordUnknownOK] {
		return nil
	}

	return err
}

// update applies change to the account with id. A non atomic update copies
// the record under the lock, decides and changes the copy outside it, and
// writes the copy back under the lock after the window: read, then write.
func (s *brokenStore) update(id string, nonAtomic bool, change func(*account.Record) bool) error {
	s.mu.Lock()
	key, rec, ok := s.byIDLocked(id)
	if !nonAtomic {
		defer s.mu.Unlock()

		if !ok || !change(&rec) {
			return account.ErrNotFound
		}

		s.byKey[key] = rec

		return nil
	}
	s.mu.Unlock()

	if !ok || !change(&rec) {
		return account.ErrNotFound
	}

	time.Sleep(window)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.byKey[key] = rec

	return nil
}

func (s *brokenStore) byIDLocked(id string) (string, account.Record, bool) {
	for key, rec := range s.byKey {
		if rec.ID == id {
			return key, rec, true
		}
	}

	return "", account.Record{}, false
}
