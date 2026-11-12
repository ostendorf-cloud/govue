// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/web-kit/logging"
)

func TestNewRefusesABrokenConfig(t *testing.T) {
	base := Config{
		Issuer:       testIssuer,
		ClientID:     testClientID,
		ClientSecret: "a client secret",
		RedirectURL:  testRedirect,
	}
	if _, err := New(base); err != nil {
		t.Fatalf("a complete config was refused: %v", err)
	}

	for name, adjust := range map[string]func(*Config){
		"no issuer":             func(c *Config) { c.Issuer = "" },
		"http issuer":           func(c *Config) { c.Issuer = "http://accounts.example.test/realms/apps" },
		"issuer without a host": func(c *Config) { c.Issuer = "https:///realms/apps" },
		"issuer that is not a url": func(c *Config) {
			c.Issuer = "https://accounts.example.test/realms/\x7f"
		},
		"no client id":     func(c *Config) { c.ClientID = "" },
		"no client secret": func(c *Config) { c.ClientSecret = "" },
		"no redirect":      func(c *Config) { c.RedirectURL = "" },
		"http redirect":    func(c *Config) { c.RedirectURL = "http://catalogue.example.test/auth/callback" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			adjust(&cfg)
			if _, err := New(cfg); err == nil {
				t.Error("the config was accepted")
			}
		})
	}
}

func TestNewNamesEveryBrokenFieldAtOnce(t *testing.T) {
	_, err := New(Config{Issuer: "", ClientID: "", ClientSecret: "", RedirectURL: ""})
	if err == nil {
		t.Fatal("an empty config was accepted")
	}
	for _, want := range []string{"issuer", "client id", "client secret", "redirect url"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q, so a deployment fixes one field per restart", err, want)
		}
	}
}

func TestNewAllowsALoopbackProviderOverPlainHTTP(t *testing.T) {
	// A provider run locally in development is reached over plain HTTP, and
	// nothing leaves the machine.
	for name, cfg := range map[string]Config{
		"localhost": {
			Issuer: "http://localhost:8080/realms/apps", ClientID: testClientID,
			ClientSecret: "a client secret", RedirectURL: "http://localhost:3000/auth/callback",
		},
		"ipv4 loopback": {
			Issuer: "http://127.0.0.1:8080/realms/apps", ClientID: testClientID,
			ClientSecret: "a client secret", RedirectURL: "http://127.0.0.1:3000/auth/callback",
		},
		"ipv6 loopback": {
			Issuer: "http://[::1]:8080/realms/apps", ClientID: testClientID,
			ClientSecret: "a client secret", RedirectURL: "http://[::1]:3000/auth/callback",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err != nil {
				t.Errorf("a loopback provider was refused: %v", err)
			}
		})
	}
}

