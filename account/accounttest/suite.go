// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package accounttest proves that an [account.Store] keeps the contract the
// account package relies on, and holds [MemoryStore], an in memory store for
// tests.
//
// The contract is public. Every product's adapter runs [Run] against its own
// storage, for example a test database, in its own test suite:
//
//	func TestStoreConformance(t *testing.T) {
//		accounttest.Run(t, accounttest.Harness{
//			NewStore:  func(t *testing.T) account.Store { return newTestAdapter(t) },
//			FoldsCase: true,
//			Disable:   disableAccount,
//		})
//	}
//
// The guards it checks are the ones the login logic does not repeat: the
// conditional counter write that refuses a replayed TOTP code, the guards of
// TOTP confirmation, the compare and set of a password hash that keeps a
// concurrent password change from being lost, the unique login, also under
// concurrent signups, that a login matches only itself, and that a disabled
// account cannot be found for login. A store that fails one of them breaks a
// security property of every login built on it.
//
// # What the concurrency checks can and cannot prove
//
// The concurrency checks start [DefaultRacers] calls at once behind a start
// barrier, count the successes, and repeat that [DefaultRounds] times. A
// store that reads, compares in Go and then writes passes a single round
// most of the time: its window is a few instructions. Over many rounds it
// fails. Measured on one machine, such stores escaped none of 200 runs
// with the defaults, with and without the race detector, against up to 178
// of 200 with the earlier 16 racers and 10 rounds.
//
// That is a measurement, not a proof. A test can only show a race it hits.
// Another scheduler, fewer cores or a store with a different latency may
// hit it less often. Load lowers it as well: next to CPU heavy tests in the
// same go test run, such a store escaped the defaults in one of ten runs.
// An adapter whose window is that narrow, one that keeps its state in Go
// memory, raises [Harness.Rounds]. A SQL adapter that reads and then writes
// has a window of a round trip and is far easier to catch. The suite is a
// safety net. What proves a SQL adapter
// atomic is its statement: one conditional UPDATE whose WHERE clause holds
// the comparison, for example
//
//	UPDATE accounts SET totp_counter = $2 WHERE id = $1 AND totp_counter > 0 AND totp_counter < $2
//
// checked by reading it. Run the suite with the race detector as well, go
// test -race, which also reports unsynchronised memory access in an adapter
// that caches in Go.
package accounttest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ostendorf-cloud/govue/account"
)

// Harness describes the store under test.
type Harness struct {
	// NewStore returns the store a check runs against. It may return a fresh
	// store or the same one every time: every check uses logins of its own,
	// with a random part, so checks do not see each other's accounts.
	// Required.
	NewStore func(t *testing.T) account.Store

	// FoldsCase declares that the store treats logins as case insensitive,
	// as most stores keyed by an email address do. With it, a login that
	// differs only in letter case must find the existing account and
	// conflict with it. Without it, such a login must be a different
	// account. Either way FindByLogin and Create must agree.
	FoldsCase bool

	// Disable disables the account with id the way the consumer does it, for
	// example by setting a status column. Afterwards FindByLogin must answer
	// account.ErrNotFound or a record with an empty PasswordHash. Required:
	// a store with no way to disable an account still passes a function
	// that clears the hash.
	Disable func(t *testing.T, store account.Store, id string)

	// Racers is how many calls each concurrency check starts at once. Zero
	// is [DefaultRacers]. A value below two is refused, since one call
	// cannot race.
	Racers int

	// Rounds is how often each concurrency check repeats its race. Zero is
	// [DefaultRounds]. Negative is refused. Lower it for a slow store only
	// when its atomicity is proven otherwise, for example by a WHERE clause
	// that compares and writes in one statement: fewer rounds catch a read
	// then write store less often.
	Rounds int
}

