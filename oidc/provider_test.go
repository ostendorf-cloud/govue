package oidc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const (
	testIssuer   = "https://accounts.example.test/realms/apps"
	testClientID = "catalogue"
	testRedirect = "https://catalogue.example.test/auth/callback"

	// plainJWKSPath is where the redirect test's downgraded JWKS lives.
	plainJWKSPath = "/realms/apps/protocol/openid-connect/plain-certs"
)

// testNow is the fixed clock every test starts from.
var testNow = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

// signingKey and ecKey are generated once per run: generating an RSA key costs
// real time, and every test that needs one needs the same one.
var (
	signingKey = sync.OnceValue(func() *rsa.PrivateKey {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		return key
	})
	otherSigningKey = sync.OnceValue(func() *rsa.PrivateKey {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		return key
	})
	ecKey = sync.OnceValue(func() *ecdsa.PrivateKey {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		return key
	})
	ecKey384 = sync.OnceValue(func() *ecdsa.PrivateKey {
		key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			panic(err)
		}
		return key
	})
)

// idp is a fake identity provider answered in process, so the tests need no
// socket and no network.
type idp struct {
	t   *testing.T
	key *rsa.PrivateKey
	kid string

	// Knobs the tests turn.
	issuerInDocument string
	redirectJWKSTo   string
	tokenURL         string
	endSessionURL    string
	jwksCacheControl string
	jwksStatus       int
	tokenStatus      int
	tokenBody        string
	idToken          string
	jwksKeys         []jsonWebKey

	mu           sync.Mutex
	jwksRequests int
	tokenForms   []map[string][]string
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	p := &idp{
		t:                t,
		key:              signingKey(),
		kid:              "key-1",
		issuerInDocument: testIssuer,
		tokenURL:         testIssuer + "/protocol/openid-connect/token",
		endSessionURL:    testIssuer + "/protocol/openid-connect/logout",
		jwksStatus:       http.StatusOK,
		tokenStatus:      http.StatusOK,
	}
	p.jwksKeys = []jsonWebKey{rsaJWK(p.kid, &p.key.PublicKey)}
	return p
}

// rsaJWK renders an RSA public key the way a provider publishes it.
func rsaJWK(kid string, key *rsa.PublicKey) jsonWebKey {
	return jsonWebKey{
		Kid: kid,
		Kty: "RSA",
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

// ecJWK renders an EC public key the way a provider publishes it, out of the
// uncompressed SEC 1 point: 0x04 followed by the two fixed width coordinates.
func ecJWK(t *testing.T, kid string, key *ecdsa.PublicKey) jsonWebKey {
	t.Helper()
	point, err := key.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	size := (len(point) - 1) / 2
	return jsonWebKey{
		Kid: kid,
		Kty: "EC",
		Use: "sig",
		Crv: key.Curve.Params().Name,
		X:   base64.RawURLEncoding.EncodeToString(point[1 : 1+size]),
		Y:   base64.RawURLEncoding.EncodeToString(point[1+size:]),
	}
}

// handler answers the endpoints a login needs.
func (p *idp) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /realms/apps/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		p.writeJSON(w, metadata{
			Issuer:        p.issuerInDocument,
			AuthURL:       testIssuer + "/protocol/openid-connect/auth",
			TokenURL:      p.tokenURL,
			JWKSURL:       testIssuer + "/protocol/openid-connect/certs",
			EndSessionURL: p.endSessionURL,
		})
	})
	mux.HandleFunc("GET /realms/apps/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.jwksRequests++
		p.mu.Unlock()

		if p.redirectJWKSTo != "" {
			http.Redirect(w, r, p.redirectJWKSTo, http.StatusFound)
			return
		}
		p.writeJWKS(w)
	})
	// Where a redirect test sends the request. It serves the same keys, so a
	// client that follows the downgrade gets a working login and the test that
	// expects a refusal fails.
	mux.HandleFunc("GET "+plainJWKSPath, func(w http.ResponseWriter, _ *http.Request) {
		p.writeJWKS(w)
	})
	mux.HandleFunc("POST /realms/apps/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			p.t.Errorf("the token request is not a form: %v", err)
		}
		p.mu.Lock()
		p.tokenForms = append(p.tokenForms, r.Form)
		p.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(p.tokenStatus)
		if p.tokenBody != "" {
			_, _ = w.Write([]byte(p.tokenBody))
			return
		}
		_, _ = w.Write([]byte(`{"id_token":"` + p.idToken + `"}`))
	})
	return mux
}

