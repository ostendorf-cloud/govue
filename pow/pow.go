// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package pow issues and verifies stateless proof of work challenges, the
// ALTCHA shape: the browser searches the number n with sha256(salt + n) ==
// challenge, and an HMAC over the challenge proves that the server issued it.
//
// A challenge carries a version (the domain separator of the HMAC), a random
// nonce, an expiry and the issue time inside its salt, and the difficulty as
// the largest number to try. The server keeps no record of an issued
// challenge. Verification recomputes everything from the solution and the
// keys.
//
// The HMAC input is "govue/pow\x00v1\x00" + audience + "\x00" + challenge. The
// audience is the site name. A key reused across sites cannot redeem the
// challenge of another site, and a challenge of another purpose cannot be
// replayed here.
//
// Proof of work is a speed bump, never the control. Native code solves the
// default difficulty in milliseconds. The rate limits of the route are the
// real control.
//
// Replay is limited by the expiry window plus a single use record. The
// default record is a bounded in memory set, so it is per process: with
// several replicas a solution can be used once per replica until it expires.
// A consumer that needs one global answer supplies its own [Seen]. A full set
// refuses with [ErrBusy], it never forgets an unexpired entry.
//
// Privacy facts a consumer's page can quote, all true of this package:
//
//   - Nothing is stored about a visitor. The server keeps no challenge, no
//     address and no identifier of a person. The single use record holds one
//     hash of a solved challenge until the challenge expires, and it is not
//     linked to a person.
//   - No cookie is set and no browser storage is used. The challenge is a
//     response body and the solution is a request body.
//   - No third party is called.
//   - The challenge is not bound to an address, a session or a device.
//
// The package uses the standard library only and imports no other govue
// package.
package pow

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Algorithm is the only hash algorithm offered and accepted.
const Algorithm = "SHA-256"

// MinKeyLen is the shortest key accepted, in bytes.
const MinKeyLen = 32

// Defaults of [Config].
const (
	DefaultMaxNumber = 300_000
	DefaultTTL       = 10 * time.Minute
	DefaultMinAge    = 3 * time.Second
	DefaultMaxUsed   = 100_000
)

// domain is the version and domain separator of the signature.
const domain = "govue/pow\x00v1\x00"

// maxSaltLen bounds the salt a solution may carry.
const maxSaltLen = 128

// nonceLen is the random part of a salt, in bytes.
const nonceLen = 12

var (
	// ErrInvalid is the parent of ErrMalformed, ErrSignature and ErrWrong.
	ErrInvalid = errors.New("pow: invalid solution")
	// ErrMalformed means a field is missing, too long, out of range or badly formed.
	ErrMalformed = fmt.Errorf("%w: malformed", ErrInvalid)
	// ErrSignature means no key signed the challenge for this audience.
	ErrSignature = fmt.Errorf("%w: bad signature", ErrInvalid)
	// ErrWrong means the number does not solve the challenge.
	ErrWrong = fmt.Errorf("%w: wrong number", ErrInvalid)
	// ErrExpired means the challenge is past its expiry.
	ErrExpired = errors.New("pow: challenge expired")
	// ErrTooFast means the solution came sooner than the minimum age.
	ErrTooFast = errors.New("pow: submitted too fast")
	// ErrReplay means the solution was used before.
	ErrReplay = errors.New("pow: solution already used")
	// ErrBusy means the single use record is full. The caller may retry later.
	ErrBusy = errors.New("pow: too many solutions in flight")
)

// Challenge is what the challenge route returns.
type Challenge struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	MaxNumber int64  `json:"maxnumber"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

// Solution is what the browser sends back.
type Solution struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	Number    int64  `json:"number"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
}

// Seen records solved challenges so that each is used once.
type Seen interface {
	// Use records id until expires. It returns ErrReplay when id is recorded
	// and not yet expired, and ErrBusy when it cannot record another id. It
	// must not forget an unexpired id to make room.
	Use(id string, expires, now time.Time) error
}

