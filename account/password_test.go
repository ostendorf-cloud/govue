// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Every password and every hash below is fake. The vectors were produced for
// these tests from a made up password and a fixed salt.
const (
	// vectorPassword is the made up password behind both vectors.
	vectorPassword = "fake-password-for-a-test-vector"

	// vectorHash is vectorPassword under today's parameters with the salt
	// 0x00..0x0f. It was produced twice, independently: by calbridge's
	// encoding over golang.org/x/crypto/argon2 and by Node's OpenSSL backed
	// crypto.argon2Sync. Both gave this string.
	vectorHash = "$argon2id$v=19$m=65536,t=3,p=4$AAECAwQFBgcICQoLDA0ODw$T5149XoF4JBfOU6jXZ4MmJZqHHvyv3c4gRDlmcrIXDU"

	// weakVectorHash is the same password and salt under m=19456,t=2,p=1,
	// weaker than today's parameters, from the same calbridge encoding.
	weakVectorHash = "$argon2id$v=19$m=19456,t=2,p=1$AAECAwQFBgcICQoLDA0ODw$fKhTnqAT5intQ6fPdpWChzWaBv8HrehCj9nqj02PHuk"

	// fakePassword is a password that passes the policy.
	fakePassword = "fake-password-not-a-secret"
)

// derivation is one recorded call of the derive function.
type derivation struct {
	password  string
	salt      []byte
	time      uint32
	memoryKiB uint32
	threads   uint8
	keyLen    uint32
}

// recorder replaces Argon2id with a cheap deterministic function and records
// every call, so a test asserts the work done instead of the time taken.
type recorder struct {
	mu    sync.Mutex
	calls []derivation
}

func (r *recorder) derive(password, salt []byte, iterations, memoryKiB uint32, threads uint8, keyLen uint32) []byte {
	r.mu.Lock()
	r.calls = append(r.calls, derivation{
		password:  string(password),
		salt:      append([]byte(nil), salt...),
		time:      iterations,
		memoryKiB: memoryKiB,
		threads:   threads,
		keyLen:    keyLen,
	})
	r.mu.Unlock()

	return fakeDerive(password, salt, iterations, memoryKiB, threads, keyLen)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.calls)
}

func (r *recorder) last(t *testing.T) derivation {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.calls) == 0 {
		t.Fatal("no derivation ran")
	}

	return r.calls[len(r.calls)-1]
}

// fakeDerive is a stand in for Argon2id that still depends on every input,
// so a parameter read wrongly from a hash changes the result.
func fakeDerive(password, salt []byte, iterations, memoryKiB uint32, threads uint8, keyLen uint32) []byte {
	h := sha256.New()
	_, _ = h.Write(password) // hash.Hash.Write never returns an error.
	_, _ = h.Write(salt)
	var params [13]byte
	binary.BigEndian.PutUint32(params[0:], iterations)
	binary.BigEndian.PutUint32(params[4:], memoryKiB)
	params[8] = threads
	binary.BigEndian.PutUint32(params[9:], keyLen)
	_, _ = h.Write(params[:])

	out := make([]byte, 0, keyLen)
	block := h.Sum(nil)
	for uint32(len(out)) < keyLen {
		out = append(out, block...)
		next := sha256.Sum256(block)
		block = next[:]
	}

	return out[:keyLen]
}

// newTestHasher builds a Hasher with a cap of one and the recorder in place
// of Argon2id.
func newTestHasher(t *testing.T) (*Hasher, *recorder) {
	t.Helper()

	h, err := NewHasher(HasherConfig{MaxConcurrentHashes: 1})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}

	rec := &recorder{}
	h.derive = rec.derive

	return h, rec
}

// fakeHash encodes a hash the fake derivation matches for fakePassword, under
// the given parameters, salt and key length.
func fakeHash(memoryKiB, iterations uint32, threads uint8, salt []byte, keyLen uint32) string {
	sum := fakeDerive([]byte(fakePassword), salt, iterations, memoryKiB, threads, keyLen)

	return encodeHash(argon2idParams{memoryKiB: memoryKiB, time: iterations, threads: threads}, salt, sum)
}