// DefaultRacers is how many calls each concurrency check starts at once when
// [Harness.Racers] is zero, and DefaultRounds how often it repeats the race
// when [Harness.Rounds] is zero.
//
// They were chosen by measurement, not by argument. Stores that read,
// compare in Go and write, with nothing between but taking a lock again,
// were run through their race check 200 times each, with and without the
// race detector, on a 15 core darwin/arm64 machine. With 16 racers and 10
// rounds they escaped up to 178 of 200 runs. With 32 racers and 200 rounds
// one still escaped once in 200 runs. With these defaults they were caught
// in every run. Without the start barrier in the race helper, 200 rounds
// let one escape in 63 of 200 runs. Another machine, scheduler or store
// latency changes these numbers, which is why the check is a safety net and
// not a proof; see the package doc. The whole suite takes about half a
// second against [MemoryStore], a few seconds under the race detector.
const (
	DefaultRacers = 32
	DefaultRounds = 1000
)

func (h Harness) racers() int {
	if h.Racers == 0 {
		return DefaultRacers
	}

	return h.Racers
}

func (h Harness) rounds() int {
	if h.Rounds == 0 {
		return DefaultRounds
	}

	return h.Rounds
}

// Fake values only. The store keeps them as given and never interprets them.
const (
	fakeHash        = "$argon2id$v=19$m=65536,t=3,p=4$ZmFrZS1zYWx0$ZmFrZS1oYXNoLW5vdC1hLXJlYWwtb25l"
	fakeOtherHash   = "$argon2id$v=19$m=65536,t=3,p=4$ZmFrZS1vdGhlcg$ZmFrZS1vdGhlci1oYXNoLW5vdC1yZWFs"
	fakeSecret      = "FAKESECRETFAKESECRETFAKESECRETFA"
	fakeOtherSecret = "FAKEOTHERFAKEOTHERFAKEOTHERFAKEO"
)

// Run checks the store h describes against the account.Store contract, each
// guard as its own subtest. It fails t for every guard the store breaks.
func Run(t *testing.T, h Harness) {
	t.Helper()

	if h.NewStore == nil || h.Disable == nil {
		t.Fatal("accounttest: Harness.NewStore and Harness.Disable are required")
	}
	if h.Racers < 0 || h.Racers == 1 || h.Rounds < 0 {
		t.Fatalf("accounttest: Harness.Racers %d and Harness.Rounds %d: want zero for the default, at least two racers and a positive number of rounds", h.Racers, h.Rounds)
	}

	checks := []struct {
		name string
		run  func(*testing.T, Harness)
	}{
		{"Create/IgnoresIDAndCounter", checkCreateIgnoresIDAndCounter},
		{"Create/StoresFieldsAsGiven", checkCreateStoresFields},
		{"Create/RefusesTakenLogin", checkCreateRefusesTaken},
		{"Create/ConcurrentSameLoginCreatesOne", checkCreateConcurrent},
		{"Login/CaseHandling", checkCaseHandling},
		{"Login/ConcurrentCaseVariants", checkConcurrentCaseVariants},
		{"FindByLogin/UnknownLogin", checkFindUnknown},
		{"FindByLogin/ExactMatch", checkFindExact},
		{"AdvanceTOTPCounter/RefusesUnconfirmed", checkAdvanceRefusesUnconfirmed},
		{"AdvanceTOTPCounter/OnlyForward", checkAdvanceOnlyForward},
		{"AdvanceTOTPCounter/UnknownID", checkAdvanceUnknownID},
		{"AdvanceTOTPCounter/ConcurrentSameCounterOnce", checkAdvanceConcurrentOnce},
		{"AdvanceTOTPCounter/ConcurrentNeverMovesBack", checkAdvanceNeverBack},
		{"ConfirmTOTP/Guards", checkConfirmGuards},
		{"ConfirmTOTP/ConcurrentOnce", checkConfirmConcurrentOnce},
		{"ReplaceTOTPSecret/OnlyUnconfirmed", checkReplaceOnlyUnconfirmed},
		{"SetPasswordHash/OnlyTheHash", checkSetPasswordHash},
		{"SetPasswordHash/RefusesAStaleHash", checkSetPasswordHashRefusesStale},
		{"SetPasswordHash/RefusesAnAccountWithoutAPassword", checkSetPasswordHashRefusesNoPassword},
		{"SetPasswordHash/ConcurrentSameOldHashOnce", checkSetPasswordHashConcurrentOnce},
		{"Writes/StayInTheirAccount", checkWritesStayInAccount},
		{"Disable/CannotLogIn", checkDisable},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, h)
		})
	}
}

