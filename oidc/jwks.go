// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// How long a fetched JWKS is trusted, and how hard this leans on the provider
// while deciding.
//
// A set is kept until its TTL runs out, which is what makes a key the provider
// has withdrawn stop being accepted: a cache with no expiry at all trusts a
// retired signing key for as long as the process runs, and that is the one
// thing key rotation is supposed to end.
//
// The TTL comes from the JWKS response's own Cache-Control max-age where there
// is one, clamped into the range below. A provider that says how long its keys
// are good for knows better than a constant compiled in here, and the clamp is
// what stops it from pinning the set for a month or from asking for a fetch per
// request.
const (
	// jwksDefaultTTL applies when the JWKS response carries no usable
	// Cache-Control.
	jwksDefaultTTL = 15 * time.Minute
	// jwksMinTTL is the floor on a provider supplied max-age.
	jwksMinTTL = time.Minute
	// jwksMaxTTL is the ceiling on one, so a withdrawn key is out of this
	// cache within the day whatever the provider claims.
	jwksMaxTTL = 24 * time.Hour

	// jwksMinRefresh bounds how often an unknown key id may cause a fetch.
	// Rotation adds a key id nothing has seen before, so a miss has to be able
	// to refetch, and without a bound on it a token carrying a made up key id
	// would be a request to the provider per login attempt.
	jwksMinRefresh = time.Minute

	// jwksStaleGrace is how long a set that is past its TTL is still used when
	// the provider cannot be reached to replace it. Refusing every login
	// during a JWKS blip is worse than briefly accepting a key the provider
	// published minutes ago, and the ID token's own expiry still bounds what
	// that key can be used for. It is not unbounded: an outage that outlasts
	// this stops the logins rather than trusting keys nobody has confirmed
	// since.
	jwksStaleGrace = time.Hour

	// jwksFetchTimeout bounds one shared fetch. The fetch outlives the request
	// that started it, so it cannot inherit that request's deadline.
	jwksFetchTimeout = 10 * time.Second

	// minRSABits rejects a modulus too short for the signature to mean
	// anything.
	minRSABits = 2048
)

// curves are the EC curves the allowed algorithms use.
var curves = map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384()}

// jsonWebKey is one entry of a JWKS, RFC 7517. use and alg are optional there,
// so neither is required here.
type jsonWebKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// keySet is the provider's signing keys and what is known about how fresh they
// are. It is safe for concurrent use, and a fetch is shared: a burst of logins
// arriving while the set is being replaced makes one request to the provider,
// not one each.
type keySet struct {
	provider *Provider

	mu          sync.Mutex
	keys        map[string]crypto.PublicKey
	expiresAt   time.Time
	lastAttempt time.Time
	inflight    *fetch
}

// fetch is one shared JWKS request. Everyone waiting on it reads its result
// after done is closed, and nothing writes to it afterwards.
type fetch struct {
	done chan struct{}
	keys map[string]crypto.PublicKey
	ttl  time.Duration
	err  error
}

func newKeySet(p *Provider) *keySet { return &keySet{provider: p} }

// key returns the public key the token's key id names.
//
// Selecting the one key the kid names, rather than trying every key in the set
// until one verifies, is deliberate. Trying them all turns a signature failure
// into an unbounded search and is the shape in which algorithm confusion gets
// reachable; RFC 8725 section 3.1 wants the key and the algorithm decided
// before the signature is checked, not by the token.
func (s *keySet) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	if key, ok := s.cached(kid, false); ok {
		return key, nil
	}

	// Either the kid is not in the set or the set is past its TTL. Both want a
	// fresh fetch, and refresh is what decides whether one is allowed now.
	if err := s.refresh(ctx); err != nil {
		// A set that is merely stale is better than no login at all while the
		// provider is unreachable, inside the grace period.
		if key, ok := s.cached(kid, true); ok {
			return key, nil
		}
		return nil, err
	}
	if key, ok := s.cached(kid, false); ok {
		return key, nil
	}
	return nil, loginProblem(fmt.Errorf("the provider's jwks holds no key %q", shorten(kid, maxKidLen)))
}

// cached looks kid up in the set held now. A set past its TTL answers only when
// stale is allowed and the grace period has not run out either.
func (s *keySet) cached(kid string, stale bool) (crypto.PublicKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.keys[kid]
	if !ok {
		return nil, false
	}
	now := s.provider.now()
	if now.Before(s.expiresAt) {
		return key, true
	}
	// The grace runs from the moment the set went stale, not from the fetch
	// that filled it. Measured from the fetch, a provider TTL of an hour or
	// more spends the whole window before the set ever expires, and the
	// fallback this grace exists for would never be reached.
	if stale && now.Sub(s.expiresAt) < jwksStaleGrace {
		return key, true
	}
	return nil, false
}

// refresh replaces the set, sharing one request between everyone that asks at
// the same time. It is refused inside the rate limit, and only a successful
// fetch is kept, so a provider that answered with something unusable is asked
// again rather than remembered.
func (s *keySet) refresh(ctx context.Context) error {
	s.mu.Lock()
	current := s.inflight
	if current == nil {
		// A fetch already running is joined whatever the rate limit says: it
		// is the request the limit exists to avoid multiplying, and waiting
		// for it costs the provider nothing.
		if s.keys != nil && s.provider.now().Sub(s.lastAttempt) < jwksMinRefresh {
			s.mu.Unlock()
			// Nothing was fetched, so the key id is unknown as far as this
			// login is concerned. A rotation is picked up by the first attempt
			// after the limit.
			return loginProblem(errors.New("the key id is not in the cached jwks and a refetch is rate limited"))
		}
		current = &fetch{done: make(chan struct{})}
		s.inflight = current
		s.lastAttempt = s.provider.now()
		// The fetch is detached from this request: a caller that gives up must
		// not cancel a request the others are waiting on. It keeps the
		// context's values, so it still logs under the request id that started
		// it.
		detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), jwksFetchTimeout)
		go func() {
			defer cancel()
			s.run(detached, current)
		}()
	}
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		return providerProblem(fmt.Errorf("waiting for the jwks: %w", ctx.Err()))
	case <-current.done:
		return current.err
	}
}