func TestNewHasher(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   int
		want    int
		wantErr bool
	}{
		{name: "zero is the default", input: 0, want: DefaultMaxConcurrentHashes},
		{name: "one", input: 1, want: 1},
		{name: "four", input: 4, want: 4},
		{name: "negative", input: -1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := NewHasher(HasherConfig{MaxConcurrentHashes: tt.input})
			if tt.wantErr {
				if err == nil {
					t.Fatal("NewHasher accepted a negative cap")
				}

				return
			}
			if err != nil {
				t.Fatalf("NewHasher: %v", err)
			}
			if got := cap(h.slots); got != tt.want {
				t.Fatalf("cap = %d, want %d", got, tt.want)
			}
			if h.maxWait != DefaultHashWait {
				t.Fatalf("maxWait = %s, want %s", h.maxWait, DefaultHashWait)
			}
		})
	}

	if DefaultMaxConcurrentHashes != 1 {
		t.Fatalf("DefaultMaxConcurrentHashes = %d, want 1", DefaultMaxConcurrentHashes)
	}
	if DefaultHashWait != 5*time.Second {
		t.Fatalf("DefaultHashWait = %s, want 5s", DefaultHashWait)
	}
}

func TestCheckPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "empty", input: "", wantErr: ErrPasswordTooShort},
		{name: "eleven characters", input: strings.Repeat("x", 11), wantErr: ErrPasswordTooShort},
		{name: "twelve characters", input: strings.Repeat("x", 12)},
		{name: "eleven multibyte characters in 22 bytes", input: strings.Repeat("ä", 11), wantErr: ErrPasswordTooShort},
		{name: "twelve multibyte characters", input: strings.Repeat("ä", 12)},
		{name: "twelve NUL bytes count as twelve", input: strings.Repeat("\x00", 12)},
		{name: "twelve invalid UTF-8 bytes count as twelve", input: strings.Repeat("\xff", 12)},
		{name: "decomposed characters are not normalised", input: strings.Repeat("ä", 6)},
		{name: "maximum length in bytes", input: strings.Repeat("x", MaxPasswordLength)},
		{name: "one byte over the maximum", input: strings.Repeat("x", MaxPasswordLength+1), wantErr: ErrPasswordTooLong},
		{name: "multibyte over the maximum in bytes", input: strings.Repeat("ä", MaxPasswordLength/2+1), wantErr: ErrPasswordTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := CheckPassword(tt.input)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("CheckPassword = %v, want %v", err, tt.wantErr)
			}
		})
	}

	if MinPasswordLength != 12 || MaxPasswordLength != 1024 {
		t.Fatalf("limits = %d..%d, want 12..1024", MinPasswordLength, MaxPasswordLength)
	}
}

var phcPattern = regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=4\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)

