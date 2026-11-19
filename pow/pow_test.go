// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package pow

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	keyA = []byte("0123456789abcdef0123456789abcdef")
	keyB = []byte("fedcba9876543210fedcba9876543210")
	t0   = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newService(t *testing.T, c *clock, mod func(*Config)) *Service {
	t.Helper()
	cfg := Config{
		Audience:  "example.test",
		Keys:      [][]byte{keyA},
		MaxNumber: 1000,
		TTL:       10 * time.Minute,
		MinAge:    3 * time.Second,
		Now:       c.now,
	}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// solve finds the number the way the browser does.
func solve(t *testing.T, c Challenge) Solution {
	t.Helper()
	for n := int64(0); n <= c.MaxNumber; n++ {
		if Hash(c.Salt, n) == c.Challenge {
			return Solution{Algorithm: c.Algorithm, Challenge: c.Challenge, Number: n, Salt: c.Salt, Signature: c.Signature}
		}
	}
	t.Fatal("no solution found")
	return Solution{}
}

func TestNew(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mod  func(*Config)
		ok   bool
	}{
		{"valid", nil, true},
		{"key of 31 bytes", func(c *Config) { c.Keys = [][]byte{keyA[:31]} }, false},
		{"second key too short", func(c *Config) { c.Keys = [][]byte{keyA, keyB[:8]} }, false},
		{"no key", func(c *Config) { c.Keys = nil }, false},
		{"empty audience", func(c *Config) { c.Audience = "" }, false},
		{"audience with NUL", func(c *Config) { c.Audience = "a\x00b" }, false},
		{"negative difficulty", func(c *Config) { c.MaxNumber = -1 }, false},
		{"negative TTL", func(c *Config) { c.TTL = -time.Second }, false},
		{"two keys", func(c *Config) { c.Keys = [][]byte{keyA, keyB} }, true},
		{"defaults", func(c *Config) { c.MaxNumber, c.TTL, c.MinAge = 0, 0, 0 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{Audience: "example.test", Keys: [][]byte{keyA}}
			if tc.mod != nil {
				tc.mod(&cfg)
			}
			_, err := New(cfg)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	s, err := New(Config{Audience: "example.test", Keys: [][]byte{keyA}})
	if err != nil {
		t.Fatal(err)
	}
	if s.maxNumber != 300_000 || s.ttl != 10*time.Minute || s.minAge != 3*time.Second {
		t.Errorf("defaults = %d %v %v", s.maxNumber, s.ttl, s.minAge)
	}
	s, err = New(Config{Audience: "example.test", Keys: [][]byte{keyA}, MinAge: -1})
	if err != nil || s.minAge != 0 {
		t.Errorf("negative MinAge: %v, %v", s.minAge, err)
	}
}

func TestKeyIsCopied(t *testing.T) {
	t.Parallel()

	k := append([]byte(nil), keyA...)
	c := &clock{t: t0}
	s := newService(t, c, func(cfg *Config) { cfg.Keys = [][]byte{k} })
	ch, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	k[0] ^= 0xff
	c.advance(5 * time.Second)
	if err := s.Verify(solve(t, ch)); err != nil {
		t.Fatalf("caller changed the key after New: %v", err)
	}
}

func TestCreate(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	s := newService(t, c, nil)
	ch, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if ch.Algorithm != Algorithm || ch.MaxNumber != 1000 || !isHex64(ch.Challenge) || !isHex64(ch.Signature) {
		t.Fatalf("challenge = %+v", ch)
	}
	exp, iss, err := parseSalt(ch.Salt)
	if err != nil {
		t.Fatal(err)
	}
	if !iss.Equal(t0) || !exp.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("salt times = %v %v", iss, exp)
	}
}

func TestCreateRandom(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	s := newService(t, c, func(cfg *Config) { cfg.Rand = bytes.NewReader(nil) })
	if _, err := s.Create(); err == nil {
		t.Fatal("Create with a failing source succeeded")
	}
	s = newService(t, c, nil)
	a, _ := s.Create()
	b, _ := s.Create()
	if a.Salt == b.Salt {
		t.Error("two challenges share a salt")
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		after  time.Duration
		mutate func(*Solution)
		want   error
	}{
		{"valid", 5 * time.Second, func(*Solution) {}, nil},
		{"valid at minimum age", 3 * time.Second, func(*Solution) {}, nil},
		{"valid just before expiry", 10*time.Minute - time.Second, func(*Solution) {}, nil},
		{"expired", 10 * time.Minute, func(*Solution) {}, ErrExpired},
		{"too fast", 2 * time.Second, func(*Solution) {}, ErrTooFast},
		{"wrong number", 5 * time.Second, func(s *Solution) { s.Number = (s.Number + 1) % 1001 }, ErrWrong},
		{"negative number", 5 * time.Second, func(s *Solution) { s.Number = -1 }, ErrMalformed},
		{"number above maximum", 5 * time.Second, func(s *Solution) { s.Number = 1001 }, ErrMalformed},
		{"bad signature", 5 * time.Second, func(s *Solution) { s.Signature = sign(keyB, "example.test", s.Challenge) }, ErrSignature},
		{"signature for another audience", 5 * time.Second, func(s *Solution) { s.Signature = sign(keyA, "other.test", s.Challenge) }, ErrSignature},
		{"signature without domain", 5 * time.Second, func(s *Solution) { s.Signature = strings.Repeat("0", 64) }, ErrSignature},
		{"salt changed", 5 * time.Second, func(s *Solution) { s.Salt += "0" }, ErrWrong},
		{"salt too long", 5 * time.Second, func(s *Solution) { s.Salt = strings.Repeat("a", 129) }, ErrMalformed},
		{"empty salt", 5 * time.Second, func(s *Solution) { s.Salt = "" }, ErrMalformed},
		{"algorithm", 5 * time.Second, func(s *Solution) { s.Algorithm = "SHA-1" }, ErrMalformed},
		{"challenge length", 5 * time.Second, func(s *Solution) { s.Challenge = s.Challenge[1:] }, ErrMalformed},
		{"signature length", 5 * time.Second, func(s *Solution) { s.Signature = "" }, ErrMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &clock{t: t0}
			s := newService(t, c, nil)
			ch, err := s.Create()
			if err != nil {
				t.Fatal(err)
			}
			sol := solve(t, ch)
			tc.mutate(&sol)
			c.advance(tc.after)
			got := s.Verify(sol)
			if !errors.Is(got, tc.want) {
				t.Fatalf("Verify = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []error{ErrMalformed, ErrSignature, ErrWrong, ErrExpired, ErrTooFast, ErrReplay, ErrBusy}
	for i, a := range all {
		for j, b := range all {
			if got := errors.Is(a, b); got != (i == j) {
				t.Errorf("errors.Is(%v, %v) = %v", a, b, got)
			}
		}
	}
	for _, e := range []error{ErrMalformed, ErrSignature, ErrWrong} {
		if !errors.Is(e, ErrInvalid) {
			t.Errorf("%v is not ErrInvalid", e)
		}
	}
	for _, e := range []error{ErrExpired, ErrTooFast, ErrReplay, ErrBusy} {
		if errors.Is(e, ErrInvalid) {
			t.Errorf("%v is ErrInvalid", e)
		}
	}
}

// A salt that is signed and solved but not canonical is malformed.
func TestSignedButMalformedSalt(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	s := newService(t, c, nil)
	for _, salt := range []string{
		"00112233445566778899aabb?expires=1790000600",
		"00112233445566778899aabb?issued=1&expires=2",
		"00112233445566778899aabb?expires=+5&issued=1",
		"00112233445566778899aabb?expires=05&issued=1",
		"00112233445566778899aabb?expires=-5&issued=1",
		"00112233445566778899aabb?expires=x&issued=1",
		"00112233445566778899AABB?expires=5&issued=1",
		"0011?expires=5&issued=1",
		"no query",
	} {
		hash := Hash(salt, 1)
		sol := Solution{Algorithm: Algorithm, Challenge: hash, Number: 1, Salt: salt, Signature: sign(keyA, "example.test", hash)}
		if err := s.Verify(sol); !errors.Is(err, ErrMalformed) {
			t.Errorf("salt %q: %v", salt, err)
		}
	}
}

func TestReplay(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	s := newService(t, c, nil)
	ch, _ := s.Create()
	sol := solve(t, ch)
	c.advance(5 * time.Second)
	if err := s.Verify(sol); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(sol); !errors.Is(err, ErrReplay) {
		t.Fatalf("second use = %v", err)
	}
}

// A refused request does not burn the solution: only a success records it.
func TestRefusalDoesNotBurn(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	s := newService(t, c, nil)
	ch, _ := s.Create()
	sol := solve(t, ch)
	c.advance(time.Second)
	if err := s.Verify(sol); !errors.Is(err, ErrTooFast) {
		t.Fatalf("too fast = %v", err)
	}
	c.advance(5 * time.Second)
	if err := s.Verify(sol); err != nil {
		t.Fatalf("after waiting: %v", err)
	}
}

func TestKeyRotation(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	old := newService(t, c, nil)
	ch, _ := old.Create()
	sol := solve(t, ch)
	c.advance(5 * time.Second)

	rotated := newService(t, c, func(cfg *Config) { cfg.Keys = [][]byte{keyB, keyA} })
	if err := rotated.Verify(sol); err != nil {
		t.Fatalf("previous key refused: %v", err)
	}
	fresh, _ := rotated.Create()
	if fresh.Signature != sign(keyB, "example.test", fresh.Challenge) {
		t.Error("new challenge not signed with the first key")
	}
	dropped := newService(t, c, func(cfg *Config) { cfg.Keys = [][]byte{keyB} })
	if err := dropped.Verify(sol); !errors.Is(err, ErrSignature) {
		t.Fatalf("dropped key = %v", err)
	}
}

func TestAudienceBinding(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	a := newService(t, c, nil)
	b := newService(t, c, func(cfg *Config) { cfg.Audience = "other.test" })
	ch, _ := a.Create()
	sol := solve(t, ch)
	c.advance(5 * time.Second)
	if err := b.Verify(sol); !errors.Is(err, ErrSignature) {
		t.Fatalf("other audience = %v", err)
	}
	if err := a.Verify(sol); err != nil {
		t.Fatalf("own audience: %v", err)
	}
}

func TestMemorySeen(t *testing.T) {
	t.Parallel()

	m := NewMemorySeen(2)
	exp := t0.Add(time.Minute)
	if err := m.Use("a", exp, t0); err != nil {
		t.Fatal(err)
	}
	if err := m.Use("a", exp, t0); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay = %v", err)
	}
	if err := m.Use("b", exp.Add(time.Minute), t0); err != nil {
		t.Fatal(err)
	}
	// Full with unexpired entries: refuse, never forget.
	if err := m.Use("c", exp, t0); !errors.Is(err, ErrBusy) {
		t.Fatalf("full = %v", err)
	}
	if err := m.Use("a", exp, t0); !errors.Is(err, ErrReplay) {
		t.Fatalf("a forgotten while full: %v", err)
	}
	if m.Len() != 2 {
		t.Fatalf("len = %d", m.Len())
	}
	// "a" expired, so it makes room, "b" stays.
	if err := m.Use("c", exp.Add(2*time.Minute), exp); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if err := m.Use("b", exp, exp); !errors.Is(err, ErrReplay) {
		t.Fatalf("b forgotten: %v", err)
	}
	if NewMemorySeen(0).max != DefaultMaxUsed {
		t.Error("default bound missing")
	}
}

type fakeSeen struct{ err error }

func (f fakeSeen) Use(string, time.Time, time.Time) error { return f.err }

func TestCustomSeen(t *testing.T) {
	t.Parallel()

	for _, want := range []error{nil, ErrReplay, ErrBusy} {
		c := &clock{t: t0}
		s := newService(t, c, func(cfg *Config) { cfg.Seen = fakeSeen{want} })
		ch, _ := s.Create()
		c.advance(5 * time.Second)
		if err := s.Verify(solve(t, ch)); !errors.Is(err, want) {
			t.Errorf("Verify = %v, want %v", err, want)
		}
	}
}

// The store is touched only after every other check passed.
func TestSeenUsedLast(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	m := NewMemorySeen(10)
	s := newService(t, c, func(cfg *Config) { cfg.Seen = m })
	ch, _ := s.Create()
	sol := solve(t, ch)
	sol.Number = (sol.Number + 1) % 1001
	c.advance(5 * time.Second)
	_ = s.Verify(sol)
	c.advance(10 * time.Minute)
	_ = s.Verify(solve(t, ch))
	if m.Len() != 0 {
		t.Errorf("a refused solution was recorded: %d", m.Len())
	}
}

func TestConcurrentSingleUse(t *testing.T) {
	t.Parallel()

	c := &clock{t: t0}
	s := newService(t, c, nil)
	ch, _ := s.Create()
	sol := solve(t, ch)
	c.advance(5 * time.Second)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Verify(sol) == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d concurrent uses succeeded, want 1", ok)
	}
}

func FuzzParseSalt(f *testing.F) {
	f.Add("00112233445566778899aabb?expires=1790000600&issued=1790000000")
	f.Add("")
	f.Add("?expires=&issued=")
	f.Add("00112233445566778899aabb?expires=-1&issued=0")
	f.Add("00112233445566778899aabb?expires=9223372036854775808&issued=0")
	f.Fuzz(func(t *testing.T, salt string) {
		exp, iss, err := parseSalt(salt)
		if err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("error %v is not ErrMalformed", err)
			}
			return
		}
		// An accepted salt is canonical: it formats back to the same string.
		nonce := make([]byte, nonceLen)
		for i := range nonce {
			nonce[i] = hexByte(salt[2*i], salt[2*i+1])
		}
		if got := formatSalt(nonce, exp.Unix(), iss.Unix()); got != salt {
			t.Fatalf("salt %q formats back as %q", salt, got)
		}
	})
}

func hexByte(a, b byte) byte {
	val := func(c byte) byte {
		if c >= 'a' {
			return c - 'a' + 10
		}
		return c - '0'
	}
	return val(a)<<4 | val(b)
}

func FuzzVerify(f *testing.F) {
	f.Add("SHA-256", strings.Repeat("a", 64), int64(1), "00112233445566778899aabb?expires=5&issued=1", strings.Repeat("b", 64))
	f.Add("", "", int64(-1), "", "")
	c := &clock{t: t0}
	s, err := New(Config{Audience: "example.test", Keys: [][]byte{keyA}, MaxNumber: 1000, Now: c.now})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, alg, challenge string, n int64, salt, sig string) {
		err := s.Verify(Solution{Algorithm: alg, Challenge: challenge, Number: n, Salt: salt, Signature: sig})
		if err == nil {
			t.Fatalf("random input verified: %q %q %d %q %q", alg, challenge, n, salt, sig)
		}
	})
}
