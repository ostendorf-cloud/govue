// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package sessiontest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/session"
)

// clock is the manager's clock, moved by hand and safe to read from many
// goroutines.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

// Now is the clock the manager reads.
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// barrierStore holds every lookup of an armed round until all parties have
// read, so they all decide on the same state. That turns a race between read
// and write into a certainty: a manager or a store that writes without a
// condition lets every party win.
type barrierStore struct {
	session.DeviceStore

	mu      sync.Mutex
	parties int
	arrived int
	release chan struct{}
}

func (b *barrierStore) arm(parties int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.parties, b.arrived, b.release = parties, 0, make(chan struct{})
}

// Lookup reads first and then waits at the barrier, so every party of an
// armed round holds the same state before any of them writes.
func (b *barrierStore) Lookup(ctx context.Context, hash session.TokenHash) (session.DeviceSession, session.DeviceSlot, error) {
	d, slot, err := b.DeviceStore.Lookup(ctx, hash)

	b.mu.Lock()
	if b.parties == 0 {
		b.mu.Unlock()
		return d, slot, err
	}
	b.arrived++
	release := b.release
	if b.arrived == b.parties {
		close(release)
		b.parties = 0
	}
	b.mu.Unlock()

	select {
	case <-release:
	case <-time.After(5 * time.Second):
		// A party that never looks up would hold the others forever. The
		// assertions of the round then fail instead.
	}
	return d, slot, err
}

type fixture struct {
	t       *testing.T
	start   time.Time
	clock   *clock
	store   session.DeviceStore
	barrier *barrierStore
	cookies *session.MemoryStore
	manager *session.Manager
}

func newFixture(t *testing.T, newStore NewStore, adjust func(*session.DeviceConfig)) *fixture {
	t.Helper()
	f := &fixture{t: t, start: base()}
	f.clock = &clock{now: f.start}
	f.store = newStore(t)
	f.barrier = &barrierStore{DeviceStore: f.store}
	f.cookies = session.NewMemoryStore(f.clock.Now, 0)

	cfg := session.Config{
		Store:   f.cookies,
		Now:     f.clock.Now,
		Devices: session.DeviceConfig{Store: f.barrier},
	}
	if adjust != nil {
		adjust(&cfg.Devices)
	}
	manager, err := session.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.manager = manager
	return f
}

// set moves the clock to start plus d.
func (f *fixture) set(d time.Duration) { f.clock.set(f.start.Add(d)) }

func (f *fixture) issue(subject string) (string, session.Device) {
	f.t.Helper()
	token, device, err := f.manager.IssueDevice(context.Background(), subject, "phone", f.clock.Now())
	if err != nil {
		f.t.Fatalf("IssueDevice: %v", err)
	}
	return token, device
}

func (f *fixture) mustAuth(token, what string) session.Device {
	f.t.Helper()
	device, err := f.manager.AuthenticateDevice(context.Background(), token)
	if err != nil {
		f.t.Fatalf("AuthenticateDevice with %s: %v", what, err)
	}
	return device
}

func (f *fixture) mustFail(token, what string) {
	f.t.Helper()
	_ = f.failErr(token, what)
}

// failErr is mustFail that returns the error, for a test that compares them.
func (f *fixture) failErr(token, what string) error {
	f.t.Helper()
	device, err := f.manager.AuthenticateDevice(context.Background(), token)
	if !errors.Is(err, session.ErrDeviceToken) {
		f.t.Fatalf("AuthenticateDevice with %s = %+v, %v, want ErrDeviceToken", what, device, err)
	}
	return err
}

func (f *fixture) mustRotate(token, nonce string) string {
	f.t.Helper()
	next, _, err := f.manager.RotateDevice(context.Background(), token, nonce)
	if err != nil || next == "" {
		f.t.Fatalf("RotateDevice = %v, want a new token", err)
	}
	return next
}

func (f *fixture) mustRefuse(token, nonce, what string) {
	f.t.Helper()
	next, _, err := f.manager.RotateDevice(context.Background(), token, nonce)
	if !errors.Is(err, session.ErrRotateRefused) || next != "" {
		f.t.Fatalf("RotateDevice %s = %v with a token %t, want ErrRotateRefused and no token", what, err, next != "")
	}
}

// record reads the stored record a token matches, straight from the store.
func (f *fixture) record(token string) (session.DeviceSession, session.DeviceSlot) {
	f.t.Helper()
	d, slot, err := f.store.Lookup(context.Background(), sha256.Sum256([]byte(token)))
	if err != nil {
		f.t.Fatalf("Lookup of a token's hash: %v", err)
	}
	return d, slot
}