// TestHasher_HashPassword_realArgon2 runs the real derivation: the format, a
// fresh salt per hash and the round trip.
func TestHasher_HashPassword_realArgon2(t *testing.T) {
	t.Parallel()

	h, err := NewHasher(HasherConfig{MaxConcurrentHashes: 2})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}

	first, err := h.HashPassword(t.Context(), fakePassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := h.HashPassword(t.Context(), fakePassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	for _, encoded := range []string{first, second} {
		if !phcPattern.MatchString(encoded) {
			t.Fatalf("hash %q is not the expected PHC shape", encoded)
		}
	}
	if first == second {
		t.Fatal("two hashes of one password are equal, the salt is not fresh")
	}
	if strings.Split(first, "$")[4] == strings.Split(second, "$")[4] {
		t.Fatal("two hashes share a salt")
	}

	got, err := h.VerifyPassword(t.Context(), first, fakePassword)
	if err != nil || !got.Match || got.NeedsRehash {
		t.Fatalf("VerifyPassword(right) = %+v, %v, want match without rehash", got, err)
	}

	got, err = h.VerifyPassword(t.Context(), first, fakePassword+"x")
	if err != nil || got.Match || got.NeedsRehash {
		t.Fatalf("VerifyPassword(wrong) = %+v, %v, want no match", got, err)
	}
}

// TestHasher_VerifyPassword_vectors checks hashes produced outside this
// package, through the real derivation.
func TestHasher_VerifyPassword_vectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		encoded  string
		password string
		want     PasswordCheck
	}{
		{name: "today's parameters", encoded: vectorHash, password: vectorPassword, want: PasswordCheck{Match: true}},
		{name: "today's parameters, wrong password", encoded: vectorHash, password: vectorPassword + "!"},
		{name: "weaker parameters", encoded: weakVectorHash, password: vectorPassword, want: PasswordCheck{Match: true, NeedsRehash: true}},
		{name: "weaker parameters, wrong password", encoded: weakVectorHash, password: "fake-wrong-password"},
	}

	h, err := NewHasher(HasherConfig{MaxConcurrentHashes: 4})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := h.VerifyPassword(t.Context(), tt.encoded, tt.password)
			if err != nil {
				t.Fatalf("VerifyPassword: %v", err)
			}
			if got != tt.want {
				t.Fatalf("VerifyPassword = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestHasher_HashPassword_parameters pins today's parameters and the salt
// and key lengths the hash is written with.
func TestHasher_HashPassword_parameters(t *testing.T) {
	t.Parallel()

	h, rec := newTestHasher(t)

	encoded, err := h.HashPassword(t.Context(), fakePassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	got := rec.last(t)
	if got.time != 3 || got.memoryKiB != 64*1024 || got.threads != 4 || got.keyLen != 32 || len(got.salt) != 16 {
		t.Fatalf("derivation = t=%d m=%d p=%d key=%d salt=%d, want t=3 m=65536 p=4 key=32 salt=16",
			got.time, got.memoryKiB, got.threads, got.keyLen, len(got.salt))
	}
	if got.password != fakePassword {
		t.Fatal("the derivation did not get the password")
	}
	if !phcPattern.MatchString(encoded) {
		t.Fatalf("hash %q is not the expected PHC shape", encoded)
	}

	check, err := h.VerifyPassword(t.Context(), encoded, fakePassword)
	if err != nil || !check.Match || check.NeedsRehash {
		t.Fatalf("VerifyPassword = %+v, %v, want match without rehash", check, err)
	}
}

func TestHasher_HashPassword_policy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "too short", input: "fake-short", wantErr: ErrPasswordTooShort},
		{name: "too long", input: strings.Repeat("f", MaxPasswordLength+1), wantErr: ErrPasswordTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, rec := newTestHasher(t)

			encoded, err := h.HashPassword(t.Context(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("HashPassword = %v, want %v", err, tt.wantErr)
			}
			if encoded != "" {
				t.Fatalf("HashPassword returned %q with an error", encoded)
			}
			if n := rec.count(); n != 0 {
				t.Fatalf("%d derivations ran for a refused password, want 0", n)
			}
		})
	}
}

// TestHasher_VerifyPassword_readsParameters proves the parameters, the salt
// and the key length come from the stored hash, not from today's constants.
func TestHasher_VerifyPassword_readsParameters(t *testing.T) {
	t.Parallel()

	salt := []byte("fake-salt-0123456789")
	encoded := fakeHash(19456, 2, 1, salt, 24)

	h, rec := newTestHasher(t)

	got, err := h.VerifyPassword(t.Context(), encoded, fakePassword)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !got.Match {
		t.Fatal("a hash under older parameters did not verify")
	}

	d := rec.last(t)
	if d.memoryKiB != 19456 || d.time != 2 || d.threads != 1 || d.keyLen != 24 || string(d.salt) != string(salt) {
		t.Fatalf("derivation = m=%d t=%d p=%d key=%d salt=%q, want the hash's own", d.memoryKiB, d.time, d.threads, d.keyLen, d.salt)
	}
}

