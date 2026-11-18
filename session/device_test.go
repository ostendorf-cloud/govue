// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package session_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/session"
	"github.com/ostendorf-cloud/govue/session/sessiontest"
)

// MemoryDeviceStore satisfies the interface this package publishes.
var _ session.DeviceStore = (*session.MemoryDeviceStore)(nil)

func TestMemoryDeviceStoreConformance(t *testing.T) {
	sessiontest.DeviceStore(t, func(*testing.T) session.DeviceStore {
		return session.NewMemoryDeviceStore(nil, 1000)
	})
}

// deviceRecord is a fresh record with only a current hash.
func deviceRecord(id, subject string, at time.Time) session.DeviceSession {
	return session.DeviceSession{
		ID: id, Subject: subject, CreatedAt: at, LastSeen: at, ExpiresAt: at.Add(time.Hour),
		DeviceState: session.DeviceState{CurrentHash: sha256.Sum256([]byte(id)), RotatedAt: at},
	}
}

// TestRetiredBoundsFollowTheADR pins the numbers ADR 0001 settled: four
// retired current hashes and two retired pending ones.
func TestRetiredBoundsFollowTheADR(t *testing.T) {
	if session.MaxRetiredCurrent != 4 || session.MaxRetiredPending != 2 {
		t.Errorf("retired bounds = %d and %d, want 4 and 2", session.MaxRetiredCurrent, session.MaxRetiredPending)
	}
}

func TestMemoryDeviceStoreRefusesEveryoneWhenFull(t *testing.T) {
	store := session.NewMemoryDeviceStore(func() time.Time { return start }, 2)
	for _, d := range []session.DeviceSession{
		deviceRecord("a", "u1", start),
		deviceRecord("b", "u2", start),
	} {
		if _, err := store.Create(context.Background(), d, 10, start); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	_, err := store.Create(context.Background(), deviceRecord("c", "u3", start), 10, start)
	if !errors.Is(err, session.ErrDeviceStoreFull) {
		t.Errorf("Create in a full store = %v, want ErrDeviceStoreFull", err)
	}
	if store.Len() != 2 {
		t.Errorf("Len = %d, want 2: a full store evicts nobody", store.Len())
	}
}

func TestMemoryDeviceStoreDropsExpiredRecords(t *testing.T) {
	now := start
	store := session.NewMemoryDeviceStore(func() time.Time { return now }, 1)
	old := deviceRecord("old", "u1", start)
	if _, err := store.Create(context.Background(), old, 10, start); err != nil {
		t.Fatalf("Create: %v", err)
	}

	now = start.Add(time.Hour)
	if _, _, err := store.Lookup(context.Background(), old.CurrentHash); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("Lookup of an expired record = %v, want ErrNotFound", err)
	}
	// The store is full of an expired record, so the sweep makes room.
	if _, err := store.Create(context.Background(), deviceRecord("new", "u2", now), 10, now); err != nil {
		t.Errorf("Create after the only record expired = %v", err)
	}
	if store.Len() != 1 {
		t.Errorf("Len = %d, want 1", store.Len())
	}
}

