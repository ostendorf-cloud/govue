// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/ostendorf-cloud/govue/passkey/passkeytest"
)

var b64 = base64.RawURLEncoding

// authenticator is passkeytest.Authenticator with the test's t, so a test
// reads as a sequence of steps without an error check after each.
type authenticator struct {
	*passkeytest.Authenticator
	t *testing.T
}

func newAuthenticator(t *testing.T) *authenticator {
	t.Helper()
	a, err := passkeytest.NewAuthenticator(testOrigin, testRPID)
	if err != nil {
		t.Fatal(err)
	}
	return &authenticator{Authenticator: a, t: t}
}

func (a *authenticator) create(beginBody []byte) json.RawMessage {
	a.t.Helper()
	raw, err := a.Create(beginBody)
	if err != nil {
		a.t.Fatal(err)
	}
	return raw
}

func (a *authenticator) get(beginBody []byte) json.RawMessage {
	a.t.Helper()
	raw, err := a.Get(beginBody)
	if err != nil {
		a.t.Fatal(err)
	}
	return raw
}

func (a *authenticator) clone() *authenticator {
	return &authenticator{Authenticator: a.Clone(), t: a.t}
}

// options is the part of a begin answer the tests check.
type options struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		UserVerification       string `json:"userVerification"`
		AuthenticatorSelection struct {
			ResidentKey      string `json:"residentKey"`
			UserVerification string `json:"userVerification"`
		} `json:"authenticatorSelection"`
		Attestation        string `json:"attestation"`
		ExcludeCredentials []struct {
			ID string `json:"id"`
		} `json:"excludeCredentials"`
		AllowCredentials []struct {
			ID string `json:"id"`
		} `json:"allowCredentials"`
	} `json:"public_key"`
}

func parseOptions(t *testing.T, body []byte) options {
	t.Helper()
	var o options
	if err := json.Unmarshal(body, &o); err != nil {
		t.Fatalf("begin body %s: %v", body, err)
	}
	if o.PublicKey.Challenge == "" {
		t.Fatalf("begin body without a challenge: %s", body)
	}
	return o
}
