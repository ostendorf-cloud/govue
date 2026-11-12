package oidc

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// login builds a login and the token the fake will answer with, signed by the
// fake's current key.
func loginAgainst(t *testing.T, fake *idp, at time.Time) Login {
	t.Helper()
	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	fake.idToken = sign(t, fake.key, "RS256", fake.kid, defaultClaims(at, login.Nonce))
	return login
}

func TestKeysAreFetchedOnceAndThenCached(t *testing.T) {
	fake := newIDP(t)
	p := provider(t, fake, fixed(testNow), nil)

	for range 3 {
		login := loginAgainst(t, fake, testNow)
		if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
			t.Fatalf("Exchange: %v", err)
		}
	}
	if got := fake.jwksCount(); got != 1 {
		t.Errorf("%d jwks requests, want 1: the set is cached between logins", got)
	}
}

func TestKeyRotationIsPickedUpAfterTheRateLimit(t *testing.T) {
	fake := newIDP(t)
	clock := newClock(testNow)
	p := provider(t, fake, clock.Now, nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if got := fake.jwksCount(); got != 1 {
		t.Fatalf("%d jwks requests, want 1", got)
	}

	// The provider rotates. The token now names a key id the cached set does
	// not hold, and inside the rate limit it is refused without a fetch: that
	// is what stops a made up key id from being one request to the provider
	// per login attempt.
	fake.kid = "key-2"
	fake.jwksKeys = []jsonWebKey{rsaJWK("key-2", &fake.key.PublicKey)}
	rotated := loginAgainst(t, fake, testNow)

	if _, err := p.Exchange(t.Context(), "the-code", rotated); !errors.Is(err, ErrLogin) {
		t.Errorf("error = %v, want the unknown key id to be refused from the cache", err)
	}
	if got := fake.jwksCount(); got != 1 {
		t.Errorf("%d jwks requests, want the refetch to be rate limited", got)
	}

	clock.Add(2 * jwksMinRefresh)
	rotated = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", rotated); err != nil {
		t.Errorf("the rotated key was not picked up: %v", err)
	}
	if got := fake.jwksCount(); got != 2 {
		t.Errorf("%d jwks requests, want 2", got)
	}
}

