// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package sessiontest checks that a [session.DeviceStore] keeps the contract the
// session package relies on.
//
// A product that writes its own device store runs [DeviceStore] against it in
// a test:
//
//	func TestDeviceStore(t *testing.T) {
//		sessiontest.DeviceStore(t, func(t *testing.T) session.DeviceStore {
//			return newEmptyStore(t)
//		})
//	}
//
// The suite runs every atomic operation of the contract on its own, then the
// rules of device token rotation and reuse detection through a
// [session.Manager] on top of the store, and then the races between them. A
// store that reads a record and writes it back in two steps fails the races.
//
// The suite works with real time: every record it writes expires a day or
// more after [time.Now], so a store that drops expired records on its own
// clock keeps them for the duration of the run.
package sessiontest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/session"
)

// NewStore returns an empty store for one subtest. It is called once per
// subtest, so state never leaks from one rule into the next.
type NewStore func(t *testing.T) session.DeviceStore

// DeviceStore runs the conformance suite against the stores newStore returns.
func DeviceStore(t *testing.T, newStore NewStore) {
	t.Helper()

	tests := []struct {
		name string
		run  func(*testing.T, NewStore)
	}{
		// The store operations on their own.
		{"store/LookupMatchesEverySlot", storeLookupMatchesEverySlot},
		{"store/LookupUnknownAndZeroHash", storeLookupUnknownAndZeroHash},
		{"store/RoundTrip", storeRoundTrip},
		{"store/CreateRefusesDuplicates", storeCreateRefusesDuplicates},
		{"store/CreateEvictsLeastRecentlySeenOfTheSubject", storeCreateEvictsLeastRecentlySeen},
		{"store/TouchMovesOnlyLastSeen", storeTouchMovesOnlyLastSeen},
		{"store/TouchNeverRecreates", storeTouchNeverRecreates},
		{"store/SwapIsConditional", storeSwapIsConditional},
		{"store/SwapNeverRecreates", storeSwapNeverRecreates},
		{"store/SwapKeepsEverythingElse", storeSwapKeepsEverythingElse},
		{"store/SwapForgetsAnOverwrittenPendingHash", storeSwapForgetsOverwrittenPending},
		{"store/RetiredIsBoundedOldestFirst", storeRetiredIsBounded},
		{"store/RevokeChecksTheSubject", storeRevokeChecksTheSubject},
		{"store/DeleteSubject", storeDeleteSubject},
		{"store/ListReturnsOnlyTheSubject", storeListReturnsOnlyTheSubject},
		{"store/NoSharedReferences", storeNoSharedReferences},

		// The store operations racing each other.
		{"race/ConcurrentSwapsOneWins", raceConcurrentSwapsOneWins},
		{"race/DeleteSubjectWins", raceDeleteSubjectWins},
		{"race/ConcurrentCreatesKeepTheCap", raceConcurrentCreatesKeepTheCap},

		// The rules of the manager on top of the store.
		{"rules/IssueStoresOnlyTheHash", rulesIssueStoresOnlyTheHash},
		{"rules/AuthenticateTouches", rulesAuthenticateTouches},
		{"rules/RotationIsPendingUntilFirstUse", rulesRotationIsPendingUntilFirstUse},
		{"rules/GraceIsExclusive", rulesGraceIsExclusive},
		{"rules/RotationKeepsIDAndExpiry", rulesRotationKeepsIDAndExpiry},
		{"rules/MinimumIntervalFromIssue", rulesMinimumIntervalFromIssue},
		{"rules/MinimumIntervalFromConfirm", rulesMinimumIntervalFromConfirm},
		{"rules/RotateWithPendingConfirmsOnly", rulesRotateWithPendingConfirmsOnly},
		{"rules/CurrentKeepsThePending", rulesCurrentKeepsThePending},
		{"rules/SameNonceInsideTimeoutOverwrites", rulesSameNonceInsideTimeoutOverwrites},
		{"rules/SameNonceAtTimeoutRetires", rulesSameNonceAtTimeoutRetires},
		{"rules/OtherNonceRetires", rulesOtherNonceRetires},
		{"rules/RotateTimeoutStartsAtFirstIssue", rulesRotateTimeoutStartsAtFirstIssue},
		{"rules/RetiredInsideGraceRotateRefused", rulesRetiredInsideGraceRotateRefused},
		{"rules/RetiredInsideLongGraceRotateRefused", rulesRetiredInsideGraceRotateRefusedPastTheInterval},
		{"rules/RetiredAfterGraceRotateRevokes", rulesRetiredAfterGraceRotateRevokes},
		{"rules/IdleIsExclusive", rulesIdleIsExclusive},
		{"rules/AbsoluteIsExclusive", rulesAbsoluteIsExclusive},
		{"rules/UnknownAndRevokedLookAlike", rulesUnknownAndRevokedLookAlike},
		{"rules/ListDevicesOnlyOwn", rulesListDevicesOnlyOwn},
		{"rules/RevokeDeviceRefusesAnotherSubject", rulesRevokeDeviceRefusesAnotherSubject},
		{"rules/EndAllCoversDevices", rulesEndAllCoversDevices},
		{"rules/CapEvictsOnlyTheSubject", rulesCapEvictsOnlyTheSubject},

		// The rules racing each other through the manager.
		{"race/ConcurrentRotationsOneWins", raceConcurrentRotationsOneWins},
		{"race/ConcurrentConfirmsRetireOnce", raceConcurrentConfirmsRetireOnce},
		{"race/SameNonceRetriesOneWins", raceSameNonceRetriesOneWins},
		{"race/ReuseRevokesUnderConcurrency", raceReuseRevokesUnderConcurrency},
		{"race/EndAllBeatsRotation", raceEndAllBeatsRotation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { test.run(t, newStore) })
	}
}