func TestNewTrimsTheIssuer(t *testing.T) {
	p, err := New(Config{
		Issuer:       testIssuer + "/",
		ClientID:     testClientID,
		ClientSecret: "a client secret",
		RedirectURL:  testRedirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Issuer() != testIssuer {
		t.Errorf("Issuer() = %q, want the trailing slash gone", p.Issuer())
	}
}

func TestAuthCodeURL(t *testing.T) {
	fake := newIDP(t)
	p := provider(t, fake, fixed(testNow), func(c *Config) {
		c.AuthParams = url.Values{"kc_idp_hint": {"corporate"}}
	})

	login, err := NewLogin("/lists/7")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.AuthCodeURL(t.Context(), login)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}

	q := parsed.Query()
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             testClientID,
		"redirect_uri":          testRedirect,
		"scope":                 "openid profile email groups",
		"state":                 login.State,
		"nonce":                 login.Nonce,
		"code_challenge":        CodeChallenge(login.Verifier),
		"code_challenge_method": "S256",
		"kc_idp_hint":           "corporate",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// The verifier never leaves the server. Only its challenge does.
	if strings.Contains(raw, login.Verifier) {
		t.Error("the code verifier is in the authorization URL")
	}
}

func TestAuthCodeURLDefaultsToTheStandardScopes(t *testing.T) {
	fake := newIDP(t)
	p := provider(t, fake, fixed(testNow), func(c *Config) { c.Scopes = nil })

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.AuthCodeURL(t.Context(), login)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.Query().Get("scope"), strings.Join(DefaultScopes, " "); got != want {
		t.Errorf("scope = %q, want %q", got, want)
	}
}

func TestAuthCodeURLKeepsItsOwnParameters(t *testing.T) {
	fake := newIDP(t)
	// A configured extra must not be able to displace the redirect this
	// application registered, which is what an authorization URL is checked
	// against.
	p := provider(t, fake, fixed(testNow), func(c *Config) {
		c.AuthParams = url.Values{
			"redirect_uri":          {"https://evil.example.test/callback"},
			"code_challenge_method": {"plain"},
			"scope":                 {"openid"},
		}
	})

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.AuthCodeURL(t.Context(), login)
	if err != nil {
		t.Fatal(err)
	}
	q, err := url.ParseQuery(mustURL(t, raw).RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Get("redirect_uri"); got != testRedirect {
		t.Errorf("redirect_uri = %q, want the configured callback", got)
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := q.Get("scope"); got != "openid profile email groups" {
		t.Errorf("scope = %q, want the configured scopes", got)
	}
}

func TestAuthCodeURLRefusesAnIncompleteLogin(t *testing.T) {
	p := provider(t, newIDP(t), fixed(testNow), nil)
	complete, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}

	for name, login := range map[string]Login{
		"no state":        {Nonce: complete.Nonce, Verifier: complete.Verifier},
		"no nonce":        {State: complete.State, Verifier: complete.Verifier},
		"short verifier":  {State: complete.State, Nonce: complete.Nonce, Verifier: "too-short"},
		"long verifier":   {State: complete.State, Nonce: complete.Nonce, Verifier: strings.Repeat("v", maxVerifierLen+1)},
		"nothing at all":  {},
		"only a verifier": {Verifier: complete.Verifier},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := p.AuthCodeURL(t.Context(), login)
			if !errors.Is(err, ErrLogin) {
				t.Errorf("error = %v, want ErrLogin", err)
			}
		})
	}
}

func TestExchange(t *testing.T) {
	fake := newIDP(t)
	p := provider(t, fake, fixed(testNow), nil)

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	fake.idToken = sign(t, fake.key, "RS256", fake.kid, defaultClaims(testNow, login.Nonce))

	identity, err := p.Exchange(t.Context(), "the-code", login)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if identity.Subject != "4f2a-user" || identity.Username != "simon" ||
		identity.Name != "Simon Ostendorf" || identity.Email != "simon@example.test" {
		t.Errorf("identity = %+v", identity)
	}
	// A group path loses its leading slash, so an application compares against
	// the group name it configured.
	if len(identity.Groups) != 2 || identity.Groups[0] != "catalogue-owners" || identity.Groups[1] != "admins" {
		t.Errorf("groups = %q", identity.Groups)
	}
	if !identity.In("admins") || identity.In("/admins") {
		t.Errorf("In does not match the normalised groups: %q", identity.Groups)
	}
	if identity.IDToken != fake.idToken {
		t.Error("the id token was not kept for the logout hint")
	}
	if !identity.ExpiresAt.Equal(testNow.Add(5 * time.Minute).UTC()) {
		t.Errorf("ExpiresAt = %s", identity.ExpiresAt)
	}
}

func TestExchangeSendsThePKCEVerifierAndTheClientSecret(t *testing.T) {
	fake := newIDP(t)
	p := provider(t, fake, fixed(testNow), nil)

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	fake.idToken = sign(t, fake.key, "RS256", fake.kid, defaultClaims(testNow, login.Nonce))
	if _, err := p.Exchange(t.Context(), "the-code", login); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.tokenForms) != 1 {
		t.Fatalf("%d token requests, want 1", len(fake.tokenForms))
	}
	form := url.Values(fake.tokenForms[0])
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "the-code",
		"redirect_uri":  testRedirect,
		"code_verifier": login.Verifier,
	} {
		if got := form.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// The secret authenticates the request in the Authorization header, not in
	// the form, where it would land in an access log on the way.
	if form.Get("client_secret") != "" {
		t.Error("the client secret was sent in the form body")
	}
}