func (p *idp) writeJWKS(w http.ResponseWriter) {
	if p.jwksCacheControl != "" {
		w.Header().Set("Cache-Control", p.jwksCacheControl)
	}
	if p.jwksStatus != http.StatusOK {
		w.WriteHeader(p.jwksStatus)
		return
	}
	p.writeJSON(w, map[string]any{"keys": p.jwksKeys})
}

func (p *idp) jwksCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jwksRequests
}

func (p *idp) writeJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		p.t.Errorf("the fake provider could not marshal its answer: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// client routes every request into the handler, with no socket involved.
func (p *idp) client() *http.Client {
	return &http.Client{Transport: handlerTransport{handler: p.handler()}}
}

type handlerTransport struct {
	handler http.Handler
}

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.handler.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

// clock is a time source a test moves by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(at time.Time) *clock { return &clock{now: at} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// claims is what a test puts into an ID token.
type claims map[string]any

// defaultClaims is a token that passes every check.
func defaultClaims(now time.Time, nonce string) claims {
	return claims{
		"iss":                testIssuer,
		"sub":                "4f2a-user",
		"aud":                testClientID,
		"azp":                testClientID,
		"exp":                now.Add(5 * time.Minute).Unix(),
		"iat":                now.Unix(),
		"nonce":              nonce,
		"preferred_username": "simon",
		"name":               "Simon Ostendorf",
		"email":              "simon@example.test",
		"groups":             []string{"/catalogue-owners", "/admins"},
	}
}

// sign builds a JWS in compact serialization from the given header fields and
// claims. key is an *rsa.PrivateKey or an *ecdsa.PrivateKey.
func sign(t *testing.T, key crypto.Signer, alg, kid string, payload claims) string {
	t.Helper()

	header := map[string]any{"alg": alg, "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	encode := func(v any) string {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(body)
	}
	signing := encode(header) + "." + encode(payload)

	switch alg {
	case "none":
		return signing + "."
	case "HS256":
		// A token signed with the RSA modulus as an HMAC key, which is the
		// algorithm confusion attack: the public key is public.
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			t.Fatalf("HS256 test token needs an RSA key, got %T", key)
		}
		return signing + "." + base64.RawURLEncoding.EncodeToString(hmacSHA256(rsaKey.N.Bytes(), signing))
	case "RS256":
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			t.Fatalf("RS256 test token needs an RSA key, got %T", key)
		}
		digest := sha256sum(signing)
		signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest)
		if err != nil {
			t.Fatal(err)
		}
		return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
	case "ES256":
		ecdsaKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			t.Fatalf("ES256 test token needs an EC key, got %T", key)
		}
		r, s, err := ecdsa.Sign(rand.Reader, ecdsaKey, sha256sum(signing))
		if err != nil {
			t.Fatal(err)
		}
		size := (ecdsaKey.Curve.Params().BitSize + 7) / 8
		signature := make([]byte, 2*size)
		r.FillBytes(signature[:size])
		s.FillBytes(signature[size:])
		return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
	default:
		t.Fatalf("the test signer does not know %q", alg)
		return ""
	}
}

// provider builds a provider against the fake, with a fixed clock.
func provider(t *testing.T, p *idp, now func() time.Time, adjust func(*Config)) *Provider {
	t.Helper()

	cfg := Config{
		Issuer:       testIssuer,
		ClientID:     testClientID,
		ClientSecret: "a client secret",
		RedirectURL:  testRedirect,
		Scopes:       []string{"openid", "profile", "email", "groups"},
		HTTPClient:   p.client(),
		Now:          now,
	}
	if adjust != nil {
		adjust(&cfg)
	}
	created, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return created
}

// fixed is the clock most tests use: one instant that does not move.
func fixed(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

func sha256sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func hmacSHA256(key []byte, s string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(s))
	return mac.Sum(nil)
}
