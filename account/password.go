// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// The Argon2id parameters every new hash is written with. They follow the
// second recommendation in RFC 9106 section 4, as cited by
// golang.org/x/crypto/argon2: three passes over 64 MiB with four lanes, for a
// host that cannot spare 2 GiB per login. The first option would turn every
// concurrent login into a 2 GiB allocation.
//
// They are fixed on purpose. A deployment does not tune its own password
// hashing cost by accident, and every hash records its own parameters, so a
// later change here keeps every older hash verifiable and asks for a rehash
// through [PasswordCheck.NeedsRehash].
const (
	argon2Time      = 3
	argon2MemoryKiB = 64 * 1024
	argon2Threads   = 4

	// argon2KeyLen is 256 bits of output, the common default and above the
	// 128 bits RFC 9106 calls sufficient.
	argon2KeyLen = 32

	// argon2SaltLen is 128 bits of fresh random salt, RFC 9106's own
	// recommendation.
	argon2SaltLen = 16
)

// The bounds a stored hash must keep before its parameters are trusted. A
// hash is read from the consumer's database, and its parameters decide how
// much memory and time one verification costs. A corrupted or planted value
// must not turn a login into a gigabyte allocation or a minute of work.
//
// maxHashMemoryKiB is today's memory cost. The concurrency cap budgets memory
// per derivation from it, so a hash asking for more is refused rather than
// let past that budget. If argon2MemoryKiB is lowered one day, this bound
// stays where it is, so the older and larger hashes still verify.
const (
	maxHashMemoryKiB = 64 * 1024
	maxHashTime      = 16
	maxHashThreads   = 16
	minHashSaltLen   = 8
	maxHashSaltLen   = 64
	minHashKeyLen    = 16
	maxHashKeyLen    = 64

	// maxEncodedHashLen bounds the string before it is split and decoded.
	// The longest hash the bounds above allow is well below it.
	maxEncodedHashLen = 256
)

// MinPasswordLength is the fewest characters a new password may have,
// counted as Unicode code points. NIST SP 800-63B asks for at least eight
// and no composition rules. Twelve is the longer minimum both existing
// implementations already use.
const MinPasswordLength = 12

// MaxPasswordLength is the most bytes a password may have. Argon2id reads the
// whole password once, so its cost barely grows with the length, but an
// unbounded value on an unauthenticated route is not worth accepting. No
// passphrase a person types needs more room. It is counted in bytes, not
// characters, because it bounds work, not what a person sees.
const MaxPasswordLength = 1024

// DefaultMaxConcurrentHashes is how many Argon2id derivations run at once
// when [HasherConfig.MaxConcurrentHashes] is zero.
//
// One derivation holds 64 MiB for its whole run, and a finished block stays
// resident until the next garbage collection. Measured in the application
// this code comes from, the heap peaks at about twice the cap times 64 MiB:
// 129 MiB at a cap of one, 257 MiB at two. One therefore fits a 256 MiB
// container with room for everything else. Logins then serialise at a few per
// second, which a small deployment does not notice. Raise the cap together
// with the container's memory limit, never alone.
const DefaultMaxConcurrentHashes = 1

// DefaultHashWait is how long a caller waits for a free derivation slot
// before it gets [ErrHashingBusy]. A rate limit burst of ten clears in about
// three seconds at a cap of one, and a waiting caller holds no memory.
const DefaultHashWait = 5 * time.Second