// base is a second aligned now, so a store that truncates timestamps keeps
// them equal.
func base() time.Time { return time.Now().UTC().Truncate(time.Second) }

func hashOf(s string) session.TokenHash { return sha256.Sum256([]byte(s)) }

// record is a fresh device session with only a current hash, the way a
// manager issues one.
func record(id, subject string, at time.Time) session.DeviceSession {
	return session.DeviceSession{
		ID:        id,
		Subject:   subject,
		Name:      "phone",
		CreatedAt: at,
		LastSeen:  at,
		ExpiresAt: at.Add(48 * time.Hour),
		DeviceState: session.DeviceState{
			CurrentHash: hashOf(id + "/current"),
			RotatedAt:   at,
		},
	}
}

func create(t *testing.T, store session.DeviceStore, d session.DeviceSession, maxPerSubject int) {
	t.Helper()
	if err := store.Create(context.Background(), d, maxPerSubject); err != nil {
		t.Fatalf("Create %s: %v", d.ID, err)
	}
}

func lookup(t *testing.T, store session.DeviceStore, h session.TokenHash) (session.DeviceSession, session.DeviceSlot) {
	t.Helper()
	d, slot, err := store.Lookup(context.Background(), h)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	return d, slot
}

func wantNotFound(t *testing.T, store session.DeviceStore, h session.TokenHash, what string) {
	t.Helper()
	if _, _, err := store.Lookup(context.Background(), h); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Lookup of %s = %v, want ErrNotFound", what, err)
	}
}

func swap(t *testing.T, store session.DeviceStore, id string, expect, next session.DeviceState, retire ...session.RetiredHash) {
	t.Helper()
	if err := store.Swap(context.Background(), id, expect, next, retire); err != nil {
		t.Fatalf("Swap: %v", err)
	}
}

func ids(devices []session.DeviceSession) []string {
	out := make([]string, 0, len(devices))
	for _, d := range devices {
		out = append(out, d.ID)
	}
	slices.Sort(out)
	return out
}

func storeLookupMatchesEverySlot(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	d := record("d1", "u1", at)
	create(t, store, d, 10)

	if got, slot := lookup(t, store, d.CurrentHash); slot != session.SlotCurrent || got.ID != "d1" {
		t.Errorf("current: slot %d, id %q", slot, got.ID)
	}

	pending := hashOf("pending")
	stable := d.DeviceState
	withPending := stable
	withPending.PendingHash = pending
	withPending.PendingNonce = "n1"
	withPending.PendingAt = at
	swap(t, store, d.ID, stable, withPending)
	if got, slot := lookup(t, store, pending); slot != session.SlotPending || got.ID != "d1" {
		t.Errorf("pending: slot %d, id %q", slot, got.ID)
	}

	confirmed := session.DeviceState{CurrentHash: pending, RotatedAt: at.Add(time.Minute)}
	graceEnd := at.Add(6 * time.Minute)
	swap(t, store, d.ID, withPending, confirmed, session.RetiredHash{Hash: d.CurrentHash, GraceEnd: graceEnd})

	got, slot := lookup(t, store, d.CurrentHash)
	if slot != session.SlotRetired || got.ID != "d1" {
		t.Errorf("retired: slot %d, id %q", slot, got.ID)
	}
	if len(got.Retired) != 1 || got.Retired[0].Hash != d.CurrentHash || !got.Retired[0].GraceEnd.Equal(graceEnd) {
		t.Errorf("retired = %+v, want the old current hash with its grace end", got.Retired)
	}
	if _, slot := lookup(t, store, pending); slot != session.SlotCurrent {
		t.Errorf("confirmed pending: slot %d, want current", slot)
	}
}