func (f *fixture) wantGone(token, what string) {
	f.t.Helper()
	if _, _, err := f.store.Lookup(context.Background(), sha256.Sum256([]byte(token))); !errors.Is(err, session.ErrNotFound) {
		f.t.Errorf("the store still holds %s: %v", what, err)
	}
}

// leaks reports whether a text carries a token or its hash in a common
// encoding.
func leaks(text, token string) bool {
	h := sha256.Sum256([]byte(token))
	for _, form := range []string{
		token,
		hex.EncodeToString(h[:]),
		base64.StdEncoding.EncodeToString(h[:]),
		base64.RawURLEncoding.EncodeToString(h[:]),
		fmt.Sprintf("%v", h),
		fmt.Sprintf("%x", h[:8]),
	} {
		if strings.Contains(text, form) {
			return true
		}
	}
	return false
}

func rulesIssueStoresOnlyTheHash(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	// The subject holds characters outside base64url, so it can never be a
	// piece of a token by chance.
	const subject = "user@1"
	token, device := f.issue(subject)

	d, slot := f.record(token)
	if slot != session.SlotCurrent || d.CurrentHash != sha256.Sum256([]byte(token)) {
		t.Fatalf("slot %d, the current hash is not SHA-256 of the token", slot)
	}
	// What counts is that no stored field carries the token or its random
	// part, which is the secret. The random part is 43 characters, so a
	// match by chance is out of reach.
	random := token[len(session.DefaultDeviceTokenPrefix)+1:][:43]
	for field, value := range map[string]string{"ID": d.ID, "Subject": d.Subject, "Name": d.Name, "PendingNonce": d.PendingNonce} {
		if strings.Contains(value, token) || strings.Contains(value, random) {
			t.Errorf("%s carries the token", field)
		}
	}
	if device.ID != d.ID || device.Subject != subject || device.Name != "phone" ||
		!device.CreatedAt.Equal(f.start) || !device.ExpiresAt.Equal(f.start.Add(session.DefaultDeviceLifetime)) {
		t.Errorf("device = %+v", device)
	}
	if !d.RotatedAt.Equal(f.start) {
		t.Errorf("RotatedAt = %s, want the issue time: issuing counts as the first rotation", d.RotatedAt)
	}

	f.set(session.DefaultDeviceMinRotateInterval)
	pending := f.mustRotate(token, "n1")
	if d, _ := f.record(token); d.PendingHash != sha256.Sum256([]byte(pending)) {
		t.Error("the pending hash is not SHA-256 of the pending token")
	}
	if pending == token {
		t.Error("rotation returned the same token")
	}
}

func rulesAuthenticateTouches(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")

	f.set(2 * time.Minute)
	device := f.mustAuth(token, "the current token")
	if !device.LastSeen.Equal(f.start.Add(2 * time.Minute)) {
		t.Errorf("returned LastSeen = %s", device.LastSeen)
	}
	if d, _ := f.record(token); !d.LastSeen.Equal(f.start.Add(2 * time.Minute)) {
		t.Errorf("stored LastSeen = %s", d.LastSeen)
	}
}

func rulesRotationIsPendingUntilFirstUse(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")
	f.set(session.DefaultDeviceMinRotateInterval)
	pending := f.mustRotate(token, "n1")

	d, slot := f.record(token)
	if slot != session.SlotCurrent || len(d.Retired) != 0 {
		t.Fatalf("after rotate: slot %d, retired %d, want the old token still current", slot, len(d.Retired))
	}
	if _, slot := f.record(pending); slot != session.SlotPending {
		t.Fatalf("new token slot %d, want pending", slot)
	}

	// Long past the grace, the old token still works: nothing was retired yet.
	confirmAt := session.DefaultDeviceMinRotateInterval + 2*session.DefaultDeviceGrace
	f.set(confirmAt)
	f.mustAuth(token, "the current token while a rotation is pending")

	f.mustAuth(pending, "the pending token")
	d, slot = f.record(pending)
	if slot != session.SlotCurrent || !d.PendingHash.IsZero() || d.PendingNonce != "" {
		t.Fatalf("after first use: slot %d, pending set %t", slot, !d.PendingHash.IsZero())
	}
	if !d.RotatedAt.Equal(f.start.Add(confirmAt)) {
		t.Errorf("RotatedAt = %s, want the confirm time", d.RotatedAt)
	}
	wantGraceEnd := f.start.Add(confirmAt + session.DefaultDeviceGrace)
	if len(d.Retired) != 1 || d.Retired[0].Hash != sha256.Sum256([]byte(token)) ||
		!d.Retired[0].GraceEnd.Equal(wantGraceEnd) || d.Retired[0].Kind != session.RetiredCurrent {
		t.Errorf("retired = %+v, want the old token with a grace end of %s", d.Retired, wantGraceEnd)
	}
}