// uniqueLogin returns a login no other check uses: name, a random part and a
// fixed domain.
func uniqueLogin(t *testing.T, name string) string {
	t.Helper()

	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("read random login part: %v", err)
	}

	return name + "-" + hex.EncodeToString(raw[:]) + "@example.com"
}

// create stores a new, unconfirmed account with fake values and fails t on
// error.
func create(t *testing.T, store account.Store, login string) account.Record {
	t.Helper()

	rec, err := store.Create(t.Context(), account.Record{Login: login, PasswordHash: fakeHash, TOTPSecret: fakeSecret})
	if err != nil {
		t.Fatalf("Create(%q): %v", login, err)
	}
	if rec.ID == "" {
		t.Fatalf("Create(%q) returned an empty ID", login)
	}

	return rec
}

// confirmedCounter is the counter createConfirmed confirms a secret at.
const confirmedCounter = 100

// createConfirmed stores a new account and confirms its secret at
// confirmedCounter.
func createConfirmed(t *testing.T, store account.Store, login string) account.Record {
	t.Helper()

	rec := create(t, store, login)
	if err := store.ConfirmTOTP(t.Context(), rec.ID, fakeSecret, confirmedCounter); err != nil {
		t.Fatalf("ConfirmTOTP(%q): %v", login, err)
	}

	rec.TOTPCounter = confirmedCounter

	return rec
}

// find looks login up and fails t on error.
func find(t *testing.T, store account.Store, login string) account.Record {
	t.Helper()

	rec, err := store.FindByLogin(t.Context(), login)
	if err != nil {
		t.Fatalf("FindByLogin(%q): %v", login, err)
	}

	return rec
}

// wantNotFound fails t unless err is account.ErrNotFound.
func wantNotFound(t *testing.T, what string, err error) {
	t.Helper()

	if !errors.Is(err, account.ErrNotFound) {
		t.Errorf("%s = %v, want account.ErrNotFound", what, err)
	}
}

// race runs call h.racers() times at once, h.rounds() times over, with a
// fresh setup value per round, and hands each round's results to check. It
// stops at the first round that fails.
//
// Every racer first checks in at a start barrier and spins, yielding, until
// all have checked in, so all of them are running when the first one calls.
// A channel close would wake them one after another instead, and a fast
// store would serve the first before the last was scheduled. The yield
// between rounds lets the scheduler spread the next round anew.
func race[T any](t *testing.T, h Harness, setup func(round int) T, call func(ctx context.Context, v T, i int) error, check func(round int, v T, errs []error)) {
	t.Helper()

	racers, rounds := h.racers(), h.rounds()

	for round := range rounds {
		v := setup(round)

		errs := make([]error, racers)

		var (
			wg      sync.WaitGroup
			arrived atomic.Int64
		)
		for i := range racers {
			wg.Go(func() {
				arrived.Add(1)
				for arrived.Load() < int64(racers) {
					runtime.Gosched()
				}

				errs[i] = call(t.Context(), v, i)
			})
		}

		wg.Wait()

		check(round, v, errs)
		if t.Failed() {
			return
		}

		runtime.Gosched()
	}
}