// TestMemoryDeviceStoreBoundsWhatItRemembersOfDeleteSubject pins the bound on
// the deletion times: past it, the store keeps one floor for every subject
// rather than forgetting a deletion while an issue may still race it.
func TestMemoryDeviceStoreBoundsWhatItRemembersOfDeleteSubject(t *testing.T) {
	now := start
	store := session.NewMemoryDeviceStore(func() time.Time { return now }, 2)
	ctx := context.Background()
	for _, subject := range []string{"u1", "u2", "u3"} {
		if err := store.DeleteSubject(ctx, subject, start); err != nil {
			t.Fatalf("DeleteSubject: %v", err)
		}
	}

	for _, subject := range []string{"u1", "u2", "u3", "u4"} {
		d := deviceRecord("stale-"+subject, subject, start)
		_, err := store.Create(ctx, d, 10, start.Add(-time.Second))
		if subject == "u4" {
			// u4 was never deleted, but the floor covers every subject once
			// the remembered deletions overflowed. That fails closed.
			if !errors.Is(err, session.ErrStaleAuthentication) {
				t.Errorf("Create for %s under the floor = %v, want ErrStaleAuthentication", subject, err)
			}
			continue
		}
		if !errors.Is(err, session.ErrStaleAuthentication) {
			t.Errorf("Create for %s = %v, want ErrStaleAuthentication", subject, err)
		}
	}
	if _, err := store.Create(ctx, deviceRecord("fresh", "u3", start), 10, start.Add(time.Nanosecond)); err != nil {
		t.Errorf("Create after the floor = %v", err)
	}

	// Long after the issue delay, nothing that old can be issued anyway, and
	// the store forgets.
	now = start.Add(3 * session.MaxDeviceIssueDelay)
	if _, err := store.Create(ctx, deviceRecord("late", "u1", now), 10, start.Add(-time.Second)); err != nil {
		t.Errorf("Create after the deletion times were dropped = %v", err)
	}
}

type deviceFixture struct {
	now     time.Time
	store   *countingDeviceStore
	manager *session.Manager
}

func newDeviceFixture(t *testing.T, adjust func(*session.Config)) *deviceFixture {
	t.Helper()
	f := &deviceFixture{now: start}
	clock := func() time.Time { return f.now }
	f.store = &countingDeviceStore{DeviceStore: session.NewMemoryDeviceStore(clock, 100)}
	cfg := session.Config{
		Store:   session.NewMemoryStore(clock, 100),
		Now:     clock,
		Devices: session.DeviceConfig{Store: f.store},
	}
	if adjust != nil {
		adjust(&cfg)
	}
	manager, err := session.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.manager = manager
	return f
}

func (f *deviceFixture) issue(t *testing.T) (string, session.Device) {
	t.Helper()
	token, device, err := f.manager.IssueDevice(context.Background(), "u1", "phone", f.now)
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	return token, device
}

// countingDeviceStore counts lookups.
type countingDeviceStore struct {
	session.DeviceStore
	lookups atomic.Int32
}

func (s *countingDeviceStore) Lookup(ctx context.Context, h session.TokenHash) (session.DeviceSession, session.DeviceSlot, error) {
	s.lookups.Add(1)
	return s.DeviceStore.Lookup(ctx, h)
}

func (s *countingDeviceStore) Len() int {
	memory, ok := s.DeviceStore.(*session.MemoryDeviceStore)
	if !ok {
		return -1
	}
	return memory.Len()
}

// hookStore is a memory store whose methods a test can replace one by one.
type hookStore struct {
	*session.MemoryDeviceStore
	lookup func(session.DeviceSession, session.DeviceSlot, error) (session.DeviceSession, session.DeviceSlot, error)
	touch  func(ctx context.Context, id string, at time.Time) error
	swap   func(ctx context.Context, id string, expect, next session.DeviceState, retire []session.RetiredHash) error
	revoke func(ctx context.Context, subject, id string) error
}

func (s *hookStore) Lookup(ctx context.Context, h session.TokenHash) (session.DeviceSession, session.DeviceSlot, error) {
	d, slot, err := s.MemoryDeviceStore.Lookup(ctx, h)
	if s.lookup != nil {
		return s.lookup(d, slot, err)
	}
	return d, slot, err
}

func (s *hookStore) Touch(ctx context.Context, id string, at time.Time) error {
	if s.touch != nil {
		return s.touch(ctx, id, at)
	}
	return s.MemoryDeviceStore.Touch(ctx, id, at)
}

func (s *hookStore) Swap(ctx context.Context, id string, expect, next session.DeviceState, retire []session.RetiredHash) error {
	if s.swap != nil {
		return s.swap(ctx, id, expect, next, retire)
	}
	return s.MemoryDeviceStore.Swap(ctx, id, expect, next, retire)
}

