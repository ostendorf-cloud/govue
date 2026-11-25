// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package optin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/mail"
	"github.com/ostendorf-cloud/govue/pow"
	"github.com/ostendorf-cloud/govue/ratelimit"
)

// Distinctive values, so a log search finds them.
const (
	visitor = "ada.quillfeather@example.org"
	other   = "bert.marrowmere@example.org"

	confirmPage = "https://example.test/waitlist/confirm"
	unsubPage   = "https://example.test/waitlist/unsubscribe"
	oneClick    = "https://example.test/api/waitlist/unsubscribe"
)

// testSecret and testPowKey are built at run time so no literal looks like a
// credential.
var (
	testSecret = []byte(strings.Repeat("s7", 16))
	testPowKey = []byte(strings.Repeat("p4", 16))
)

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

// fakeSender records messages and never opens a socket.
type fakeSender struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

func (f *fakeSender) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeSender) messages() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mail.Message(nil), f.sent...)
}

func (f *fakeSender) to(address string) []mail.Message {
	var out []mail.Message
	for _, m := range f.messages() {
		if len(m.To) == 1 && m.To[0] == address {
			out = append(out, m)
		}
	}
	return out
}

type env struct {
	h      *Handler
	clock  *clock
	sender *fakeSender
	store  *MemoryStore
	logs   *bytes.Buffer
	pow    *pow.Service
}

// limiter is a generous limiter on the test clock, so only the tests that
// set their own limiter meet a limit.
func limiter(c *clock, per time.Duration) *ratelimit.Limiter {
	l := ratelimit.New(100, per, 1000)
	l.Now = c.now
	return l
}

func confirmationText(d ConfirmationData) Content {
	return Content{
		Subject: "Please confirm",
		Body:    "Confirm: " + d.ConfirmURL + "\nNot you? " + d.UnsubscribeURL + "\n",
	}
}

func baseConfig(e *env) Config {
	c := e.clock
	return Config{
		Secret:           testSecret,
		PoW:              e.pow,
		Sender:           e.sender,
		From:             "Site <noreply@example.test>",
		Store:            e.store,
		ConsentVersion:   "2026-11-25",
		ConfirmURL:       confirmPage,
		UnsubscribeURL:   unsubPage,
		OneClickURL:      oneClick,
		Confirmation:     confirmationText,
		Limiter:          limiter(c, time.Minute),
		MailLimiter:      limiter(c, time.Hour),
		ChallengeLimiter: limiter(c, time.Minute),
		ConfirmLimiter:   limiter(c, time.Minute),
		Logger:           logging.New(e.logs, slog.LevelDebug),
		Now:              c.now,
	}
}

func newEnv(t *testing.T, mod func(*Config)) *env {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	svc, err := pow.New(pow.Config{Audience: "example.test", Keys: [][]byte{testPowKey}, MaxNumber: 300, Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{clock: c, sender: &fakeSender{}, store: NewMemoryStore(0), logs: &bytes.Buffer{}, pow: svc}
	cfg := baseConfig(e)
	if mod != nil {
		mod(&cfg)
	}
	e.h, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) solve(t *testing.T) pow.Solution {
	t.Helper()
	c, err := e.pow.Create()
	if err != nil {
		t.Fatal(err)
	}
	e.clock.advance(4 * time.Second)
	for n := int64(0); n <= c.MaxNumber; n++ {
		if pow.Hash(c.Salt, n) == c.Challenge {
			return pow.Solution{Algorithm: c.Algorithm, Challenge: c.Challenge, Number: n, Salt: c.Salt, Signature: c.Signature}
		}
	}
	t.Fatal("no solution")
	return pow.Solution{}
}

type reqOpt func(*http.Request)

func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func serve(t *testing.T, h http.Handler, method, target string, body []byte, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, target, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.RemoteAddr = "203.0.113.9:4000"
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// request posts the form for address with consent and a solved challenge,
// then waits for the mail in flight.
func (e *env) request(t *testing.T, address string, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"email": address, "consent": true, "language": "de", "pow": e.solve(t)}
	w := serve(t, e.h.Request(), http.MethodPost, "/api/waitlist", jsonBody(t, body), opts...)
	e.wait(t)
	return w
}

func (e *env) wait(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := e.h.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func (e *env) confirm(t *testing.T, token string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, e.h.Confirm(), http.MethodPost, "/api/waitlist/confirm", jsonBody(t, map[string]string{"token": token}))
}

func (e *env) unsubscribe(t *testing.T, token string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, e.h.Unsubscribe(), http.MethodPost, "/api/waitlist/unsubscribe", jsonBody(t, map[string]string{"token": token}))
}

func (e *env) entry(t *testing.T, address string) (Entry, bool) {
	t.Helper()
	got, err := e.store.Get(t.Context(), e.h.keys.addressKey(address))
	if errors.Is(err, ErrNotFound) {
		return Entry{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return got, true
}

// tokenAfter returns the token of the link that starts with base in body.
func tokenAfter(t *testing.T, body, base string) string {
	t.Helper()
	i := strings.Index(body, base+"?token=")
	if i < 0 {
		t.Fatalf("no link to %s in the body", base)
	}
	rest := body[i+len(base+"?token="):]
	if j := strings.IndexAny(rest, " \n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
	return b.Error.Code
}

func details(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var b struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
	return b.Error.Details
}

func TestNew_refusesAnIncompleteConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mod  func(*Config)
	}{
		{"short secret", func(c *Config) { c.Secret = []byte("short") }},
		{"no store", func(c *Config) { c.Store = nil }},
		{"no consent version", func(c *Config) { c.ConsentVersion = " " }},
		{"consent version with a line break", func(c *Config) { c.ConsentVersion = "v1\nBcc: x" }},
		{"http confirm url", func(c *Config) { c.ConfirmURL = "http://example.test/confirm" }},
		{"confirm url with a query", func(c *Config) { c.ConfirmURL = confirmPage + "?a=b" }},
		{"unsubscribe url with a fragment", func(c *Config) { c.UnsubscribeURL = unsubPage + "#x" }},
		{"one click url with an encoded word marker", func(c *Config) { c.OneClickURL = oneClick + "/=?x" }},
		{"one click url with user info", func(c *Config) { c.OneClickURL = "https://u@example.test/x" }},
		{"empty one click url", func(c *Config) { c.OneClickURL = "" }},
		{"no confirmation function", func(c *Config) { c.Confirmation = nil }},
		{"confirmation without the confirm link", func(c *Config) {
			c.Confirmation = func(d ConfirmationData) Content {
				return Content{Subject: "x", Body: "Not you? " + d.UnsubscribeURL}
			}
		}},
		{"confirmation without the unsubscribe link", func(c *Config) {
			c.Confirmation = func(d ConfirmationData) Content {
				return Content{Subject: "x", Body: "Confirm: " + d.ConfirmURL}
			}
		}},
		{"panicking confirmation", func(c *Config) {
			c.Confirmation = func(ConfirmationData) Content { panic("boom") }
		}},
		{"negative ttl", func(c *Config) { c.PendingTTL = -time.Hour }},
		{"bad from", func(c *Config) { c.From = "not an address" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := &env{clock: &clock{t: time.Unix(0, 0)}, sender: &fakeSender{}, store: NewMemoryStore(0), logs: &bytes.Buffer{}}
			cfg := baseConfig(e)
			tc.mod(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("New accepted the config")
			}
		})
	}
}

func TestHandler_answersNotConfiguredWithoutSecretPowOrSender(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mod  func(*Config)
	}{
		{"no secret", func(c *Config) { c.Secret = nil }},
		{"no pow", func(c *Config) { c.PoW = nil }},
		{"no sender", func(c *Config) { c.Sender = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, tc.mod)
			if e.h.Configured() {
				t.Fatal("Configured reports true")
			}
			for name, h := range map[string]http.Handler{"request": e.h.Request(), "confirm": e.h.Confirm(), "unsubscribe": e.h.Unsubscribe()} {
				w := serve(t, h, http.MethodPost, "/x", []byte(`{}`))
				if w.Code != http.StatusServiceUnavailable || errCode(t, w) != CodeNotConfigured {
					t.Errorf("%s: got %d %s", name, w.Code, w.Body)
				}
			}
			w := serve(t, e.h.Challenge(), http.MethodGet, "/x", nil)
			if w.Code != http.StatusServiceUnavailable {
				t.Errorf("challenge: got %d", w.Code)
			}
		})
	}
}