// countOutcomes counts nil errors and account.ErrNotFound or ErrConflict
// refusals, and fails t for any other error.
func countOutcomes(t *testing.T, errs []error, refusal error) (ok, refused int) {
	t.Helper()

	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, refusal):
			refused++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}

	return ok, refused
}

func checkCreateIgnoresIDAndCounter(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "ignore")

	rec, err := store.Create(t.Context(), account.Record{
		ID:           "chosen-by-caller",
		Login:        login,
		PasswordHash: fakeHash,
		TOTPSecret:   fakeSecret,
		TOTPCounter:  99,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.ID == "" || rec.ID == "chosen-by-caller" {
		t.Errorf("Create returned ID %q, want one the store chose", rec.ID)
	}
	if rec.TOTPCounter != 0 {
		t.Errorf("Create returned TOTPCounter %d, want 0", rec.TOTPCounter)
	}

	got := find(t, store, login)
	if got.ID != rec.ID {
		t.Errorf("FindByLogin ID = %q, want %q", got.ID, rec.ID)
	}
	if got.TOTPCounter != 0 {
		t.Errorf("stored TOTPCounter = %d, want 0: a new account must start unconfirmed", got.TOTPCounter)
	}

	// A caller supplied counter must not confirm the secret.
	wantNotFound(t, "AdvanceTOTPCounter on a new account", store.AdvanceTOTPCounter(t.Context(), rec.ID, 100))
}

func checkCreateStoresFields(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "fields")

	rec := create(t, store, login)

	got := find(t, store, login)
	if got.Login != login || got.PasswordHash != fakeHash || got.TOTPSecret != fakeSecret || got.ID != rec.ID {
		t.Errorf("FindByLogin = %+v, want the created login, hash, secret and id %q", got, rec.ID)
	}

	// An account without a password or a secret, one created through OIDC.
	bare := uniqueLogin(t, "bare")
	if _, err := store.Create(t.Context(), account.Record{Login: bare}); err != nil {
		t.Fatalf("Create without hash and secret: %v", err)
	}
	if got := find(t, store, bare); got.PasswordHash != "" || got.TOTPSecret != "" {
		t.Errorf("FindByLogin of a bare account = %+v, want an empty hash and secret", got)
	}
}

func checkCreateRefusesTaken(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "taken")

	first := create(t, store, login)

	_, err := store.Create(t.Context(), account.Record{Login: login, PasswordHash: fakeOtherHash, TOTPSecret: fakeOtherSecret})
	if !errors.Is(err, account.ErrConflict) {
		t.Fatalf("second Create = %v, want account.ErrConflict", err)
	}

	got := find(t, store, login)
	if got.ID != first.ID || got.PasswordHash != fakeHash || got.TOTPSecret != fakeSecret {
		t.Errorf("FindByLogin after a refused Create = %+v, want the first account unchanged", got)
	}
}

func checkCreateConcurrent(t *testing.T, h Harness) {
	store := h.NewStore(t)

	race(t, h,
		func(int) string { return uniqueLogin(t, "racing-signup") },
		func(ctx context.Context, login string, _ int) error {
			_, err := store.Create(ctx, account.Record{Login: login, PasswordHash: fakeHash, TOTPSecret: fakeSecret})
			return err
		},
		func(round int, _ string, errs []error) {
			if ok, refused := countOutcomes(t, errs, account.ErrConflict); ok != 1 || refused != len(errs)-1 {
				t.Errorf("round %d: %d of %d concurrent Create calls for one login succeeded, want exactly 1", round, ok, len(errs))
			}
		})
}

// variantOf returns login with its letter case changed.
func variantOf(login string) string {
	return strings.ToUpper(login[:1]) + login[1:strings.Index(login, "@")] + "@EXAMPLE.COM"
}

