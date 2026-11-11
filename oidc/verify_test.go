package oidc

import (
	"crypto"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExchangeRefusesABadToken(t *testing.T) {
	for name, tc := range map[string]struct {
		alg    string
		kid    string
		key    crypto.Signer
		adjust func(claims)
		// token replaces the whole compact serialization.
		token string
	}{
		"signed by another key": {alg: "RS256", kid: "key-1", key: otherSigningKey()},
		"alg none": {
			alg: "none", kid: "key-1",
		},
		"alg HS256, which a public jwks would make forgeable": {
			alg: "HS256", kid: "key-1",
		},
		"alg ES256 over the rsa key named by the kid": {
			// The header picks an algorithm the key cannot be used with, which
			// is the other half of algorithm confusion.
			alg: "ES256", kid: "key-1", key: ecKey(),
		},
		"no kid":      {alg: "RS256", kid: ""},
		"unknown kid": {alg: "RS256", kid: "key-does-not-exist"},
		"overlong kid": {
			alg: "RS256", kid: strings.Repeat("k", maxKidLen+1),
		},
		"another issuer": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["iss"] = "https://evil.example.test/realms/apps" },
		},
		"the issuer with a trailing slash": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["iss"] = testIssuer + "/" },
		},
		"no sub": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { delete(c, "sub") },
		},
		"another audience": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["aud"] = "another-client" },
		},
		"a list of other audiences": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["aud"] = []string{"a", "b"} },
		},
		"no audience": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { delete(c, "aud") },
		},
		"an audience that is an object": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["aud"] = map[string]string{"client": testClientID} },
		},
		"another authorized party": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["azp"] = "another-client" },
		},
		"expired beyond the skew": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["exp"] = testNow.Add(-10 * time.Minute).Unix() },
		},
		"issued beyond the skew in the future": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["iat"] = testNow.Add(10 * time.Minute).Unix() },
		},
		"no exp": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { delete(c, "exp") },
		},
		"no iat": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { delete(c, "iat") },
		},
		"another nonce": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["nonce"] = "somebody else's login" },
		},
		"no nonce in the token": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { delete(c, "nonce") },
		},
		"a group claim that is an object": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["groups"] = map[string]string{"a": "b"} },
		},
		"a group claim that is a list of numbers": {
			alg: "RS256", kid: "key-1",
			adjust: func(c claims) { c["groups"] = []int{1, 2} },
		},
		"not a jws at all": {token: "not-a-token"},
		"two parts":        {token: "aaaa.bbbb"},
		"a header that is base64 of nothing json": {token: "!!!.bbbb.cccc"},
		"a payload that is not base64url": {
			token: base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"key-1"}`)) + ".!!!.cccc",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newIDP(t)
			p := provider(t, fake, fixed(testNow), nil)
			login, err := NewLogin("")
			if err != nil {
				t.Fatal(err)
			}

			if tc.token != "" {
				fake.idToken = tc.token
			} else {
				payload := defaultClaims(testNow, login.Nonce)
				if tc.adjust != nil {
					tc.adjust(payload)
				}
				var key crypto.Signer = fake.key
				if tc.key != nil {
					key = tc.key
				}
				fake.idToken = sign(t, key, tc.alg, tc.kid, payload)
			}

			_, err = p.Exchange(t.Context(), "the-code", login)
			if err == nil {
				t.Fatal("the token was accepted")
			}
			if !errors.Is(err, ErrLogin) && !errors.Is(err, ErrProvider) {
				t.Fatalf("error = %v, want one of the two opaque errors", err)
			}
			// No error ever carries the token or a part of it.
			if strings.Contains(err.Error(), fake.idToken) {
				t.Errorf("the token leaked into the error: %q", err)
			}
		})
	}
}

func TestExchangeAcceptsAnECToken(t *testing.T) {
	fake := newIDP(t)
	fake.kid = "ec-key"
	fake.jwksKeys = []jsonWebKey{ecJWK(t, fake.kid, &ecKey().PublicKey)}
	p := provider(t, fake, fixed(testNow), nil)

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	fake.idToken = sign(t, ecKey(), "ES256", fake.kid, defaultClaims(testNow, login.Nonce))

	identity, err := p.Exchange(t.Context(), "the-code", login)
	if err != nil {
		t.Fatalf("an ES256 token was refused: %v", err)
	}
	if identity.Subject != "4f2a-user" {
		t.Errorf("identity = %+v", identity)
	}
}

// TestTheAlgorithmMustMatchTheKey covers the other direction of algorithm
// confusion: the key is the one the key id names, and the header does not get
// to say what it is or how long its signature is.
func TestTheAlgorithmMustMatchTheKey(t *testing.T) {
	for name, tc := range map[string]struct {
		jwk   jsonWebKey
		alg   string
		key   crypto.Signer
		shrug func(string) string
	}{
		"an RS256 header over the EC key the kid names": {
			jwk: ecJWK(t, "the-key", &ecKey().PublicKey),
			alg: "RS256", key: signingKey(),
		},
		"an ES256 header over a P-384 key": {
			jwk: ecJWK(t, "the-key", &ecKey384().PublicKey),
			alg: "ES256", key: ecKey384(),
		},
		"an ES256 signature of the wrong length": {
			jwk: ecJWK(t, "the-key", &ecKey().PublicKey),
			alg: "ES256", key: ecKey(),
			shrug: func(token string) string {
				parts := strings.Split(token, ".")
				signature, err := base64.RawURLEncoding.DecodeString(parts[2])
				if err != nil {
					t.Fatal(err)
				}
				return parts[0] + "." + parts[1] + "." +
					base64.RawURLEncoding.EncodeToString(signature[:len(signature)-2])
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newIDP(t)
			fake.kid = "the-key"
			fake.jwksKeys = []jsonWebKey{tc.jwk}
			p := provider(t, fake, fixed(testNow), nil)

			login, err := NewLogin("")
			if err != nil {
				t.Fatal(err)
			}
			token := sign(t, tc.key, tc.alg, fake.kid, defaultClaims(testNow, login.Nonce))
			if tc.shrug != nil {
				token = tc.shrug(token)
			}
			fake.idToken = token

			if _, err := p.Exchange(t.Context(), "the-code", login); !errors.Is(err, ErrLogin) {
				t.Errorf("error = %v, want the token to be refused", err)
			}
		})
	}
}

func TestExchangeWithinTheClockSkew(t *testing.T) {
	for name, adjust := range map[string]func(claims){
		"expired, inside the allowance": func(c claims) {
			c["exp"] = testNow.Add(-time.Minute).Unix()
		},
		"issued in the future, inside the allowance": func(c claims) {
			c["iat"] = testNow.Add(time.Minute).Unix()
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newIDP(t)
			p := provider(t, fake, fixed(testNow), nil)
			login, err := NewLogin("")
			if err != nil {
				t.Fatal(err)
			}

			payload := defaultClaims(testNow, login.Nonce)
			adjust(payload)
			fake.idToken = sign(t, fake.key, "RS256", fake.kid, payload)

			if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
				t.Errorf("a token inside the %s clock skew was refused: %v", ClockSkew, err)
			}
		})
	}
}

func TestExchangeGroupsVariants(t *testing.T) {
	for name, tc := range map[string]struct {
		claim any
		// absent deletes the claim instead of setting it.
		absent bool
		want   []string
	}{
		"absent":                  {absent: true},
		"null":                    {claim: nil},
		"one string":              {claim: "/catalogue-owners", want: []string{"catalogue-owners"}},
		"one string, no slash":    {claim: "admins", want: []string{"admins"}},
		"a list without slashes":  {claim: []string{"admins"}, want: []string{"admins"}},
		"an empty list":           {claim: []string{}},
		"only a slash":            {claim: []string{"/"}},
		"an empty string":         {claim: ""},
		"a nested path":           {claim: []string{"/teams/platform"}, want: []string{"teams/platform"}},
		"duplicates are kept":     {claim: []string{"a", "a"}, want: []string{"a", "a"}},
		"a mix of good and empty": {claim: []string{"/", "admins", ""}, want: []string{"admins"}},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newIDP(t)
			p := provider(t, fake, fixed(testNow), nil)
			login, err := NewLogin("")
			if err != nil {
				t.Fatal(err)
			}

			payload := defaultClaims(testNow, login.Nonce)
			if tc.absent {
				delete(payload, "groups")
			} else {
				payload["groups"] = tc.claim
			}
			fake.idToken = sign(t, fake.key, "RS256", fake.kid, payload)

			identity, err := p.Exchange(t.Context(), "the-code", login)
			if err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if len(identity.Groups) != len(tc.want) {
				t.Fatalf("groups = %q, want %q", identity.Groups, tc.want)
			}
			for i, want := range tc.want {
				if identity.Groups[i] != want {
					t.Errorf("group %d = %q, want %q", i, identity.Groups[i], want)
				}
			}
		})
	}
}

func TestExchangeWithAnotherGroupsClaim(t *testing.T) {
	for name, tc := range map[string]struct {
		configured string
		payload    func(claims)
		want       []string
		wantErr    bool
	}{
		"a flat claim": {
			configured: "roles",
			payload:    func(c claims) { c["roles"] = []string{"editor"} },
			want:       []string{"editor"},
		},
		"a nested claim": {
			configured: "realm_access.roles",
			payload: func(c claims) {
				c["realm_access"] = map[string]any{"roles": []string{"editor", "viewer"}}
			},
			want: []string{"editor", "viewer"},
		},
		"a nested claim whose parent is missing": {
			configured: "realm_access.roles",
			payload:    func(claims) {},
		},
		"a nested claim whose parent is not an object": {
			// A claim path that runs into something that cannot be walked is a
			// token shaped differently from the one configured for, not an
			// account without groups.
			configured: "realm_access.roles",
			payload:    func(c claims) { c["realm_access"] = []string{"editor"} },
			wantErr:    true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newIDP(t)
			p := provider(t, fake, fixed(testNow), func(c *Config) { c.GroupsClaim = tc.configured })
			login, err := NewLogin("")
			if err != nil {
				t.Fatal(err)
			}

			payload := defaultClaims(testNow, login.Nonce)
			delete(payload, "groups")
			tc.payload(payload)
			fake.idToken = sign(t, fake.key, "RS256", fake.kid, payload)

			identity, err := p.Exchange(t.Context(), "the-code", login)
			if tc.wantErr {
				if !errors.Is(err, ErrLogin) {
					t.Fatalf("error = %v, want ErrLogin", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if len(identity.Groups) != len(tc.want) {
				t.Fatalf("groups = %q, want %q", identity.Groups, tc.want)
			}
			for i, want := range tc.want {
				if identity.Groups[i] != want {
					t.Errorf("group %d = %q, want %q", i, identity.Groups[i], want)
				}
			}
		})
	}
}

func TestStringOrList(t *testing.T) {
	for name, tc := range map[string]struct {
		raw     string
		want    []string
		wantErr bool
	}{
		"a string":          {raw: `"one"`, want: []string{"one"}},
		"a list":            {raw: `["one","two"]`, want: []string{"one", "two"}},
		"an empty list":     {raw: `[]`, want: []string{}},
		"null":              {raw: `null`},
		"nothing":           {raw: ``},
		"a number":          {raw: `7`, wantErr: true},
		"a list of numbers": {raw: `[1,2]`, wantErr: true},
		"an object":         {raw: `{"a":"b"}`, wantErr: true},
		"a mixed list":      {raw: `["one",2]`, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := stringOrList(json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("%s was accepted as %q", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("stringOrList(%s): %v", tc.raw, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
