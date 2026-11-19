// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package pow

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"testing"
	"time"
)

const vectorFile = "testdata/vectors.json"

var update = flag.Bool("update", false, "rewrite testdata/vectors.json")

// vectorFileData is the shared contract with the browser solver of #198. The
// TypeScript test reads the same file: hashes check the solver, cases check
// a verifier port, if there is one.
type vectorFileData struct {
	Comment       string       `json:"comment"`
	Version       int          `json:"version"`
	Material      string       `json:"material"`
	Audience      string       `json:"audience"`
	MaxNumber     int64        `json:"maxNumber"`
	MinAgeSeconds int64        `json:"minAgeSeconds"`
	Hashes        []hashVector `json:"hashes"`
	Cases         []verifyCase `json:"cases"`
	Challenge     Challenge    `json:"challenge"`
	Solution      Solution     `json:"solution"`
}

type hashVector struct {
	Salt      string `json:"salt"`
	Number    int64  `json:"number"`
	Challenge string `json:"challenge"`
}

type verifyCase struct {
	Name     string   `json:"name"`
	Now      int64    `json:"now"`
	Audience string   `json:"audience,omitempty"`
	Solution Solution `json:"solution"`
	Want     string   `json:"want"`
}

var outcomeErrors = map[string]error{
	"ok":        nil,
	"malformed": ErrMalformed,
	"signature": ErrSignature,
	"wrong":     ErrWrong,
	"expired":   ErrExpired,
	"too_fast":  ErrTooFast,
}

const (
	vecAudience = "example.test"
	vecIssued   = 1790000000
	vecTTL      = 600
	vecNumber   = 4242
	vecSalt     = "00112233445566778899aabb?expires=1790000600&issued=1790000000&"
)

var vecKey = []byte("0123456789abcdef0123456789abcdef")