func checkCaseHandling(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "case")
	variant := variantOf(login)

	rec := create(t, store, login)

	found, findErr := store.FindByLogin(t.Context(), variant)
	_, createErr := store.Create(t.Context(), account.Record{Login: variant, PasswordHash: fakeOtherHash, TOTPSecret: fakeOtherSecret})

	if h.FoldsCase {
		if findErr != nil || found.ID != rec.ID {
			t.Errorf("FindByLogin(%q) = %+v, %v, want the account %q: the store folds case", variant, found, findErr, rec.ID)
		}
		if !errors.Is(createErr, account.ErrConflict) {
			t.Errorf("Create(%q) = %v, want account.ErrConflict: the store folds case", variant, createErr)
		}

		return
	}

	if !errors.Is(findErr, account.ErrNotFound) {
		t.Errorf("FindByLogin(%q) = %+v, %v, want account.ErrNotFound: the store does not fold case", variant, found, findErr)
	}
	if createErr != nil {
		t.Errorf("Create(%q) = %v, want a separate account: the store does not fold case", variant, createErr)
	}
	if got := find(t, store, login); got.ID != rec.ID {
		t.Errorf("FindByLogin(%q) = %q after creating a case variant, want %q", login, got.ID, rec.ID)
	}
}

func checkConcurrentCaseVariants(t *testing.T, h Harness) {
	store := h.NewStore(t)

	want := 1
	if !h.FoldsCase {
		want = 2
	}

	race(t, h,
		func(int) string { return uniqueLogin(t, "racing-case") },
		func(ctx context.Context, login string, i int) error {
			if i%2 == 1 {
				login = variantOf(login)
			}
			_, err := store.Create(ctx, account.Record{Login: login, PasswordHash: fakeHash, TOTPSecret: fakeSecret})
			return err
		},
		func(round int, _ string, errs []error) {
			if ok, _ := countOutcomes(t, errs, account.ErrConflict); ok != want {
				t.Errorf("round %d: %d concurrent Create calls for two case variants succeeded, want %d", round, ok, want)
			}
		})
}

func checkFindUnknown(t *testing.T, h Harness) {
	store := h.NewStore(t)

	_, err := store.FindByLogin(t.Context(), uniqueLogin(t, "unknown"))
	wantNotFound(t, "FindByLogin of an unknown login", err)
}

// checkFindExact refuses a lookup that matches more than the login itself:
// SQL LIKE wildcards, a prefix and a longer login must find nothing. An
// adapter written as WHERE login LIKE $1 would let % log in as anybody.
func checkFindExact(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "exact")
	create(t, store, login)

	at := strings.Index(login, "@")
	probes := []string{
		"%",
		"%" + login[at:],
		"_" + login[1:],
		login[:at] + "%",
		login[:len(login)-1],
		login + "x",
	}

	for _, probe := range probes {
		got, err := store.FindByLogin(t.Context(), probe)
		if !errors.Is(err, account.ErrNotFound) {
			t.Errorf("FindByLogin(%q) = %+v, %v, want account.ErrNotFound: a login matches only itself", probe, got, err)
		}
	}
}

func checkAdvanceRefusesUnconfirmed(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "unconfirmed")
	rec := create(t, store, login)

	for _, counter := range []int64{1, 100} {
		wantNotFound(t, "AdvanceTOTPCounter on an unconfirmed secret", store.AdvanceTOTPCounter(t.Context(), rec.ID, counter))
	}

	if got := find(t, store, login); got.TOTPCounter != 0 {
		t.Errorf("TOTPCounter = %d after refused advances, want 0: a login must never confirm a secret", got.TOTPCounter)
	}
}

func checkAdvanceOnlyForward(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "forward")
	rec := createConfirmed(t, store, login)

	for _, counter := range []int64{0, -1, 99, 100} {
		wantNotFound(t, "AdvanceTOTPCounter to a counter not above the stored one", store.AdvanceTOTPCounter(t.Context(), rec.ID, counter))
	}

	if err := store.AdvanceTOTPCounter(t.Context(), rec.ID, 101); err != nil {
		t.Fatalf("AdvanceTOTPCounter(101): %v", err)
	}

	wantNotFound(t, "AdvanceTOTPCounter with the same counter again", store.AdvanceTOTPCounter(t.Context(), rec.ID, 101))

	if got := find(t, store, login); got.TOTPCounter != 101 {
		t.Errorf("TOTPCounter = %d, want 101", got.TOTPCounter)
	}
}

