// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// macCall is one recorded HMAC of a TOTP verification.
type macCall struct {
	keyLen  int
	counter uint64
}

// macRecorder wraps hmacSHA1 and records every call, so a test asserts the
// work of a verification instead of its time.
type macRecorder struct {
	mu    sync.Mutex
	calls []macCall
}

func (r *macRecorder) mac(key []byte, counter uint64) []byte {
	r.mu.Lock()
	r.calls = append(r.calls, macCall{keyLen: len(key), counter: counter})
	r.mu.Unlock()

	return hmacSHA1(key, counter)
}

// TestVerifyTOTPCode_equalWork asserts that every path computes one HMAC per
// step of the window, over the same three counters and with a key of at least
// 128 bits, whether the secret is valid or not and whether the code matches
// or not.
func TestVerifyTOTPCode_equalWork(t *testing.T) {
	t.Parallel()

	at := time.Unix(1_700_000_010, 0)
	now := uint64(counterAt(at))
	good := codeAt(t, counterAt(at))

	tests := []struct {
		name   string
		secret string
		code   string
		wantOK bool
	}{
		{name: "valid secret, right code", secret: fakeSecret, code: good, wantOK: true},
		{name: "valid secret, wrong code", secret: fakeSecret, code: "000000"},
		{name: "valid secret, malformed code", secret: fakeSecret, code: "abc"},
		{name: "empty secret", secret: "", code: good},
		{name: "secret not base32", secret: "FAKE1SECRET0FAKE1SECRET0FAKE1SEC", code: good},
		{name: "secret below 128 bits", secret: fakeSecret[:24], code: good},
		{name: "secret above 512 bits", secret: strings.Repeat("A", 104), code: good},
		{name: "secret far above the length bound", secret: strings.Repeat("A", 4096), code: good},
		{name: "padded secret", secret: fakeSecret + "========", code: good},
		{name: "secret not canonical", secret: nonCanonicalSecret, code: good},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &macRecorder{}

			_, ok := verifyTOTPCode(rec.mac, tt.secret, tt.code, at)
			if ok != tt.wantOK {
				t.Fatalf("verifyTOTPCode ok = %v, want %v", ok, tt.wantOK)
			}

			if len(rec.calls) != 2*totpSkewSteps+1 {
				t.Fatalf("%d HMACs ran, want %d", len(rec.calls), 2*totpSkewSteps+1)
			}
			for i, call := range rec.calls {
				if want := now - totpSkewSteps + uint64(i); call.counter != want {
					t.Errorf("HMAC %d over counter %d, want %d", i, call.counter, want)
				}
				if call.keyLen < minTOTPSecretBytes {
					t.Errorf("HMAC %d keyed with %d bytes, want at least %d", i, call.keyLen, minTOTPSecretBytes)
				}
			}
		})
	}
}

// TestVerifyTOTPCode_equalWorkAtTheEpoch covers a window that holds counters
// below one. They are computed like any other step, then refused.
func TestVerifyTOTPCode_equalWorkAtTheEpoch(t *testing.T) {
	t.Parallel()

	rec := &macRecorder{}

	// Counter 0, so the window is -1, 0 and 1. A negative counter wraps.
	at := time.Unix(15, 0)
	if _, ok := verifyTOTPCode(rec.mac, fakeSecret, "000000", at); ok {
		t.Fatal("an arbitrary code matched at the epoch")
	}

	minusOne := int64(-1)
	want := []uint64{uint64(minusOne), 0, 1}
	if len(rec.calls) != len(want) {
		t.Fatalf("%d HMACs ran, want %d", len(rec.calls), len(want))
	}
	for i, call := range rec.calls {
		if call.counter != want[i] {
			t.Errorf("HMAC %d over counter %d, want %d", i, call.counter, want[i])
		}
	}
}

// The collision vector: a made up secret whose six digit code is the same
// for the two adjacent steps collisionCounter and collisionCounter+1. It was
// found by a search over secrets and checked in TestVerifyTOTPCode_highestCounter.
const (
	collisionSecret  = "AAAAAAAAATD5MAAAAAAAAAAAAAAAAAAA"
	collisionCode    = "656207"
	collisionCounter = 56666667
)

// TestVerifyTOTPCode_highestCounter pins that a code valid for two steps of
// the window returns the later one. Returning the earlier one would let the
// same code pass AdvanceTOTPCounter again one step later, while the later
// step is still in the window.
func TestVerifyTOTPCode_highestCounter(t *testing.T) {
	t.Parallel()

	for _, c := range []int64{collisionCounter, collisionCounter + 1} {
		got, err := TOTPCode(collisionSecret, time.Unix(c*totpPeriod, 0))
		if err != nil || got != collisionCode {
			t.Fatalf("TOTPCode at counter %d = %q, %v, want %q", c, got, err, collisionCode)
		}
	}

	// At every point of the window both steps are in, the later one wins.
	for _, now := range []int64{collisionCounter, collisionCounter + 1} {
		counter, ok := VerifyTOTPCode(collisionSecret, collisionCode, time.Unix(now*totpPeriod, 0))
		if !ok || counter != collisionCounter+1 {
			t.Fatalf("VerifyTOTPCode at counter %d = %d, %v, want %d, true", now, counter, ok, collisionCounter+1)
		}
	}
}