func TestHasher_VerifyPassword_needsRehash(t *testing.T) {
	t.Parallel()

	salt16 := []byte("fake-salt-16byte")

	tests := []struct {
		name    string
		encoded string
		want    bool
	}{
		{name: "today's parameters", encoded: fakeHash(65536, 3, 4, salt16, 32), want: false},
		{name: "less memory", encoded: fakeHash(32768, 3, 4, salt16, 32), want: true},
		{name: "fewer passes", encoded: fakeHash(65536, 2, 4, salt16, 32), want: true},
		{name: "more passes", encoded: fakeHash(65536, 4, 4, salt16, 32), want: true},
		{name: "fewer lanes", encoded: fakeHash(65536, 3, 2, salt16, 32), want: true},
		{name: "more lanes", encoded: fakeHash(65536, 3, 8, salt16, 32), want: true},
		{name: "shorter salt", encoded: fakeHash(65536, 3, 4, salt16[:8], 32), want: true},
		{name: "longer salt", encoded: fakeHash(65536, 3, 4, []byte("fake-salt-32-bytes-long-00000000"), 32), want: true},
		{name: "shorter key", encoded: fakeHash(65536, 3, 4, salt16, 16), want: true},
		{name: "longer key", encoded: fakeHash(65536, 3, 4, salt16, 64), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newTestHasher(t)

			got, err := h.VerifyPassword(t.Context(), tt.encoded, fakePassword)
			if err != nil {
				t.Fatalf("VerifyPassword: %v", err)
			}
			if !got.Match || got.NeedsRehash != tt.want {
				t.Fatalf("VerifyPassword = %+v, want match with NeedsRehash %v", got, tt.want)
			}

			// A wrong password never asks for a rehash, so a caller cannot
			// rehash on a failed login.
			got, err = h.VerifyPassword(t.Context(), tt.encoded, "fake-wrong-password")
			if err != nil {
				t.Fatalf("VerifyPassword(wrong): %v", err)
			}
			if got.Match || got.NeedsRehash {
				t.Fatalf("VerifyPassword(wrong) = %+v, want neither", got)
			}
		})
	}
}