// confirmed issues a token, rotates it after the minimum interval and confirms
// the new one at once. It returns both and the confirm offset.
func confirmed(f *fixture, subject string) (old, current string, at time.Duration) {
	f.t.Helper()
	old, _ = f.issue(subject)
	at = session.DefaultDeviceMinRotateInterval
	f.set(at)
	current = f.mustRotate(old, "n1")
	f.mustAuth(current, "the pending token")
	return old, current, at
}

func rulesGraceIsExclusive(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	old, current, at := confirmed(f, "u1")

	f.set(at + session.DefaultDeviceGrace - time.Nanosecond)
	f.mustAuth(old, "a retired token one moment before its grace ends")

	f.set(at + session.DefaultDeviceGrace)
	f.mustFail(old, "a retired token at the end of its grace")
	f.mustFail(current, "the current token of a device revoked for reuse")
	f.wantGone(current, "a device revoked for reuse")
}

func rulesRotationKeepsIDAndExpiry(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	_, issued := f.issue("u0")
	_, current, _ := confirmed(f, "u1")

	device := f.mustAuth(current, "the confirmed token")
	d, _ := f.record(current)
	want := f.start.Add(session.DefaultDeviceLifetime)
	if !device.ExpiresAt.Equal(want) || !d.ExpiresAt.Equal(want) || !d.CreatedAt.Equal(f.start) {
		t.Errorf("ExpiresAt = %s, stored %s, CreatedAt %s, want the issue's", device.ExpiresAt, d.ExpiresAt, d.CreatedAt)
	}
	if list, _ := f.manager.ListDevices(context.Background(), "u1"); len(list) != 1 || list[0].ID != device.ID {
		t.Errorf("rotation changed the device id or added a device: %+v", list)
	}
	if issued.ID == device.ID {
		t.Error("two devices share an id")
	}
}

func rulesMinimumIntervalFromIssue(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")

	f.set(session.DefaultDeviceMinRotateInterval - time.Nanosecond)
	f.mustRefuse(token, "n1", "before the minimum interval since issue")
	f.mustAuth(token, "the token after a refused rotation")
	if d, _ := f.record(token); !d.PendingHash.IsZero() {
		t.Error("a refused rotation left a pending token")
	}

	f.set(session.DefaultDeviceMinRotateInterval)
	f.mustRotate(token, "n1")
}

func rulesMinimumIntervalFromConfirm(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	_, current, at := confirmed(f, "u1")

	f.set(at + session.DefaultDeviceMinRotateInterval - time.Nanosecond)
	f.mustRefuse(current, "n2", "before the minimum interval since the confirm")
	f.set(at + session.DefaultDeviceMinRotateInterval)
	f.mustRotate(current, "n3")
}

func rulesRotateWithPendingConfirmsOnly(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")
	f.set(session.DefaultDeviceMinRotateInterval)
	pending := f.mustRotate(token, "n1")

	f.set(session.DefaultDeviceMinRotateInterval + time.Minute)
	f.mustRefuse(pending, "n2", "with the pending token")
	d, slot := f.record(pending)
	if slot != session.SlotCurrent || !d.PendingHash.IsZero() {
		t.Errorf("slot %d, pending set %t, want the pending token confirmed and no new pending", slot, !d.PendingHash.IsZero())
	}
	if _, slot := f.record(token); slot != session.SlotRetired {
		t.Errorf("old token slot %d, want retired", slot)
	}
}

func rulesCurrentKeepsThePending(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")
	f.set(session.DefaultDeviceMinRotateInterval)
	pending := f.mustRotate(token, "n1")

	f.set(session.DefaultDeviceMinRotateInterval + time.Minute)
	f.mustAuth(token, "the current token")
	if d, _ := f.record(token); d.PendingHash != sha256.Sum256([]byte(pending)) {
		t.Fatal("a request with the current token dropped the pending token")
	}
	f.mustAuth(pending, "the pending token")
}

