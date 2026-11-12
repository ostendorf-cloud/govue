// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package oidc runs the OpenID Connect authorization code flow with PKCE
// against a standards compliant provider and verifies the ID token itself,
// with the standard library only.
//
// # What a caller is told
//
// Every failure of a login reaches the caller as one of two opaque errors,
// [ErrLogin] or [ErrProvider], and never as the cause. A bad client secret, a
// provider that is down, a token signed by the wrong key and a clock that has
// drifted all look the same from outside, because the caller is an HTTP
// handler and whatever it is handed tends to end up in a response. The cause
// is written to the logger carried by the context instead, with the request id,
// so an operator can still find out which of them it was.
//
// The two errors are a category, not a cause: [ErrLogin] means this browser
// has to start the login again, [ErrProvider] means the application cannot
// talk to its identity provider at all and a 503 is the honest answer.
// [New] is the exception. It runs at startup, not inside a request, and its
// error names every field that is wrong.
//
// # The login in flight
//
// The state, the nonce and the PKCE verifier of a login that has not happened
// yet have to survive the redirect to the provider and back, and at that point
// there is no session to keep them in. [Login] is that state, it carries JSON
// tags for exactly this reason, and session.Codec signs it into a short lived
// cookie:
//
//	login, err := oidc.NewLogin("/lists/7")
//	value, err := codec.Encode(login, 10*time.Minute)
//
// Nothing here imports the session package. A [Login] is a plain value, and an
// application that signs it some other way is free to.
//
// # The two handlers
//
// [NewHandlers] is the whole flow already assembled: [Handlers.Login] starts a
// login and [Handlers.Callback] finishes one, in the order the steps have to
// happen in and with the login good for one callback only. An application
// supplies the provider, something to keep the login in flight and the one
// thing this package cannot know, what belongs in its session:
//
//	handlers, err := oidc.NewHandlers(oidc.HandlerConfig{
//		Provider: provider,
//		Logins:   codec,
//		StartSession: func(ctx context.Context, w http.ResponseWriter, _ *http.Request, id oidc.Identity) error {
//			_, err := sessions.Start(ctx, w, session.Session{Subject: id.Subject, Email: id.Email, Groups: id.Groups})
//			return err
//		},
//	})
//	mux.Handle("GET /auth/login", handlers.Login())
//	mux.Handle("GET /auth/callback", handlers.Callback())
//
// [LoginStore] is still an interface and still keeps this package free of a
// session import. A *session.Codec satisfies it with no adapter, which is why
// the wiring is that short.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ostendorf-cloud/web-kit/logging"
)

// ErrLogin is every failure that ends this one login: a code the provider
// refuses, an ID token that does not verify, a nonce that does not match, a
// cookie that was lost on the way. The browser can start over, and nothing
// about which of them it was is said here. The log line holds that.
var ErrLogin = errors.New("oidc: the login could not be completed")

// ErrProvider means the identity provider could not be reached or did not
// answer usably: discovery failed, the JWKS could not be fetched, the token
// endpoint timed out. Starting the login again will not help, so an
// application answers 503 rather than redirecting into another attempt.
var ErrProvider = errors.New("oidc: the identity provider is not available")

const (
	// maxResponseBytes bounds every answer read from the provider. A discovery
	// document, a JWKS and a token response are all small, and a provider that
	// has been taken over does not get to hand an application a gigabyte.
	maxResponseBytes = 1 << 20

	// Verifier lengths of RFC 7636 section 4.1.
	minVerifierLen = 43
	maxVerifierLen = 128

	// DefaultGroupsClaim is the claim name group membership is read from when
	// [Config.GroupsClaim] is empty.
	DefaultGroupsClaim = "groups"

	// DefaultTimeout bounds one request to the provider when [Config.HTTPClient]
	// is nil. A login is a person waiting at a redirect, so the ceiling is low.
	DefaultTimeout = 10 * time.Second

	// ClockSkew is how far the provider's clock may be off this one's before an
	// ID token is refused. Two minutes is what NTP synchronised machines drift
	// by in practice and is small enough that a token whose expiry really has
	// passed is not usable for meaningfully longer.
	ClockSkew = 2 * time.Minute
)

// DefaultScopes asks for the claims an application needs to show who is logged
// in. A group claim usually needs its own scope added, commonly "groups".
var DefaultScopes = []string{"openid", "profile", "email"}

