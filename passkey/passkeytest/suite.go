// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package passkeytest proves that a [passkey.Store] keeps the contract the
// passkey package relies on, and holds [MemoryStore], an in memory store for
// tests.
//
// Every product's adapter runs [Run] against its own storage:
//
//	func TestPasskeyStoreConformance(t *testing.T) {
//		passkeytest.Run(t, passkeytest.Harness{
//			NewStore:   func(t *testing.T) passkey.Store { return newTestAdapter(t) },
//			NewAccount: createTestAccount,
//			Disable:    disableAccount,
//		})
//	}
//
// The guards it checks are the ones the handlers do not repeat: a credential
// id is unique across accounts, an account keeps one user handle, also under
// racing first registrations, a user handle matches only itself, the sign
// count update is a compare and set, a delete never reaches another account,
// and the last credential survives racing deletes when it has to. The race
// checks are a safety net, as in accounttest: what proves a SQL adapter
// atomic is its statement, checked by reading it.
package passkeytest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/passkey"
)

// DefaultRacers and DefaultRounds size the race checks when the [Harness]
// leaves them zero.
const (
	DefaultRacers = 32
	DefaultRounds = 200
)

// Harness is what [Run] needs from an adapter.
type Harness struct {
	// NewStore returns the store under test. Required. It may return the
	// same store for every call, since every check makes its own accounts.
	NewStore func(t *testing.T) passkey.Store

	// NewAccount creates an account the store can answer FindByUserHandle
	// for, and returns its Identity. Required.
	NewAccount func(t *testing.T, store passkey.Store) account.Identity

	// Disable holds an account back from login, the way the product refuses
	// a disabled account. Required.
	Disable func(t *testing.T, store passkey.Store, accountID string)

	// Racers and Rounds size the race checks. Zero is the default.
	Racers, Rounds int
}

// Run runs every check against the store h builds.
func Run(t *testing.T, h Harness) {
	t.Helper()
	if h.NewStore == nil || h.NewAccount == nil || h.Disable == nil {
		t.Fatal("passkeytest: NewStore, NewAccount and Disable are required")
	}
	if h.Racers == 0 {
		h.Racers = DefaultRacers
	}
	if h.Rounds == 0 {
		h.Rounds = DefaultRounds
	}

	checks := []struct {
		name string
		run  func(*testing.T, Harness)
	}{
		{"AddAndList", checkAddAndList},
		{"AddRefusesTakenID", checkAddRefusesTakenID},
		{"AddRefusesSecondHandle", checkAddRefusesSecondHandle},
		{"AddConcurrentFirstHandleOnce", checkAddConcurrentFirstHandle},
		{"CredentialsOfUnknownAccount", checkCredentialsUnknown},
		{"FindByUserHandleExact", checkFindExact},
		{"FindByUserHandleDisabled", checkFindDisabled},
		{"UpdateIsCompareAndSet", checkUpdateCompareAndSet},
		{"UpdateStaysInAccount", checkUpdateStaysInAccount},
		{"UpdateConcurrentOnce", checkUpdateConcurrentOnce},
		{"DeleteStaysInAccount", checkDeleteStaysInAccount},
		{"DeleteKeepOne", checkDeleteKeepOne},
		{"DeleteKeepOneConcurrent", checkDeleteKeepOneConcurrent},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) { c.run(t, h) })
	}
}

var seq atomic.Uint64

// newCredential returns a credential with fresh random looking ids.
func newCredential(accountID string, handle []byte) passkey.Credential {
	n := seq.Add(1)
	return passkey.Credential{
		ID:         fmt.Appendf(nil, "credential-%d-%d", time.Now().UnixNano(), n),
		AccountID:  accountID,
		UserHandle: handle,
		Name:       fmt.Sprintf("key %d", n),
		SignCount:  0,
		Data:       []byte(`{"fake":true}`),
		CreatedAt:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func newHandle() []byte {
	return fmt.Appendf(nil, "handle-%032d", seq.Add(1))
}

func add(t *testing.T, store passkey.Store, c passkey.Credential) {
	t.Helper()
	if err := store.AddCredential(context.Background(), c); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
}

func credentials(t *testing.T, store passkey.Store, accountID string) []passkey.Credential {
	t.Helper()
	got, err := store.Credentials(context.Background(), accountID)
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}

	return got
}

func wantNotFound(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, passkey.ErrNotFound) {
		t.Fatalf("%s: got %v, want passkey.ErrNotFound", what, err)
	}
}

func checkAddAndList(t *testing.T, h Harness) {
	store := h.NewStore(t)
	id := h.NewAccount(t, store)
	handle := newHandle()
	first, second := newCredential(id.ID, handle), newCredential(id.ID, handle)
	add(t, store, first)
	add(t, store, second)

	got := credentials(t, store, id.ID)
	if len(got) != 2 {
		t.Fatalf("Credentials: got %d, want 2", len(got))
	}
	for i, want := range []passkey.Credential{first, second} {
		c := got[i]
		if !bytes.Equal(c.ID, want.ID) || c.AccountID != want.AccountID || !bytes.Equal(c.UserHandle, want.UserHandle) ||
			c.Name != want.Name || c.SignCount != want.SignCount || !bytes.Equal(c.Data, want.Data) ||
			!c.CreatedAt.Equal(want.CreatedAt) || !c.LastUsedAt.IsZero() {
			t.Fatalf("Credentials[%d]: got %+v, want %+v, oldest first", i, c, want)
		}
	}
}