func TestHasher_VerifyPassword_invalidEncoding(t *testing.T) {
	t.Parallel()

	const (
		salt   = "AAECAwQFBgcICQoLDA0ODw"                      // 16 bytes
		digest = "T5149XoF4JBfOU6jXZ4MmJZqHHvyv3c4gRDlmcrIXDU" // 32 bytes, the fake vector's
	)

	valid := "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + digest

	tests := []struct {
		name  string
		input string
	}{
		{name: "plain text", input: "fake-plain-text"},
		{name: "bcrypt", input: "$2b$12$fakefakefakefakefakefuFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE"},
		{name: "argon2i", input: strings.Replace(valid, "argon2id", "argon2i", 1)},
		{name: "argon2d", input: strings.Replace(valid, "argon2id", "argon2d", 1)},
		{name: "upper case algorithm", input: strings.Replace(valid, "argon2id", "ARGON2ID", 1)},
		{name: "version 16", input: strings.Replace(valid, "v=19", "v=16", 1)},
		{name: "version with a leading zero", input: strings.Replace(valid, "v=19", "v=019", 1)},
		{name: "version missing", input: "$argon2id$m=65536,t=3,p=4$" + salt + "$" + digest},
		{name: "memory zero", input: strings.Replace(valid, "m=65536", "m=0", 1)},
		{name: "memory below eight per lane", input: strings.Replace(valid, "m=65536", "m=31", 1)},
		{name: "memory above the bound", input: strings.Replace(valid, "m=65536", "m=65537", 1)},
		{name: "memory overflows uint32", input: strings.Replace(valid, "m=65536", "m=4294967296", 1)},
		{name: "memory with a leading zero", input: strings.Replace(valid, "m=65536", "m=065536", 1)},
		{name: "memory with a sign", input: strings.Replace(valid, "m=65536", "m=+65536", 1)},
		{name: "memory empty", input: strings.Replace(valid, "m=65536", "m=", 1)},
		{name: "passes zero", input: strings.Replace(valid, "t=3", "t=0", 1)},
		{name: "passes above the bound", input: strings.Replace(valid, "t=3", "t=17", 1)},
		{name: "lanes zero", input: strings.Replace(valid, "p=4", "p=0", 1)},
		{name: "lanes above the bound", input: strings.Replace(valid, "p=4", "p=17", 1)},
		{name: "lanes overflow uint8", input: strings.Replace(valid, "p=4", "p=260", 1)},
		{name: "parameters out of order", input: strings.Replace(valid, "m=65536,t=3,p=4", "t=3,m=65536,p=4", 1)},
		{name: "extra parameter", input: strings.Replace(valid, "p=4", "p=4,keyid=0", 1)},
		{name: "parameter with spaces", input: strings.Replace(valid, "t=3", "t= 3", 1)},
		{name: "salt missing", input: "$argon2id$v=19$m=65536,t=3,p=4$" + digest},
		{name: "salt empty", input: "$argon2id$v=19$m=65536,t=3,p=4$$" + digest},
		{name: "salt below eight bytes", input: "$argon2id$v=19$m=65536,t=3,p=4$AAECAwQFBg$" + digest},
		{name: "salt above 64 bytes", input: "$argon2id$v=19$m=65536,t=3,p=4$" + strings.Repeat("A", 88) + "$" + digest},
		{name: "key below 16 bytes", input: "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$AAECAwQFBgcICQoLDA0O"},
		{name: "key above 64 bytes", input: "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + strings.Repeat("A", 88)},
		{name: "salt padded", input: "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "==$" + digest},
		{name: "key padded", input: valid + "="},
		{name: "salt not canonical", input: "$argon2id$v=19$m=65536,t=3,p=4$AAECAwQFBgcICQoLDA0ODx$" + digest},
		{name: "url safe alphabet", input: "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$T5149XoF4JBfOU6jXZ4MmJZqHHvyv3c4gRDlmcrIXD_"},
		{name: "trailing separator", input: valid + "$"},
		{name: "leading space", input: " " + valid},
		{name: "trailing newline", input: valid + "\n"},
		{name: "newline inside the salt", input: "$argon2id$v=19$m=65536,t=3,p=4$AAECAwQFBgcI\nCQoLDA0ODw$" + digest},
		{name: "carriage return inside the key", input: "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$T5149XoF4JBfOU6jXZ4\rMmJZqHHvyv3c4gRDlmcrIXDU"},
		{name: "overlong", input: valid + strings.Repeat("A", 600)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, rec := newTestHasher(t)

			got, err := h.VerifyPassword(t.Context(), tt.input, vectorPassword)
			if !errors.Is(err, ErrPasswordHashInvalid) {
				t.Fatalf("VerifyPassword = %+v, %v, want ErrPasswordHashInvalid", got, err)
			}
			if got.Match || got.NeedsRehash {
				t.Fatalf("VerifyPassword = %+v for an invalid hash", got)
			}

			// An invalid hash costs the same derivation as a valid one.
			assertDummyWork(t, rec)
		})
	}

	// The valid string the cases start from does parse.
	h, _ := newTestHasher(t)
	if _, err := h.VerifyPassword(t.Context(), valid, vectorPassword); err != nil {
		t.Fatalf("VerifyPassword(valid base) = %v", err)
	}
}

// assertDummyWork checks that exactly one derivation ran, under today's
// parameters and key length and never over a password above the limit.
func assertDummyWork(t *testing.T, rec *recorder) {
	t.Helper()

	if n := rec.count(); n != 1 {
		t.Fatalf("%d derivations ran, want exactly 1", n)
	}

	d := rec.last(t)
	if d.time != 3 || d.memoryKiB != 64*1024 || d.threads != 4 || d.keyLen != 32 || len(d.salt) != 16 {
		t.Fatalf("dummy derivation = t=%d m=%d p=%d key=%d salt=%d, want today's parameters",
			d.time, d.memoryKiB, d.threads, d.keyLen, len(d.salt))
	}
	if len(d.password) > MaxPasswordLength {
		t.Fatalf("the dummy derivation hashed %d bytes, above the limit", len(d.password))
	}
}

// TestHasher_VerifyPassword_equalWork asserts the work of every path a login
// can take: a known account, an unknown one, an invalid hash and an over long
// password each run exactly one derivation at today's cost.
func TestHasher_VerifyPassword_equalWork(t *testing.T) {
	t.Parallel()

	known := fakeHash(65536, 3, 4, []byte("fake-salt-16byte"), 32)

	tests := []struct {
		name      string
		encoded   string
		password  string
		wantMatch bool
		wantErr   error
	}{
		{name: "known account, right password", encoded: known, password: fakePassword, wantMatch: true},
		{name: "known account, wrong password", encoded: known, password: "fake-wrong-password"},
		{name: "unknown account", encoded: "", password: fakePassword},
		{name: "unknown account, empty password", encoded: "", password: ""},
		{name: "invalid hash", encoded: "fake-not-a-hash", password: fakePassword, wantErr: ErrPasswordHashInvalid},
		{name: "password above the limit", encoded: known, password: strings.Repeat("f", MaxPasswordLength+1), wantErr: ErrPasswordTooLong},
		{name: "unknown account, password above the limit", encoded: "", password: strings.Repeat("f", MaxPasswordLength+1), wantErr: ErrPasswordTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, rec := newTestHasher(t)

			got, err := h.VerifyPassword(t.Context(), tt.encoded, tt.password)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("VerifyPassword error = %v, want %v", err, tt.wantErr)
			}
			if got.Match != tt.wantMatch || got.NeedsRehash {
				t.Fatalf("VerifyPassword = %+v, want Match %v", got, tt.wantMatch)
			}

			assertDummyWork(t, rec)
		})
	}
}