// Config describes this application's client registration at the provider.
// Issuer, ClientID, ClientSecret and RedirectURL are required; every other
// field is secure when left zero.
type Config struct {
	// Issuer is the URL the discovery document lives under, without the
	// /.well-known/openid-configuration suffix. A trailing slash is trimmed.
	Issuer string

	// ClientID is this application's client at the provider.
	ClientID string

	// ClientSecret authenticates the token request. It is required: this is a
	// confidential client, PKCE is a second defence here and not a substitute
	// for the secret.
	ClientSecret string

	// RedirectURL is this application's callback, as registered at the
	// provider. The provider matches it exactly.
	RedirectURL string

	// Scopes defaults to [DefaultScopes].
	Scopes []string

	// GroupsClaim is the claim carrying group membership. Empty uses
	// [DefaultGroupsClaim], and a provider that sends none simply has no
	// groups.
	//
	// A dotted name walks into nested objects, which is where several
	// providers put role membership: "realm_access.roles" reads roles out of
	// the realm_access object. A claim whose own name contains a dot cannot be
	// addressed this way.
	GroupsClaim string

	// AuthParams are extra authorization request parameters the provider
	// understands, such as a login hint. They are set before this package's
	// own parameters, so none of those can be overwritten from here.
	AuthParams url.Values

	// HTTPClient talks to the provider. Nil is a client with a
	// [DefaultTimeout] timeout.
	//
	// A client supplied here is copied, and a redirect onto plain HTTP is
	// refused on it as well. A redirect policy it already carries is kept and
	// runs after that refusal, so it cannot turn the check off.
	HTTPClient *http.Client

	// Now is the clock. Zero is [time.Now]. Tests replace it.
	Now func() time.Time
}

// Identity is who logged in, taken from an ID token whose signature and claims
// have been verified.
type Identity struct {
	// Subject is the provider's stable id for the account. It is what a
	// session is keyed on, never the email or the username, both of which can
	// be reassigned to somebody else.
	Subject  string
	Username string
	Name     string
	Email    string
	Groups   []string

	// IDToken is kept for the logout hint, see [Provider.LogoutURL], and for
	// nothing else. It is a credential: it does not belong in a log, in a
	// response body or in anything a browser can read.
	IDToken string

	// ExpiresAt is the ID token's expiry. It bounds how long the logout hint is
	// good for and says nothing about how long the application's own session
	// lasts.
	ExpiresAt time.Time
}

// In reports whether the account is in the group.
func (i Identity) In(group string) bool { return slices.Contains(i.Groups, group) }

// InAny reports whether the account is in at least one of the groups. An empty
// list means no group is required, which is a deployment decision, and is why
// it returns true rather than false.
func (i Identity) InAny(groups []string) bool {
	if len(groups) == 0 {
		return true
	}
	for _, group := range groups {
		if i.In(group) {
			return true
		}
	}
	return false
}

// Login is the state of a login in flight: what the callback needs to finish
// one. It is signed into a short lived cookie and never kept server side, so
// the field names are short on purpose, a cookie has about 4 KiB.
type Login struct {
	State    string `json:"st"`
	Nonce    string `json:"no"`
	Verifier string `json:"cv"`

	// Next is where the browser goes after a successful login. It comes from
	// whoever started the login, so a caller checks that it is a path on its
	// own site before redirecting to it. This package only carries it.
	Next string `json:"nx,omitempty"`
}

// NewLogin generates the state, the nonce and the PKCE verifier, each from 32
// bytes of the system random source. The error is that source failing, which
// on a running machine does not happen.
func NewLogin(next string) (Login, error) {
	state, err := randomString(32)
	if err != nil {
		return Login{}, err
	}
	nonce, err := randomString(32)
	if err != nil {
		return Login{}, err
	}
	verifier, err := randomString(32)
	if err != nil {
		return Login{}, err
	}
	return Login{State: state, Nonce: nonce, Verifier: verifier, Next: next}, nil
}

// MatchesState compares the state the provider sent back with this login's, in
// constant time. It is what ties the callback to the browser that started the
// login, and a callback whose state does not match is not this browser's.
func (l Login) MatchesState(state string) bool {
	if l.State == "" || state == "" || len(l.State) != len(state) {
		return false
	}
	var diff byte
	for i := range state {
		diff |= l.State[i] ^ state[i]
	}
	return diff == 0
}

// metadata is the part of the discovery document this package uses.
type metadata struct {
	Issuer        string `json:"issuer"`
	AuthURL       string `json:"authorization_endpoint"`
	TokenURL      string `json:"token_endpoint"`
	JWKSURL       string `json:"jwks_uri"`
	EndSessionURL string `json:"end_session_endpoint"`
}