func checkAdvanceUnknownID(t *testing.T, h Harness) {
	store := h.NewStore(t)

	wantNotFound(t, "AdvanceTOTPCounter of an unknown id", store.AdvanceTOTPCounter(t.Context(), "no-such-account-id", 100))
}

func checkAdvanceConcurrentOnce(t *testing.T, h Harness) {
	store := h.NewStore(t)

	race(t, h,
		func(int) account.Record { return createConfirmed(t, store, uniqueLogin(t, "racing-replay")) },
		func(ctx context.Context, rec account.Record, _ int) error {
			return store.AdvanceTOTPCounter(ctx, rec.ID, 101)
		},
		func(round int, rec account.Record, errs []error) {
			if ok, refused := countOutcomes(t, errs, account.ErrNotFound); ok != 1 || refused != len(errs)-1 {
				t.Errorf("round %d: %d of %d concurrent advances with one counter succeeded, want exactly 1", round, ok, len(errs))
			}
			if got := find(t, store, rec.Login); got.TOTPCounter != 101 {
				t.Errorf("round %d: TOTPCounter = %d, want 101", round, got.TOTPCounter)
			}
		})
}

func checkAdvanceNeverBack(t *testing.T, h Harness) {
	store := h.NewStore(t)

	race(t, h,
		func(int) account.Record { return createConfirmed(t, store, uniqueLogin(t, "racing-forward")) },
		func(ctx context.Context, rec account.Record, i int) error {
			return store.AdvanceTOTPCounter(ctx, rec.ID, 101+int64(i))
		},
		func(round int, rec account.Record, errs []error) {
			countOutcomes(t, errs, account.ErrNotFound)
			if want := int64(confirmedCounter + len(errs)); find(t, store, rec.Login).TOTPCounter != want {
				t.Errorf("round %d: TOTPCounter = %d after racing advances, want the highest, %d", round, find(t, store, rec.Login).TOTPCounter, want)
			}
		})
}

func checkConfirmGuards(t *testing.T, h Harness) {
	store := h.NewStore(t)

	bare, err := store.Create(t.Context(), account.Record{Login: uniqueLogin(t, "no-secret"), PasswordHash: fakeHash})
	if err != nil {
		t.Fatalf("Create without a secret: %v", err)
	}
	wantNotFound(t, "ConfirmTOTP of an account without a secret", store.ConfirmTOTP(t.Context(), bare.ID, "", 100))

	login := uniqueLogin(t, "confirm")
	rec := create(t, store, login)

	for _, counter := range []int64{0, -1} {
		wantNotFound(t, "ConfirmTOTP with a counter not above zero", store.ConfirmTOTP(t.Context(), rec.ID, fakeSecret, counter))
	}
	wantNotFound(t, "ConfirmTOTP with a secret that is not the stored one", store.ConfirmTOTP(t.Context(), rec.ID, fakeOtherSecret, 100))
	wantNotFound(t, "ConfirmTOTP of an unknown id", store.ConfirmTOTP(t.Context(), "no-such-account-id", fakeSecret, 100))

	if got := find(t, store, login); got.TOTPCounter != 0 {
		t.Fatalf("TOTPCounter = %d after refused confirms, want 0", got.TOTPCounter)
	}

	if err := store.ConfirmTOTP(t.Context(), rec.ID, fakeSecret, 100); err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}
	if got := find(t, store, login); got.TOTPCounter != 100 {
		t.Errorf("TOTPCounter = %d after confirm, want 100, the confirming code's counter", got.TOTPCounter)
	}

	wantNotFound(t, "ConfirmTOTP of a confirmed secret", store.ConfirmTOTP(t.Context(), rec.ID, fakeSecret, 101))
	wantNotFound(t, "AdvanceTOTPCounter with the confirming code's counter", store.AdvanceTOTPCounter(t.Context(), rec.ID, 100))
}