// TestHasher_VerifyPassword_maximumLength makes sure a password of exactly
// MaxPasswordLength bytes, which HashPassword accepts, also logs in.
func TestHasher_VerifyPassword_maximumLength(t *testing.T) {
	t.Parallel()

	h, rec := newTestHasher(t)
	longest := strings.Repeat("f", MaxPasswordLength)

	encoded, err := h.HashPassword(t.Context(), longest)
	if err != nil {
		t.Fatalf("HashPassword(%d bytes): %v", MaxPasswordLength, err)
	}

	got, err := h.VerifyPassword(t.Context(), encoded, longest)
	if err != nil || !got.Match {
		t.Fatalf("VerifyPassword(%d bytes) = %+v, %v, want a match", MaxPasswordLength, got, err)
	}
	if d := rec.last(t); d.password != longest {
		t.Fatal("the verification did not hash the password itself")
	}
}

// TestHasher_VerifyPassword_unknownNeverMatches makes sure the dummy path
// cannot match, whatever password is tried, including the dummy's own.
func TestHasher_VerifyPassword_unknownNeverMatches(t *testing.T) {
	t.Parallel()

	h, err := NewHasher(HasherConfig{})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}

	for _, password := range []string{"", fakePassword, dummyPassword} {
		got, err := h.VerifyPassword(t.Context(), "", password)
		if err != nil || got.Match || got.NeedsRehash {
			t.Fatalf("VerifyPassword(no hash, %q) = %+v, %v, want no match", password, got, err)
		}
	}
}

// gate is a derive function that blocks every call until released, and
// tracks how many run at once.
type gate struct {
	entered  chan struct{}
	release  chan struct{}
	inFlight atomic.Int32
	peak     atomic.Int32
	total    atomic.Int32
}

func newGate() *gate {
	return &gate{entered: make(chan struct{}, 64), release: make(chan struct{})}
}

func (g *gate) derive(password, salt []byte, iterations, memoryKiB uint32, threads uint8, keyLen uint32) []byte {
	now := g.inFlight.Add(1)
	for {
		peak := g.peak.Load()
		if now <= peak || g.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	g.total.Add(1)
	g.entered <- struct{}{}
	<-g.release
	g.inFlight.Add(-1)

	return fakeDerive(password, salt, iterations, memoryKiB, threads, keyLen)
}

func TestHasher_concurrencyCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input int
		want  int
	}{
		{name: "default", input: 0, want: 1},
		{name: "one", input: 1, want: 1},
		{name: "three", input: 3, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := NewHasher(HasherConfig{MaxConcurrentHashes: tt.input})
			if err != nil {
				t.Fatalf("NewHasher: %v", err)
			}

			g := newGate()
			h.derive = g.derive
			h.maxWait = time.Minute

			const callers = 8

			var wg sync.WaitGroup
			errs := make(chan error, callers)
			for i := range callers {
				wg.Go(func() {
					var err error
					if i%2 == 0 {
						_, err = h.HashPassword(t.Context(), fakePassword)
					} else {
						_, err = h.VerifyPassword(t.Context(), "", fakePassword)
					}
					errs <- err
				})
			}

			// Wait until the cap is full, then let one derivation finish at
			// a time. Each release makes room for exactly one more.
			for range tt.want {
				<-g.entered
			}
			for i := range callers {
				g.release <- struct{}{}
				if i+tt.want < callers {
					<-g.entered
				}
			}

			wg.Wait()
			close(errs)

			for err := range errs {
				if err != nil {
					t.Fatalf("a caller failed: %v", err)
				}
			}
			if got := g.peak.Load(); int(got) != tt.want {
				t.Fatalf("peak concurrent derivations = %d, want %d", got, tt.want)
			}
			if got := g.total.Load(); got != callers {
				t.Fatalf("derivations = %d, want %d", got, callers)
			}
		})
	}
}