func storeLookupUnknownAndZeroHash(t *testing.T, newStore NewStore) {
	store := newStore(t)
	create(t, store, record("d1", "u1", base()), 10)

	wantNotFound(t, store, hashOf("nobody"), "an unknown hash")
	// The record has no pending hash. The empty slot must never match.
	wantNotFound(t, store, session.TokenHash{}, "the zero hash")
}

func storeRoundTrip(t *testing.T, newStore NewStore) {
	store := newStore(t)
	d := record("d1", "u1", base())
	d.Name = "Simon's tablet"
	create(t, store, d, 10)

	got, _ := lookup(t, store, d.CurrentHash)
	if got.ID != d.ID || got.Subject != d.Subject || got.Name != d.Name ||
		!got.CreatedAt.Equal(d.CreatedAt) || !got.LastSeen.Equal(d.LastSeen) ||
		!got.ExpiresAt.Equal(d.ExpiresAt) || got.CurrentHash != d.CurrentHash ||
		!got.PendingHash.IsZero() || got.PendingNonce != "" || !got.RotatedAt.Equal(d.RotatedAt) ||
		len(got.Retired) != 0 {
		t.Errorf("record = %+v, want %+v", got, d)
	}
}

func storeCreateRefusesDuplicates(t *testing.T, newStore NewStore) {
	store := newStore(t)
	d := record("d1", "u1", base())
	create(t, store, d, 10)

	sameID := record("d1", "u2", base())
	sameID.CurrentHash = hashOf("other")
	if err := store.Create(context.Background(), sameID, 10); !errors.Is(err, session.ErrDeviceConflict) {
		t.Errorf("Create with a taken id = %v, want ErrDeviceConflict", err)
	}

	sameHash := record("d2", "u2", base())
	sameHash.CurrentHash = d.CurrentHash
	if err := store.Create(context.Background(), sameHash, 10); !errors.Is(err, session.ErrDeviceConflict) {
		t.Errorf("Create with a taken hash = %v, want ErrDeviceConflict", err)
	}

	if got, _ := lookup(t, store, d.CurrentHash); got.Subject != "u1" {
		t.Errorf("the first record was replaced: %+v", got)
	}
}

func storeCreateEvictsLeastRecentlySeen(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	a1 := record("a1", "u1", at)
	a2 := record("a2", "u1", at.Add(time.Minute))
	b1 := record("b1", "u2", at.Add(-time.Hour))
	for _, d := range []session.DeviceSession{a1, a2, b1} {
		create(t, store, d, 2)
	}
	// a1 is seen after a2, so a2 is now the least recently seen of u1. b1 is
	// older than both and belongs to another subject.
	if err := store.Touch(context.Background(), "a1", at.Add(2*time.Minute)); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	create(t, store, record("a3", "u1", at.Add(3*time.Minute)), 2)

	list, err := store.List(context.Background(), "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := ids(list); !slices.Equal(got, []string{"a1", "a3"}) {
		t.Errorf("u1 devices = %v, want a1 and a3", got)
	}
	wantNotFound(t, store, a2.CurrentHash, "the evicted device")
	if _, slot := lookup(t, store, b1.CurrentHash); slot != session.SlotCurrent {
		t.Error("another subject's device was evicted")
	}
}