// run performs one shared fetch and installs its result.
func (s *keySet) run(ctx context.Context, f *fetch) {
	f.keys, f.ttl, f.err = s.fetch(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight = nil
	if f.err == nil {
		now := s.provider.now()
		s.keys, s.expiresAt = f.keys, now.Add(f.ttl)
	}
	// done is closed last, under the lock, so nobody it releases can read the
	// set from before this fetch.
	close(f.done)
}

// fetch reads the JWKS named by the discovery document and returns the usable
// signing keys in it, with how long they may be kept.
func (s *keySet) fetch(ctx context.Context) (map[string]crypto.PublicKey, time.Duration, error) {
	p := s.provider
	m, err := p.discover(ctx)
	if err != nil {
		return nil, 0, err
	}

	var set struct {
		Keys []jsonWebKey `json:"keys"`
	}
	header, err := p.getJSON(ctx, m.JWKSURL, &set)
	if err != nil {
		return nil, 0, providerProblem(fmt.Errorf("jwks: %w", err))
	}

	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, jwk := range set.Keys {
		// A key id is what a token selects by, so a key without one cannot be
		// selected. An encryption key is skipped, and so is a type or a curve
		// this cannot verify with: a provider publishes both kinds in one set
		// and the rest of it stays usable.
		//
		// use is optional in RFC 7517, so only a use that is present and is
		// not "sig" disqualifies a key.
		if jwk.Kid == "" || (jwk.Use != "" && jwk.Use != "sig") {
			continue
		}
		key, err := jwk.publicKey()
		if err != nil {
			continue
		}
		// Distinct key ids are a SHOULD, not a MUST. If a set repeats one, the
		// first entry wins rather than the last, so the set a token is checked
		// against does not depend on the order a provider happened to send.
		if _, taken := keys[jwk.Kid]; taken {
			continue
		}
		keys[jwk.Kid] = key
	}
	if len(keys) == 0 {
		return nil, 0, providerProblem(errors.New("jwks: no usable signing key"))
	}
	return keys, cacheTTL(header), nil
}

// cacheTTL reads how long the JWKS may be kept out of the response's
// Cache-Control, clamped into the range this package is willing to accept.
func cacheTTL(header http.Header) time.Duration {
	age, ok := maxAge(header)
	if !ok {
		return jwksDefaultTTL
	}
	return min(max(age, jwksMinTTL), jwksMaxTTL)
}

// maxAge reads the max-age of a Cache-Control header. no-store and no-cache
// are a provider asking not to be cached, which here is the shortest TTL this
// package has rather than a fetch per request.
func maxAge(header http.Header) (time.Duration, bool) {
	for _, value := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(value, ",") {
			directive = strings.ToLower(strings.TrimSpace(directive))
			switch {
			case directive == "no-store", directive == "no-cache":
				return 0, true
			case strings.HasPrefix(directive, "max-age="):
				seconds, err := strconv.Atoi(strings.TrimPrefix(directive, "max-age="))
				if err != nil || seconds < 0 {
					continue
				}
				return time.Duration(seconds) * time.Second, true
			}
		}
	}
	return 0, false
}

// publicKey turns one JWK into an RSA or EC public key, refusing anything this
// package is not prepared to verify a signature with.
func (jwk jsonWebKey) publicKey() (crypto.PublicKey, error) {
	switch jwk.Kty {
	case "RSA":
		n, err := unsignedBigInt(jwk.N)
		if err != nil {
			return nil, fmt.Errorf("rsa modulus: %w", err)
		}
		e, err := unsignedBigInt(jwk.E)
		if err != nil {
			return nil, fmt.Errorf("rsa exponent: %w", err)
		}
		if n.BitLen() < minRSABits {
			return nil, fmt.Errorf("rsa key of %d bit", n.BitLen())
		}
		if !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31 {
			return nil, errors.New("rsa exponent out of range")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		curve, ok := curves[jwk.Crv]
		if !ok {
			return nil, fmt.Errorf("unsupported curve %q", shorten(jwk.Crv, 20))
		}
		size := (curve.Params().BitSize + 7) / 8
		x, err := base64.RawURLEncoding.DecodeString(jwk.X)
		if err != nil || len(x) != size {
			return nil, errors.New("ec x coordinate")
		}
		y, err := base64.RawURLEncoding.DecodeString(jwk.Y)
		if err != nil || len(y) != size {
			return nil, errors.New("ec y coordinate")
		}
		// The uncompressed SEC 1 point. Parsing it is also what proves the
		// point is on the curve, which an invalid curve attack depends on not
		// being checked.
		point := make([]byte, 0, 1+2*size)
		point = append(point, 4)
		point = append(point, x...)
		point = append(point, y...)
		return ecdsa.ParseUncompressedPublicKey(curve, point)
	default:
		return nil, fmt.Errorf("unsupported key type %q", shorten(jwk.Kty, 20))
	}
}

func unsignedBigInt(raw string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("not base64url")
	}
	if len(b) == 0 {
		return nil, errors.New("empty")
	}
	return new(big.Int).SetBytes(b), nil
}