func TestKeysAreRefetchedWhenTheTTLRunsOut(t *testing.T) {
	fake := newIDP(t)
	clock := newClock(testNow)
	p := provider(t, fake, clock.Now, nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	// Inside the TTL nothing is fetched again, even though the set is used.
	clock.Add(jwksDefaultTTL / 2)
	login = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if got := fake.jwksCount(); got != 1 {
		t.Fatalf("%d jwks requests, want 1 inside the ttl", got)
	}

	// Past it, the set is replaced before it is used. A key the provider has
	// withdrawn stops being accepted at this point and not at process exit.
	clock.Add(jwksDefaultTTL)
	login = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if got := fake.jwksCount(); got != 2 {
		t.Errorf("%d jwks requests, want the expired set to be replaced", got)
	}
}

func TestAWithdrawnKeyStopsBeingAccepted(t *testing.T) {
	fake := newIDP(t)
	clock := newClock(testNow)
	p := provider(t, fake, clock.Now, nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	// The provider withdraws the key the token is signed with and publishes
	// another one. The token itself stays valid for a while, so only the set
	// expiring ends it.
	withdrawn := fake.kid
	fake.jwksKeys = []jsonWebKey{rsaJWK("key-2", &otherSigningKey().PublicKey)}
	clock.Add(jwksDefaultTTL + time.Minute)

	stale, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	fake.idToken = sign(t, fake.key, "RS256", withdrawn, defaultClaims(clock.Now(), stale.Nonce))
	if _, err := p.Exchange(t.Context(), "the-code", stale); !errors.Is(err, ErrLogin) {
		t.Errorf("error = %v, want a token signed by a withdrawn key to be refused", err)
	}
}

func TestTheProvidersCacheControlDecidesTheTTL(t *testing.T) {
	for name, tc := range map[string]struct {
		header string
		want   time.Duration
	}{
		"no header":                 {header: "", want: jwksDefaultTTL},
		"a max-age":                 {header: "max-age=300", want: 5 * time.Minute},
		"a max-age among others":    {header: "public, max-age=600, must-revalidate", want: 10 * time.Minute},
		"a max-age below the floor": {header: "max-age=1", want: jwksMinTTL},
		"a max-age over the ceiling": {
			header: "max-age=2592000", want: jwksMaxTTL,
		},
		"no-store":           {header: "no-store", want: jwksMinTTL},
		"no-cache":           {header: "no-cache", want: jwksMinTTL},
		"an unreadable one":  {header: "max-age=soon", want: jwksDefaultTTL},
		"a negative max-age": {header: "max-age=-5", want: jwksDefaultTTL},
	} {
		t.Run(name, func(t *testing.T) {
			header := http.Header{}
			if tc.header != "" {
				header.Set("Cache-Control", tc.header)
			}
			if got := cacheTTL(header); got != tc.want {
				t.Errorf("cacheTTL(%q) = %s, want %s", tc.header, got, tc.want)
			}
		})
	}
}

func TestTheProvidersCacheControlIsUsedForRealRequests(t *testing.T) {
	fake := newIDP(t)
	fake.jwksCacheControl = "max-age=120"
	clock := newClock(testNow)
	p := provider(t, fake, clock.Now, nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	// Past the provider's own max-age and well inside this package's default,
	// so only the header can explain a second fetch.
	clock.Add(3 * time.Minute)
	login = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if got := fake.jwksCount(); got != 2 {
		t.Errorf("%d jwks requests, want the provider's max-age to have expired the set", got)
	}
}

func TestAnUnreachableProviderFallsBackToTheCachedKeys(t *testing.T) {
	fake := newIDP(t)
	clock := newClock(testNow)
	p := provider(t, fake, clock.Now, nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	// The JWKS endpoint breaks and the set expires. Refusing every login over
	// a blip is worse than using a key the provider published minutes ago.
	fake.jwksStatus = http.StatusBadGateway
	clock.Add(jwksDefaultTTL + time.Minute)
	login = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Errorf("a login was refused while the provider was briefly unreachable: %v", err)
	}

	// An outage that outlasts the grace period ends the logins rather than
	// trusting keys nobody has confirmed since.
	clock.Add(jwksStaleGrace)
	login = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", login); !errors.Is(err, ErrProvider) {
		t.Errorf("error = %v, want the stale set to have run out", err)
	}
}

func TestTheStaleGraceRunsFromTheExpiryNotFromTheFetch(t *testing.T) {
	// A provider TTL longer than the grace itself. Measured from the fetch,
	// the whole window is spent before the set goes stale and the fallback
	// never applies.
	const ttl = 2 * time.Hour

	fake := newIDP(t)
	fake.jwksCacheControl = "max-age=7200"
	clock := newClock(testNow)
	p := provider(t, fake, clock.Now, nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	fake.jwksStatus = http.StatusBadGateway
	clock.Add(ttl + time.Minute)
	login = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Errorf("a login was refused one minute past a %s TTL: %v", ttl, err)
	}

	// Past the TTL and past the grace that follows it.
	clock.Add(jwksStaleGrace)
	login = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", login); !errors.Is(err, ErrProvider) {
		t.Errorf("error = %v, want the stale set to have run out", err)
	}
}

func TestARedirectOntoPlainHTTPIsRefused(t *testing.T) {
	// The endpoints are checked once, when the discovery document is read. A
	// redirect at request time is the way around that check, so the client
	// refuses one that leaves https.
	fake := newIDP(t)
	fake.redirectJWKSTo = "http://accounts.example.test" + plainJWKSPath
	p := provider(t, fake, fixed(testNow), nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); !errors.Is(err, ErrProvider) {
		t.Errorf("error = %v, want the downgrade to plain HTTP to be refused", err)
	}
}

func TestABrokenJWKSIsNotCached(t *testing.T) {
	fake := newIDP(t)
	fake.jwksStatus = http.StatusInternalServerError
	clock := newClock(testNow)
	p := provider(t, fake, clock.Now, nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); !errors.Is(err, ErrProvider) {
		t.Fatalf("error = %v, want ErrProvider", err)
	}

	fake.jwksStatus = http.StatusOK
	clock.Add(2 * jwksMinRefresh)
	login = loginAgainst(t, fake, clock.Now())
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Errorf("the repaired jwks was not picked up: %v", err)
	}
}