func rulesSameNonceInsideTimeoutOverwrites(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")
	at := session.DefaultDeviceMinRotateInterval
	f.set(at)
	first := f.mustRotate(token, "n1")

	f.set(at + session.DefaultDeviceRotateTimeout - time.Nanosecond)
	second := f.mustRotate(token, "n1")
	if second == first {
		t.Fatal("the retry returned the same token")
	}
	d, _ := f.record(token)
	if len(d.Retired) != 0 {
		t.Errorf("an overwritten pending token was retired: %d entries", len(d.Retired))
	}

	// The overwritten token is unknown, not retired: no revocation.
	f.mustFail(first, "an overwritten pending token")
	f.mustAuth(token, "the current token after an unknown token was presented")
	f.mustAuth(second, "the retry's pending token")
}

func rulesSameNonceAtTimeoutRetires(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")
	at := session.DefaultDeviceMinRotateInterval
	f.set(at)
	first := f.mustRotate(token, "n1")

	f.set(at + session.DefaultDeviceRotateTimeout)
	second := f.mustRotate(token, "n1")
	d, _ := f.record(token)
	wantEnd := f.start.Add(at + session.DefaultDeviceRotateTimeout)
	if len(d.Retired) != 1 || d.Retired[0].Hash != sha256.Sum256([]byte(first)) ||
		!d.Retired[0].GraceEnd.Equal(wantEnd) || d.Retired[0].Kind != session.RetiredPending {
		t.Fatalf("retired = %+v, want the first pending token without grace", d.Retired)
	}

	f.mustFail(first, "a retired pending token")
	f.mustFail(token, "the current token of a device revoked for reuse")
	f.mustFail(second, "the pending token of a device revoked for reuse")
}

func rulesOtherNonceRetires(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")
	at := session.DefaultDeviceMinRotateInterval
	f.set(at)
	first := f.mustRotate(token, "n1")

	f.set(at + time.Second)
	f.mustRotate(token, "n2")
	f.mustFail(first, "a pending token retired by a rotation with another nonce")
	f.mustFail(token, "the current token of a device revoked for reuse")
}

func rulesRotateTimeoutStartsAtFirstIssue(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")
	at := session.DefaultDeviceMinRotateInterval
	timeout := session.DefaultDeviceRotateTimeout
	f.set(at)
	f.mustRotate(token, "n1")

	// The retry inside the timeout overwrites. A third call with the same
	// nonce is inside the timeout counted from the retry, but not from the
	// first issue, so it retires the retry's token. Otherwise a holder of the
	// nonce could overwrite silently for as long as it keeps calling.
	f.set(at + timeout/2)
	second := f.mustRotate(token, "n1")
	f.set(at + timeout + timeout/4)
	f.mustRotate(token, "n1")

	f.mustFail(second, "a pending token retired after the rotate timeout")
	f.mustFail(token, "the current token of a device revoked for reuse")
}

func rulesRetiredInsideGraceRotateRefused(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	old, current, at := confirmed(f, "u1")

	f.set(at + time.Minute)
	f.mustRefuse(old, "n2", "with a retired token inside its grace")
	f.mustAuth(current, "the current token after a refused rotation")
}

// rulesRetiredInsideGraceRotateRefusedPastTheInterval pins the refusal where
// the minimum interval would not: a grace longer than the interval, and a
// rotation with the retired token after the interval has passed.
func rulesRetiredInsideGraceRotateRefusedPastTheInterval(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, func(c *session.DeviceConfig) { c.Grace = 3 * session.DefaultDeviceMinRotateInterval })
	old, current, at := confirmed(f, "u1")

	f.set(at + 2*session.DefaultDeviceMinRotateInterval)
	f.mustRefuse(old, "n2", "with a retired token inside a long grace")
	if d, _ := f.record(current); !d.PendingHash.IsZero() {
		t.Error("a retired token started a rotation")
	}
}

func rulesRetiredAfterGraceRotateRevokes(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	old, current, at := confirmed(f, "u1")

	f.set(at + session.DefaultDeviceGrace)
	next, _, err := f.manager.RotateDevice(context.Background(), old, "n2")
	if !errors.Is(err, session.ErrDeviceToken) || next != "" {
		t.Fatalf("RotateDevice with a retired token after its grace = %v, want ErrDeviceToken", err)
	}
	f.mustFail(current, "the current token of a device revoked for reuse")
}