func (s *hookStore) Revoke(ctx context.Context, subject, id string) error {
	if s.revoke != nil {
		return s.revoke(ctx, subject, id)
	}
	return s.MemoryDeviceStore.Revoke(ctx, subject, id)
}

// hooked is a manager on a hookStore with a clock the test moves by hand.
type hooked struct {
	now     time.Time
	store   *hookStore
	manager *session.Manager
}

func newHooked(t *testing.T) *hooked {
	t.Helper()
	h := &hooked{now: start}
	clock := func() time.Time { return h.now }
	h.store = &hookStore{MemoryDeviceStore: session.NewMemoryDeviceStore(clock, 100)}
	manager, err := session.New(session.Config{
		Store:   session.NewMemoryStore(clock, 100),
		Now:     clock,
		Devices: session.DeviceConfig{Store: h.store},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.manager = manager
	return h
}

func (h *hooked) issue(t *testing.T) string {
	t.Helper()
	token, _, err := h.manager.IssueDevice(context.Background(), "u1", "phone", h.now)
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	return token
}

// confirm rotates after the minimum interval and confirms the new token.
func (h *hooked) confirm(t *testing.T, token string) string {
	t.Helper()
	h.now = h.now.Add(session.DefaultDeviceMinRotateInterval)
	next, _, err := h.manager.RotateDevice(context.Background(), token, "n1")
	if err != nil {
		t.Fatalf("RotateDevice: %v", err)
	}
	if _, err := h.manager.AuthenticateDevice(context.Background(), next); err != nil {
		t.Fatalf("AuthenticateDevice: %v", err)
	}
	return next
}

func TestDeviceTokenShape(t *testing.T) {
	for _, prefix := range []string{"", "acme"} {
		f := newDeviceFixture(t, func(c *session.Config) { c.Devices.TokenPrefix = prefix })
		token, _ := f.issue(t)

		want := prefix
		if want == "" {
			want = session.DefaultDeviceTokenPrefix
		}
		if !strings.HasPrefix(token, want+"_") {
			t.Errorf("token does not start with %q", want+"_")
		}
		rest := strings.TrimPrefix(token, want+"_")
		// 32 random bytes are 43 base64url characters, then a separator and
		// a six character checksum.
		if len(rest) != 43+1+6 || rest[43] != '_' {
			t.Fatalf("token body has the wrong shape: %d characters", len(rest))
		}
		raw, err := base64.RawURLEncoding.DecodeString(rest[:43])
		if err != nil || len(raw) != 32 {
			t.Errorf("token body is not 32 base64url bytes: %v", err)
		}
		if _, err := f.manager.AuthenticateDevice(context.Background(), token); err != nil {
			t.Errorf("AuthenticateDevice of a fresh token: %v", err)
		}
	}
}

func TestEveryDeviceTokenIsNew(t *testing.T) {
	f := newDeviceFixture(t, func(c *session.Config) { c.Devices.MaxPerSubject = 100 })
	seen := map[string]bool{}
	for range 50 {
		token, device := f.issue(t)
		if seen[token] || seen[device.ID] {
			t.Fatal("a token or a device id repeated")
		}
		seen[token], seen[device.ID] = true, true
	}
}

func TestMalformedTokenNeverReachesTheStore(t *testing.T) {
	f := newDeviceFixture(t, nil)
	token, _ := f.issue(t)
	f.store.lookups.Store(0)

	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	body := len(session.DefaultDeviceTokenPrefix) + 1
	malformed := map[string]string{
		"empty":             "",
		"prefix only":       session.DefaultDeviceTokenPrefix + "_",
		"truncated":         token[:len(token)-1],
		"extended":          token + "A",
		"other prefix":      "other_" + token[body:],
		"upper case prefix": strings.ToUpper(session.DefaultDeviceTokenPrefix) + token[len(session.DefaultDeviceTokenPrefix):],
		"flipped checksum":  flip(token, len(token)-1),
		"flipped body":      flip(token, body+5),
		"missing separator": token[:len(token)-7] + "-" + token[len(token)-6:],
		"with scheme":       "Bearer " + token,
		"padded":            token + "=",
		"space":             " " + token,
	}
	for name, value := range malformed {
		if _, err := f.manager.AuthenticateDevice(context.Background(), value); !errors.Is(err, session.ErrDeviceToken) {
			t.Errorf("%s: AuthenticateDevice = %v, want ErrDeviceToken", name, err)
		}
		if _, _, err := f.manager.RotateDevice(context.Background(), value, "n1"); !errors.Is(err, session.ErrDeviceToken) {
			t.Errorf("%s: RotateDevice = %v, want ErrDeviceToken", name, err)
		}
	}
	if n := f.store.lookups.Load(); n != 0 {
		t.Errorf("malformed tokens reached the store %d times", n)
	}
}

func TestDevicesDisabledWithoutAStore(t *testing.T) {
	manager, err := session.New(session.Config{Store: session.NewMemoryStore(nil, 10)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	_, _, issueErr := manager.IssueDevice(ctx, "u1", "phone", time.Now())
	_, authErr := manager.AuthenticateDevice(ctx, "device_x")
	_, _, rotateErr := manager.RotateDevice(ctx, "device_x", "n1")
	_, listErr := manager.ListDevices(ctx, "u1")
	revokeErr := manager.RevokeDevice(ctx, "u1", "d1")
	for name, err := range map[string]error{
		"IssueDevice": issueErr, "AuthenticateDevice": authErr, "RotateDevice": rotateErr,
		"ListDevices": listErr, "RevokeDevice": revokeErr,
	} {
		if !errors.Is(err, session.ErrDevicesDisabled) {
			t.Errorf("%s = %v, want ErrDevicesDisabled", name, err)
		}
	}
	if err := manager.EndAll(ctx, "u1"); err != nil {
		t.Errorf("EndAll without a device store = %v", err)
	}
}

func TestNewValidatesTheDeviceConfig(t *testing.T) {
	store := session.NewMemoryDeviceStore(nil, 10)
	cases := map[string]session.DeviceConfig{
		"negative idle":              {Store: store, Idle: -time.Hour},
		"negative lifetime":          {Store: store, Lifetime: -time.Hour},
		"negative grace":             {Store: store, Grace: -time.Second},
		"negative minimum interval":  {Store: store, MinRotateInterval: -time.Second},
		"negative rotate timeout":    {Store: store, RotateTimeout: -time.Second},
		"negative cap":               {Store: store, MaxPerSubject: -1},
		"idle not above touch":       {Store: store, Idle: time.Minute},
		"prefix with upper case":     {Store: store, TokenPrefix: "Acme"},
		"prefix with an underscore":  {Store: store, TokenPrefix: "ac_me"},
		"prefix with a dash":         {Store: store, TokenPrefix: "ac-me"},
		"prefix longer than sixteen": {Store: store, TokenPrefix: "abcdefghijklmnopq"},
	}
	for name, devices := range cases {
		if _, err := session.New(session.Config{Store: session.NewMemoryStore(nil, 10), Devices: devices}); err == nil {
			t.Errorf("%s: New accepted the config", name)
		}
	}
}

func TestIssueDeviceValidatesSubjectAndName(t *testing.T) {
	f := newDeviceFixture(t, nil)
	ctx := context.Background()

	if _, _, err := f.manager.IssueDevice(ctx, "", "phone", f.now); err == nil {
		t.Error("an empty subject was accepted")
	}
	for name, value := range map[string]string{
		"too long":             strings.Repeat("a", session.MaxDeviceNameBytes+1),
		"control":              "pho\nne",
		"invalid utf-8":        "\xff",
		"right to left mark":   "phone" + string(rune(0x200f)),
		"bidi override":        string(rune(0x202e)) + "enohp",
		"bidi isolate":         string(rune(0x2066)) + "phone" + string(rune(0x2069)),
		"zero width space":     "pho" + string(rune(0x200b)) + "ne",
		"zero width no-break":  string(rune(0xfeff)) + "phone",
		"soft hyphen (format)": "pho" + string(rune(0x00ad)) + "ne",
	} {
		if _, _, err := f.manager.IssueDevice(ctx, "u1", value, f.now); !errors.Is(err, session.ErrInvalidDeviceName) {
			t.Errorf("%s: IssueDevice = %v, want ErrInvalidDeviceName", name, err)
		}
	}
	if _, _, err := f.manager.IssueDevice(ctx, "u1", strings.Repeat("ä", session.MaxDeviceNameBytes/2), f.now); err != nil {
		t.Errorf("a name of exactly the limit was refused: %v", err)
	}
	if f.store.Len() != 1 {
		t.Errorf("Len = %d, want only the valid device", f.store.Len())
	}
}

func TestRotateDeviceValidatesTheNonce(t *testing.T) {
	f := newDeviceFixture(t, nil)
	token, _ := f.issue(t)
	f.now = start.Add(session.DefaultDeviceMinRotateInterval)

	for name, nonce := range map[string]string{
		"empty":    "",
		"too long": strings.Repeat("n", session.MaxDeviceNonceBytes+1),
		"control":  "n\x00",
		"space":    "n 1",
	} {
		if next, _, err := f.manager.RotateDevice(context.Background(), token, nonce); !errors.Is(err, session.ErrInvalidNonce) || next != "" {
			t.Errorf("%s: RotateDevice = %v, want ErrInvalidNonce", name, err)
		}
	}
	if _, _, err := f.manager.RotateDevice(context.Background(), token, strings.Repeat("n", session.MaxDeviceNonceBytes)); err != nil {
		t.Errorf("a nonce of exactly the limit was refused: %v", err)
	}
}

// leaksToken reports whether a text carries a token or its hash.
func leaksToken(text, token string) bool {
	h := sha256.Sum256([]byte(token))
	for _, form := range []string{token, hex.EncodeToString(h[:]), base64.RawURLEncoding.EncodeToString(h[:]), base64.StdEncoding.EncodeToString(h[:])} {
		if strings.Contains(text, form) {
			return true
		}
	}
	return false
}

func TestReuseIsLoggedWithoutTheToken(t *testing.T) {
	f := newDeviceFixture(t, nil)
	var buf bytes.Buffer
	ctx := logging.NewContext(context.Background(), slog.New(slog.NewJSONHandler(&buf, nil)))

	old, device := f.issue(t)
	f.now = start.Add(session.DefaultDeviceMinRotateInterval)
	current, _, err := f.manager.RotateDevice(ctx, old, "n1")
	if err != nil {
		t.Fatalf("RotateDevice: %v", err)
	}
	if _, err := f.manager.AuthenticateDevice(ctx, current); err != nil {
		t.Fatalf("AuthenticateDevice: %v", err)
	}
	f.now = f.now.Add(session.DefaultDeviceGrace)
	_, err = f.manager.AuthenticateDevice(ctx, old)
	if !errors.Is(err, session.ErrDeviceToken) {
		t.Fatalf("AuthenticateDevice with a reused token = %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, device.ID) || !strings.Contains(logged, `"u1"`) {
		t.Errorf("the reuse log lacks the device id or the subject: %s", logged)
	}
	for _, token := range []string{old, current} {
		if leaksToken(logged, token) || leaksToken(err.Error(), token) {
			t.Errorf("a log line or the error carries a token or its hash")
		}
	}
}

// TestReuseWithAFailingRevokeStillRefuses is a reuse whose revocation the
// store cannot carry out. The token still does not authenticate, the error
// reads exactly like every other token failure, and the failure is logged.
func TestReuseWithAFailingRevokeStillRefuses(t *testing.T) {
	h := newHooked(t)
	var buf bytes.Buffer
	ctx := logging.NewContext(context.Background(), slog.New(slog.NewJSONHandler(&buf, nil)))
	old := h.issue(t)
	current := h.confirm(t, old)
	h.store.revoke = func(context.Context, string, string) error { return errStore }

	h.now = h.now.Add(session.DefaultDeviceGrace)
	device, err := h.manager.AuthenticateDevice(ctx, old)
	if !errors.Is(err, session.ErrDeviceToken) || device.ID != "" {
		t.Fatalf("reused token with a failing revoke = %+v, %v, want ErrDeviceToken", device, err)
	}
	if err.Error() != session.ErrDeviceToken.Error() {
		t.Errorf("error text %q differs from every other token failure", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, errStore.Error()) {
		t.Errorf("the failed revocation is not logged: %s", logged)
	}
	for _, token := range []string{old, current} {
		if leaksToken(logged, token) {
			t.Error("the log carries a token or its hash")
		}
	}
}

// TestAStoreThatLiesAboutTheSlotIsRefused is a store that reports a retired
// or pending hash as the current one. The slot decides everything, so the
// manager takes it from the record and refuses the disagreement.
func TestAStoreThatLiesAboutTheSlotIsRefused(t *testing.T) {
	h := newHooked(t)
	old := h.issue(t)
	h.confirm(t, old)
	h.store.lookup = func(d session.DeviceSession, _ session.DeviceSlot, err error) (session.DeviceSession, session.DeviceSlot, error) {
		return d, session.SlotCurrent, err
	}

	h.now = h.now.Add(session.DefaultDeviceGrace)
	if device, err := h.manager.AuthenticateDevice(context.Background(), old); err == nil {
		t.Errorf("a retired token reported as current authenticated: %+v", device)
	}
	if next, _, err := h.manager.RotateDevice(context.Background(), old, "n2"); err == nil || next != "" {
		t.Errorf("a retired token reported as current rotated: %v", err)
	}
}

// TestARecordWithoutAnIDIsRefused is defence in depth against a store that
// returns a record it cannot name: nothing could revoke or touch it.
func TestARecordWithoutAnIDIsRefused(t *testing.T) {
	h := newHooked(t)
	token := h.issue(t)
	h.store.lookup = func(d session.DeviceSession, slot session.DeviceSlot, err error) (session.DeviceSession, session.DeviceSlot, error) {
		d.ID = ""
		return d, slot, err
	}

	if device, err := h.manager.AuthenticateDevice(context.Background(), token); err == nil {
		t.Errorf("a record without an id authenticated: %+v", device)
	}
	if next, _, err := h.manager.RotateDevice(context.Background(), token, "n1"); err == nil || next != "" {
		t.Errorf("a record without an id rotated: %v", err)
	}
}

func TestAFailedTouchStillAuthenticates(t *testing.T) {
	h := newHooked(t)
	token := h.issue(t)
	h.store.touch = func(context.Context, string, time.Time) error { return errStore }

	h.now = h.now.Add(session.DefaultTouchEvery)
	device, err := h.manager.AuthenticateDevice(context.Background(), token)
	if err != nil || device.Subject != "u1" {
		t.Errorf("AuthenticateDevice with a failing touch = %+v, %v, want the device", device, err)
	}
}

// TestRotateAgainstAConcurrentlyRevokedDevice is a revocation that lands
// between a rotation's read and its swap.
func TestRotateAgainstAConcurrentlyRevokedDevice(t *testing.T) {
	h := newHooked(t)
	token := h.issue(t)
	h.store.swap = func(ctx context.Context, id string, expect, next session.DeviceState, retire []session.RetiredHash) error {
		if err := h.store.MemoryDeviceStore.Revoke(ctx, "u1", id); err != nil {
			return err
		}
		return h.store.MemoryDeviceStore.Swap(ctx, id, expect, next, retire)
	}

	h.now = h.now.Add(session.DefaultDeviceMinRotateInterval)
	next, device, err := h.manager.RotateDevice(context.Background(), token, "n1")
	if !errors.Is(err, session.ErrDeviceToken) || next != "" || device.ID != "" {
		t.Errorf("RotateDevice of a revoked device = %q, %+v, %v, want ErrDeviceToken and nothing", next, device, err)
	}
	if h.store.Len() != 0 {
		t.Error("the rotation brought the revoked device back")
	}
}

func TestRotateSwapOutageIsNotARefusal(t *testing.T) {
	h := newHooked(t)
	token := h.issue(t)
	h.store.swap = func(context.Context, string, session.DeviceState, session.DeviceState, []session.RetiredHash) error {
		return errStore
	}

	h.now = h.now.Add(session.DefaultDeviceMinRotateInterval)
	next, _, err := h.manager.RotateDevice(context.Background(), token, "n1")
	if next != "" || !errors.Is(err, errStore) || errors.Is(err, session.ErrRotateRefused) || errors.Is(err, session.ErrDeviceToken) {
		t.Errorf("RotateDevice with a store outage = %q, %v, want the store error only", next, err)
	}
}

func TestRotateWithThePendingTokenReturnsTheDevice(t *testing.T) {
	h := newHooked(t)
	token := h.issue(t)
	h.now = h.now.Add(session.DefaultDeviceMinRotateInterval)
	pending, _, err := h.manager.RotateDevice(context.Background(), token, "n1")
	if err != nil {
		t.Fatalf("RotateDevice: %v", err)
	}

	next, device, err := h.manager.RotateDevice(context.Background(), pending, "n2")
	if !errors.Is(err, session.ErrRotateRefused) || next != "" || device.Subject != "u1" {
		t.Errorf("RotateDevice with the pending token = %q, %+v, %v, want the confirmed device and ErrRotateRefused", next, device, err)
	}
}

func TestCapEvictionIsLogged(t *testing.T) {
	f := newDeviceFixture(t, func(c *session.Config) { c.Devices.MaxPerSubject = 1 })
	var buf bytes.Buffer
	ctx := logging.NewContext(context.Background(), slog.New(slog.NewJSONHandler(&buf, nil)))

	first, evicted, err := f.manager.IssueDevice(ctx, "u1", "phone", f.now)
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	f.now = f.now.Add(time.Second)
	second, _, err := f.manager.IssueDevice(ctx, "u1", "tablet", f.now)
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, evicted.ID) || !strings.Contains(logged, `"u1"`) {
		t.Errorf("the eviction log lacks the device id or the subject: %s", logged)
	}
	if leaksToken(logged, first) || leaksToken(logged, second) {
		t.Error("the eviction log carries a token or its hash")
	}
}

// failingDeviceStore cannot be reached.
type failingDeviceStore struct{}

func (failingDeviceStore) Create(context.Context, session.DeviceSession, int, time.Time) ([]string, error) {
	return nil, errStore
}

func (failingDeviceStore) Lookup(context.Context, session.TokenHash) (session.DeviceSession, session.DeviceSlot, error) {
	return session.DeviceSession{}, 0, errStore
}
func (failingDeviceStore) Touch(context.Context, string, time.Time) error { return errStore }
func (failingDeviceStore) Swap(context.Context, string, session.DeviceState, session.DeviceState, []session.RetiredHash) error {
	return errStore
}

func (failingDeviceStore) Revoke(context.Context, string, string) error { return errStore }

func (failingDeviceStore) DeleteSubject(context.Context, string, time.Time) error { return errStore }

func (failingDeviceStore) List(context.Context, string) ([]session.DeviceSession, error) {
	return nil, errStore
}

func TestDeviceStoreOutageIsNotATokenError(t *testing.T) {
	token, _ := newDeviceFixture(t, nil).issue(t)
	manager, err := session.New(session.Config{
		Store:   session.NewMemoryStore(nil, 10),
		Devices: session.DeviceConfig{Store: failingDeviceStore{}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// An outage must stay apart from a bad token, so a caller can answer 503
	// instead of signing the app out.
	_, authErr := manager.AuthenticateDevice(context.Background(), token)
	_, _, rotateErr := manager.RotateDevice(context.Background(), token, "n1")
	for name, err := range map[string]error{"AuthenticateDevice": authErr, "RotateDevice": rotateErr} {
		if err == nil || errors.Is(err, session.ErrDeviceToken) || !errors.Is(err, errStore) {
			t.Errorf("%s = %v, want the store error and not ErrDeviceToken", name, err)
		}
		if leaksToken(err.Error(), token) {
			t.Errorf("%s error carries the token", name)
		}
	}
}

func TestEndAllEndsDevicesWhenTheCookieStoreFails(t *testing.T) {
	devices := session.NewMemoryDeviceStore(nil, 10)
	manager, err := session.New(session.Config{Store: failingStore{}, Devices: session.DeviceConfig{Store: devices}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token, _, err := manager.IssueDevice(context.Background(), "u1", "phone", time.Now())
	if err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}

	if err := manager.EndAll(context.Background(), "u1"); !errors.Is(err, errStore) {
		t.Errorf("EndAll = %v, want the cookie store error", err)
	}
	if _, err := manager.AuthenticateDevice(context.Background(), token); !errors.Is(err, session.ErrDeviceToken) {
		t.Errorf("a device survived EndAll: %v", err)
	}
}

func TestEndAllReportsADeviceStoreFailure(t *testing.T) {
	manager, err := session.New(session.Config{
		Store:   session.NewMemoryStore(nil, 10),
		Devices: session.DeviceConfig{Store: failingDeviceStore{}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := manager.EndAll(context.Background(), "u1"); !errors.Is(err, errStore) {
		t.Errorf("EndAll = %v, want the device store error", err)
	}
}

// leakyDeviceStore lists every record, whatever the subject.
type leakyDeviceStore struct {
	*session.MemoryDeviceStore
	subjects []string
}

func (s leakyDeviceStore) List(ctx context.Context, _ string) ([]session.DeviceSession, error) {
	var all []session.DeviceSession
	for _, subject := range s.subjects {
		records, err := s.MemoryDeviceStore.List(ctx, subject)
		if err != nil {
			return nil, err
		}
		all = append(all, records...)
	}
	return all, nil
}

func TestListDevicesFiltersAStoreThatLeaks(t *testing.T) {
	store := leakyDeviceStore{MemoryDeviceStore: session.NewMemoryDeviceStore(nil, 10), subjects: []string{"u1", "u2"}}
	manager, err := session.New(session.Config{Store: session.NewMemoryStore(nil, 10), Devices: session.DeviceConfig{Store: store}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, subject := range []string{"u1", "u2"} {
		if _, _, err := manager.IssueDevice(context.Background(), subject, "phone", time.Now()); err != nil {
			t.Fatalf("IssueDevice: %v", err)
		}
	}

	list, err := manager.ListDevices(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(list) != 1 || list[0].Subject != "u1" {
		t.Errorf("ListDevices(u1) = %+v, want only the device of u1", list)
	}
}

// TestEndBetweenLookupAndTouchWins is EndAll landing between a request's
// lookup and its touch.
func TestEndBetweenLookupAndTouchWins(t *testing.T) {
	h := newHooked(t)
	token := h.issue(t)
	h.store.touch = func(ctx context.Context, id string, at time.Time) error {
		if err := h.store.DeleteSubject(ctx, "u1", h.now); err != nil {
			return err
		}
		return h.store.MemoryDeviceStore.Touch(ctx, id, at)
	}

	h.now = h.now.Add(session.DefaultTouchEvery)
	if device, err := h.manager.AuthenticateDevice(context.Background(), token); !errors.Is(err, session.ErrDeviceToken) {
		t.Errorf("AuthenticateDevice of a device ended before its touch = %+v, %v, want ErrDeviceToken", device, err)
	}
}