// Config configures a [Service].
type Config struct {
	// Audience is the site name. It is part of every signature. Required.
	Audience string
	// Keys are the signing keys, each at least [MinKeyLen] bytes. The first
	// signs new challenges, all of them verify, so a rotation lists the new
	// key first and keeps the old one for one TTL. Required.
	Keys [][]byte
	// MaxNumber is the largest secret number and sets the work.
	// Zero means [DefaultMaxNumber].
	MaxNumber int64
	// TTL is how long a challenge stays valid. Zero means [DefaultTTL].
	TTL time.Duration
	// MinAge is the minimum time between issue and solution.
	// Zero means [DefaultMinAge]. A negative value means none.
	MinAge time.Duration
	// Seen is the single use record. Nil means a [MemorySeen] of
	// [DefaultMaxUsed] entries, which is per process.
	Seen Seen
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Rand is the randomness source. Nil means crypto/rand.
	Rand io.Reader
}

// Service issues and verifies challenges. It is safe for concurrent use.
type Service struct {
	audience  string
	keys      [][]byte
	maxNumber int64
	ttl       time.Duration
	minAge    time.Duration
	seen      Seen
	now       func() time.Time
	rand      io.Reader
}

// New validates cfg and returns a service. A key shorter than [MinKeyLen]
// bytes, an empty audience or a negative difficulty is refused.
func New(cfg Config) (*Service, error) {
	if cfg.Audience == "" || strings.ContainsRune(cfg.Audience, 0) {
		return nil, errors.New("pow: audience must be a non empty site name without NUL")
	}
	if len(cfg.Keys) == 0 {
		return nil, errors.New("pow: no key")
	}
	keys := make([][]byte, len(cfg.Keys))
	for i, k := range cfg.Keys {
		if len(k) < MinKeyLen {
			return nil, fmt.Errorf("pow: key %d has %d bytes, need at least %d", i, len(k), MinKeyLen)
		}
		keys[i] = append([]byte(nil), k...)
	}
	s := &Service{
		audience:  cfg.Audience,
		keys:      keys,
		maxNumber: cfg.MaxNumber,
		ttl:       cfg.TTL,
		minAge:    cfg.MinAge,
		seen:      cfg.Seen,
		now:       cfg.Now,
		rand:      cfg.Rand,
	}
	if s.maxNumber < 0 || s.ttl < 0 {
		return nil, errors.New("pow: negative difficulty or TTL")
	}
	if s.maxNumber == 0 {
		s.maxNumber = DefaultMaxNumber
	}
	if s.ttl == 0 {
		s.ttl = DefaultTTL
	}
	switch {
	case s.minAge == 0:
		s.minAge = DefaultMinAge
	case s.minAge < 0:
		s.minAge = 0
	}
	if s.seen == nil {
		s.seen = NewMemorySeen(DefaultMaxUsed)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.rand == nil {
		s.rand = rand.Reader
	}
	return s, nil
}

// Hash returns hex(sha256(salt + number)), the value the browser searches for.
func Hash(salt string, number int64) string {
	sum := sha256.Sum256([]byte(salt + strconv.FormatInt(number, 10)))
	return hex.EncodeToString(sum[:])
}

// sign returns hex(HMAC-SHA-256(key, domain + audience + 0x00 + challenge)).
func sign(key []byte, audience, challenge string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(domain))
	mac.Write([]byte(audience))
	mac.Write([]byte{0})
	mac.Write([]byte(challenge))
	return hex.EncodeToString(mac.Sum(nil))
}

// formatSalt is the one shape of a salt.
func formatSalt(nonce []byte, expires, issued int64) string {
	return fmt.Sprintf("%x?expires=%d&issued=%d", nonce, expires, issued)
}

// Create issues a new challenge, signed with the first key.
func (s *Service) Create() (Challenge, error) {
	var nonce [nonceLen]byte
	if _, err := io.ReadFull(s.rand, nonce[:]); err != nil {
		return Challenge{}, fmt.Errorf("pow: nonce: %w", err)
	}
	n, err := rand.Int(s.rand, big.NewInt(s.maxNumber+1))
	if err != nil {
		return Challenge{}, fmt.Errorf("pow: number: %w", err)
	}
	now := s.now()
	salt := formatSalt(nonce[:], now.Add(s.ttl).Unix(), now.Unix())
	challenge := Hash(salt, n.Int64())
	return Challenge{
		Algorithm: Algorithm,
		Challenge: challenge,
		MaxNumber: s.maxNumber,
		Salt:      salt,
		Signature: sign(s.keys[0], s.audience, challenge),
	}, nil
}