var (
	// ErrPasswordTooShort is what [CheckPassword] and [Hasher.HashPassword]
	// answer for a password below [MinPasswordLength] characters.
	ErrPasswordTooShort = fmt.Errorf("account: password is shorter than %d characters", MinPasswordLength)

	// ErrPasswordTooLong is what [CheckPassword], [Hasher.HashPassword] and
	// [Hasher.VerifyPassword] answer for a password above
	// [MaxPasswordLength] bytes. The login logic answers it like a wrong
	// password, with [ErrInvalidCredentials].
	ErrPasswordTooLong = fmt.Errorf("account: password is longer than %d bytes", MaxPasswordLength)

	// ErrHashingBusy is what a call answers when no derivation slot came free
	// within [DefaultHashWait], or when ctx ended first. It is not a login
	// outcome. A handler answers it with 503, never with a failed login,
	// which would tell a person with the right password that it is wrong.
	ErrHashingBusy = errors.New("account: too many password hashes are running")

	// ErrPasswordHashInvalid is what [Hasher.VerifyPassword] answers when the
	// stored value is not an Argon2id hash within the bounds this package
	// accepts. It is an operational signal for the log, a corrupted column or
	// a hash from another scheme. The login logic answers it like a wrong
	// password, with [ErrInvalidCredentials].
	ErrPasswordHashInvalid = errors.New("account: password hash is not a recognised argon2id encoding")
)

// dummyPassword is what the dummy derivation hashes when the password itself
// must not be hashed, because it is above [MaxPasswordLength]. It is no
// account's password, and the dummy derivation never matches anyway.
const dummyPassword = "not-an-account-timing-equaliser-only"

// dummySalt is the salt of the dummy derivation. It needs no randomness: the
// result is compared and thrown away, and only its cost matters.
var dummySalt = [argon2SaltLen]byte{}

// dummyKey is what the dummy derivation is compared against. All zero bytes,
// which no Argon2id output is expected to be. The result of that comparison
// is discarded anyway.
var dummyKey = [argon2KeyLen]byte{}

// deriveFunc is the signature of argon2.IDKey.
type deriveFunc func(password, salt []byte, iterations, memoryKiB uint32, threads uint8, keyLen uint32) []byte

// HasherConfig configures a [Hasher]. The zero value is the secure default.
type HasherConfig struct {
	// MaxConcurrentHashes caps how many Argon2id derivations run at once in
	// this process, each holding 64 MiB. Zero is
	// [DefaultMaxConcurrentHashes]. Negative is refused. See
	// DefaultMaxConcurrentHashes before raising it.
	MaxConcurrentHashes int
}

// Hasher hashes and verifies passwords with Argon2id, at most
// HasherConfig.MaxConcurrentHashes at a time. Create one per process and
// share it: the cap holds per Hasher. It is safe for concurrent use.
type Hasher struct {
	slots   chan struct{}
	maxWait time.Duration

	// derive is argon2.IDKey. Tests replace it to count and inspect the
	// work. Nothing outside a test assigns to it.
	derive deriveFunc
}

// NewHasher validates cfg and returns a Hasher.
func NewHasher(cfg HasherConfig) (*Hasher, error) {
	if cfg.MaxConcurrentHashes < 0 {
		return nil, fmt.Errorf("account: max concurrent hashes %d is negative", cfg.MaxConcurrentHashes)
	}
	if cfg.MaxConcurrentHashes == 0 {
		cfg.MaxConcurrentHashes = DefaultMaxConcurrentHashes
	}

	return &Hasher{
		slots:   make(chan struct{}, cfg.MaxConcurrentHashes),
		maxWait: DefaultHashWait,
		derive:  argon2.IDKey,
	}, nil
}

// CheckPassword applies the policy for a new password: at least
// [MinPasswordLength] characters and at most [MaxPasswordLength] bytes. There
// are no composition rules. [Hasher.HashPassword] applies it too, so a
// caller may use it alone to answer a form before any hashing.
//
// It does not normalise Unicode. A password is hashed as the bytes given, the
// same as the existing implementations, so their hashes keep verifying.
func CheckPassword(password string) error {
	if len(password) > MaxPasswordLength {
		return ErrPasswordTooLong
	}
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}

	return nil
}

// PasswordCheck is the outcome of [Hasher.VerifyPassword].
type PasswordCheck struct {
	// Match reports that the password is the one the hash was made from.
	Match bool

	// NeedsRehash reports that the hash matched but was written with other
	// parameters, salt length or key length than a new hash would be. The
	// caller then hashes the password again and stores it with
	// Store.SetPasswordHash. It is never true without Match, so a failed
	// login can never trigger a write.
	NeedsRehash bool
}