func checkConfirmConcurrentOnce(t *testing.T, h Harness) {
	store := h.NewStore(t)

	race(t, h,
		func(int) account.Record { return create(t, store, uniqueLogin(t, "racing-confirm")) },
		func(ctx context.Context, rec account.Record, i int) error {
			return store.ConfirmTOTP(ctx, rec.ID, fakeSecret, 100+int64(i%2))
		},
		func(round int, _ account.Record, errs []error) {
			if ok, refused := countOutcomes(t, errs, account.ErrNotFound); ok != 1 || refused != len(errs)-1 {
				t.Errorf("round %d: %d of %d concurrent confirms succeeded, want exactly 1", round, ok, len(errs))
			}
		})
}

func checkReplaceOnlyUnconfirmed(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "replace")
	rec := create(t, store, login)

	if err := store.ReplaceTOTPSecret(t.Context(), rec.ID, fakeOtherSecret); err != nil {
		t.Fatalf("ReplaceTOTPSecret of an unconfirmed secret: %v", err)
	}
	if got := find(t, store, login); got.TOTPSecret != fakeOtherSecret || got.TOTPCounter != 0 {
		t.Fatalf("after replace = %+v, want the new secret, still unconfirmed", got)
	}

	// A code verified against the replaced secret must not confirm the new one.
	wantNotFound(t, "ConfirmTOTP with the replaced secret", store.ConfirmTOTP(t.Context(), rec.ID, fakeSecret, 100))

	if err := store.ConfirmTOTP(t.Context(), rec.ID, fakeOtherSecret, 100); err != nil {
		t.Fatalf("ConfirmTOTP with the new secret: %v", err)
	}

	wantNotFound(t, "ReplaceTOTPSecret of a confirmed secret", store.ReplaceTOTPSecret(t.Context(), rec.ID, fakeSecret))
	if got := find(t, store, login); got.TOTPSecret != fakeOtherSecret || got.TOTPCounter != 100 {
		t.Errorf("after a refused replace = %+v, want the confirmed secret unchanged", got)
	}

	wantNotFound(t, "ReplaceTOTPSecret of an unknown id", store.ReplaceTOTPSecret(t.Context(), "no-such-account-id", fakeSecret))
}

func checkSetPasswordHash(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "password")
	rec := createConfirmed(t, store, login)

	if err := store.SetPasswordHash(t.Context(), rec.ID, fakeHash, fakeOtherHash); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}

	got := find(t, store, login)
	want := account.Record{ID: rec.ID, Login: login, PasswordHash: fakeOtherHash, TOTPSecret: fakeSecret, TOTPCounter: 100}
	if got != want {
		t.Errorf("after SetPasswordHash = %+v, want %+v: only the hash may change", got, want)
	}

	wantNotFound(t, "SetPasswordHash of an unknown id", store.SetPasswordHash(t.Context(), "no-such-account-id", fakeHash, fakeOtherHash))
}

func checkSetPasswordHashRefusesStale(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "stale")
	rec := createConfirmed(t, store, login)

	// fakeOtherHash is not the stored hash, so the caller read something
	// else, or a change landed after its read.
	wantNotFound(t, "SetPasswordHash from a hash that is not the stored one",
		store.SetPasswordHash(t.Context(), rec.ID, fakeOtherHash, fakeOtherHash))
	wantNotFound(t, "SetPasswordHash from an empty hash on an account with a password",
		store.SetPasswordHash(t.Context(), rec.ID, "", fakeOtherHash))

	if got := find(t, store, login); got != rec {
		t.Errorf("after a stale SetPasswordHash = %+v, want %+v unchanged", got, rec)
	}
}