// parseSalt reads the one canonical salt shape: 24 lower case hex digits, a
// question mark, expires and issued as plain decimal numbers. It refuses
// anything else, so a parsed salt formats back to the same string.
func parseSalt(salt string) (expires, issued time.Time, err error) {
	if len(salt) > maxSaltLen {
		return time.Time{}, time.Time{}, ErrMalformed
	}
	nonce, query, ok := strings.Cut(salt, "?")
	if !ok || len(nonce) != 2*nonceLen || !isLowerHex(nonce) {
		return time.Time{}, time.Time{}, ErrMalformed
	}
	rest, ok := strings.CutPrefix(query, "expires=")
	if !ok {
		return time.Time{}, time.Time{}, ErrMalformed
	}
	expText, issText, ok := strings.Cut(rest, "&issued=")
	if !ok {
		return time.Time{}, time.Time{}, ErrMalformed
	}
	exp, err1 := strconv.ParseInt(expText, 10, 64)
	iss, err2 := strconv.ParseInt(issText, 10, 64)
	if err1 != nil || err2 != nil ||
		strconv.FormatInt(exp, 10) != expText || strconv.FormatInt(iss, 10) != issText ||
		exp < 0 || iss < 0 {
		return time.Time{}, time.Time{}, ErrMalformed
	}
	return time.Unix(exp, 0), time.Unix(iss, 0), nil
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func isHex64(s string) bool { return len(s) == 64 && isLowerHex(s) }

// Verify checks sol and records it as used. Call it only after the rest of
// the request is valid, so a typo does not burn the solution. The checks run
// in the order malformed, signature, number, expiry, minimum age, replay.
func (s *Service) Verify(sol Solution) error {
	if sol.Algorithm != Algorithm || !isHex64(sol.Challenge) || !isHex64(sol.Signature) ||
		sol.Salt == "" || len(sol.Salt) > maxSaltLen || sol.Number < 0 || sol.Number > s.maxNumber {
		return ErrMalformed
	}
	signed := false
	for _, key := range s.keys {
		// No early exit on a match: the time does not tell which key signed.
		if hmac.Equal([]byte(sign(key, s.audience, sol.Challenge)), []byte(sol.Signature)) {
			signed = true
		}
	}
	if !signed {
		return ErrSignature
	}
	if !hmac.Equal([]byte(Hash(sol.Salt, sol.Number)), []byte(sol.Challenge)) {
		return ErrWrong
	}
	expires, issued, err := parseSalt(sol.Salt)
	if err != nil {
		return err
	}
	now := s.now()
	if !now.Before(expires) {
		return ErrExpired
	}
	if now.Sub(issued) < s.minAge {
		return ErrTooFast
	}
	return s.seen.Use(sol.Challenge, expires, now)
}

// MemorySeen is a bounded in memory [Seen]. It is per process.
type MemorySeen struct {
	mu   sync.Mutex
	max  int
	used map[string]time.Time
}

// NewMemorySeen returns a set that holds at most max unexpired entries.
// A max below one means [DefaultMaxUsed].
func NewMemorySeen(max int) *MemorySeen {
	if max < 1 {
		max = DefaultMaxUsed
	}
	return &MemorySeen{max: max, used: map[string]time.Time{}}
}

// Use implements [Seen]. Only an expired entry is dropped to make room.
func (m *MemorySeen) Use(id string, expires, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if exp, ok := m.used[id]; ok && now.Before(exp) {
		return ErrReplay
	}
	if len(m.used) >= m.max {
		for k, exp := range m.used {
			if !now.Before(exp) {
				delete(m.used, k)
			}
		}
		if len(m.used) >= m.max {
			// Refusing is safe: forgetting an unexpired entry would allow a replay.
			return ErrBusy
		}
	}
	m.used[id] = expires
	return nil
}

// Len returns the number of recorded entries.
func (m *MemorySeen) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.used)
}