func rulesIdleIsExclusive(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	early, _ := f.issue("u1")
	late, _ := f.issue("u2")

	f.set(session.DefaultDeviceIdle - time.Nanosecond)
	f.mustAuth(early, "a token one moment before the idle timeout")

	f.set(session.DefaultDeviceIdle)
	f.mustFail(late, "a token at the idle timeout")
	f.wantGone(late, "an idle device")
}

func rulesAbsoluteIsExclusive(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, _ := f.issue("u1")

	step := session.DefaultDeviceIdle - 24*time.Hour
	for at := step; at < session.DefaultDeviceLifetime; at += step {
		f.set(at)
		f.mustAuth(token, "an active token")
	}
	f.set(session.DefaultDeviceLifetime - time.Nanosecond)
	f.mustAuth(token, "a token one moment before the absolute lifetime")

	f.set(session.DefaultDeviceLifetime)
	f.mustFail(token, "an active token at the absolute lifetime")
	f.wantGone(token, "an expired device")
}

func rulesUnknownAndRevokedLookAlike(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	other := newFixture(t, newStore, nil)
	unknown, _ := other.issue("u9")

	revoked, device := f.issue("u1")
	if err := f.manager.RevokeDevice(context.Background(), "u1", device.ID); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	reused, _, at := confirmed(f, "u2")
	expired, _ := f.issue("u3")

	errs := map[string]error{
		"unknown":   f.failErr(unknown, "an unknown token"),
		"revoked":   f.failErr(revoked, "a revoked token"),
		"malformed": f.failErr("not a token", "a malformed token"),
		"empty":     f.failErr("", "an empty token"),
	}
	f.set(at + session.DefaultDeviceGrace)
	errs["reused"] = f.failErr(reused, "a reused token")
	f.set(session.DefaultDeviceIdle + at)
	errs["expired"] = f.failErr(expired, "an expired token")

	want := errs["unknown"].Error()
	for kind, err := range errs {
		if err.Error() != want {
			t.Errorf("%s token error %q differs from the unknown token's %q", kind, err, want)
		}
		for _, token := range []string{unknown, revoked, reused, expired} {
			if leaks(err.Error(), token) {
				t.Errorf("%s token error carries a token or its hash", kind)
			}
		}
	}
}

func rulesListDevicesOnlyOwn(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	_, a1 := f.issue("u1")
	_, a2 := f.issue("u1")
	f.issue("u2")

	list, err := f.manager.ListDevices(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	got := []string{}
	for _, d := range list {
		got = append(got, d.ID)
		if d.Subject != "u1" {
			t.Errorf("listed a device of %q", d.Subject)
		}
	}
	want := []string{a1.ID, a2.ID}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("ListDevices(u1) = %v, want %v", got, want)
	}
	if list, err := f.manager.ListDevices(context.Background(), "u3"); err != nil || len(list) != 0 {
		t.Errorf("ListDevices(u3) = %+v, %v, want nothing", list, err)
	}
}

func rulesRevokeDeviceRefusesAnotherSubject(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	token, device := f.issue("u1")
	f.issue("u2")

	foreign := f.manager.RevokeDevice(context.Background(), "u2", device.ID)
	if !errors.Is(foreign, session.ErrNotFound) {
		t.Fatalf("RevokeDevice of another subject's device = %v, want ErrNotFound", foreign)
	}
	f.mustAuth(token, "a device another subject tried to revoke")

	unknown := f.manager.RevokeDevice(context.Background(), "u2", "no-such-device")
	if unknown == nil || foreign.Error() != unknown.Error() {
		t.Errorf("a foreign id (%v) and an unknown id (%v) are told apart", foreign, unknown)
	}

	if err := f.manager.RevokeDevice(context.Background(), "u1", device.ID); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	f.mustFail(token, "a revoked device")
}

