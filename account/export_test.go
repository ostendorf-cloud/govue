// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

// The hooks below let the external tests of Service count the work of a
// login, as the internal tests of Hasher and VerifyTOTPCode do. They exist in
// test builds only.

// DeriveFunc is the signature of argon2.IDKey.
type DeriveFunc = deriveFunc

// SetDerive replaces Argon2id in h with derive.
func SetDerive(h *Hasher, derive DeriveFunc) { h.derive = derive }

// FakeDerive is a cheap deterministic stand in for Argon2id.
var FakeDerive DeriveFunc = fakeDerive

// MACFunc is the signature of the TOTP HMAC.
type MACFunc = macFunc

// SetMAC replaces the TOTP HMAC of s with mac.
func SetMAC(s *Service, mac MACFunc) { s.mac = mac }

// HMACSHA1 is the TOTP HMAC.
var HMACSHA1 MACFunc = hmacSHA1

// FakeHashOf encodes the FakeDerive hash of password under the given
// parameters with a 16 byte zero salt and a 32 byte key.
func FakeHashOf(password string, memoryKiB, iterations uint32, threads uint8) string {
	salt := make([]byte, argon2SaltLen)
	sum := fakeDerive([]byte(password), salt, iterations, memoryKiB, threads, argon2KeyLen)

	return encodeHash(argon2idParams{memoryKiB: memoryKiB, time: iterations, threads: threads}, salt, sum)
}