func TestASetWithNoUsableKeyIsRefused(t *testing.T) {
	fake := newIDP(t)
	fake.jwksKeys = []jsonWebKey{
		{Kid: "", Kty: "RSA", N: "AQAB", E: "AQAB"},                       // no key id
		{Kid: "enc", Kty: "RSA", Use: "enc", N: "AQAB", E: "AQAB"},        // not a signing key
		{Kid: "oct", Kty: "oct"},                                          // a type this cannot verify with
		{Kid: "small", Kty: "RSA", Use: "sig", N: "AQAB", E: "AQAB"},      // an RSA key far under the minimum
		{Kid: "curve", Kty: "EC", Use: "sig", Crv: "P-521", X: "", Y: ""}, // a curve outside the allowed algorithms
	}
	p := provider(t, fake, fixed(testNow), nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); !errors.Is(err, ErrProvider) {
		t.Errorf("error = %v, want a set with no usable signing key to be refused", err)
	}
}

func TestAnUnusableKeyDoesNotDiscardTheRest(t *testing.T) {
	fake := newIDP(t)
	fake.jwksKeys = []jsonWebKey{
		{Kid: "enc", Kty: "RSA", Use: "enc", N: "AQAB", E: "AQAB"},
		{Kid: "small", Kty: "RSA", Use: "sig", N: "AQAB", E: "AQAB"},
		rsaJWK(fake.kid, &fake.key.PublicKey),
	}
	p := provider(t, fake, fixed(testNow), nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Errorf("a set holding one unusable key was discarded whole: %v", err)
	}
}

func TestAKeyWithoutUseIsAccepted(t *testing.T) {
	// use is optional in RFC 7517, so a set that omits it is not a set
	// without signing keys.
	fake := newIDP(t)
	key := rsaJWK(fake.kid, &fake.key.PublicKey)
	key.Use = ""
	fake.jwksKeys = []jsonWebKey{key}
	p := provider(t, fake, fixed(testNow), nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Errorf("a key without a use was skipped: %v", err)
	}
}

func TestARepeatedKeyIDKeepsTheFirstEntry(t *testing.T) {
	// Distinct key ids are a SHOULD in RFC 7517. If a provider repeats one,
	// which key a token is checked against must not depend on the order the
	// set happened to arrive in.
	fake := newIDP(t)
	fake.jwksKeys = []jsonWebKey{
		rsaJWK(fake.kid, &fake.key.PublicKey),
		rsaJWK(fake.kid, &otherSigningKey().PublicKey),
	}
	p := provider(t, fake, fixed(testNow), nil)

	login := loginAgainst(t, fake, testNow)
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Errorf("the first entry for the key id was not the one used: %v", err)
	}
}

func TestConcurrentLoginsShareOneFetch(t *testing.T) {
	fake := newIDP(t)
	p := provider(t, fake, fixed(testNow), nil)

	// Every login carries the same token, so the only thing being measured is
	// how many times the key set is fetched for them.
	login := loginAgainst(t, fake, testNow)

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = p.Exchange(t.Context(), "the-code", login)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("login %d: %v", i, err)
		}
	}
	if got := fake.jwksCount(); got != 1 {
		t.Errorf("%d jwks requests for 8 concurrent logins, want 1", got)
	}
}
