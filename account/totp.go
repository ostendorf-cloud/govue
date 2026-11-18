// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238 and RFC 4226 specify HMAC-SHA-1. As a MAC, SHA-1's collisions do not matter.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// TOTP follows RFC 6238 on top of the HOTP of RFC 4226, on the standard
// library. The parameters are what every common authenticator app assumes:
// HMAC-SHA-1, six digits, a 30 second step. One step either side of now is
// accepted as well, so a code typed late or a clock a little off still works.
// Nothing here widens that window.
const (
	totpDigits    = 6
	totpPeriod    = 30 // seconds
	totpSkewSteps = 1

	// totpSecretBytes is 160 bits, RFC 4226's recommended secret length,
	// and 32 base32 characters without padding. Both existing
	// implementations use it.
	totpSecretBytes = 20

	// minTOTPSecretBytes is RFC 4226's floor of 128 bits. A shorter secret
	// is refused, never padded.
	minTOTPSecretBytes = 16

	// maxTOTPSecretBytes bounds what one verification hashes. 512 bits is
	// the HMAC-SHA-1 block size, so a longer key would be hashed down first
	// anyway.
	maxTOTPSecretBytes = 64

	// maxTOTPIssuer and maxTOTPAccountName bound the label. 254 bytes is the
	// longest address RFC 5321 allows, and a QR code of the URI stays a
	// size a phone camera reads.
	maxTOTPIssuer      = 64
	maxTOTPAccountName = 254
)

var (
	// ErrTOTPSecretInvalid is what TOTPCode and TOTPProvisioningURI answer
	// for a secret that is not upper or lower case base32 without padding,
	// or whose length is outside 128 to 512 bits.
	ErrTOTPSecretInvalid = errors.New("account: totp secret is not valid base32 of 128 to 512 bits")

	// ErrTOTPLabelInvalid is what TOTPProvisioningURI answers for an issuer
	// or an account name that is empty, too long, not valid UTF-8, or holds
	// a colon or a character that is not printable.
	ErrTOTPLabelInvalid = errors.New("account: totp issuer or account name is not a valid label")

	// ErrTOTPCounterInvalid is what TOTPCode answers for a time whose step
	// counter is below one. No code of such a step is ever accepted.
	ErrTOTPCounterInvalid = errors.New("account: totp counter is below one")
)

// totpEncoding is RFC 4648 base32 without padding, the form an authenticator
// app shows and a store holds.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// dummyTOTPKey stands in for a secret that does not decode, so such a call
// still computes the same HMACs as one with a valid secret. It never yields
// a match, because the result is refused regardless.
var dummyTOTPKey = [totpSecretBytes]byte{}

// GenerateTOTPSecret returns a fresh random 160 bit secret as 32 upper case
// base32 characters without padding.
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("account: read totp secret: %w", err)
	}

	return totpEncoding.EncodeToString(raw), nil
}

// TOTPProvisioningURI returns the otpauth URI an authenticator app reads, in
// the Key URI Format most apps follow:
//
//	otpauth://totp/<issuer>:<accountName>?secret=<secret>&issuer=<issuer>&algorithm=SHA1&digits=6&period=30
//
// issuer is the application's name, the group the app shows the entry under.
// accountName is usually the login. Both are percent encoded: every byte
// outside the RFC 3986 unreserved set is escaped, and a space becomes %20,
// never +. The label keeps @ as it is, which a path allows. Neither may hold
// a colon, which the format reserves as the separator, nor a character that
// is not printable, which an app would show as something else. secret is
// validated and written in upper case.
func TOTPProvisioningURI(issuer, accountName, secret string) (string, error) {
	if !validLabel(issuer, maxTOTPIssuer) || !validLabel(accountName, maxTOTPAccountName) {
		return "", ErrTOTPLabelInvalid
	}

	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}

	return "otpauth://totp/" + escapeComponent(issuer, true) + ":" + escapeComponent(accountName, true) +
		"?secret=" + totpEncoding.EncodeToString(key) +
		"&issuer=" + escapeComponent(issuer, false) +
		"&algorithm=SHA1&digits=6&period=30", nil
}

// TOTPCode returns the six digit code of secret for the step that holds at.
// It is for tests and for tooling that has the secret anyway, never for
// handing a code to a client.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}

	counter := counterAt(at)
	if counter < 1 {
		return "", ErrTOTPCounterInvalid
	}

	return hotp(key, uint64(counter), totpDigits), nil
}