func TestExchangeRefusesAnIncompleteLogin(t *testing.T) {
	fake := newIDP(t)
	p := provider(t, fake, fixed(testNow), nil)
	complete, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		code  string
		login Login
	}{
		"no code":        {"", complete},
		"no nonce":       {"the-code", Login{State: complete.State, Verifier: complete.Verifier}},
		"short verifier": {"the-code", Login{State: complete.State, Nonce: complete.Nonce, Verifier: "short"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := p.Exchange(t.Context(), tc.code, tc.login)
			if !errors.Is(err, ErrLogin) {
				t.Errorf("error = %v, want ErrLogin", err)
			}
		})
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.tokenForms) != 0 {
		t.Errorf("%d token requests, want none: an incomplete login is refused before the provider is asked", len(fake.tokenForms))
	}
}

func TestExchangeWithAProviderError(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"the grant was refused": {
			status: http.StatusBadRequest,
			body:   `{"error":"invalid_grant","error_description":"Code not valid"}`,
			want:   ErrLogin,
		},
		"the client secret is wrong": {
			status: http.StatusUnauthorized,
			body:   `{"error":"invalid_client"}`,
			want:   ErrLogin,
		},
		"the provider is broken": {
			status: http.StatusInternalServerError,
			body:   `{}`,
			want:   ErrProvider,
		},
		"the answer is not json": {
			status: http.StatusOK,
			body:   `<html>a proxy error page</html>`,
			want:   ErrProvider,
		},
		"no id token": {
			status: http.StatusOK,
			body:   `{"access_token":"only-this"}`,
			want:   ErrProvider,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newIDP(t)
			fake.tokenStatus = tc.status
			fake.tokenBody = tc.body
			p := provider(t, fake, fixed(testNow), nil)

			login, err := NewLogin("")
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Exchange(t.Context(), "the-code", login)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestExchangeLogsTheCauseAndTellsTheCallerNothing(t *testing.T) {
	fake := newIDP(t)
	fake.tokenStatus = http.StatusBadRequest
	fake.tokenBody = `{"error":"invalid_client","error_description":"Invalid client credentials"}`
	p := provider(t, fake, fixed(testNow), nil)

	var buf bytes.Buffer
	ctx := logging.WithRequestID(
		logging.NewContext(t.Context(), slog.New(slog.NewJSONHandler(&buf, nil))),
		"req-42",
	)

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exchange(ctx, "the-code", login)
	if !errors.Is(err, ErrLogin) {
		t.Fatalf("error = %v, want ErrLogin", err)
	}
	// What the caller may put in a response says nothing about the cause.
	if strings.Contains(err.Error(), "invalid_client") || strings.Contains(err.Error(), "credentials") {
		t.Errorf("the cause reached the caller: %q", err)
	}

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("the failure was not logged as one JSON line: %v, got %q", err, buf.String())
	}
	if line["request_id"] != "req-42" {
		t.Errorf("request_id = %v, want the one from the context", line["request_id"])
	}
	logged, _ := line["error"].(string)
	if !strings.Contains(logged, "invalid_client") || !strings.Contains(logged, "Invalid client credentials") {
		t.Errorf("the log line does not hold the cause: %q", logged)
	}
}

func TestFailureWithoutALoggerIsNotAPanic(t *testing.T) {
	fake := newIDP(t)
	fake.tokenStatus = http.StatusInternalServerError
	fake.tokenBody = `{}`
	p := provider(t, fake, fixed(testNow), nil)

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	// context.Background carries no logger and no request id: a call made
	// outside a request still has to come back with the opaque error.
	if _, err := p.Exchange(context.Background(), "the-code", login); !errors.Is(err, ErrProvider) {
		t.Errorf("error = %v, want ErrProvider", err)
	}
}

func TestDiscoveryRefusesAnotherIssuer(t *testing.T) {
	fake := newIDP(t)
	fake.issuerInDocument = "https://evil.example.test/realms/apps"
	p := provider(t, fake, fixed(testNow), nil)

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AuthCodeURL(t.Context(), login); !errors.Is(err, ErrProvider) {
		t.Errorf("error = %v, want a document naming another issuer to be refused", err)
	}
}

func TestDiscoveryIsFetchedOnceAndNotCachedWhenItFails(t *testing.T) {
	fake := newIDP(t)
	// A document that names another issuer is a failure, and a failure must
	// not be remembered: the next login has to try again.
	fake.issuerInDocument = "https://evil.example.test/realms/apps"
	p := provider(t, fake, fixed(testNow), nil)

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AuthCodeURL(t.Context(), login); err == nil {
		t.Fatal("the broken document was accepted")
	}

	fake.issuerInDocument = testIssuer
	if _, err := p.AuthCodeURL(t.Context(), login); err != nil {
		t.Errorf("the repaired document was not picked up: %v", err)
	}
}

func TestDiscoveryRefusesAPlainHTTPEndpoint(t *testing.T) {
	fake := newIDP(t)
	// The authorization code is exchanged over this URL, with the client
	// secret in the request, so a document downgrading it to plain HTTP is not
	// usable however correct the rest of it is.
	fake.tokenURL = "http://accounts.example.test/realms/apps/protocol/openid-connect/token"
	p := provider(t, fake, fixed(testNow), nil)

	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AuthCodeURL(t.Context(), login); !errors.Is(err, ErrProvider) {
		t.Errorf("error = %v, want a plain HTTP token endpoint to be refused", err)
	}
}

func TestLogoutURL(t *testing.T) {
	fake := newIDP(t)
	p := provider(t, fake, fixed(testNow), nil)

	raw, err := p.LogoutURL(t.Context(), "the-id-token", "https://catalogue.example.test/")
	if err != nil {
		t.Fatal(err)
	}
	q := mustURL(t, raw).Query()
	if got := q.Get("id_token_hint"); got != "the-id-token" {
		t.Errorf("id_token_hint = %q", got)
	}
	if got := q.Get("post_logout_redirect_uri"); got != "https://catalogue.example.test/" {
		t.Errorf("post_logout_redirect_uri = %q", got)
	}

	if _, err := p.LogoutURL(t.Context(), "", ""); !errors.Is(err, ErrLogin) {
		t.Errorf("error = %v, want a logout without a token to be refused", err)
	}
}

func TestLogoutURLWithoutAnEndSessionEndpoint(t *testing.T) {
	fake := newIDP(t)
	fake.endSessionURL = ""
	p := provider(t, fake, fixed(testNow), nil)

	raw, err := p.LogoutURL(t.Context(), "the-id-token", "")
	if err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		t.Errorf("LogoutURL = %q, want empty when the provider publishes none", raw)
	}
}