func buildVectors() vectorFileData {
	sig := func(salt string, n int64) Solution {
		c := Hash(salt, n)
		return Solution{Algorithm: Algorithm, Challenge: c, Number: n, Salt: salt, Signature: sign(vecKey, vecAudience, c)}
	}
	valid := sig(vecSalt, vecNumber)
	with := func(f func(*Solution)) Solution {
		s := valid
		f(&s)
		return s
	}
	noExpiry := sig("00112233445566778899aabb?issued=1790000000&", 7)
	// The old salt shape without the trailing separator, and its splice: the
	// last digit of issued moved into the number keeps hash and signature.
	legacySalt := "00112233445566778899aabb?expires=1790000603&issued=1790000003"
	legacy := sig(legacySalt, 7)
	legacySplice := Solution{
		Algorithm: Algorithm, Challenge: legacy.Challenge, Number: 37,
		Salt: legacySalt[:len(legacySalt)-1], Signature: legacy.Signature,
	}
	// The same attempt against a challenge of the current shape: the separator
	// stands where the digit stood, so no shorter salt hashes to the challenge.
	spliceNew := with(func(s *Solution) {
		s.Salt = vecSalt[:len(vecSalt)-2]
		s.Number = 4242
	})
	cases := []verifyCase{
		{"valid", vecIssued + 5, "", valid, "ok"},
		{"valid at minimum age", vecIssued + 3, "", valid, "ok"},
		{"valid one second before expiry", vecIssued + vecTTL - 1, "", valid, "ok"},
		{"expired at the expiry second", vecIssued + vecTTL, "", valid, "expired"},
		{"too fast", vecIssued + 2, "", valid, "too_fast"},
		{"other audience", vecIssued + 5, "other.test", valid, "signature"},
		{"signature of another key", vecIssued + 5, "", with(func(s *Solution) {
			s.Signature = sign([]byte("fedcba9876543210fedcba9876543210"), vecAudience, s.Challenge)
		}), "signature"},
		{"wrong number", vecIssued + 5, "", with(func(s *Solution) { s.Number++ }), "wrong"},
		{"salt changed", vecIssued + 5, "", with(func(s *Solution) { s.Salt = "00112233445566778899aabc?expires=1790000600&issued=1790000000&" }), "wrong"},
		{"salt without trailing separator", vecIssued + 5, "", legacy, "malformed"},
		{"splice of issued digit into number, old shape, age 0", vecIssued + 3, "", legacySplice, "malformed"},
		{"salt shortened, number unchanged", vecIssued + 5, "", spliceNew, "wrong"},
		{"separator removed from the salt", vecIssued + 5, "", with(func(s *Solution) { s.Salt = vecSalt[:len(vecSalt)-1] }), "wrong"},
		{"unknown algorithm", vecIssued + 5, "", with(func(s *Solution) { s.Algorithm = "SHA-1" }), "malformed"},
		{"negative number", vecIssued + 5, "", with(func(s *Solution) { s.Number = -1 }), "malformed"},
		{"number above maximum", vecIssued + 5, "", with(func(s *Solution) { s.Number = 10001 }), "malformed"},
		{"challenge not hex", vecIssued + 5, "", with(func(s *Solution) { s.Challenge = "zz" + s.Challenge[2:] }), "malformed"},
		{"upper case signature", vecIssued + 5, "", with(func(s *Solution) { s.Signature = "A" + s.Signature[1:] }), "malformed"},
		{"empty salt", vecIssued + 5, "", with(func(s *Solution) { s.Salt = "" }), "malformed"},
		{"salt without expiry", vecIssued + 5, "", noExpiry, "malformed"},
		{"signed salt, leading zero in issued", vecIssued + 5, "", sig("00112233445566778899aabb?expires=1790000600&issued=01790000000&", 7), "malformed"},
		{"signed salt, upper case nonce", vecIssued + 5, "", sig("00112233445566778899AABB?expires=1790000600&issued=1790000000&", 7), "malformed"},
		{"signed salt, trailing double separator", vecIssued + 5, "", sig(vecSalt+"&", 7), "malformed"},
	}
	return vectorFileData{
		Comment: "Shared vectors of the pow package, written by go test -update. " +
			"challenge = hex(sha256(salt + number)), the number in decimal with no separator, " +
			"the salt string as sent. " +
			"salt = 24 lower case hex digits + '?expires=<unix>&issued=<unix>&'. The trailing '&' " +
			"is required: it ends issued explicitly, so no digit of the salt can move into the " +
			"number (CVE-2025-68113). A solver and a verifier port must refuse a salt without it. " +
			"signature = hex(hmac_sha256(key, 'govue/pow\\x00v1\\x00' + audience + '\\x00' + challenge)). " +
			"Verify checks in this order and returns the first failure: " +
			"1 the fields (algorithm, 64 lower case hex challenge and signature, salt not empty and not too long, " +
			"number from 0 to maxNumber): malformed; " +
			"2 the signature over the challenge: signature; " +
			"3 the hash of salt + number against the challenge: wrong; " +
			"4 the salt shape, parsed strictly and canonical: malformed; " +
			"5 expiry, expired when now is not before expires; " +
			"6 minimum age, too_fast when now - issued is below minAgeSeconds; " +
			"7 single use, replay. " +
			"now is Unix seconds. Every case runs on a fresh service.",
		Version:       1,
		Material:      string(vecKey),
		Audience:      vecAudience,
		MaxNumber:     10000,
		MinAgeSeconds: 3,
		Hashes: []hashVector{
			{vecSalt, vecNumber, Hash(vecSalt, vecNumber)},
			{vecSalt, 0, Hash(vecSalt, 0)},
			{vecSalt, 10000, Hash(vecSalt, 10000)},
			{"", 1, Hash("", 1)},
		},
		Cases:     cases,
		Challenge: Challenge{Algorithm: Algorithm, Challenge: valid.Challenge, MaxNumber: 10000, Salt: valid.Salt, Signature: valid.Signature},
		Solution:  valid,
	}
}

func TestVectors(t *testing.T) {
	t.Parallel()

	if *update {
		v := buildVectors()
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorFile, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(vectorFile)
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFileData
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	key := []byte(v.Material)
	for _, h := range v.Hashes {
		if got := Hash(h.Salt, h.Number); got != h.Challenge {
			t.Errorf("Hash(%q, %d) = %s, want %s", h.Salt, h.Number, got, h.Challenge)
		}
	}
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			audience := v.Audience
			if c.Audience != "" {
				audience = c.Audience
			}
			svc, err := New(Config{
				Audience:  audience,
				Keys:      [][]byte{key},
				MaxNumber: v.MaxNumber,
				MinAge:    time.Duration(v.MinAgeSeconds) * time.Second,
				Now:       func() time.Time { return time.Unix(c.Now, 0) },
			})
			if err != nil {
				t.Fatal(err)
			}
			want, ok := outcomeErrors[c.Want]
			if !ok {
				t.Fatalf("unknown outcome %q", c.Want)
			}
			if got := svc.Verify(c.Solution); !errors.Is(got, want) {
				t.Errorf("Verify = %v, want %v", got, want)
			}
		})
	}
}