func TestChallenge_issuesAChallenge(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := serve(t, e.h.Challenge(), http.MethodGet, "/x", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"signature"`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestRequest_sendsOneConfirmationMailWithLinksAndHeaders(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := e.request(t, visitor)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	got := e.sender.to(visitor)
	if len(got) != 1 {
		t.Fatalf("got %d mails, want 1", len(got))
	}
	m := got[0]
	if m.From != "Site <noreply@example.test>" || m.ReplyTo != "" {
		t.Errorf("envelope from %q reply-to %q", m.From, m.ReplyTo)
	}
	unsub := tokenAfter(t, m.Body, unsubPage)
	if want := "<" + oneClick + "?token=" + unsub + ">"; m.Headers["List-Unsubscribe"] != want {
		t.Errorf("List-Unsubscribe %q, want %q", m.Headers["List-Unsubscribe"], want)
	}
	if m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post %q", m.Headers["List-Unsubscribe-Post"])
	}
	if _, err := mail.Build(m); err != nil {
		t.Fatalf("the mail package refuses the message: %v", err)
	}
	raw, _ := mail.Build(m)
	if !bytes.Contains(raw, []byte("List-Unsubscribe: <"+oneClick+"?token=")) {
		t.Error("the built message carries the header encoded or not at all")
	}
	en, ok := e.entry(t, visitor)
	if !ok || en.State != StatePending || en.ConsentVersion != "2026-11-25" || en.Language != "de" || en.Sends != 0 {
		t.Fatalf("entry %+v", en)
	}
	counter, err := e.store.Get(t.Context(), e.h.keys.mailboxKey(visitor))
	if err != nil || counter.State != StateCounter || counter.Sends != 1 || counter.Address != "" {
		t.Fatalf("counter %+v %v", counter, err)
	}
	if en.ConfirmHash == (TokenHash{}) {
		t.Fatal("no token hash stored")
	}
	token := tokenAfter(t, m.Body, confirmPage)
	if bytes.Contains(en.ConfirmHash[:], []byte(token)) || hashConfirmString(t, token) != en.ConfirmHash {
		t.Fatal("the store holds something other than the hash of the token")
	}
}

func hashConfirmString(t *testing.T, token string) TokenHash {
	t.Helper()
	h, ok := parseConfirmToken(token)
	if !ok {
		t.Fatalf("token %q does not parse", token)
	}
	return h
}

// TestRequest_answersTheSameForEveryState is the enumeration rule: status and
// body are equal for a new, a pending, a limited, a confirmed and an
// unsubscribed address.
func TestRequest_answersTheSameForEveryState(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)

	answers := map[string]*httptest.ResponseRecorder{}
	answers["new"] = e.request(t, visitor)
	answers["pending within the resend interval"] = e.request(t, visitor)

	e.request(t, other)
	token := tokenAfter(t, e.sender.to(other)[0].Body, confirmPage)
	if w := e.confirm(t, token); w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	const gone = "carla.fenwick@example.org"
	e.request(t, gone)
	unsub := tokenAfter(t, e.sender.to(gone)[0].Body, unsubPage)
	if w := e.unsubscribe(t, unsub); w.Code != http.StatusOK {
		t.Fatalf("unsubscribe: %d %s", w.Code, w.Body)
	}
	// Past the resend interval, so only the state decides about a mail.
	e.clock.advance(DefaultResendInterval)
	answers["confirmed"] = e.request(t, other)
	answers["unsubscribed"] = e.request(t, gone)

	want := answers["new"]
	for name, w := range answers {
		if w.Code != want.Code || w.Body.String() != want.Body.String() {
			t.Errorf("%s: %d %q, new address: %d %q", name, w.Code, w.Body, want.Code, want.Body)
		}
		if w.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
			t.Errorf("%s: content type differs", name)
		}
	}
	if n := len(e.sender.to(other)); n != 1 {
		t.Errorf("a confirmed address got %d mails from the form, want only the first confirmation", n)
	}
	if n := len(e.sender.to(gone)); n != 2 {
		t.Errorf("an unsubscribed address got %d mails, want a new confirmation after the first", n)
	}
}

// TestRequest_foldsPlusTagsIntoOneMailbox pins that the per address limit
// counts the mailbox: "+tag" variants and case share one budget, while each
// full address keeps its own entry.
func TestRequest_foldsPlusTagsIntoOneMailbox(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) { c.MaxSendsPerAddress = 2 })
	variants := []string{
		"ada.quillfeather+1@example.org",
		"ADA.Quillfeather+news@Example.org",
		"ada.quillfeather+2@example.org",
		"ada.quillfeather@example.org",
	}
	e.request(t, variants[0])
	e.request(t, variants[1]) // within the resend interval of the mailbox
	if n := len(e.sender.messages()); n != 1 {
		t.Fatalf("a plus variant within the interval got a mail: %d mails, want 1", n)
	}
	e.clock.advance(DefaultResendInterval)
	e.request(t, variants[2])
	e.clock.advance(DefaultResendInterval)
	e.request(t, variants[3])
	if n := len(e.sender.messages()); n != 2 {
		t.Fatalf("%d mails to one mailbox, want the limit of 2", n)
	}
	if _, ok := e.entry(t, variants[0]); !ok {
		t.Fatal("the first variant has no entry of its own")
	}
	if _, ok := e.entry(t, variants[2]); !ok {
		t.Fatal("the second mailed variant has no entry of its own")
	}
	if _, ok := e.entry(t, variants[3]); ok {
		t.Fatal("an entry was written for a variant that got no mail")
	}
}

func TestMailbox_foldsCaseAndPlusTagOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, input, want string
	}{
		{"plain", "ada@example.org", "ada@example.org"},
		{"case", "Ada@Example.ORG", "ada@example.org"},
		{"plus tag", "ada+news@example.org", "ada@example.org"},
		{"two plus signs", "ada+a+b@example.org", "ada@example.org"},
		{"leading plus kept", "+ada@example.org", "+ada@example.org"},
		{"dots kept", "a.d.a@gmail.com", "a.d.a@gmail.com"},
		{"plus in the domain kept", "ada@ex+ample.org", "ada@ex+ample.org"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mailbox(tc.input); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequest_limitsConfirmationMailsPerAddress(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) { c.MaxSendsPerAddress = 3; c.ResendInterval = 10 * time.Minute })
	e.request(t, visitor)
	e.request(t, visitor) // within the interval
	if n := len(e.sender.to(visitor)); n != 1 {
		t.Fatalf("after a request within the interval: %d mails, want 1", n)
	}
	e.clock.advance(10 * time.Minute)
	e.request(t, visitor)
	e.clock.advance(10 * time.Minute)
	e.request(t, visitor)
	if n := len(e.sender.to(visitor)); n != 3 {
		t.Fatalf("after three due requests: %d mails, want 3", n)
	}
	e.clock.advance(10 * time.Minute)
	w := e.request(t, visitor)
	if w.Code != http.StatusOK {
		t.Fatalf("the limited request answered %d", w.Code)
	}
	if n := len(e.sender.to(visitor)); n != 3 {
		t.Fatalf("the fourth request sent a mail: %d mails, want 3", n)
	}
}

func TestRequest_resendReplacesTheTokenAndKeepsThePeriod(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	first, _ := e.entry(t, visitor)
	oldToken := tokenAfter(t, e.sender.to(visitor)[0].Body, confirmPage)
	e.clock.advance(DefaultResendInterval)
	e.request(t, visitor)
	second, _ := e.entry(t, visitor)
	if !second.Created.Equal(first.Created) {
		t.Errorf("a resend moved the start of the period from %v to %v", first.Created, second.Created)
	}
	if w := e.confirm(t, oldToken); w.Code != http.StatusBadRequest || errCode(t, w) != CodeTokenInvalid {
		t.Fatalf("the replaced token: %d %s", w.Code, w.Body)
	}
	newToken := tokenAfter(t, e.sender.to(visitor)[1].Body, confirmPage)
	if w := e.confirm(t, newToken); w.Code != http.StatusOK {
		t.Fatalf("the newest token: %d %s", w.Code, w.Body)
	}
}

func TestRequest_requiresConsent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body map[string]any
	}{
		{"consent false", map[string]any{"email": visitor, "consent": false}},
		{"consent missing", map[string]any{"email": visitor}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, nil)
			tc.body["pow"] = e.solve(t)
			w := serve(t, e.h.Request(), http.MethodPost, "/x", jsonBody(t, tc.body))
			e.wait(t)
			if w.Code != http.StatusBadRequest || details(t, w)[FieldConsent] != CodeRequired {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
			if _, ok := e.entry(t, visitor); ok {
				t.Error("an entry was stored without consent")
			}
			if n := len(e.sender.messages()); n != 0 {
				t.Errorf("%d mails sent without consent", n)
			}
		})
	}
}

func TestRequest_validatesTheAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, address, want string
	}{
		{"empty", "", CodeRequired},
		{"too long", strings.Repeat("a", 250) + "@example.org", CodeTooLong},
		{"line break", "a@example.org\r\nBcc: b@example.org", CodeInvalidChars},
		{"non ascii", "jürgen@example.org", CodeNonASCIIEmail},
		{"display name", "Ada <a@example.org>", CodeInvalid},
		{"group", "g: a@example.org;", CodeInvalid},
		{"no domain", "ada", CodeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, nil)
			body := map[string]any{"email": tc.address, "consent": true, "pow": e.solve(t)}
			w := serve(t, e.h.Request(), http.MethodPost, "/x", jsonBody(t, body))
			if w.Code != http.StatusBadRequest || details(t, w)[FieldEmail] != tc.want {
				t.Fatalf("got %d %s, want %s", w.Code, w.Body, tc.want)
			}
		})
	}
}

func TestHandlers_refuseACrossOriginRequest(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	cross := header("Sec-Fetch-Site", "cross-site")
	origin := header("Origin", "https://evil.test")
	for name, h := range map[string]http.Handler{"request": e.h.Request(), "confirm": e.h.Confirm(), "unsubscribe": e.h.Unsubscribe()} {
		w := serve(t, h, http.MethodPost, "/x", []byte(`{}`), cross, origin)
		if w.Code != http.StatusForbidden || errCode(t, w) != CodeCrossOrigin {
			t.Errorf("%s: got %d %s", name, w.Code, w.Body)
		}
	}
	if n := len(e.sender.messages()); n != 0 {
		t.Errorf("%d mails sent", n)
	}
}

func TestRequest_refusesStrictJSONViolations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		body   string
		ctype  string
		status int
	}{
		{"no content type", `{}`, "", http.StatusUnsupportedMediaType},
		{"form content type", `email=x`, "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"unknown field", `{"email":"a@example.org","consent":true,"ip":"1.2.3.4"}`, "application/json", http.StatusBadRequest},
		{"too large", `{"email":"` + strings.Repeat("a", 20<<10) + `"}`, "application/json", http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, nil)
			w := serve(t, e.h.Request(), http.MethodPost, "/x", []byte(tc.body), header("Content-Type", tc.ctype))
			if w.Code != tc.status {
				t.Fatalf("got %d %s, want %d", w.Code, w.Body, tc.status)
			}
		})
	}
}

func TestRequest_honeypotAnswersLikeASuccessAndSendsNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	body := map[string]any{"email": visitor, "consent": true, "website": "https://spam.test", "pow": e.solve(t)}
	w := serve(t, e.h.Request(), http.MethodPost, "/x", jsonBody(t, body))
	e.wait(t)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if _, ok := e.entry(t, visitor); ok || len(e.sender.messages()) != 0 {
		t.Fatal("the honeypot stored or sent")
	}
}

func TestRequest_requiresProofOfWork(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := serve(t, e.h.Request(), http.MethodPost, "/x", jsonBody(t, map[string]any{"email": visitor, "consent": true}))
	if w.Code != http.StatusBadRequest || errCode(t, w) != CodePowRequired {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	sol := e.solve(t)
	e.request(t, other) // uses a fresh solution
	body := map[string]any{"email": visitor, "consent": true, "pow": sol}
	serve(t, e.h.Request(), http.MethodPost, "/x", jsonBody(t, body))
	w = serve(t, e.h.Request(), http.MethodPost, "/x", jsonBody(t, body))
	if w.Code != http.StatusBadRequest || errCode(t, w) != CodePowReplay {
		t.Fatalf("replay: got %d %s", w.Code, w.Body)
	}
}

func TestRequest_limitsPerClient(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) { c.Limiter = ratelimit.New(1, time.Hour, 10) })
	e.request(t, visitor)
	w := e.request(t, other)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if w := e.request(t, other, func(r *http.Request) { r.RemoteAddr = "198.51.100.7:4000" }); w.Code != http.StatusOK {
		t.Fatalf("another client: got %d %s", w.Code, w.Body)
	}
}

// TestRequest_ceilingRefusesKnownAndUnknownAlike pins that the global ceiling
// is taken before the state is known.
func TestRequest_ceilingRefusesKnownAndUnknownAlike(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, other)
	token := tokenAfter(t, e.sender.to(other)[0].Body, confirmPage)
	e.confirm(t, token)

	e2 := newEnv(t, func(c *Config) { c.Store = e.store; c.MailLimiter = ratelimit.New(1, time.Hour, 1) })
	// The confirmed address takes the one token, though it gets no mail.
	if w := e2.request(t, other); w.Code != http.StatusOK {
		t.Fatalf("first request: %d %s", w.Code, w.Body)
	}
	known := e2.request(t, other)
	unknown := e2.request(t, visitor)
	if known.Code != http.StatusServiceUnavailable || errCode(t, known) != CodeMailLimit {
		t.Fatalf("known: %d %s", known.Code, known.Body)
	}
	if unknown.Code != known.Code || unknown.Body.String() != known.Body.String() {
		t.Fatalf("unknown %d %q, known %d %q", unknown.Code, unknown.Body, known.Code, known.Body)
	}
}

func TestRequest_fullStoreAnswersTheSameAndSendsNothing(t *testing.T) {
	t.Parallel()
	// Two records: the counter and the entry of the first address.
	e2 := newEnv(t, func(c *Config) { c.Store = NewMemoryStore(2) })
	e2.request(t, other)
	if n := len(e2.sender.to(other)); n != 1 {
		t.Fatalf("the first address got %d mails, want 1", n)
	}
	w := e2.request(t, visitor)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if n := len(e2.sender.to(visitor)); n != 0 {
		t.Fatalf("%d mails to an address the store could not hold", n)
	}
}

func TestConfirm_confirmsOnceAndClearsTheToken(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	token := tokenAfter(t, e.sender.to(visitor)[0].Body, confirmPage)
	e.clock.advance(time.Hour)
	if w := e.confirm(t, token); w.Code != http.StatusOK {
		t.Fatalf("first use: %d %s", w.Code, w.Body)
	}
	en, _ := e.entry(t, visitor)
	if en.State != StateConfirmed || !en.Confirmed.Equal(e.clock.now()) || en.ConfirmHash != (TokenHash{}) {
		t.Fatalf("entry after confirm %+v", en)
	}
	if w := e.confirm(t, token); w.Code != http.StatusBadRequest || errCode(t, w) != CodeTokenInvalid {
		t.Fatalf("second use: %d %s", w.Code, w.Body)
	}
}

func TestConfirm_refusesEveryBadTokenWithOneCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		token func(t *testing.T, e *env) string
	}{
		{"empty", func(*testing.T, *env) string { return "" }},
		{"malformed", func(*testing.T, *env) string { return "not-a-token" }},
		{"unknown", func(t *testing.T, _ *env) string {
			tok, _, err := newConfirmToken(strings.NewReader(strings.Repeat("z", 64)))
			if err != nil {
				t.Fatal(err)
			}
			return tok
		}},
		{"expired", func(t *testing.T, e *env) string {
			e.request(t, visitor)
			e.clock.advance(DefaultPendingTTL)
			return tokenAfter(t, e.sender.to(visitor)[0].Body, confirmPage)
		}},
		{"unsubscribe token", func(t *testing.T, e *env) string {
			e.request(t, visitor)
			return tokenAfter(t, e.sender.to(visitor)[0].Body, unsubPage)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, nil)
			w := e.confirm(t, tc.token(t, e))
			if w.Code != http.StatusBadRequest || errCode(t, w) != CodeTokenInvalid {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestConfirm_justBeforeExpiryWorks(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	token := tokenAfter(t, e.sender.to(visitor)[0].Body, confirmPage)
	e.clock.advance(DefaultPendingTTL - time.Second)
	if w := e.confirm(t, token); w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

// TestConfirm_checksTheHashUnderTheLock drives a store whose lookup answers a
// key for any hash: the handler's own comparison must refuse.
func TestConfirm_checksTheHashUnderTheLock(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	key := e.h.keys.addressKey(visitor)
	e2 := newEnv(t, func(c *Config) { c.Store = anyHashStore{MemoryStore: e.store, key: key} })
	tok, _, err := newConfirmToken(strings.NewReader(strings.Repeat("q", 64)))
	if err != nil {
		t.Fatal(err)
	}
	if w := e2.confirm(t, tok); w.Code != http.StatusBadRequest || errCode(t, w) != CodeTokenInvalid {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if en, _ := e.entry(t, visitor); en.State != StatePending {
		t.Fatalf("the entry is %s", en.State)
	}
}

// TestConfirm_refusesAnEntryThatIsNoLongerPending covers the state check on
// its own: the entry keeps its hash, as an adapter that forgot to clear it
// would.
func TestConfirm_refusesAnEntryThatIsNoLongerPending(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	token := tokenAfter(t, e.sender.to(visitor)[0].Body, confirmPage)
	key := e.h.keys.addressKey(visitor)
	mustUpdate(t, e.store, key, func(en *Entry) { en.State = StateConfirmed })
	e2 := newEnv(t, func(c *Config) { c.Store = anyHashStore{MemoryStore: e.store, key: key} })
	if w := e2.confirm(t, token); w.Code != http.StatusBadRequest || errCode(t, w) != CodeTokenInvalid {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

// blockingSender holds every send until release is closed.
type blockingSender struct {
	fakeSender
	release chan struct{}
}

func (b *blockingSender) Send(ctx context.Context, m mail.Message) error {
	<-b.release
	return b.fakeSender.Send(ctx, m)
}

// TestRequest_answersBeforeTheMailIsSent pins that the SMTP time never shows
// in the answer: the answer comes while the sender still blocks.
func TestRequest_answersBeforeTheMailIsSent(t *testing.T) {
	t.Parallel()
	sender := &blockingSender{release: make(chan struct{})}
	e := newEnv(t, func(c *Config) { c.Sender = sender })
	body := map[string]any{"email": visitor, "consent": true, "pow": e.solve(t)}
	answered := make(chan int, 1)
	go func() {
		answered <- serve(t, e.h.Request(), http.MethodPost, "/x", jsonBody(t, body)).Code
	}()
	select {
	case code := <-answered:
		if code != http.StatusOK {
			t.Fatalf("got %d", code)
		}
	case <-time.After(5 * time.Second):
		close(sender.release)
		t.Fatal("the answer waited for the send")
	}
	close(sender.release)
	e.wait(t)
	if n := len(sender.to(visitor)); n != 1 {
		t.Fatalf("%d mails", n)
	}
}

type anyHashStore struct {
	*MemoryStore
	key Key
}

func (s anyHashStore) FindByConfirmHash(context.Context, TokenHash) (Key, error) { return s.key, nil }

func TestConfirm_isRateLimitedPerClient(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) { c.ConfirmLimiter = ratelimit.New(1, time.Hour, 10) })
	e.confirm(t, "x")
	if w := e.confirm(t, "x"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d", w.Code)
	}
}

func TestUnsubscribe_deletesAConfirmedEntryAtOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	m := e.sender.to(visitor)[0]
	if w := e.confirm(t, tokenAfter(t, m.Body, confirmPage)); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	if en, ok := e.entry(t, visitor); !ok || en.State != StateConfirmed {
		t.Fatalf("before: entry %+v present %v", en, ok)
	}
	if w := e.unsubscribe(t, tokenAfter(t, m.Body, unsubPage)); w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if en, ok := e.entry(t, visitor); ok {
		t.Fatalf("the unsubscribed address is still stored: %+v", en)
	}
	// A second use answers the same.
	if w := e.unsubscribe(t, tokenAfter(t, m.Body, unsubPage)); w.Code != http.StatusOK {
		t.Fatalf("second use: %d", w.Code)
	}
}

// TestUnsubscribe_addressIsGoneAndCanSignUpAgain pins the owner decision: no
// marker is kept, and a new request enters the address again, bounded only
// by the counter of the mailbox.
func TestUnsubscribe_addressIsGoneAndCanSignUpAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	unsub := tokenAfter(t, e.sender.to(visitor)[0].Body, unsubPage)
	if _, ok := e.entry(t, visitor); !ok {
		t.Fatal("no entry before the unsubscribe")
	}
	if w := e.unsubscribe(t, unsub); w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if _, ok := e.entry(t, visitor); ok {
		t.Fatal("the unsubscribed address is still stored")
	}
	for range 3 {
		e.clock.advance(DefaultResendInterval)
		e.request(t, visitor)
		if w := e.unsubscribe(t, unsub); w.Code != http.StatusOK {
			t.Fatalf("unsubscribe: %d", w.Code)
		}
	}
	// Three mails in the period: the first and two after unsubscribes. The
	// third restart is refused by the counter, which an unsubscribe keeps.
	if n := len(e.sender.to(visitor)); n != DefaultMaxSendsPerAddress {
		t.Fatalf("%d mails, want %d", n, DefaultMaxSendsPerAddress)
	}
}

func TestUnsubscribe_acceptsTheOneClickForm(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	unsub := tokenAfter(t, e.sender.to(visitor)[0].Body, unsubPage)
	target := "/api/waitlist/unsubscribe?token=" + url.QueryEscape(unsub)
	// A mail provider posts from its server: no Sec-Fetch-Site, no Origin.
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader("List-Unsubscribe=One-Click"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	e.h.Unsubscribe().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if en, ok := e.entry(t, visitor); ok {
		t.Fatalf("still stored in state %s", en.State)
	}
}

func TestUnsubscribe_refusesABadOneClickForm(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	unsub := tokenAfter(t, e.sender.to(visitor)[0].Body, unsubPage)
	cases := []struct {
		name, target, body string
		code               string
	}{
		{"no one click value", "/x?token=" + unsub, "List-Unsubscribe=Yes", "invalid_argument"},
		{"no token", "/x", "List-Unsubscribe=One-Click", CodeTokenInvalid},
		{"token in the body only", "/x", "List-Unsubscribe=One-Click&token=" + unsub, CodeTokenInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.target, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			e.h.Unsubscribe().ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest || errCode(t, w) != tc.code {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
		})
	}
	if en, _ := e.entry(t, visitor); en.State != StatePending {
		t.Fatalf("state %s", en.State)
	}
}

func TestUnsubscribe_refusesAForgedToken(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	good := tokenAfter(t, e.sender.to(visitor)[0].Body, unsubPage)
	forgedKeys := deriveKeys([]byte(strings.Repeat("x9", 16)))
	cases := map[string]string{
		"other secret": forgedKeys.unsubscribeToken(e.h.keys.addressKey(visitor)),
		"flipped mac":  good[:len(good)-2] + flip(good[len(good)-2:]),
		"confirm":      tokenAfter(t, e.sender.to(visitor)[0].Body, confirmPage),
		"empty":        "",
	}
	for name, tok := range cases {
		w := e.unsubscribe(t, tok)
		if w.Code != http.StatusBadRequest || errCode(t, w) != CodeTokenInvalid {
			t.Errorf("%s: got %d %s", name, w.Code, w.Body)
		}
	}
	if en, _ := e.entry(t, visitor); en.State != StatePending {
		t.Fatalf("state %s", en.State)
	}
}

func flip(s string) string {
	if s[0] == 'A' {
		return "B" + s[1:]
	}
	return "A" + s[1:]
}

func TestPurge_deletesExpiredPendingEntriesOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, other)
	e.confirm(t, tokenAfter(t, e.sender.to(other)[0].Body, confirmPage))
	e.request(t, visitor)
	created, _ := e.entry(t, visitor)
	counterKey := e.h.keys.mailboxKey(visitor)

	e.clock.advance(DefaultPendingTTL - time.Second - e.clock.now().Sub(created.Created))
	if _, err := e.h.Purge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.entry(t, visitor); !ok {
		t.Fatal("purge deleted a pending entry before its period ended")
	}
	if _, err := e.store.Get(t.Context(), counterKey); err != nil {
		t.Fatalf("purge deleted a counter before its period ended: %v", err)
	}
	e.clock.advance(time.Second)
	if _, err := e.h.Purge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.entry(t, visitor); ok {
		t.Error("the expired pending entry is still there")
	}
	if _, err := e.store.Get(t.Context(), counterKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("the expired counter is still there: %v", err)
	}
	if en, _ := e.entry(t, other); en.State != StateConfirmed {
		t.Error("purge touched a confirmed entry")
	}
}

func TestDeleteConfirmedBefore_deletesOnlyConfirmedEntriesAtOrBeforeTheCutoff(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, other)
	e.confirm(t, tokenAfter(t, e.sender.to(other)[0].Body, confirmPage))
	cutoff := e.clock.now()
	const later = "carla.fenwick@example.org"
	e.request(t, later)
	e.confirm(t, tokenAfter(t, e.sender.to(later)[0].Body, confirmPage))
	e.request(t, visitor) // pending

	n, err := e.h.DeleteConfirmedBefore(t.Context(), cutoff)
	if err != nil || n != 1 {
		t.Fatalf("got %d %v", n, err)
	}
	if _, ok := e.entry(t, other); ok {
		t.Error("the entry confirmed at the cutoff is still there")
	}
	if en, ok := e.entry(t, later); !ok || en.State != StateConfirmed {
		t.Error("an entry confirmed after the cutoff was deleted")
	}
	if en, ok := e.entry(t, visitor); !ok || en.State != StatePending {
		t.Error("a pending entry was deleted")
	}
	if _, err := e.store.Get(t.Context(), e.h.keys.mailboxKey(visitor)); err != nil {
		t.Errorf("a counter was deleted: %v", err)
	}
}

func TestRequest_afterExpiryStartsANewPeriod(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) { c.MaxSendsPerAddress = 1 })
	e.request(t, visitor)
	e.clock.advance(DefaultPendingTTL)
	e.request(t, visitor)
	if n := len(e.sender.to(visitor)); n != 2 {
		t.Fatalf("got %d mails, want 2", n)
	}
	en, _ := e.entry(t, visitor)
	if !en.Created.Equal(e.clock.now()) {
		t.Fatalf("entry %+v", en)
	}
}

// TestRequest_keepsTheGapAcrossTheEndOfAPeriod pins that a new counting
// period keeps the time of the last mail: three mails late in one period
// cannot be followed by more right after the reset, also when a purge ran in
// between.
func TestRequest_keepsTheGapAcrossTheEndOfAPeriod(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	counter, err := e.store.Get(t.Context(), e.h.keys.mailboxKey(visitor))
	if err != nil {
		t.Fatal(err)
	}
	start := counter.Created
	// at runs a request at start+d. The proof of work of a request moves
	// the clock by 4 seconds first.
	at := func(d time.Duration) {
		t.Helper()
		e.clock.advance(start.Add(d).Sub(e.clock.now()) - 4*time.Second)
		e.request(t, visitor)
	}
	at(DefaultPendingTTL - 25*time.Minute)
	at(DefaultPendingTTL - 5*time.Minute)
	if n := len(e.sender.to(visitor)); n != 3 {
		t.Fatalf("%d mails in the first period, want 3", n)
	}
	e.clock.advance(start.Add(DefaultPendingTTL + time.Second).Sub(e.clock.now()))
	if _, err := e.h.Purge(t.Context()); err != nil {
		t.Fatal(err)
	}
	at(DefaultPendingTTL + time.Minute) // a new period, 6 minutes after the last mail
	if n := len(e.sender.to(visitor)); n != 3 {
		t.Fatalf("a mail 6 minutes after the last one: %d mails, want 3", n)
	}
	at(DefaultPendingTTL + 5*time.Minute + 4*time.Second) // 10 minutes after the last mail
	if n := len(e.sender.to(visitor)); n != 4 {
		t.Fatalf("after the gap in the new period: %d mails, want 4", n)
	}
}

func TestRunPurge_purgesUntilTheContextEnds(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) { c.PurgeInterval = time.Millisecond })
	e.request(t, visitor)
	e.clock.advance(DefaultPendingTTL)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.h.RunPurge(ctx) }()
	deadline := time.After(5 * time.Second)
	for e.store.Len() != 0 {
		select {
		case <-deadline:
			t.Fatal("RunPurge did not purge")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMailConfirmed_mailsConfirmedEntriesOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, other)
	e.confirm(t, tokenAfter(t, e.sender.to(other)[0].Body, confirmPage))
	e.request(t, visitor) // stays pending
	const gone = "carla.fenwick@example.org"
	e.request(t, gone)
	e.confirm(t, tokenAfter(t, e.sender.to(gone)[0].Body, confirmPage))
	e.unsubscribe(t, tokenAfter(t, e.sender.to(gone)[0].Body, unsubPage))

	res, err := e.h.MailConfirmed(t.Context(), func(d MailingData) Content {
		return Content{Subject: "We launched", Body: "Hello.\nUnsubscribe: " + d.UnsubscribeURL + "\n"}
	})
	if err != nil || res.Sent != 1 || res.Failed != 0 {
		t.Fatalf("result %+v %v", res, err)
	}
	if n := len(e.sender.to(visitor)); n != 1 {
		t.Errorf("the pending address got %d mails, want the confirmation only", n)
	}
	if n := len(e.sender.to(gone)); n != 1 {
		t.Errorf("the unsubscribed address got %d mails, want the confirmation only", n)
	}
	got := e.sender.to(other)
	if len(got) != 2 {
		t.Fatalf("the confirmed address got %d mails, want 2", len(got))
	}
	m := got[1]
	if m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" || !strings.HasPrefix(m.Headers["List-Unsubscribe"], "<"+oneClick+"?token=") {
		t.Errorf("headers %v", m.Headers)
	}
	if w := e.unsubscribe(t, tokenAfter(t, m.Body, unsubPage)); w.Code != http.StatusOK {
		t.Fatalf("the mailing's unsubscribe link: %d", w.Code)
	}
}

func TestMailConfirmed_refusesABodyWithoutTheUnsubscribeLink(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, other)
	e.confirm(t, tokenAfter(t, e.sender.to(other)[0].Body, confirmPage))
	res, err := e.h.MailConfirmed(t.Context(), func(MailingData) Content {
		return Content{Subject: "We launched", Body: "Hello, no way out."}
	})
	if err != nil || res.Sent != 0 || res.Failed != 1 {
		t.Fatalf("result %+v %v", res, err)
	}
	if n := len(e.sender.to(other)); n != 1 {
		t.Fatalf("%d mails, want the confirmation only", n)
	}
}

func TestRequest_doesNotSendAConfirmationWithoutTheLinks(t *testing.T) {
	t.Parallel()
	// The site's text drops the links for one language only, so the startup
	// probe passes and the send must catch it.
	e := newEnv(t, func(c *Config) {
		c.Confirmation = func(d ConfirmationData) Content {
			if d.Language == "de" {
				return Content{Subject: "Bitte bestaetigen", Body: "Bitte bestaetigen."}
			}
			return confirmationText(d)
		}
	})
	if w := e.request(t, visitor); w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	if n := len(e.sender.messages()); n != 0 {
		t.Fatalf("%d mails without the links", n)
	}
	if !strings.Contains(e.logs.String(), "confirmation mail not sent") {
		t.Error("no log line for the refused mail")
	}
}

// lyingStore lists pending entries as confirmed, as a broken adapter might.
type lyingStore struct{ *MemoryStore }

func (s lyingStore) EachConfirmed(ctx context.Context, fn func(Entry) error) error {
	s.mu.Lock()
	var list []Entry
	for _, e := range s.entries {
		list = append(list, e)
	}
	s.mu.Unlock()
	for _, e := range list {
		if err := fn(e); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func TestMailConfirmed_sendsNothingToAPendingEntryAStoreListed(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.request(t, visitor)
	e2 := newEnv(t, func(c *Config) { c.Store = lyingStore{e.store} })
	res, err := e2.h.MailConfirmed(t.Context(), func(d MailingData) Content {
		return Content{Subject: "We launched", Body: d.UnsubscribeURL}
	})
	// The lying store lists the pending entry and the counter of its mailbox.
	if err != nil || res.Sent != 0 || res.Skipped != 2 {
		t.Fatalf("result %+v %v", res, err)
	}
	if n := len(e2.sender.messages()); n != 0 {
		t.Fatalf("%d mails to an unconfirmed address or a counter", n)
	}
}

func TestNewRecipient_allowsOnlyTheMailTheStateAllows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		state State
		kind  mailKind
		want  bool
	}{
		{"confirmation to pending", StatePending, kindConfirmation, true},
		{"mailing to confirmed", StateConfirmed, kindMailing, true},
		{"mailing to pending", StatePending, kindMailing, false},
		{"confirmation to confirmed", StateConfirmed, kindConfirmation, false},
		{"confirmation to a counter", StateCounter, kindConfirmation, false},
		{"mailing to a counter", StateCounter, kindMailing, false},
		{"confirmation to none", StateNone, kindConfirmation, false},
		{"unknown kind", StatePending, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newRecipient(Entry{State: tc.state, Address: visitor}, tc.kind)
			if got := err == nil; got != tc.want {
				t.Fatalf("allowed %v, want %v", got, tc.want)
			}
			if !tc.want && !errors.Is(err, errKindNotAllowed) {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestDeliver_refusesTheZeroRecipient(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	err := e.h.deliver(t.Context(), recipient{address: visitor}, Content{Subject: "x", Body: unsubPage})
	if !errors.Is(err, errKindNotAllowed) {
		t.Fatalf("got %v", err)
	}
	if len(e.sender.messages()) != 0 {
		t.Fatal("a mail left")
	}
}

func TestHandlers_logNoAddressAndNoToken(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.sender.err = errors.New("smtp refused " + other)
	e.request(t, other)
	e.sender.err = nil
	e.request(t, visitor)
	m := e.sender.to(visitor)[0]
	confirmTok := tokenAfter(t, m.Body, confirmPage)
	unsubTok := tokenAfter(t, m.Body, unsubPage)
	e.confirm(t, confirmTok)
	e.confirm(t, confirmTok)
	e.unsubscribe(t, unsubTok)
	e.unsubscribe(t, "bogus")
	logs := e.logs.String()
	if !strings.Contains(logs, "confirmation mail not sent") {
		t.Fatal("the send failure was not logged")
	}
	for _, secret := range []string{visitor, other, "quillfeather", "marrowmere", confirmTok, unsubTok} {
		if strings.Contains(strings.ToLower(logs), strings.ToLower(secret)) {
			t.Errorf("the log holds %q", secret)
		}
	}
}

// TestRequest_panicInConfirmationDoesNotCrash covers the mail goroutine: a
// panic there, http.ErrAbortHandler included, would end the process, because
// no server recovers a goroutine it did not start.
func TestRequest_panicInConfirmationDoesNotCrash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value func(ConfirmationData) any
		want  string
	}{
		{"value holding the address", func(d ConfirmationData) any { return d.Address }, "panicked with string"},
		{"abort handler", func(ConfirmationData) any { return http.ErrAbortHandler }, "panicked with *errors.errorString"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, func(c *Config) {
				c.Confirmation = func(d ConfirmationData) Content {
					if d.Address != probeAddress {
						panic(tc.value(d))
					}
					return confirmationText(d)
				}
			})
			if w := e.request(t, visitor); w.Code != http.StatusOK {
				t.Fatalf("got %d", w.Code)
			}
			logs := e.logs.String()
			if strings.Contains(logs, visitor) {
				t.Fatal("the panic value reached the log")
			}
			if !strings.Contains(logs, tc.want) {
				t.Fatalf("no log line naming the panic type %q", tc.want)
			}
		})
	}
}

func TestWait_returnsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.h.inflight.Add(1)
	defer e.h.inflight.Done()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := e.h.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestHandlers_refuseOtherMethods(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	for name, h := range map[string]http.Handler{"request": e.h.Request(), "confirm": e.h.Confirm(), "unsubscribe": e.h.Unsubscribe()} {
		w := serve(t, h, http.MethodGet, "/x?token=abc", nil)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
			t.Errorf("%s: got %d", name, w.Code)
		}
	}
	w := serve(t, e.h.Challenge(), http.MethodPost, "/x", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("challenge: got %d", w.Code)
	}
}
