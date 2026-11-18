// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkeytest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// The flags of the authenticator data, WebAuthn section 6.1.
const (
	FlagUserPresent    byte = 0x01
	FlagUserVerified   byte = 0x04
	FlagBackupEligible byte = 0x08
	FlagBackedUp       byte = 0x10
	flagAttested       byte = 0x40
)

var b64 = base64.RawURLEncoding

// Authenticator is a software passkey for tests: one ES256 key, attestation
// "none" and a signature counter the test sets. It plays the browser and the
// authenticator together. Each exported field is a knob a test turns to
// produce one specific wrong answer, a wrong origin or a counter that went
// back, against handlers that otherwise accept it.
//
// It is a test helper. It never protects a key and must not hold a real one.
type Authenticator struct {
	// Origin is the origin the client data names.
	Origin string

	// RPID is hashed into the authenticator data.
	RPID string

	// Count is the signature counter of the next response. Zero keeps it at
	// zero, as most synced passkeys do.
	Count uint32

	// Flags are the authenticator data flags of the next response. The
	// default is user present, user verified, backup eligible and backed up.
	Flags byte

	// CredentialID is the credential id, 16 random bytes.
	CredentialID []byte

	// UserHandle is the user handle the next assertion carries. Create sets
	// it from the registration options.
	UserHandle []byte

	key *ecdsa.PrivateKey
}

// NewAuthenticator returns an authenticator with a fresh key for origin and
// rpID.
func NewAuthenticator(origin, rpID string) (*Authenticator, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("passkeytest: key: %w", err)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("passkeytest: credential id: %w", err)
	}

	return &Authenticator{
		Origin: origin, RPID: rpID, CredentialID: id, key: key,
		Flags: FlagUserPresent | FlagUserVerified | FlagBackupEligible | FlagBackedUp,
	}, nil
}

// Clone returns a second authenticator with the same key, credential and
// counter, as a copied authenticator has.
func (a *Authenticator) Clone() *Authenticator {
	c := *a
	return &c
}

// beginOptions is the part of a begin answer the authenticator reads.
type beginOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"public_key"`
}

// Challenge returns the challenge of a begin answer body.
func Challenge(beginBody []byte) (string, error) {
	var o beginOptions
	if err := json.Unmarshal(beginBody, &o); err != nil {
		return "", fmt.Errorf("passkeytest: begin body: %w", err)
	}
	if o.PublicKey.Challenge == "" {
		return "", errors.New("passkeytest: begin body has no challenge")
	}

	return o.PublicKey.Challenge, nil
}

// Create answers a registration begin body the way
// PublicKeyCredential.toJSON() does after navigator.credentials.create.
func (a *Authenticator) Create(beginBody []byte) (json.RawMessage, error) {
	var o beginOptions
	if err := json.Unmarshal(beginBody, &o); err != nil {
		return nil, fmt.Errorf("passkeytest: begin body: %w", err)
	}
	handle, err := b64.DecodeString(o.PublicKey.User.ID)
	if err != nil || len(handle) == 0 {
		return nil, fmt.Errorf("passkeytest: user id %q is not base64url", o.PublicKey.User.ID)
	}
	a.UserHandle = handle

	authData, err := a.authData(true)
	if err != nil {
		return nil, err
	}
	// The keys in the canonical CBOR order: shorter first.
	var att cbor
	att.head(5, 3)
	att.text("fmt")
	att.text("none")
	att.text("attStmt")
	att.head(5, 0)
	att.text("authData")
	att.bytes(authData)

	clientData, err := a.clientData("webauthn.create", o.PublicKey.Challenge)
	if err != nil {
		return nil, err
	}

	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.CredentialID), "rawId": b64.EncodeToString(a.CredentialID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal"},
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	})
}

// Get answers a login begin body the way PublicKeyCredential.toJSON() does
// after navigator.credentials.get.
func (a *Authenticator) Get(beginBody []byte) (json.RawMessage, error) {
	challenge, err := Challenge(beginBody)
	if err != nil {
		return nil, err
	}

	return a.Sign(challenge)
}

// Sign returns an assertion over challenge, a base64url string as a begin
// answer carries it.
func (a *Authenticator) Sign(challenge string) (json.RawMessage, error) {
	clientData, err := a.clientData("webauthn.get", challenge)
	if err != nil {
		return nil, err
	}
	authData, err := a.authData(false)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(clientData)
	signed := sha256.Sum256(append(append([]byte{}, authData...), hash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, signed[:])
	if err != nil {
		return nil, fmt.Errorf("passkeytest: sign: %w", err)
	}

	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(a.CredentialID), "rawId": b64.EncodeToString(a.CredentialID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData),
			"authenticatorData": b64.EncodeToString(authData),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.UserHandle),
		},
		"clientExtensionResults":  map[string]any{},
		"authenticatorAttachment": "platform",
	})
}

func (a *Authenticator) clientData(kind, challenge string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": kind, "challenge": challenge, "origin": a.Origin, "crossOrigin": false,
	})
}

// authData is the authenticator data, with the attested credential data and
// the COSE public key when attested is true.
func (a *Authenticator) authData(attested bool) ([]byte, error) {
	rpHash := sha256.Sum256([]byte(a.RPID))
	out := append([]byte{}, rpHash[:]...)
	flags := a.Flags
	if attested {
		flags |= flagAttested
	}
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, a.Count)
	if !attested {
		return out, nil
	}

	pub, err := a.key.PublicKey.ECDH()
	if err != nil {
		return nil, fmt.Errorf("passkeytest: public key: %w", err)
	}
	point := pub.Bytes() // 0x04 || X || Y

	// The COSE_Key of an ES256 key, RFC 9053, in canonical order: kty EC2,
	// alg ES256, crv P-256, x, y.
	var key cbor
	key.head(5, 5)
	key.int(1)
	key.int(2)
	key.int(3)
	key.int(-7)
	key.int(-1)
	key.int(1)
	key.int(-2)
	key.bytes(point[1:33])
	key.int(-3)
	key.bytes(point[33:65])

	out = append(out, make([]byte, 16)...) // AAGUID, zero for "none"
	out = binary.BigEndian.AppendUint16(out, uint16(len(a.CredentialID)))
	out = append(out, a.CredentialID...)

	return append(out, key...), nil
}

// cbor is the few CBOR items, RFC 8949, a registration needs. It is written
// here so this package does not import the WebAuthn library.
type cbor []byte

// head appends the initial byte and the argument of an item.
func (c *cbor) head(major byte, n uint64) {
	switch {
	case n < 24:
		*c = append(*c, major<<5|byte(n))
	case n < 1<<8:
		*c = append(*c, major<<5|24, byte(n))
	case n < 1<<16:
		*c = binary.BigEndian.AppendUint16(append(*c, major<<5|25), uint16(n))
	default:
		*c = binary.BigEndian.AppendUint32(append(*c, major<<5|26), uint32(n))
	}
}

func (c *cbor) int(v int64) {
	if v >= 0 {
		c.head(0, uint64(v))
		return
	}
	c.head(1, uint64(-1-v))
}

func (c *cbor) bytes(b []byte) {
	c.head(2, uint64(len(b)))
	*c = append(*c, b...)
}

func (c *cbor) text(s string) {
	c.head(3, uint64(len(s)))
	*c = append(*c, s...)
}