// HashPassword checks password against [CheckPassword] and returns its
// Argon2id hash with a fresh random salt, in the PHC string format both
// existing implementations write:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
//
// The salt and hash are unpadded standard base64. ctx bounds only the wait
// for a derivation slot, never the derivation, and a wait that ends answers
// [ErrHashingBusy].
func (h *Hasher) HashPassword(ctx context.Context, password string) (string, error) {
	if err := CheckPassword(password); err != nil {
		return "", err
	}

	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("account: read password salt: %w", err)
	}

	params := currentParams()

	sum, err := h.run(ctx, []byte(password), salt, params, argon2KeyLen)
	if err != nil {
		return "", err
	}

	return encodeHash(params, salt, sum), nil
}

// VerifyPassword reports whether password matches encoded, a hash from
// [Hasher.HashPassword] or any Argon2id PHC string within this package's
// bounds. The parameters, the salt and the key length are read from encoded,
// not from today's constants, so a hash written under older parameters still
// verifies and reports [PasswordCheck.NeedsRehash].
//
// Pass the empty string as encoded for a login that found no account, or an
// account without a password. Every call runs exactly one derivation at
// today's cost whatever the outcome: a match, a mismatch, no hash, an invalid
// hash or a password above [MaxPasswordLength]. A login for an unknown
// account thus costs the same work as a wrong password. The one exception is
// a stored hash with weaker parameters, which costs what its parameters say.
//
// The comparison is constant time. An empty encoded answers no match and a
// nil error. An invalid hash answers [ErrPasswordHashInvalid], a password
// above the limit [ErrPasswordTooLong], both with no match. The login logic
// collapses all of these into [ErrInvalidCredentials]. [ErrHashingBusy] is
// different: it means nothing was decided.
func (h *Hasher) VerifyPassword(ctx context.Context, encoded, password string) (PasswordCheck, error) {
	if len(password) > MaxPasswordLength {
		return PasswordCheck{}, h.dummy(ctx, ErrPasswordTooLong)
	}
	if encoded == "" {
		return PasswordCheck{}, h.dummy(ctx, nil)
	}

	stored, err := decodeHash(encoded)
	if err != nil {
		return PasswordCheck{}, h.dummy(ctx, err)
	}

	// decodeHash bounds the key length to maxHashKeyLen, so it fits.
	sum, err := h.run(ctx, []byte(password), stored.salt, stored.params, uint32(len(stored.key)))
	if err != nil {
		return PasswordCheck{}, err
	}

	if subtle.ConstantTimeCompare(sum, stored.key) == 1 {
		return PasswordCheck{Match: true, NeedsRehash: stored.outdated()}, nil
	}

	return PasswordCheck{}, nil
}

// dummy runs one derivation at today's cost that can never match, then
// answers outcome. A busy hasher answers ErrHashingBusy instead, exactly
// as a real verification would.
func (h *Hasher) dummy(ctx context.Context, outcome error) error {
	sum, err := h.run(ctx, []byte(dummyPassword), dummySalt[:], currentParams(), argon2KeyLen)
	if err != nil {
		return err
	}

	// The result is discarded. The comparison only keeps the work equal to
	// a real verification.
	_ = subtle.ConstantTimeCompare(sum, dummyKey[:])

	return outcome
}

// run takes a derivation slot, waits at most h.maxWait for one, and runs one
// derivation under it.
func (h *Hasher) run(ctx context.Context, password, salt []byte, params argon2idParams, keyLen uint32) ([]byte, error) {
	if err := h.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-h.slots }()

	return h.derive(password, salt, params.time, params.memoryKiB, params.threads, keyLen), nil
}

// acquire takes a slot. A context that has already ended sheds at once, even
// with a slot free: the caller has gone, and a slot spent on it is one kept
// from somebody still there.
func (h *Hasher) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrHashingBusy, err)
	}

	timer := time.NewTimer(h.maxWait)
	defer timer.Stop()

	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrHashingBusy, ctx.Err())
	case <-timer.C:
		return ErrHashingBusy
	}
}