func storeTouchMovesOnlyLastSeen(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	d := record("d1", "u1", at)
	create(t, store, d, 10)

	later := at.Add(time.Hour)
	if err := store.Touch(context.Background(), "d1", later); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, _ := lookup(t, store, d.CurrentHash)
	if !got.LastSeen.Equal(later) {
		t.Errorf("LastSeen = %s, want %s", got.LastSeen, later)
	}
	if !got.ExpiresAt.Equal(d.ExpiresAt) || !got.CreatedAt.Equal(d.CreatedAt) ||
		got.DeviceState != d.DeviceState || got.Subject != d.Subject || got.Name != d.Name {
		t.Errorf("Touch changed more than LastSeen: %+v", got)
	}

	// A touch that arrives late never moves the last use backwards.
	if err := store.Touch(context.Background(), "d1", at); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if got, _ := lookup(t, store, d.CurrentHash); !got.LastSeen.Equal(later) {
		t.Errorf("LastSeen moved back to %s", got.LastSeen)
	}
}

func storeTouchNeverRecreates(t *testing.T, newStore NewStore) {
	store := newStore(t)
	d := record("d1", "u1", base())
	create(t, store, d, 10)
	if err := store.Revoke(context.Background(), "u1", "d1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if err := store.Touch(context.Background(), "d1", base()); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Touch of a revoked device = %v, want ErrNotFound", err)
	}
	wantNotFound(t, store, d.CurrentHash, "a revoked device after a touch")
}

func storeSwapIsConditional(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	d := record("d1", "u1", at)
	d.PendingHash = hashOf("pending")
	d.PendingNonce = "n1"
	d.PendingAt = at
	create(t, store, d, 10)

	next := session.DeviceState{CurrentHash: hashOf("next"), RotatedAt: at}
	wrong := map[string]session.DeviceState{
		"current": {CurrentHash: hashOf("x"), PendingHash: d.PendingHash, PendingNonce: d.PendingNonce},
		"pending": {CurrentHash: d.CurrentHash, PendingHash: hashOf("x"), PendingNonce: d.PendingNonce},
		"nonce":   {CurrentHash: d.CurrentHash, PendingHash: d.PendingHash, PendingNonce: "n2"},
	}
	for field, expect := range wrong {
		err := store.Swap(context.Background(), "d1", expect, next, []session.RetiredHash{{Hash: d.CurrentHash, GraceEnd: at}})
		if !errors.Is(err, session.ErrDeviceConflict) {
			t.Errorf("Swap with a stale %s = %v, want ErrDeviceConflict", field, err)
		}
	}

	got, slot := lookup(t, store, d.CurrentHash)
	if slot != session.SlotCurrent || got.DeviceState != d.DeviceState || len(got.Retired) != 0 {
		t.Errorf("a refused swap changed the record: %+v", got)
	}
	wantNotFound(t, store, next.CurrentHash, "the hash of a refused swap")
}

func storeSwapNeverRecreates(t *testing.T, newStore NewStore) {
	store := newStore(t)
	d := record("d1", "u1", base())
	create(t, store, d, 10)
	if err := store.DeleteSubject(context.Background(), "u1"); err != nil {
		t.Fatalf("DeleteSubject: %v", err)
	}

	next := session.DeviceState{CurrentHash: hashOf("next")}
	if err := store.Swap(context.Background(), "d1", d.DeviceState, next, nil); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Swap of a deleted device = %v, want ErrNotFound", err)
	}
	wantNotFound(t, store, next.CurrentHash, "the hash of a swap on a deleted device")
	wantNotFound(t, store, d.CurrentHash, "a deleted device")
}

func storeSwapKeepsEverythingElse(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	d := record("d1", "u1", at)
	create(t, store, d, 10)

	next := d.DeviceState
	next.PendingHash = hashOf("pending")
	next.PendingNonce = "n1"
	next.PendingAt = at.Add(time.Hour)
	swap(t, store, d.ID, d.DeviceState, next)

	got, _ := lookup(t, store, d.CurrentHash)
	if got.DeviceState != next {
		t.Errorf("state = %+v, want %+v", got.DeviceState, next)
	}
	if got.Subject != d.Subject || got.Name != d.Name || !got.CreatedAt.Equal(d.CreatedAt) ||
		!got.LastSeen.Equal(d.LastSeen) || !got.ExpiresAt.Equal(d.ExpiresAt) {
		t.Errorf("Swap changed more than the state: %+v", got)
	}
}

func storeSwapForgetsOverwrittenPending(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	d := record("d1", "u1", at)
	create(t, store, d, 10)

	first := d.DeviceState
	first.PendingHash = hashOf("p1")
	first.PendingNonce = "n1"
	first.PendingAt = at
	swap(t, store, d.ID, d.DeviceState, first)

	second := first
	second.PendingHash = hashOf("p2")
	swap(t, store, d.ID, first, second)

	wantNotFound(t, store, hashOf("p1"), "an overwritten pending hash")
	if _, slot := lookup(t, store, hashOf("p2")); slot != session.SlotPending {
		t.Errorf("new pending: slot %d", slot)
	}
}