func rulesEndAllCoversDevices(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	cookie, err := f.manager.Start(context.Background(), httptest.NewRecorder(), session.Session{Subject: "u1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	a1, _ := f.issue("u1")
	_, a2, _ := confirmed(f, "u1")
	b1, _ := f.issue("u2")

	if err := f.manager.EndAll(context.Background(), "u1"); err != nil {
		t.Fatalf("EndAll: %v", err)
	}
	f.mustFail(a1, "a device after EndAll")
	f.mustFail(a2, "a rotated device after EndAll")
	f.mustAuth(b1, "another subject's device after EndAll")
	if _, err := f.cookies.Get(context.Background(), cookie.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("the cookie session survived EndAll: %v", err)
	}
}

func rulesCapEvictsOnlyTheSubject(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, func(c *session.DeviceConfig) { c.MaxPerSubject = 2 })
	a1, _ := f.issue("u1")
	b1, _ := f.issue("u2")
	f.set(time.Minute)
	a2, _ := f.issue("u1")
	f.set(2 * time.Minute)
	f.mustAuth(a1, "the first device")

	f.set(3 * time.Minute)
	a3, _ := f.issue("u1")
	f.mustFail(a2, "the least recently seen device past the cap")
	f.mustAuth(a1, "a recently seen device")
	f.mustAuth(a3, "the new device")
	f.mustAuth(b1, "another subject's device")
}

// rulesRotationsCannotFlushTheRetiredCurrent is the informed thief: it holds
// the app's token, rotates and confirms, then replaces pending tokens over and
// over to push the app's retired token out. The replaced pending hashes have
// their own list, so the app's next request still reveals the theft.
func rulesRotationsCannotFlushTheRetiredCurrent(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	app, _ := f.issue("u1")
	f.set(session.DefaultDeviceMinRotateInterval)
	thief := f.mustRotate(app, "t0")
	f.mustAuth(thief, "the thief's pending token")

	f.set(2 * session.DefaultDeviceMinRotateInterval)
	for i := range session.MaxRetiredCurrent + session.MaxRetiredPending + 1 {
		f.mustRotate(thief, fmt.Sprintf("t%d", i+1))
	}
	d, _ := f.record(thief)
	pendings := 0
	for _, r := range d.Retired {
		if r.Kind == session.RetiredPending {
			pendings++
		}
	}
	if pendings > session.MaxRetiredPending {
		t.Errorf("%d retired pending hashes, want at most %d", pendings, session.MaxRetiredPending)
	}

	f.mustFail(app, "the app's retired token after the thief's rotations")
	f.mustFail(thief, "the thief's token after the app revealed the reuse")
	f.wantGone(thief, "a device revoked for reuse")
}

// rulesIssueAfterEndAllRefused is a password change that lands between the
// check of the old password and the issue of the device.
func rulesIssueAfterEndAllRefused(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	f.set(time.Minute)
	checked := f.clock.Now()

	f.set(2 * time.Minute)
	endedAt := f.clock.Now()
	if err := f.manager.EndAll(context.Background(), "u1"); err != nil {
		t.Fatalf("EndAll: %v", err)
	}
	// The issue itself runs after EndAll. Only the time of the credential
	// check can tell that the check came first.
	f.set(2*time.Minute + 30*time.Second)
	for name, at := range map[string]time.Time{"before EndAll": checked, "at EndAll": endedAt} {
		token, _, err := f.manager.IssueDevice(context.Background(), "u1", "phone", at)
		if !errors.Is(err, session.ErrStaleAuthentication) || token != "" {
			t.Errorf("IssueDevice with a credential check %s = %v, want ErrStaleAuthentication", name, err)
		}
	}
	if list, err := f.manager.ListDevices(context.Background(), "u1"); err != nil || len(list) != 0 {
		t.Errorf("devices after a refused issue = %+v, %v", list, err)
	}

	f.set(3 * time.Minute)
	token, _ := f.issue("u1")
	f.mustAuth(token, "a device whose credential check followed EndAll")
}

func rulesIssueRefusesAnOldAuthentication(t *testing.T, newStore NewStore) {
	f := newFixture(t, newStore, nil)
	f.set(time.Hour)
	now := f.clock.Now()

	issue := func(at time.Time) error {
		_, _, err := f.manager.IssueDevice(context.Background(), "u1", "phone", at)
		return err
	}
	if err := issue(now.Add(-session.MaxDeviceIssueDelay)); !errors.Is(err, session.ErrStaleAuthentication) {
		t.Errorf("a credential check one issue delay ago = %v, want ErrStaleAuthentication", err)
	}
	if err := issue(time.Time{}); err == nil {
		t.Error("a zero credential check time was accepted")
	}
	if err := issue(now.Add(time.Nanosecond)); err == nil {
		t.Error("a credential check in the future was accepted")
	}
	if err := issue(now.Add(-session.MaxDeviceIssueDelay + time.Nanosecond)); err != nil {
		t.Errorf("a credential check inside the issue delay = %v", err)
	}
}

// race runs fn on n goroutines at once and waits for all of them.
func race(n int, fn func(i int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			fn(i)
		})
	}
	close(start)
	wg.Wait()
}

