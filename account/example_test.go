// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/ostendorf-cloud/govue/account"
)

// mapStore is the smallest store that keeps the contract, one map behind one
// mutex. A database adapter keeps it with a unique index and a conditional
// UPDATE instead. It is an illustration, not the conformance fake.
type mapStore struct {
	mu      sync.Mutex
	next    int
	byLogin map[string]account.Record
}

var _ account.Store = (*mapStore)(nil)

func newMapStore() *mapStore {
	return &mapStore{byLogin: map[string]account.Record{}}
}

func (s *mapStore) FindByLogin(_ context.Context, login string) (account.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.byLogin[login]
	if !ok {
		return account.Record{}, account.ErrNotFound
	}

	return rec, nil
}

func (s *mapStore) Create(_ context.Context, rec account.Record) (account.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, taken := s.byLogin[rec.Login]; taken {
		return account.Record{}, account.ErrConflict
	}

	// A caller supplied ID and counter are ignored: every account starts
	// unconfirmed under an id the store chose.
	s.next++
	rec.ID = "acct-" + strconv.Itoa(s.next)
	rec.TOTPCounter = 0
	s.byLogin[rec.Login] = rec

	return rec, nil
}

func (s *mapStore) AdvanceTOTPCounter(_ context.Context, id string, counter int64) error {
	return s.update(id, func(rec *account.Record) bool {
		if rec.TOTPCounter == 0 || counter <= rec.TOTPCounter {
			return false
		}

		rec.TOTPCounter = counter

		return true
	})
}

func (s *mapStore) ConfirmTOTP(_ context.Context, id string, counter int64) error {
	return s.update(id, func(rec *account.Record) bool {
		if rec.TOTPSecret == "" || rec.TOTPCounter != 0 || counter <= 0 {
			return false
		}

		rec.TOTPCounter = counter

		return true
	})
}

func (s *mapStore) ReplaceTOTPSecret(_ context.Context, id, secret string) error {
	return s.update(id, func(rec *account.Record) bool {
		if rec.TOTPCounter != 0 {
			return false
		}

		rec.TOTPSecret = secret

		return true
	})
}

func (s *mapStore) SetPasswordHash(_ context.Context, id, hash string) error {
	return s.update(id, func(rec *account.Record) bool {
		rec.PasswordHash = hash

		return true
	})
}

// update applies change to the account with id under the lock and answers
// ErrNotFound when there is no such account or change declines to write.
func (s *mapStore) update(id string, change func(*account.Record) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for login, rec := range s.byLogin {
		if rec.ID != id {
			continue
		}

		if !change(&rec) {
			return account.ErrNotFound
		}

		s.byLogin[login] = rec

		return nil
	}

	return account.ErrNotFound
}

// ExampleStore walks the store contract: a login is unique, an unconfirmed
// secret can be replaced, a secret is confirmed once by the counter of its
// first code, and a counter only moves forward, so a replayed code fails in
// the store rather than in a check before it.
func ExampleStore() {
	ctx := context.Background()

	var store account.Store = newMapStore()

	// Fake values only. The hash and the secrets are placeholders.
	rec, err := store.Create(ctx, account.Record{
		ID:           "chosen-by-caller",
		Login:        "alice@example.com",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$fake$fake",
		TOTPSecret:   "FAKESECRETFAKESECRETFAKESECRETFA",
		TOTPCounter:  99,
	})
	fmt.Println("create:", rec.ID, rec.TOTPCounter, err)

	_, err = store.Create(ctx, account.Record{Login: "alice@example.com"})
	fmt.Println("create again, conflict:", errors.Is(err, account.ErrConflict))

	err = store.AdvanceTOTPCounter(ctx, rec.ID, 100)
	fmt.Println("advance before confirm, refused:", errors.Is(err, account.ErrNotFound))

	fmt.Println("replace unconfirmed secret:", store.ReplaceTOTPSecret(ctx, rec.ID, "FAKEREPLACEDFAKEREPLACEDFAKEREPL"))

	fmt.Println("confirm:", store.ConfirmTOTP(ctx, rec.ID, 100))

	err = store.ReplaceTOTPSecret(ctx, rec.ID, "FAKEAGAINFAKEAGAINFAKEAGAINFAKEA")
	fmt.Println("replace confirmed secret, refused:", errors.Is(err, account.ErrNotFound))

	err = store.ConfirmTOTP(ctx, rec.ID, 101)
	fmt.Println("confirm again, refused:", errors.Is(err, account.ErrNotFound))

	err = store.AdvanceTOTPCounter(ctx, rec.ID, 100)
	fmt.Println("replay of the confirming code, refused:", errors.Is(err, account.ErrNotFound))

	fmt.Println("advance:", store.AdvanceTOTPCounter(ctx, rec.ID, 102))

	err = store.AdvanceTOTPCounter(ctx, rec.ID, 102)
	fmt.Println("same code again, refused:", errors.Is(err, account.ErrNotFound))

	_, err = store.FindByLogin(ctx, "bob@example.com")
	fmt.Println("unknown login:", errors.Is(err, account.ErrNotFound))

	// Output:
	// create: acct-1 0 <nil>
	// create again, conflict: true
	// advance before confirm, refused: true
	// replace unconfirmed secret: <nil>
	// confirm: <nil>
	// replace confirmed secret, refused: true
	// confirm again, refused: true
	// replay of the confirming code, refused: true
	// advance: <nil>
	// same code again, refused: true
	// unknown login: true
}
