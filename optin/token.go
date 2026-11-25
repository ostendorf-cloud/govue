// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package optin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// MinSecretLen is the shortest Config.Secret.
const MinSecretLen = 32

// The domain separators. A version change in one of them is a new format.
const (
	labelAddressKey     = "govue/optin\x00v1\x00address-key"
	labelUnsubscribeKey = "govue/optin\x00v1\x00unsubscribe-key"
	labelConfirm        = "govue/optin\x00v1\x00confirm\x00"
)

// confirmTokenBytes is the size of a confirmation token, 256 bits.
const confirmTokenBytes = 32

// confirmTokenLen is the length of a confirmation token in base64url.
var confirmTokenLen = base64.RawURLEncoding.EncodedLen(confirmTokenBytes)

// unsubscribeTokenLen is the length of an unsubscribe token in base64url:
// the 32 byte key and the 32 byte MAC.
var unsubscribeTokenLen = base64.RawURLEncoding.EncodedLen(2 * sha256.Size)

// keys holds the two keys derived from Config.Secret.
type keys struct {
	address     []byte
	unsubscribe []byte
}

func mac(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

func deriveKeys(secret []byte) keys {
	return keys{
		address:     mac(secret, []byte(labelAddressKey)),
		unsubscribe: mac(secret, []byte(labelUnsubscribeKey)),
	}
}

// addressKey is the entry key of an address. The address is ASCII by
// validation, so lower casing it is byte wise and stable.
func (k keys) addressKey(address string) Key {
	var out Key
	copy(out[:], mac(k.address, []byte(strings.ToLower(address))))
	return out
}

// unsubscribeToken is the key followed by its MAC, in base64url. Nothing of
// it is stored: the MAC proves the package wrote it.
func (k keys) unsubscribeToken(key Key) string {
	raw := make([]byte, 0, 2*sha256.Size)
	raw = append(raw, key[:]...)
	raw = append(raw, mac(k.unsubscribe, key[:])...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// parseUnsubscribeToken returns the key of a token whose MAC is valid. The
// MAC is compared in constant time.
func (k keys) parseUnsubscribeToken(s string) (Key, bool) {
	if len(s) != unsubscribeTokenLen {
		return Key{}, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(raw) != 2*sha256.Size {
		return Key{}, false
	}
	var key Key
	copy(key[:], raw[:sha256.Size])
	if !hmac.Equal(raw[sha256.Size:], mac(k.unsubscribe, key[:])) {
		return Key{}, false
	}
	return key, true
}

// newConfirmToken returns a fresh token for the link and the hash to store.
func newConfirmToken(random io.Reader) (string, TokenHash, error) {
	raw := make([]byte, confirmTokenBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", TokenHash{}, fmt.Errorf("optin: confirmation token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), hashConfirm(raw), nil
}

func hashConfirm(raw []byte) TokenHash {
	h := sha256.New()
	h.Write([]byte(labelConfirm))
	h.Write(raw)
	var out TokenHash
	copy(out[:], h.Sum(nil))
	return out
}

// parseConfirmToken returns the hash of a well formed token.
func parseConfirmToken(s string) (TokenHash, bool) {
	if len(s) != confirmTokenLen {
		return TokenHash{}, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(raw) != confirmTokenBytes {
		return TokenHash{}, false
	}
	return hashConfirm(raw), true
}