func raceConcurrentRotationsOneWins(t *testing.T, newStore NewStore) {
	const rounds, callers = 10, 8
	for range rounds {
		f := newFixture(t, newStore, nil)
		token, _ := f.issue("u1")
		f.set(session.DefaultDeviceMinRotateInterval)

		tokens := make([]string, callers)
		errs := make([]error, callers)
		f.barrier.arm(callers)
		race(callers, func(i int) {
			tokens[i], _, errs[i] = f.manager.RotateDevice(context.Background(), token, fmt.Sprintf("n%d", i))
		})

		winner := ""
		for i := range callers {
			switch {
			case errs[i] == nil && winner != "":
				t.Fatal("two concurrent rotations both issued a pending token")
			case errs[i] == nil:
				winner = tokens[i]
			case !errors.Is(errs[i], session.ErrRotateRefused):
				t.Fatalf("losing rotation = %v, want ErrRotateRefused", errs[i])
			}
		}
		if winner == "" {
			t.Fatal("no rotation won")
		}
		d, _ := f.record(token)
		if d.PendingHash != sha256.Sum256([]byte(winner)) || len(d.Retired) != 0 {
			t.Fatalf("the stored pending token is not the winner's, retired %d", len(d.Retired))
		}
	}
}

func raceConcurrentConfirmsRetireOnce(t *testing.T, newStore NewStore) {
	const rounds, callers = 10, 8
	for range rounds {
		f := newFixture(t, newStore, nil)
		token, _ := f.issue("u1")
		f.set(session.DefaultDeviceMinRotateInterval)
		pending := f.mustRotate(token, "n1")

		errs := make([]error, callers)
		f.barrier.arm(callers)
		race(callers, func(i int) {
			_, errs[i] = f.manager.AuthenticateDevice(context.Background(), pending)
		})
		for _, err := range errs {
			if err != nil {
				t.Fatalf("a concurrent first use of the pending token failed: %v", err)
			}
		}
		d, slot := f.record(pending)
		if slot != session.SlotCurrent || len(d.Retired) != 1 {
			t.Fatalf("slot %d, %d retired entries, want one confirm", slot, len(d.Retired))
		}
	}
}

func raceSameNonceRetriesOneWins(t *testing.T, newStore NewStore) {
	const rounds, callers = 10, 8
	for range rounds {
		f := newFixture(t, newStore, nil)
		token, _ := f.issue("u1")
		f.set(session.DefaultDeviceMinRotateInterval)
		first := f.mustRotate(token, "n1")
		f.set(session.DefaultDeviceMinRotateInterval + time.Second)

		tokens := make([]string, callers)
		errs := make([]error, callers)
		f.barrier.arm(callers)
		race(callers, func(i int) {
			tokens[i], _, errs[i] = f.manager.RotateDevice(context.Background(), token, "n1")
		})

		wins := 0
		winner := ""
		for i := range callers {
			if errs[i] == nil {
				wins++
				winner = tokens[i]
			} else if !errors.Is(errs[i], session.ErrRotateRefused) {
				t.Fatalf("losing retry = %v, want ErrRotateRefused", errs[i])
			}
		}
		if wins != 1 {
			t.Fatalf("%d concurrent retries overwrote the pending token, want 1", wins)
		}
		d, _ := f.record(token)
		if d.PendingHash != sha256.Sum256([]byte(winner)) || len(d.Retired) != 0 {
			t.Fatalf("the pending token is not the winner's, or a retry retired one")
		}
		f.mustFail(first, "an overwritten pending token")
		f.mustAuth(token, "the current token")
	}
}

func raceReuseRevokesUnderConcurrency(t *testing.T, newStore NewStore) {
	const rounds, callers = 10, 8
	for range rounds {
		f := newFixture(t, newStore, nil)
		old, current, at := confirmed(f, "u1")
		f.set(at + session.DefaultDeviceGrace)

		oldErrs := make([]error, callers)
		f.barrier.arm(2 * callers)
		race(2*callers, func(i int) {
			if i%2 == 0 {
				_, oldErrs[i/2] = f.manager.AuthenticateDevice(context.Background(), old)
				return
			}
			_, _ = f.manager.AuthenticateDevice(context.Background(), current)
		})
		for _, err := range oldErrs {
			if !errors.Is(err, session.ErrDeviceToken) {
				t.Fatalf("a reused token = %v, want ErrDeviceToken", err)
			}
		}
		f.mustFail(current, "the current token after concurrent reuse")
		if list, err := f.manager.ListDevices(context.Background(), "u1"); err != nil || len(list) != 0 {
			t.Fatalf("devices after reuse = %+v, %v, want none", list, err)
		}
	}
}