func storeRetiredIsBounded(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	d := record("d1", "u1", at)
	create(t, store, d, 10)

	state := d.DeviceState
	retiredHashes := []session.TokenHash{}
	for i := range session.MaxRetiredHashes + 1 {
		next := session.DeviceState{CurrentHash: hashOf(fmt.Sprintf("current-%d", i)), RotatedAt: at}
		retired := session.RetiredHash{Hash: state.CurrentHash, GraceEnd: at.Add(time.Duration(i) * time.Minute)}
		swap(t, store, d.ID, state, next, retired)
		retiredHashes = append(retiredHashes, state.CurrentHash)
		state = next
	}

	got, _ := lookup(t, store, state.CurrentHash)
	if len(got.Retired) != session.MaxRetiredHashes {
		t.Fatalf("retired = %d entries, want %d", len(got.Retired), session.MaxRetiredHashes)
	}
	// The oldest is dropped first, and the rest stay in order.
	wantNotFound(t, store, retiredHashes[0], "the oldest retired hash")
	for i, entry := range got.Retired {
		if entry.Hash != retiredHashes[i+1] {
			t.Errorf("retired[%d] is not the %d. retired hash", i, i+2)
		}
	}
	for _, h := range retiredHashes[1:] {
		if _, slot := lookup(t, store, h); slot != session.SlotRetired {
			t.Errorf("a kept retired hash: slot %d", slot)
		}
	}
}