// argon2idParams are the cost parameters a hash records.
type argon2idParams struct {
	memoryKiB uint32
	time      uint32
	threads   uint8
}

// currentParams are the parameters a new hash is written with.
func currentParams() argon2idParams {
	return argon2idParams{memoryKiB: argon2MemoryKiB, time: argon2Time, threads: argon2Threads}
}

// storedHash is a decoded PHC string.
type storedHash struct {
	params argon2idParams
	salt   []byte
	key    []byte
}

// outdated reports whether a new hash would be written differently.
func (s storedHash) outdated() bool {
	return s.params != currentParams() || len(s.salt) != argon2SaltLen || len(s.key) != argon2KeyLen
}

// phcBase64 is unpadded standard base64 that refuses non zero trailing bits,
// so every hash has exactly one spelling.
var phcBase64 = base64.RawStdEncoding.Strict()

// encodeHash writes the PHC string.
func encodeHash(params argon2idParams, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.memoryKiB, params.time, params.threads,
		phcBase64.EncodeToString(salt), phcBase64.EncodeToString(key))
}

// decodeHash parses a PHC string strictly: the argon2id variant, version 19,
// the parameters in the order m, t, p as canonical decimals within the
// bounds, and salt and key in canonical unpadded base64 within their bounds.
// Every failure is ErrPasswordHashInvalid, whatever the reason.
func decodeHash(encoded string) (storedHash, error) {
	if len(encoded) > maxEncodedHashLen {
		return storedHash{}, ErrPasswordHashInvalid
	}

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return storedHash{}, ErrPasswordHashInvalid
	}

	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return storedHash{}, ErrPasswordHashInvalid
	}

	memoryKiB, okM := decimalField(fields[0], "m=", maxHashMemoryKiB)
	iterations, okT := decimalField(fields[1], "t=", maxHashTime)
	threads, okP := decimalField(fields[2], "p=", maxHashThreads)
	if !okM || !okT || !okP || iterations < 1 || threads < 1 || memoryKiB < 8*threads {
		return storedHash{}, ErrPasswordHashInvalid
	}

	salt, err := decodePHCBase64(parts[4])
	if err != nil || len(salt) < minHashSaltLen || len(salt) > maxHashSaltLen {
		return storedHash{}, ErrPasswordHashInvalid
	}

	key, err := decodePHCBase64(parts[5])
	if err != nil || len(key) < minHashKeyLen || len(key) > maxHashKeyLen {
		return storedHash{}, ErrPasswordHashInvalid
	}

	return storedHash{
		params: argon2idParams{
			memoryKiB: memoryKiB,
			time:      iterations,
			threads:   uint8(threads), // Bounded by maxHashThreads above.
		},
		salt: salt,
		key:  key,
	}, nil
}

// decodePHCBase64 decodes s with phcBase64 after checking that it holds only
// the base64 alphabet. The decoder itself skips carriage returns and line
// feeds, which a stored hash must not carry.
func decodePHCBase64(s string) ([]byte, error) {
	for i := range len(s) {
		c := s[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '+' && c != '/' {
			return nil, ErrPasswordHashInvalid
		}
	}

	return phcBase64.DecodeString(s)
}

// decimalField parses "<prefix><digits>" as a canonical decimal no larger
// than limit: ASCII digits only, no sign, no leading zero.
func decimalField(field, prefix string, limit uint32) (uint32, bool) {
	digits, ok := strings.CutPrefix(field, prefix)
	if !ok || digits == "" || len(digits) > 10 || (len(digits) > 1 && digits[0] == '0') {
		return 0, false
	}
	for _, c := range []byte(digits) {
		if c < '0' || c > '9' {
			return 0, false
		}
	}

	value, err := strconv.ParseUint(digits, 10, 32)
	if err != nil || value > uint64(limit) {
		return 0, false
	}

	return uint32(value), true
}