func TestCodeChallengeMatchesRFC7636(t *testing.T) {
	// The test vector of RFC 7636 appendix B.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := CodeChallenge(verifier); got != want {
		t.Errorf("CodeChallenge = %q, want %q", got, want)
	}
}

func TestNewLoginIsUnique(t *testing.T) {
	first, err := NewLogin("/next")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewLogin("/next")
	if err != nil {
		t.Fatal(err)
	}

	if first.State == second.State || first.Nonce == second.Nonce || first.Verifier == second.Verifier {
		t.Error("two logins share a value")
	}
	if first.State == first.Nonce || first.State == first.Verifier || first.Nonce == first.Verifier {
		t.Error("state, nonce and verifier are not independent of each other")
	}
	if err := checkVerifier(first.Verifier); err != nil {
		t.Errorf("the generated verifier is not valid: %v", err)
	}
	if first.Next != "/next" {
		t.Errorf("Next = %q", first.Next)
	}
}

func TestMatchesState(t *testing.T) {
	login, err := NewLogin("")
	if err != nil {
		t.Fatal(err)
	}

	if !login.MatchesState(login.State) {
		t.Error("the login does not match its own state")
	}
	if login.MatchesState("") || login.MatchesState(login.State+"x") ||
		login.MatchesState(strings.Repeat("a", len(login.State))) {
		t.Error("MatchesState accepted another state")
	}
	if (Login{}).MatchesState("") {
		t.Error("a login with no state matches an empty state")
	}
}

func TestIdentityInAny(t *testing.T) {
	identity := Identity{Groups: []string{"catalogue-owners"}}

	if !identity.InAny(nil) {
		t.Error("an empty list must let everybody through: requiring no group is a deployment decision")
	}
	if !identity.InAny([]string{"admins", "catalogue-owners"}) {
		t.Error("a member was refused")
	}
	if identity.InAny([]string{"admins"}) {
		t.Error("a non member was let through")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}