func storeRevokeChecksTheSubject(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	d := record("d1", "u1", at)
	create(t, store, d, 10)
	pending := d.DeviceState
	pending.PendingHash = hashOf("pending")
	pending.PendingNonce = "n1"
	swap(t, store, d.ID, d.DeviceState, pending)
	confirmed := session.DeviceState{CurrentHash: pending.PendingHash}
	swap(t, store, d.ID, pending, confirmed, session.RetiredHash{Hash: d.CurrentHash, GraceEnd: at})

	if err := store.Revoke(context.Background(), "u2", "d1"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Revoke by another subject = %v, want ErrNotFound", err)
	}
	if _, slot := lookup(t, store, confirmed.CurrentHash); slot != session.SlotCurrent {
		t.Error("another subject revoked the device")
	}

	if err := store.Revoke(context.Background(), "u1", "d1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	wantNotFound(t, store, confirmed.CurrentHash, "the current hash of a revoked device")
	wantNotFound(t, store, d.CurrentHash, "a retired hash of a revoked device")
	if err := store.Revoke(context.Background(), "u1", "d1"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("second Revoke = %v, want ErrNotFound", err)
	}
	if err := store.Revoke(context.Background(), "u1", "nothing"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Revoke of an unknown id = %v, want ErrNotFound", err)
	}
}

func storeDeleteSubject(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	a1, a2, b1 := record("a1", "u1", at), record("a2", "u1", at), record("b1", "u2", at)
	for _, d := range []session.DeviceSession{a1, a2, b1} {
		create(t, store, d, 10)
	}

	if err := store.DeleteSubject(context.Background(), "u1"); err != nil {
		t.Fatalf("DeleteSubject: %v", err)
	}
	wantNotFound(t, store, a1.CurrentHash, "a1")
	wantNotFound(t, store, a2.CurrentHash, "a2")
	if _, slot := lookup(t, store, b1.CurrentHash); slot != session.SlotCurrent {
		t.Error("another subject's device was deleted")
	}
	if err := store.DeleteSubject(context.Background(), "nobody"); err != nil {
		t.Errorf("DeleteSubject of a subject without devices = %v", err)
	}
}

func storeListReturnsOnlyTheSubject(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	for _, d := range []session.DeviceSession{record("a1", "u1", at), record("a2", "u1", at), record("b1", "u2", at)} {
		create(t, store, d, 10)
	}

	list, err := store.List(context.Background(), "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := ids(list); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Errorf("List(u1) = %v, want a1 and a2", got)
	}
	list, err = store.List(context.Background(), "nobody")
	if err != nil || len(list) != 0 {
		t.Errorf("List(nobody) = %v, %v, want nothing", ids(list), err)
	}
}

func storeNoSharedReferences(t *testing.T, newStore NewStore) {
	store := newStore(t)
	at := base()
	d := record("d1", "u1", at)
	create(t, store, d, 10)
	next := session.DeviceState{CurrentHash: hashOf("next")}
	swap(t, store, d.ID, d.DeviceState, next, session.RetiredHash{Hash: d.CurrentHash, GraceEnd: at})

	got, _ := lookup(t, store, next.CurrentHash)
	got.Retired[0].GraceEnd = at.Add(time.Hour)
	list, _ := store.List(context.Background(), "u1")
	list[0].Retired[0].GraceEnd = at.Add(time.Hour)

	again, _ := lookup(t, store, next.CurrentHash)
	if !again.Retired[0].GraceEnd.Equal(at) {
		t.Error("a caller changed the stored grace end through a returned value")
	}
}

func raceConcurrentSwapsOneWins(t *testing.T, newStore NewStore) {
	const rounds, callers = 50, 16
	store := newStore(t)
	at := base()

	for round := range rounds {
		d := record(fmt.Sprintf("d%d", round), "u1", at)
		create(t, store, d, rounds)

		var wg sync.WaitGroup
		results := make([]error, callers)
		start := make(chan struct{})
		for i := range callers {
			wg.Go(func() {
				<-start
				next := d.DeviceState
				next.PendingHash = hashOf(fmt.Sprintf("%s/p%d", d.ID, i))
				next.PendingNonce = fmt.Sprintf("n%d", i)
				results[i] = store.Swap(context.Background(), d.ID, d.DeviceState, next, nil)
			})
		}
		close(start)
		wg.Wait()

		winner := -1
		for i, err := range results {
			switch {
			case err == nil && winner >= 0:
				t.Fatalf("round %d: two swaps on the same state both won", round)
			case err == nil:
				winner = i
			case !errors.Is(err, session.ErrDeviceConflict):
				t.Fatalf("round %d: losing swap = %v, want ErrDeviceConflict", round, err)
			}
		}
		if winner < 0 {
			t.Fatalf("round %d: no swap won", round)
		}
		got, slot := lookup(t, store, hashOf(fmt.Sprintf("%s/p%d", d.ID, winner)))
		if slot != session.SlotPending || got.PendingNonce != fmt.Sprintf("n%d", winner) {
			t.Fatalf("round %d: the stored pending state is not the winner's", round)
		}
	}
}

func raceDeleteSubjectWins(t *testing.T, newStore NewStore) {
	const rounds, writers = 30, 8
	store := newStore(t)
	at := base()

	for round := range rounds {
		d := record(fmt.Sprintf("d%d", round), "u1", at)
		create(t, store, d, rounds)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range writers {
			wg.Go(func() {
				<-start
				_ = store.Touch(context.Background(), d.ID, at.Add(time.Duration(i)*time.Second))
				next := d.DeviceState
				next.PendingHash = hashOf(fmt.Sprintf("%s/p%d", d.ID, i))
				_ = store.Swap(context.Background(), d.ID, d.DeviceState, next, nil)
			})
		}
		wg.Go(func() {
			<-start
			if err := store.DeleteSubject(context.Background(), "u1"); err != nil {
				t.Errorf("DeleteSubject: %v", err)
			}
		})
		close(start)
		wg.Wait()

		wantNotFound(t, store, d.CurrentHash, "a device after DeleteSubject")
		for i := range writers {
			wantNotFound(t, store, hashOf(fmt.Sprintf("%s/p%d", d.ID, i)), "a pending hash after DeleteSubject")
		}
		if list, err := store.List(context.Background(), "u1"); err != nil || len(list) != 0 {
			t.Fatalf("round %d: List after DeleteSubject = %v, %v", round, ids(list), err)
		}
	}
}

func raceConcurrentCreatesKeepTheCap(t *testing.T, newStore NewStore) {
	const callers, maxPerSubject = 32, 3
	store := newStore(t)
	at := base()

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Go(func() {
			<-start
			if err := store.Create(context.Background(), record(fmt.Sprintf("d%d", i), "u1", at), maxPerSubject); err != nil {
				t.Errorf("Create: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()

	list, err := store.List(context.Background(), "u1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != maxPerSubject {
		t.Errorf("u1 has %d devices, want the cap of %d", len(list), maxPerSubject)
	}
}