func checkAddRefusesTakenID(t *testing.T, h Harness) {
	store := h.NewStore(t)
	a, b := h.NewAccount(t, store), h.NewAccount(t, store)
	c := newCredential(a.ID, newHandle())
	add(t, store, c)

	taken := newCredential(b.ID, newHandle())
	taken.ID = c.ID
	if err := store.AddCredential(context.Background(), taken); !errors.Is(err, passkey.ErrConflict) {
		t.Fatalf("AddCredential with a taken id: got %v, want passkey.ErrConflict", err)
	}
	if got := credentials(t, store, b.ID); len(got) != 0 {
		t.Fatalf("the refused credential was stored for the other account: %+v", got)
	}
}

// checkAddRefusesSecondHandle: an account has one user handle. A credential
// with another handle is refused, and the account keeps finding its passkeys
// through the first.
func checkAddRefusesSecondHandle(t *testing.T, h Harness) {
	store := h.NewStore(t)
	id := h.NewAccount(t, store)
	first := newHandle()
	add(t, store, newCredential(id.ID, first))

	second := newHandle()
	if err := store.AddCredential(context.Background(), newCredential(id.ID, second)); !errors.Is(err, passkey.ErrConflict) {
		t.Fatalf("AddCredential with a second handle for one account: got %v, want passkey.ErrConflict", err)
	}
	if got := credentials(t, store, id.ID); len(got) != 1 {
		t.Fatalf("the refused credential was stored: %d credentials", len(got))
	}
	_, _, err := store.FindByUserHandle(context.Background(), second)
	wantNotFound(t, "FindByUserHandle of the refused handle", err)

	// The same handle is still accepted.
	add(t, store, newCredential(id.ID, first))
}

// checkAddConcurrentFirstHandle: first registrations racing from several
// browsers each bring their own handle. Exactly one handle wins.
func checkAddConcurrentFirstHandle(t *testing.T, h Harness) {
	store := h.NewStore(t)
	for round := range h.Rounds {
		id := h.NewAccount(t, store)
		creds := make([]passkey.Credential, h.Racers)
		for i := range creds {
			creds[i] = newCredential(id.ID, newHandle())
		}
		errs := race(h.Racers, func(i int) error {
			return store.AddCredential(context.Background(), creds[i])
		})
		ok := 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case !errors.Is(err, passkey.ErrConflict):
				t.Fatalf("unexpected error: %v", err)
			}
		}
		got := credentials(t, store, id.ID)
		if ok != 1 || len(got) != 1 {
			t.Fatalf("round %d: %d of %d racing first registrations with their own handles succeeded, %d stored, want exactly 1",
				round, ok, h.Racers, len(got))
		}
	}
}

func checkCredentialsUnknown(t *testing.T, h Harness) {
	store := h.NewStore(t)
	if got := credentials(t, store, "passkeytest-unknown-account"); len(got) != 0 {
		t.Fatalf("Credentials of an unknown account: got %+v, want none", got)
	}
}

func checkFindExact(t *testing.T, h Harness) {
	store := h.NewStore(t)
	ctx := context.Background()
	id := h.NewAccount(t, store)
	handle := newHandle()
	add(t, store, newCredential(id.ID, handle))

	got, creds, err := store.FindByUserHandle(ctx, handle)
	if err != nil {
		t.Fatalf("FindByUserHandle: %v", err)
	}
	if got != id {
		t.Fatalf("FindByUserHandle: got %+v, want %+v", got, id)
	}
	if len(creds) != 1 {
		t.Fatalf("FindByUserHandle: got %d credentials, want 1", len(creds))
	}

	for name, probe := range map[string][]byte{
		"prefix":    handle[:len(handle)-1],
		"extension": append(bytes.Clone(handle), 'x'),
		"empty":     {},
		"unknown":   newHandle(),
	} {
		_, _, err := store.FindByUserHandle(ctx, probe)
		wantNotFound(t, "FindByUserHandle "+name, err)
	}
}

func checkFindDisabled(t *testing.T, h Harness) {
	store := h.NewStore(t)
	id := h.NewAccount(t, store)
	handle := newHandle()
	add(t, store, newCredential(id.ID, handle))
	h.Disable(t, store, id.ID)

	_, _, err := store.FindByUserHandle(context.Background(), handle)
	wantNotFound(t, "FindByUserHandle of a disabled account", err)
}