// VerifyTOTPCode reports whether code is valid for secret at time at, within
// one step either side of the step that holds at, and returns the counter of
// the step that matched. If more than one step matches, the highest counter
// is returned.
//
// The check is not complete without the counter. A code stays valid for its
// whole window, so the caller has to consume it with
// Store.AdvanceTOTPCounter(ctx, id, counter), or Store.ConfirmTOTP for a
// secret being confirmed, and refuse the login when that answers
// ErrNotFound. That conditional write is the replay protection. Comparing
// the counter with a value read earlier is not, because two racing logins
// would both pass.
//
// A step whose counter is below one never matches. The counter is 0 exactly
// when ok is false. code must be exactly six ASCII digits. A secret that is
// not valid answers false, after the same work as a valid one, so an unknown
// account with no secret costs what a known one does. The comparison is
// constant time, and every step of the window is computed whatever matches.
func VerifyTOTPCode(secret, code string, at time.Time) (counter int64, ok bool) {
	key, err := decodeTOTPSecret(secret)
	secretOK := err == nil
	if !secretOK {
		key = dummyTOTPKey[:]
	}

	now := counterAt(at)
	matched := int64(0)

	for offset := -totpSkewSteps; offset <= totpSkewSteps; offset++ {
		candidate := now + int64(offset)

		// A candidate below one is computed like any other, for the same
		// work, and then never accepted. The conversion of a negative
		// candidate wraps, which is harmless for a value thrown away.
		want := hotp(key, uint64(candidate), totpDigits)
		equal := subtle.ConstantTimeCompare([]byte(want), []byte(code))

		if equal == 1 && candidate >= 1 {
			matched = candidate
		}
	}

	if !secretOK || !wellFormedCode(code) || matched < 1 {
		return 0, false
	}

	return matched, true
}

// counterAt is the RFC 6238 step counter of at, floor((unix time) / 30),
// rounded down for a time before 1970 as well.
func counterAt(at time.Time) int64 {
	unix := at.Unix()

	counter := unix / totpPeriod
	if unix%totpPeriod < 0 {
		counter--
	}

	return counter
}

// hotp is RFC 4226's HOTP: an HMAC-SHA-1 of the eight byte big endian
// counter, dynamically truncated and reduced to digits decimal digits.
// digits is at most nine, so 10^digits fits a uint32.
func hotp(key []byte, counter uint64, digits int) string {
	mod := uint32(1)
	for range digits {
		mod *= 10
	}

	return fmt.Sprintf("%0*d", digits, truncate(hmacSHA1(key, counter))%mod)
}

// hmacSHA1 is the HMAC-SHA-1 of the eight byte big endian counter.
func hmacSHA1(key []byte, counter uint64) []byte {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg[:]) // hash.Hash.Write never returns an error.

	return mac.Sum(nil)
}

// truncate is RFC 4226 section 5.3's dynamic truncation: the low four bits of
// the last byte pick an offset, and the 31 bits read there, with the top bit
// masked off, are the result.
func truncate(sum []byte) uint32 {
	offset := sum[len(sum)-1] & 0x0f

	return binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
}

// decodeTOTPSecret decodes a base32 secret strictly: letters of either case
// and the digits 2 to 7 only, no padding, no whitespace, no non zero
// trailing bits, and 128 to 512 bits long.
func decodeTOTPSecret(secret string) ([]byte, error) {
	upper := make([]byte, len(secret))
	for i := range len(secret) {
		c := secret[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= '2' && c <= '7':
			upper[i] = c
		case c >= 'a' && c <= 'z':
			upper[i] = c - 'a' + 'A'
		default:
			return nil, ErrTOTPSecretInvalid
		}
	}

	key, err := totpEncoding.DecodeString(string(upper))
	if err != nil || len(key) < minTOTPSecretBytes || len(key) > maxTOTPSecretBytes {
		return nil, ErrTOTPSecretInvalid
	}

	// One spelling per secret: trailing bits that decoding drops are refused.
	if totpEncoding.EncodeToString(key) != string(upper) {
		return nil, ErrTOTPSecretInvalid
	}

	return key, nil
}

// wellFormedCode reports whether code is exactly totpDigits ASCII digits.
func wellFormedCode(code string) bool {
	if len(code) != totpDigits {
		return false
	}
	for i := range len(code) {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}

	return true
}

// validLabel reports whether s can be an issuer or an account name: not
// empty, at most limit bytes, valid UTF-8, printable, and free of the colon
// the label format uses as its separator.
func validLabel(s string, limit int) bool {
	if s == "" || len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == ':' || !unicode.IsPrint(r) {
			return false
		}
	}

	return true
}

// escapeComponent percent encodes every byte outside the RFC 3986 unreserved
// set, with upper case hex. keepAt leaves @ as it is, which a path segment
// allows and a query value here does not need.
func escapeComponent(s string, keepAt bool) string {
	const hex = "0123456789ABCDEF"

	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~', keepAt && c == '@':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}

	return b.String()
}