// TestHasher_busy holds the only slot and checks what a second caller gets.
func TestHasher_busy(t *testing.T) {
	t.Parallel()

	type call func(h *Hasher, ctx context.Context) error

	hash := func(h *Hasher, ctx context.Context) error {
		_, err := h.HashPassword(ctx, fakePassword)
		return err
	}
	verify := func(h *Hasher, ctx context.Context) error {
		_, err := h.VerifyPassword(ctx, "", fakePassword)
		return err
	}

	tests := []struct {
		name string
		call call
	}{
		{name: "hash", call: hash},
		{name: "verify", call: verify},
	}

	for _, tt := range tests {
		t.Run(tt.name+" times out", func(t *testing.T) {
			t.Parallel()

			h, g := holdSlot(t)
			h.maxWait = 20 * time.Millisecond

			start := time.Now()
			err := within(t, func() error { return tt.call(h, t.Context()) })
			if !errors.Is(err, ErrHashingBusy) {
				t.Fatalf("second caller = %v, want ErrHashingBusy", err)
			}
			if waited := time.Since(start); waited < h.maxWait {
				t.Fatalf("second caller gave up after %s, before the wait of %s", waited, h.maxWait)
			}
			if got := g.total.Load(); got != 1 {
				t.Fatalf("derivations = %d, want only the holder's", got)
			}

			g.release <- struct{}{}
		})

		t.Run(tt.name+" gets a slot freed while waiting", func(t *testing.T) {
			t.Parallel()

			h, g := holdSlot(t)
			h.maxWait = time.Minute

			done := make(chan error, 1)
			go func() { done <- tt.call(h, t.Context()) }()

			g.release <- struct{}{} // the holder finishes
			<-g.entered             // the waiting caller takes the slot
			g.release <- struct{}{}

			if err := <-done; err != nil {
				t.Fatalf("waiting caller = %v, want success", err)
			}
		})

		t.Run(tt.name+" cancelled while waiting", func(t *testing.T) {
			t.Parallel()

			h, g := holdSlot(t)
			h.maxWait = time.Minute

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := within(t, func() error { return tt.call(h, ctx) })
			if !errors.Is(err, ErrHashingBusy) || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled caller = %v, want ErrHashingBusy wrapping context.Canceled", err)
			}

			g.release <- struct{}{}
		})

		t.Run(tt.name+" deadline passes while waiting", func(t *testing.T) {
			t.Parallel()

			h, g := holdSlot(t)
			h.maxWait = time.Minute

			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()

			err := within(t, func() error { return tt.call(h, ctx) })
			if !errors.Is(err, ErrHashingBusy) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("caller = %v, want ErrHashingBusy wrapping context.DeadlineExceeded", err)
			}

			g.release <- struct{}{}
		})

		t.Run(tt.name+" cancelled with a free slot", func(t *testing.T) {
			t.Parallel()

			h, rec := newTestHasher(t)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			for range 20 {
				err := tt.call(h, ctx)
				if !errors.Is(err, ErrHashingBusy) || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled caller = %v, want ErrHashingBusy wrapping context.Canceled", err)
				}
			}
			if n := rec.count(); n != 0 {
				t.Fatalf("%d derivations ran for a cancelled caller", n)
			}
		})
	}
}

// holdSlot returns a Hasher with a cap of one whose only slot is held by a
// derivation blocked in the gate.
func holdSlot(t *testing.T) (*Hasher, *gate) {
	t.Helper()

	h, err := NewHasher(HasherConfig{MaxConcurrentHashes: 1})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}

	g := newGate()
	h.derive = g.derive

	// The holder's error is of no interest: it only keeps the slot.
	go func() { _, _ = h.HashPassword(t.Context(), fakePassword) }()
	<-g.entered

	return h, g
}

// within runs call and fails the test when it does not return in time,
// instead of hanging the suite.
func within(t *testing.T, call func() error) error {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- call() }()

	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("call did not return")
		return nil
	}
}