func checkUpdateCompareAndSet(t *testing.T, h Harness) {
	store := h.NewStore(t)
	ctx := context.Background()
	id := h.NewAccount(t, store)
	c := newCredential(id.ID, newHandle())
	add(t, store, c)

	next := c
	next.SignCount = 7
	next.LastUsedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	next.Data = []byte(`{"fake":2}`)
	wantNotFound(t, "UpdateCredential from a stale count", store.UpdateCredential(ctx, next, 3))
	if got := credentials(t, store, id.ID)[0]; got.SignCount != 0 {
		t.Fatalf("a refused update wrote: %+v", got)
	}

	if err := store.UpdateCredential(ctx, next, 0); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}
	got := credentials(t, store, id.ID)[0]
	if got.SignCount != 7 || !got.LastUsedAt.Equal(next.LastUsedAt) || !bytes.Equal(got.Data, next.Data) ||
		got.Name != c.Name || !got.CreatedAt.Equal(c.CreatedAt) {
		t.Fatalf("after UpdateCredential: got %+v", got)
	}
	wantNotFound(t, "UpdateCredential from the old count again", store.UpdateCredential(ctx, next, 0))
}

func checkUpdateStaysInAccount(t *testing.T, h Harness) {
	store := h.NewStore(t)
	a, b := h.NewAccount(t, store), h.NewAccount(t, store)
	c := newCredential(a.ID, newHandle())
	add(t, store, c)

	wrong := c
	wrong.AccountID = b.ID
	wrong.SignCount = 9
	wantNotFound(t, "UpdateCredential through another account", store.UpdateCredential(context.Background(), wrong, 0))
	if got := credentials(t, store, a.ID)[0]; got.SignCount != 0 {
		t.Fatalf("an update through another account wrote: %+v", got)
	}
}

func checkUpdateConcurrentOnce(t *testing.T, h Harness) {
	store := h.NewStore(t)
	id := h.NewAccount(t, store)
	handle := newHandle()
	for round := range h.Rounds {
		c := newCredential(id.ID, handle)
		add(t, store, c)
		errs := race(h.Racers, func(i int) error {
			next := c
			next.SignCount = uint32(i) + 1
			return store.UpdateCredential(context.Background(), next, 0)
		})
		if ok := successes(t, errs); ok != 1 {
			t.Fatalf("round %d: %d of %d racing updates from one count succeeded, want exactly 1", round, ok, h.Racers)
		}
	}
}

func checkDeleteStaysInAccount(t *testing.T, h Harness) {
	store := h.NewStore(t)
	ctx := context.Background()
	a, b := h.NewAccount(t, store), h.NewAccount(t, store)
	c := newCredential(a.ID, newHandle())
	add(t, store, c)

	wantNotFound(t, "DeleteCredential through another account", store.DeleteCredential(ctx, b.ID, c.ID, false))
	if got := credentials(t, store, a.ID); len(got) != 1 {
		t.Fatalf("a delete through another account removed the credential")
	}
	wantNotFound(t, "DeleteCredential of an unknown id", store.DeleteCredential(ctx, a.ID, []byte("passkeytest-unknown"), false))
	if err := store.DeleteCredential(ctx, a.ID, c.ID, false); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	if got := credentials(t, store, a.ID); len(got) != 0 {
		t.Fatalf("after DeleteCredential: got %+v", got)
	}
}

func checkDeleteKeepOne(t *testing.T, h Harness) {
	store := h.NewStore(t)
	ctx := context.Background()
	id := h.NewAccount(t, store)
	handle := newHandle()
	first, second := newCredential(id.ID, handle), newCredential(id.ID, handle)
	add(t, store, first)
	add(t, store, second)

	if err := store.DeleteCredential(ctx, id.ID, first.ID, true); err != nil {
		t.Fatalf("DeleteCredential with keepOne and two credentials: %v", err)
	}
	wantNotFound(t, "DeleteCredential of the last one with keepOne", store.DeleteCredential(ctx, id.ID, second.ID, true))
	if got := credentials(t, store, id.ID); len(got) != 1 {
		t.Fatalf("the last credential was deleted with keepOne")
	}
}

func checkDeleteKeepOneConcurrent(t *testing.T, h Harness) {
	store := h.NewStore(t)
	for round := range h.Rounds {
		id := h.NewAccount(t, store)
		handle := newHandle()
		creds := make([]passkey.Credential, h.Racers)
		for i := range creds {
			creds[i] = newCredential(id.ID, handle)
			add(t, store, creds[i])
		}
		race(h.Racers, func(i int) error {
			return store.DeleteCredential(context.Background(), id.ID, creds[i].ID, true)
		})
		if got := credentials(t, store, id.ID); len(got) != 1 {
			t.Fatalf("round %d: %d racing deletes with keepOne left %d credentials, want exactly 1", round, h.Racers, len(got))
		}
	}
}

// race runs call racers times at once behind a start barrier, with at least
// four processors, and returns every error in call order.
func race(racers int, call func(i int) error) []error {
	if procs := runtime.GOMAXPROCS(0); procs < 4 {
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(4))
	}

	errs := make([]error, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			errs[i] = call(i)
		})
	}
	close(start)
	wg.Wait()

	return errs
}

// successes counts nil errors and fails on an error other than ErrNotFound.
func successes(t *testing.T, errs []error) int {
	t.Helper()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, passkey.ErrNotFound):
			t.Fatalf("unexpected error: %v", err)
		}
	}

	return ok
}