// raceRevokeBeatsRotateAndConfirm is the owner revoking a device while its
// tokens are confirmed and rotated. Once all calls return, the device is gone
// and no token of it authenticates.
func raceRevokeBeatsRotateAndConfirm(t *testing.T, newStore NewStore) {
	const rounds, callers = 10, 9
	for range rounds {
		f := newFixture(t, newStore, nil)
		token, device := f.issue("u1")
		f.set(session.DefaultDeviceMinRotateInterval)
		pending := f.mustRotate(token, "n0")
		f.set(session.DefaultDeviceMinRotateInterval + time.Second)

		var mu sync.Mutex
		all := []string{token, pending}
		race(callers, func(i int) {
			switch {
			case i == 0:
				err := f.manager.RevokeDevice(context.Background(), "u1", device.ID)
				if err != nil && !errors.Is(err, session.ErrNotFound) {
					t.Errorf("RevokeDevice: %v", err)
				}
			case i%3 == 0:
				_, _ = f.manager.AuthenticateDevice(context.Background(), pending)
			case i%3 == 1:
				next, _, err := f.manager.RotateDevice(context.Background(), token, fmt.Sprintf("n%d", i))
				if err == nil {
					mu.Lock()
					all = append(all, next)
					mu.Unlock()
				}
			default:
				_, _ = f.manager.AuthenticateDevice(context.Background(), token)
			}
		})

		for _, x := range all {
			f.mustFail(x, "a token of a revoked device")
		}
		if list, err := f.manager.ListDevices(context.Background(), "u1"); err != nil || len(list) != 0 {
			t.Fatalf("devices after a revoke = %+v, %v, want none", list, err)
		}
	}
}

// raceConfirmAgainstRotation is the first use of a pending token racing a
// rotation with the current token that would retire it. Never both win, and
// when the rotation wins, the confirm reveals a second holder.
func raceConfirmAgainstRotation(t *testing.T, newStore NewStore) {
	const rounds = 20
	for range rounds {
		f := newFixture(t, newStore, nil)
		current, _ := f.issue("u1")
		f.set(session.DefaultDeviceMinRotateInterval)
		first := f.mustRotate(current, "thief")
		f.set(session.DefaultDeviceMinRotateInterval + time.Second)

		var confirmErr, rotateErr error
		var second string
		f.barrier.arm(2)
		race(2, func(i int) {
			if i == 0 {
				_, confirmErr = f.manager.AuthenticateDevice(context.Background(), first)
				return
			}
			second, _, rotateErr = f.manager.RotateDevice(context.Background(), current, "app")
		})

		switch {
		case confirmErr == nil && rotateErr == nil:
			t.Fatal("the confirm and the rotation both won")
		case errors.Is(confirmErr, session.ErrDeviceToken):
			for _, x := range []string{current, first, second} {
				if x != "" {
					f.mustFail(x, "a token of a device revoked for reuse")
				}
			}
		case confirmErr != nil:
			t.Fatalf("confirm = %v", confirmErr)
		}
	}
}

func raceEndAllBeatsRotation(t *testing.T, newStore NewStore) {
	const rounds, callers = 10, 8
	for range rounds {
		f := newFixture(t, newStore, nil)
		token, _ := f.issue("u1")
		f.set(session.DefaultDeviceMinRotateInterval)

		var mu sync.Mutex
		issued := []string{}
		race(2*callers+1, func(i int) {
			switch {
			case i == 2*callers:
				if err := f.manager.EndAll(context.Background(), "u1"); err != nil {
					t.Errorf("EndAll: %v", err)
				}
			case i%2 == 0:
				next, _, err := f.manager.RotateDevice(context.Background(), token, fmt.Sprintf("n%d", i))
				if err == nil {
					mu.Lock()
					issued = append(issued, next)
					mu.Unlock()
				}
			default:
				_, _ = f.manager.AuthenticateDevice(context.Background(), token)
			}
		})

		f.mustFail(token, "a token after EndAll")
		for _, next := range issued {
			f.mustFail(next, "a token issued by a rotation racing EndAll")
		}
		if list, err := f.manager.ListDevices(context.Background(), "u1"); err != nil || len(list) != 0 {
			t.Fatalf("devices after EndAll = %+v, %v, want none", list, err)
		}
	}
}