// Provider is one issuer and one client registration. It is safe for
// concurrent use, and it holds the discovery document and the signing keys
// once they have been fetched.
type Provider struct {
	issuer       string
	clientID     string
	clientSecret string
	redirectURL  string
	scopes       string
	groupsPath   []string
	authParams   url.Values
	http         *http.Client
	now          func() time.Time

	metaMu sync.Mutex
	meta   *metadata

	keys *keySet
}

// New validates the configuration. Nothing is fetched here: an identity
// provider that is not reachable yet must not stop an application from
// starting, and a readiness probe is where that belongs.
//
// The error names every field that is wrong at once. It is a startup error,
// read by whoever deployed the application, and it is the one error in this
// package that carries its cause.
func New(cfg Config) (*Provider, error) {
	// Trailing slashes are trimmed once, so the issuer is one canonical string
	// for the discovery URL and for every issuer comparison after it.
	issuer := strings.TrimRight(cfg.Issuer, "/")

	var problems []error
	if err := checkEndpoint("issuer", issuer); err != nil {
		problems = append(problems, err)
	}
	if cfg.ClientID == "" {
		problems = append(problems, errors.New("client id is empty"))
	}
	if cfg.ClientSecret == "" {
		problems = append(problems, errors.New("client secret is empty"))
	}
	if err := checkEndpoint("redirect url", cfg.RedirectURL); err != nil {
		problems = append(problems, err)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("oidc config: %w", err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	groupsClaim := cfg.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = DefaultGroupsClaim
	}

	p := &Provider{
		issuer:       issuer,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		redirectURL:  cfg.RedirectURL,
		scopes:       strings.Join(scopes, " "),
		groupsPath:   strings.Split(groupsClaim, "."),
		authParams:   cfg.AuthParams,
		http:         cfg.HTTPClient,
		now:          cfg.Now,
	}
	p.http = clientWith(cfg.HTTPClient)
	if p.now == nil {
		p.now = time.Now
	}
	p.keys = newKeySet(p)
	return p, nil
}

// Issuer is the canonical issuer URL this provider talks to, with any trailing
// slash already trimmed.
func (p *Provider) Issuer() string { return p.issuer }

// checkEndpoint refuses anything but an absolute https URL: the authorization
// code, the tokens and the client secret all travel over these. Plain HTTP is
// allowed against a loopback address alone, which is where a provider used in
// development runs and where nothing leaves the machine.
func checkEndpoint(name, raw string) error {
	if raw == "" {
		return fmt.Errorf("%s is empty", name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a URL: %w", name, err)
	}
	if u.Host == "" {
		return fmt.Errorf("%s %q has no host", name, shorten(raw, 200))
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopback(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("%s %q is not an https URL", name, shorten(raw, 200))
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// clientWith is the client this package talks to the provider over.
//
// Checking the configured endpoints is not enough on its own: they are checked
// once, and a provider that answers a discovery or JWKS request with a
// redirect could still move it onto plain HTTP afterwards. So every request
// this package makes refuses that redirect, whether the client came from the
// configuration or not. A caller's own redirect policy is kept and asked
// after the scheme check, never instead of it: a policy set for an unrelated
// reason is not a decision to let a token endpoint move onto plain HTTP.
func clientWith(configured *http.Client) *http.Client {
	if configured == nil {
		return &http.Client{Timeout: DefaultTimeout, CheckRedirect: refuseDowngrade}
	}
	client := *configured
	client.CheckRedirect = refuseDowngradeThen(client.CheckRedirect)
	return &client
}

// refuseDowngradeThen is the redirect policy for a configured client: the
// scheme check first, and the caller's own policy only once that has passed.
func refuseDowngradeThen(next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	if next == nil {
		return refuseDowngrade
	}
	return func(req *http.Request, via []*http.Request) error {
		if err := refuseDowngrade(req, via); err != nil {
			return err
		}
		return next(req, via)
	}
}

// refuseDowngrade stops a redirect onto anything but https, and keeps the ten
// hop limit the standard library applies by default.
func refuseDowngrade(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Scheme == "https" || (req.URL.Scheme == "http" && isLoopback(req.URL.Hostname())) {
		return nil
	}
	return fmt.Errorf("redirect to %q is not an https URL", shorten(req.URL.Scheme+"://"+req.URL.Host, 200))
}

// AuthCodeURL is where the browser is sent to log in. The returned URL carries
// the PKCE challenge, never the verifier: the verifier stays in the cookie and
// is shown to the provider only at [Provider.Exchange], which is what makes an
// intercepted code useless.
func (p *Provider) AuthCodeURL(ctx context.Context, login Login) (string, error) {
	raw, err := p.authCodeURL(ctx, login)
	if err != nil {
		return "", report(ctx, "oidc authorization url", err)
	}
	return raw, nil
}

func (p *Provider) authCodeURL(ctx context.Context, login Login) (string, error) {
	if login.State == "" {
		return "", loginProblem(errors.New("state is empty"))
	}
	if login.Nonce == "" {
		return "", loginProblem(errors.New("nonce is empty"))
	}
	if err := checkVerifier(login.Verifier); err != nil {
		return "", err
	}
	m, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(m.AuthURL)
	if err != nil {
		return "", providerProblem(fmt.Errorf("authorization endpoint: %w", err))
	}

	q := u.Query()
	// The configured extras go first, so a stray redirect_uri or scope in them
	// cannot displace the values below.
	for key, values := range p.authParams {
		for _, value := range values {
			q.Set(key, value)
		}
	}
	q.Set("response_type", "code")
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", p.redirectURL)
	q.Set("scope", p.scopes)
	q.Set("state", login.State)
	q.Set("nonce", login.Nonce)
	q.Set("code_challenge", CodeChallenge(login.Verifier))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// tokenResponse is the token endpoint's answer, success or OAuth error.
type tokenResponse struct {
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Exchange trades the authorization code for tokens and verifies the ID token,
// the nonce included, returning who the token is for.
//
// The caller has already checked that the state the provider sent back matches
// the login, with [Login.MatchesState]. That check belongs to the callback
// handler, which is what holds both values.
func (p *Provider) Exchange(ctx context.Context, code string, login Login) (Identity, error) {
	identity, err := p.exchange(ctx, code, login)
	if err != nil {
		return Identity{}, report(ctx, "oidc token exchange", err)
	}
	return identity, nil
}

func (p *Provider) exchange(ctx context.Context, code string, login Login) (Identity, error) {
	if code == "" {
		return Identity{}, loginProblem(errors.New("authorization code is empty"))
	}
	if login.Nonce == "" {
		return Identity{}, loginProblem(errors.New("nonce is empty"))
	}
	if err := checkVerifier(login.Verifier); err != nil {
		return Identity{}, err
	}
	m, err := p.discover(ctx)
	if err != nil {
		return Identity{}, err
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.redirectURL)
	form.Set("code_verifier", login.Verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, providerProblem(fmt.Errorf("token request: %w", err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// client_secret_basic, which OpenID Connect Core requires every provider to
	// support. Both halves are form encoded before base64, RFC 6749 section
	// 2.3.1, which is the step a secret with a non alphanumeric character in it
	// otherwise fails on.
	req.SetBasicAuth(url.QueryEscape(p.clientID), url.QueryEscape(p.clientSecret))

	resp, err := p.http.Do(req)
	if err != nil {
		return Identity{}, providerProblem(fmt.Errorf("token endpoint: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Identity{}, providerProblem(fmt.Errorf("token response: %w", err))
	}

	var tr tokenResponse
	// The body carries tokens, so neither it nor a decoder message quoting part
	// of it is allowed into an error, not even one that only reaches the log.
	if json.Unmarshal(body, &tr) != nil {
		return Identity{}, providerProblem(fmt.Errorf("token response with status %d is not JSON", resp.StatusCode))
	}
	if tr.Error != "" {
		// An OAuth error answer is the provider refusing this grant: a code
		// already used, a redirect that does not match, a secret that is wrong.
		// The application cannot fix it inside the request, the browser starts
		// over, and the detail goes to the log.
		return Identity{}, loginProblem(fmt.Errorf("provider refused the grant: %s", oauthError(tr.Error, tr.ErrorDescription)))
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, providerProblem(fmt.Errorf("token endpoint answered %d", resp.StatusCode))
	}
	if tr.IDToken == "" {
		return Identity{}, providerProblem(errors.New("token response carries no id_token"))
	}
	return p.verifyIDToken(ctx, tr.IDToken, login.Nonce)
}

// oauthError renders an OAuth error answer on one line and shortened. Both
// fields come from the provider rather than from this application, and they
// reach a log, so neither is copied in at whatever length it arrived.
func oauthError(code, description string) string {
	code = shorten(code, 100)
	if description == "" {
		return code
	}
	return code + ": " + shorten(strings.Join(strings.Fields(description), " "), 300)
}

func shorten(s string, limit int) string {
	if len(s) > limit {
		return s[:limit]
	}
	return s
}

// LogoutURL is the provider's RP initiated logout endpoint with the id token as
// the hint, or an empty string when the provider publishes no such endpoint.
//
// Ending the application's own session is its own job and happens whether this
// exists or not. This only ends the login at the provider, so that the next
// login is not answered instantly from the provider's session.
func (p *Provider) LogoutURL(ctx context.Context, idToken, postLogoutRedirect string) (string, error) {
	raw, err := p.logoutURL(ctx, idToken, postLogoutRedirect)
	if err != nil {
		return "", report(ctx, "oidc logout url", err)
	}
	return raw, nil
}

func (p *Provider) logoutURL(ctx context.Context, idToken, postLogoutRedirect string) (string, error) {
	if idToken == "" {
		return "", loginProblem(errors.New("id token is empty"))
	}
	m, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	if m.EndSessionURL == "" {
		return "", nil
	}
	u, err := url.Parse(m.EndSessionURL)
	if err != nil {
		return "", providerProblem(fmt.Errorf("end session endpoint: %w", err))
	}
	q := u.Query()
	q.Set("id_token_hint", idToken)
	if postLogoutRedirect != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirect)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// discover returns the provider metadata, fetched once and then kept. A failure
// is never kept, so the next login retries rather than being stuck behind a
// provider that was restarting at the moment of the first login.
func (p *Provider) discover(ctx context.Context) (*metadata, error) {
	p.metaMu.Lock()
	defer p.metaMu.Unlock()
	if p.meta != nil {
		return p.meta, nil
	}

	var m metadata
	if _, err := p.getJSON(ctx, p.issuer+"/.well-known/openid-configuration", &m); err != nil {
		return nil, providerProblem(fmt.Errorf("discovery: %w", err))
	}
	// A document naming another issuer is a misrouted or a hostile provider,
	// and its endpoints must not be used: that is the check that stops a
	// redirect of the discovery URL from moving the login somewhere else.
	if m.Issuer != p.issuer {
		return nil, providerProblem(fmt.Errorf("discovery document names issuer %q, want %q", shorten(m.Issuer, 200), p.issuer))
	}
	for _, endpoint := range []struct{ name, raw string }{
		{"authorization_endpoint", m.AuthURL},
		{"token_endpoint", m.TokenURL},
		{"jwks_uri", m.JWKSURL},
	} {
		if err := checkEndpoint(endpoint.name, endpoint.raw); err != nil {
			return nil, providerProblem(fmt.Errorf("discovery: %w", err))
		}
	}
	p.meta = &m
	return p.meta, nil
}

// getJSON reads one JSON document with a size cap, and returns the response
// headers for a caller that reads caching directives off them.
func (p *Provider) getJSON(ctx context.Context, endpoint string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read the response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.Header, fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.Header, errors.New("the response is not the JSON document expected here")
	}
	return resp.Header, nil
}

// CodeChallenge is the S256 code challenge of a PKCE verifier, RFC 7636
// section 4.2. It is exported because a caller that builds its own
// authorization URL still needs the challenge derived the one correct way.
func CodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oidc: read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// checkVerifier catches a verifier that is not one [NewLogin] produced, which
// in practice is a cookie that was truncated, lost or never set.
func checkVerifier(verifier string) error {
	if len(verifier) < minVerifierLen || len(verifier) > maxVerifierLen {
		return loginProblem(fmt.Errorf("code verifier is %d characters, want %d to %d",
			len(verifier), minVerifierLen, maxVerifierLen))
	}
	return nil
}

// problem is an internal failure: the real cause, and which of the two opaque
// errors the caller is told about. It never leaves this package.
type problem struct {
	public error
	cause  error
}

func (p *problem) Error() string { return p.cause.Error() }

// loginProblem is a failure of this one login attempt.
func loginProblem(cause error) error {
	return &problem{public: ErrLogin, cause: cause}
}

// providerProblem is a failure of the provider or of the path to it.
func providerProblem(cause error) error {
	return &problem{public: ErrProvider, cause: cause}
}

// report is the single boundary between what is known inside this package and
// what a caller is handed. It writes the cause to the request's logger and
// returns the opaque error, so no path out of this package can leak a detail by
// forgetting to.
//
// An unclassified error is reported as [ErrProvider]: that is the answer that
// does not send a user into an endless redirect loop against something that is
// broken rather than merely over.
func report(ctx context.Context, operation string, err error) error {
	public := ErrProvider
	level := slog.LevelError

	var prob *problem
	if errors.As(err, &prob) {
		public = prob.public
		if errors.Is(public, ErrLogin) {
			// One login that did not complete is not an outage. It is a lost
			// cookie, a reused code or a user who waited too long on the
			// provider's page, and a site with users produces a steady trickle
			// of them.
			level = slog.LevelWarn
		}
	}

	// No request_id key is added here: that is middleware.RequestID's job,
	// and its absence is not papered over with an empty or invented one.
	logging.FromContext(ctx).Log(ctx, level, "oidc login failed", "operation", operation, "error", err.Error())
	return public
}
