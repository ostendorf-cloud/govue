// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package accounttest_test

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"

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

	// Found in review: a confirm that overwrites a confirmed counter when
	// the new one is higher, a lookup that reads the login as a LIKE
	// pattern, an empty old hash read as "do not compare", a counter that
	// may move back, and a replace that reaches every unconfirmed account.
	confirmForward
	findPattern
	setPasswordEmptyOldNoCompare
	advanceBackwards
	replaceAllUnconfirmed

	// Found in the second review: the reviewer's own read then write
	// stores, written apart from update, and a lookup that answers a
	// wildcard with an error instead of ErrNotFound.
	reviewAdvanceNoWindow
	reviewSetPasswordNoWindow
	findPatternError
)

// broken is one broken store and the checks that must catch it.
type broken struct {
	name      string
	flaws     []flaw
	foldsCase bool
	mustFail  []string

	// oneProcessor runs the child with -test.cpu=1, GOMAXPROCS 1, as a
	// product CI on a one core container would. The suite must still
	// catch the store.
	oneProcessor bool
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
		{name: "confirm overwrites a confirmed counter moving forward", flaws: []flaw{confirmForward}, mustFail: []string{"ConfirmTOTP/Guards"}},
		{name: "folds case in create only, declared not folding", flaws: []flaw{foldInCreateOnly}, mustFail: []string{"Login/CaseHandling"}},
		{name: "find by login as a LIKE pattern", flaws: []flaw{findPattern}, mustFail: []string{"FindByLogin/ExactMatch"}},
		{name: "empty old hash skips the compare", flaws: []flaw{setPasswordEmptyOldNoCompare}, mustFail: []string{"SetPasswordHash/RefusesAStaleHash"}},
		{name: "counter moves backwards", flaws: []flaw{advanceBackwards}, mustFail: []string{"AdvanceTOTPCounter/OnlyForward"}},
		{name: "replace reaches every unconfirmed account", flaws: []flaw{replaceAllUnconfirmed}, mustFail: []string{"Writes/StayInTheirAccount"}},
		{name: "review advance read then write", flaws: []flaw{reviewAdvanceNoWindow}, mustFail: []string{"AdvanceTOTPCounter/ConcurrentSameCounterOnce"}},
		{name: "review set password read then write", flaws: []flaw{reviewSetPasswordNoWindow}, mustFail: []string{"SetPasswordHash/ConcurrentSameOldHashOnce"}},
		{name: "review advance read then write on one processor", flaws: []flaw{reviewAdvanceNoWindow}, mustFail: []string{"AdvanceTOTPCounter/ConcurrentSameCounterOnce"}, oneProcessor: true},
		{name: "review set password read then write on one processor", flaws: []flaw{reviewSetPasswordNoWindow}, mustFail: []string{"SetPasswordHash/ConcurrentSameOldHashOnce"}, oneProcessor: true},
		{name: "non atomic create on one processor", flaws: []flaw{nonAtomicCreate}, mustFail: []string{"Create/ConcurrentSameLoginCreatesOne"}, oneProcessor: true},
		{name: "non atomic confirm on one processor", flaws: []flaw{nonAtomicConfirm}, mustFail: []string{"ConfirmTOTP/ConcurrentOnce"}, oneProcessor: true},
		{name: "find answers a wildcard with an error", flaws: []flaw{findPatternError}, mustFail: []string{"FindByLogin/ExactMatch"}},
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
	if !ok && s.flaws[findPatternError] && strings.ContainsAny(login, "%_") {
		return account.Record{}, errors.New("broken: invalid pattern")
	}
	if !ok && s.flaws[findPattern] {
		rec, ok = s.likeLocked(login)
	}
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

	// A non atomic create inserts in a second step, with nothing between the
	// check and the insert but releasing and taking the lock.
	if !s.flaws[nonAtomicCreate] {
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
	if s.flaws[reviewAdvanceNoWindow] {
		return s.advanceNoWindow(id, counter)
	}

	return s.update(id, s.flaws[nonAtomicAdvance], func(rec *account.Record) bool {
		switch {
		case s.flaws[advanceAnyCounter]:
		case rec.TOTPCounter <= 0 && !s.flaws[advanceUnconfirmed]:
			return false
		case s.flaws[advanceBackwards]:
			// Refuses zero, negative and the same counter, but takes a
			// lower positive one: the counter moves back.
			if counter <= 0 || counter == rec.TOTPCounter {
				return false
			}
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
		if rec.TOTPCounter != 0 && !s.flaws[confirmTwice] && (!s.flaws[confirmForward] || counter <= rec.TOTPCounter) {
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
	if s.flaws[replaceAllUnconfirmed] {
		s.mu.Lock()
		defer s.mu.Unlock()

		for key, rec := range s.byKey {
			if rec.TOTPCounter == 0 {
				rec.TOTPSecret = secret
				s.byKey[key] = rec
			}
		}

		return nil
	}

	return s.update(id, false, func(rec *account.Record) bool {
		if rec.TOTPCounter != 0 && !s.flaws[replaceConfirmed] {
			return false
		}

		rec.TOTPSecret = secret

		return true
	})
}

func (s *brokenStore) SetPasswordHash(_ context.Context, id, oldHash, newHash string) error {
	if s.flaws[reviewSetPasswordNoWindow] {
		return s.setPasswordNoWindow(id, oldHash, newHash)
	}

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
		if !s.flaws[setPasswordUnconditional] && (!s.flaws[setPasswordEmptyOldNoCompare] || oldHash != "") {
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
// writes the copy back under the lock: read, then write. Nothing but
// releasing and taking the lock sits between the two, no sleep, so the race
// is as narrow as in a real adapter that reads and then writes.
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

	s.mu.Lock()
	defer s.mu.Unlock()

	s.byKey[key] = rec

	return nil
}

// advanceNoWindow reads the counter under the lock, compares outside it and
// writes under the lock again, with no sleep in between. Written by the
// reviewer, independent of update.
func (s *brokenStore) advanceNoWindow(id string, counter int64) error {
	s.mu.Lock()
	key, rec, ok := s.byIDLocked(id)
	s.mu.Unlock()

	if !ok || rec.TOTPCounter <= 0 || counter <= rec.TOTPCounter {
		return account.ErrNotFound
	}

	s.mu.Lock()
	cur := s.byKey[key]
	cur.TOTPCounter = counter
	s.byKey[key] = cur
	s.mu.Unlock()

	return nil
}

// setPasswordNoWindow is the same read, compare, write for the hash.
func (s *brokenStore) setPasswordNoWindow(id, oldHash, newHash string) error {
	s.mu.Lock()
	key, rec, ok := s.byIDLocked(id)
	s.mu.Unlock()

	if !ok || rec.PasswordHash == "" || rec.PasswordHash != oldHash {
		return account.ErrNotFound
	}

	s.mu.Lock()
	cur := s.byKey[key]
	cur.PasswordHash = newHash
	s.byKey[key] = cur
	s.mu.Unlock()

	return nil
}

// likeLocked matches login as a SQL LIKE pattern, % any run, _ one
// character, the way an adapter written as WHERE email ILIKE $1 does.
func (s *brokenStore) likeLocked(pattern string) (account.Record, bool) {
	var re strings.Builder
	re.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '%':
			re.WriteString(".*")
		case '_':
			re.WriteString(".")
		default:
			re.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	re.WriteString("$")

	rx := regexp.MustCompile(re.String())
	for key, rec := range s.byKey {
		if rx.MatchString(key) {
			return rec, true
		}
	}

	return account.Record{}, false
}

func (s *brokenStore) byIDLocked(id string) (string, account.Record, bool) {
	for key, rec := range s.byKey {
		if rec.ID == id {
			return key, rec, true
		}
	}

	return "", account.Record{}, false
}