func checkSetPasswordHashRefusesNoPassword(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "no-password")

	bare, err := store.Create(t.Context(), account.Record{Login: login})
	if err != nil {
		t.Fatalf("Create without a hash: %v", err)
	}

	wantNotFound(t, "SetPasswordHash on an account without a password",
		store.SetPasswordHash(t.Context(), bare.ID, "", fakeHash))

	if got := find(t, store, login); got.PasswordHash != "" {
		t.Errorf("an account without a password got the hash %q", got.PasswordHash)
	}
}

func checkSetPasswordHashConcurrentOnce(t *testing.T, h Harness) {
	store := h.NewStore(t)

	race(t, h,
		func(int) account.Record { return createConfirmed(t, store, uniqueLogin(t, "racing-password")) },
		func(ctx context.Context, rec account.Record, i int) error {
			return store.SetPasswordHash(ctx, rec.ID, fakeHash, fakeOtherHash+"-"+strconv.Itoa(i))
		},
		func(round int, rec account.Record, errs []error) {
			ok, refused := countOutcomes(t, errs, account.ErrNotFound)
			if ok != 1 || refused != len(errs)-1 {
				t.Errorf("round %d: %d of %d concurrent writes from one old hash succeeded, want exactly 1", round, ok, len(errs))

				return
			}

			winner := slices.IndexFunc(errs, func(err error) bool { return err == nil })
			if got, want := find(t, store, rec.Login).PasswordHash, fakeOtherHash+"-"+strconv.Itoa(winner); got != want {
				t.Errorf("round %d: stored hash = %q, want the winner's %q", round, got, want)
			}
		})
}

func checkWritesStayInAccount(t *testing.T, h Harness) {
	store := h.NewStore(t)

	confirmed := uniqueLogin(t, "bystander-confirmed")
	unconfirmed := uniqueLogin(t, "bystander-unconfirmed")
	wantConfirmed := createConfirmed(t, store, confirmed)
	wantUnconfirmed := create(t, store, unconfirmed)

	other := createConfirmed(t, store, uniqueLogin(t, "target"))
	otherNew := create(t, store, uniqueLogin(t, "target-new"))

	ctx := t.Context()
	if err := store.AdvanceTOTPCounter(ctx, other.ID, 200); err != nil {
		t.Fatalf("AdvanceTOTPCounter: %v", err)
	}
	if err := store.SetPasswordHash(ctx, other.ID, fakeHash, fakeOtherHash); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}
	if err := store.ReplaceTOTPSecret(ctx, otherNew.ID, fakeOtherSecret); err != nil {
		t.Fatalf("ReplaceTOTPSecret: %v", err)
	}
	if err := store.ConfirmTOTP(ctx, otherNew.ID, fakeOtherSecret, 300); err != nil {
		t.Fatalf("ConfirmTOTP: %v", err)
	}

	if got := find(t, store, confirmed); got != wantConfirmed {
		t.Errorf("bystander = %+v, want %+v unchanged", got, wantConfirmed)
	}
	if got := find(t, store, unconfirmed); got != wantUnconfirmed {
		t.Errorf("bystander = %+v, want %+v unchanged", got, wantUnconfirmed)
	}
}

func checkDisable(t *testing.T, h Harness) {
	store := h.NewStore(t)
	login := uniqueLogin(t, "disabled")
	rec := createConfirmed(t, store, login)

	h.Disable(t, store, rec.ID)

	got, err := store.FindByLogin(t.Context(), login)
	switch {
	case errors.Is(err, account.ErrNotFound):
	case err == nil && got.PasswordHash == "":
	default:
		t.Errorf("FindByLogin of a disabled account = %+v, %v, want account.ErrNotFound or an empty PasswordHash", got, err)
	}
}
