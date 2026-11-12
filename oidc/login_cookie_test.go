// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package oidc_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/oidc"
	"github.com/ostendorf-cloud/govue/session"
)

// TestLoginSurvivesASignedCookie is the one place the two packages meet. oidc
// imports nothing from session, so what holds them together is that a Login is
// a plain value a session.Codec can sign, and that is what this checks.
func TestLoginSurvivesASignedCookie(t *testing.T) {
	key := make([]byte, session.MinKeyBytes)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	codec, err := session.NewCodec(session.CodecConfig{Key: key})
	if err != nil {
		t.Fatal(err)
	}

	login, err := oidc.NewLogin("/lists/7")
	if err != nil {
		t.Fatal(err)
	}
	value, err := codec.Encode(login, 10*time.Minute)
	if err != nil {
		t.Fatalf("a login does not fit in a signed cookie: %v", err)
	}
	// A browser keeps about 4 KiB per cookie, which is what the codec's own
	// limit is set from. A login has to be nowhere near it.
	if len(value) > session.MaxEncodedBytes/2 {
		t.Errorf("the signed login is %d bytes, which leaves a cookie no room", len(value))
	}

	var back oidc.Login
	if err := codec.Decode(value, &back); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back != login {
		t.Errorf("the login came back as %+v, want %+v", back, login)
	}
	if !back.MatchesState(login.State) {
		t.Error("the state did not survive the cookie")
	}
}

// TestATamperedLoginCookieIsRefused is the property the whole redirect depends
// on: the state, the nonce and the PKCE verifier come back from the browser,
// so nothing but the signature stops the browser from choosing them.
func TestATamperedLoginCookieIsRefused(t *testing.T) {
	key := make([]byte, session.MinKeyBytes)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	codec, err := session.NewCodec(session.CodecConfig{Key: key})
	if err != nil {
		t.Fatal(err)
	}

	login, err := oidc.NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	value, err := codec.Encode(login, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// One character of the signed payload changed, which is as little as a
	// browser can do to it.
	tampered := []byte(value)
	if tampered[5] == 'A' {
		tampered[5] = 'B'
	} else {
		tampered[5] = 'A'
	}

	var back oidc.Login
	if err := codec.Decode(string(tampered), &back); err == nil {
		t.Error("an edited login cookie was accepted")
	}
}
