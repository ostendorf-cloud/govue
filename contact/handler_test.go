// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package contact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/mail"
	"github.com/ostendorf-cloud/govue/pow"
	"github.com/ostendorf-cloud/govue/ratelimit"
)

// The visitor's data, distinctive enough to find in a log line.
const (
	secretName    = "Zora Quillfeather"
	secretEmail   = "zora.quillfeather@example.org"
	secretMessage = "Quillfeather asks about the invoice number 4711."
)

// testKey is built at run time so no literal in the source looks like a credential.
var testKey = []byte(strings.Repeat("k3", 16))

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

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type env struct {
	h      *Handler
	clock  *clock
	sender *fakeSender
	logs   *bytes.Buffer
	pow    *pow.Service
}

func limiter(c *clock, burst int, per time.Duration) *ratelimit.Limiter {
	l := ratelimit.New(burst, per, 1000)
	l.Now = c.now
	return l
}

func newEnv(t *testing.T, mod func(*Config)) *env {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	svc, err := pow.New(pow.Config{Audience: "example.test", Keys: [][]byte{testKey}, MaxNumber: 500, Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{clock: c, sender: &fakeSender{}, logs: &bytes.Buffer{}, pow: svc}
	cfg := Config{
		PoW:              svc,
		Sender:           e.sender,
		From:             "Site <noreply@example.test>",
		To:               []string{"owner@example.test"},
		Limiter:          limiter(c, 5, 10*time.Minute),
		MailLimiter:      limiter(c, 50, time.Hour),
		ChallengeLimiter: limiter(c, 10, 20*time.Second),
		Logger:           logging.New(e.logs, slog.LevelDebug),
		Now:              c.now,
	}
	if mod != nil {
		mod(&cfg)
	}
	e.h, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func code(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
	return b.Error.Code
}

// challenge fetches and solves one challenge as the browser does, then waits
// out the minimum age.
func (e *env) challenge(t *testing.T, svc *pow.Service) pow.Solution {
	t.Helper()
	var c pow.Challenge
	if svc == nil {
		svc = e.pow
	}
	c, err := svc.Create()
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

func (e *env) body(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"name": secretName, "email": secretEmail, "message": secretMessage,
		"pow": e.challenge(t, nil),
	}
}

type reqOpt func(*http.Request)

func (e *env) post(t *testing.T, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case []byte:
		raw = b
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/contact", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("User-Agent", "TestBrowser/1.0")
	r.RemoteAddr = "203.0.113.9:4000"
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	e.h.Submit().ServeHTTP(w, r)
	return w
}

func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func from(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }

func (e *env) assertNoContentInLogs(t *testing.T) {
	t.Helper()
	logs := e.logs.String()
	for _, secret := range []string{"Quillfeather", "zora", "invoice", secretName, secretEmail, secretMessage} {
		if strings.Contains(strings.ToLower(logs), strings.ToLower(secret)) {
			t.Fatalf("log holds %q:\n%s", secret, logs)
		}
	}
}

func TestSubmitSendsOneMail(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := e.post(t, e.body(t))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) != 0 || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("a cookie was set")
	}
	if e.sender.count() != 1 {
		t.Fatalf("sent %d mails", e.sender.count())
	}
	m := e.sender.sent[0]
	if m.From != "Site <noreply@example.test>" || len(m.To) != 1 || m.To[0] != "owner@example.test" {
		t.Fatalf("envelope = %q %v", m.From, m.To)
	}
	if m.ReplyTo != secretEmail {
		t.Fatalf("Reply-To = %q", m.ReplyTo)
	}
	if m.Headers["Auto-Submitted"] != "auto-generated" {
		t.Fatalf("headers = %v", m.Headers)
	}
	if strings.Contains(m.Subject, "Quillfeather") {
		t.Fatalf("subject holds the name: %q", m.Subject)
	}
	if !strings.Contains(m.Body, secretMessage) || !strings.Contains(m.Body, secretName) {
		t.Fatalf("body = %q", m.Body)
	}
	for _, s := range []string{"203.0.113.9", "TestBrowser"} {
		if strings.Contains(m.Subject+m.Body+m.From+m.ReplyTo+strings.Join(m.To, ","), s) {
			t.Fatalf("mail holds %q", s)
		}
		for k, v := range m.Headers {
			if strings.Contains(k+v, s) {
				t.Fatalf("header holds %q", s)
			}
		}
	}
	// The built message must pass the real builder, which is what a sender runs.
	if _, err := mail.Build(m); err != nil {
		t.Fatalf("Build: %v", err)
	}
	e.assertNoContentInLogs(t)
}

func TestSubmitUsesTheSiteSubjectAndBody(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) {
		c.Subject = func(Request) string { return "Contact" }
		c.Body = func(r Request) string { return r.Message }
	})
	if w := e.post(t, e.body(t)); w.Code != http.StatusOK {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if m := e.sender.sent[0]; m.Subject != "Contact" || m.Body != secretMessage {
		t.Fatalf("mail = %+v", m)
	}
}

func TestSubmitRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mod    func(*Config)
		run    func(t *testing.T, e *env) *httptest.ResponseRecorder
		status int
		code   string
	}{
		{
			name: "cross site fetch metadata",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				return e.post(t, e.body(t), header("Sec-Fetch-Site", "cross-site"))
			},
			status: 403, code: CodeCrossOrigin,
		},
		{
			name: "foreign origin without fetch metadata",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				return e.post(t, e.body(t), header("Sec-Fetch-Site", ""), header("Origin", "https://evil.example"))
			},
			status: 403, code: CodeCrossOrigin,
		},
		{
			name: "no content type",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				return e.post(t, e.body(t), func(r *http.Request) { r.Header.Del("Content-Type") })
			},
			status: 415, code: "unsupported_media_type",
		},
		{
			name: "form content type",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				return e.post(t, e.body(t), header("Content-Type", "application/x-www-form-urlencoded"))
			},
			status: 415, code: "unsupported_media_type",
		},
		{
			name: "text plain",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				return e.post(t, e.body(t), header("Content-Type", "text/plain"))
			},
			status: 415, code: "unsupported_media_type",
		},
		{
			name:   "not JSON",
			run:    func(t *testing.T, e *env) *httptest.ResponseRecorder { return e.post(t, "name=Zora") },
			status: 400, code: "invalid_argument",
		},
		{
			name: "unknown field",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				b := e.body(t)
				b["admin"] = true
				return e.post(t, b)
			},
			status: 400, code: "invalid_argument",
		},
		{
			name: "second JSON value",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				raw, _ := json.Marshal(e.body(t))
				return e.post(t, string(raw)+`{}`)
			},
			status: 400, code: "invalid_argument",
		},
		{
			name: "body over 64 KiB",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				b := e.body(t)
				b["message"] = strings.Repeat("a", 64<<10)
				return e.post(t, b)
			},
			status: 413, code: "request_too_large",
		},
		{
			name: "invalid fields",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				b := e.body(t)
				b["name"] = ""
				b["email"] = "not an address"
				return e.post(t, b)
			},
			status: 400, code: "invalid_argument",
		},
		{
			name: "no proof of work",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				b := e.body(t)
				delete(b, "pow")
				return e.post(t, b)
			},
			status: 400, code: CodePowRequired,
		},
		{
			name: "wrong proof of work",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				b := e.body(t)
				s, _ := b["pow"].(pow.Solution)
				s.Number++
				b["pow"] = s
				return e.post(t, b)
			},
			status: 400, code: CodePowInvalid,
		},
		{
			name: "challenge of another site",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				other, err := pow.New(pow.Config{Audience: "other.test", Keys: [][]byte{testKey}, MaxNumber: 500, Now: e.clock.now})
				if err != nil {
					t.Fatal(err)
				}
				b := e.body(t)
				b["pow"] = e.challenge(t, other)
				return e.post(t, b)
			},
			status: 400, code: CodePowInvalid,
		},
		{
			name: "expired challenge",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				b := e.body(t)
				e.clock.advance(11 * time.Minute)
				return e.post(t, b)
			},
			status: 400, code: CodePowExpired,
		},
		{
			name: "challenge solved too fast",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				c, err := e.pow.Create()
				if err != nil {
					t.Fatal(err)
				}
				var sol pow.Solution
				for n := int64(0); n <= c.MaxNumber; n++ {
					if pow.Hash(c.Salt, n) == c.Challenge {
						sol = pow.Solution{Algorithm: c.Algorithm, Challenge: c.Challenge, Number: n, Salt: c.Salt, Signature: c.Signature}
					}
				}
				return e.post(t, map[string]any{"name": secretName, "email": secretEmail, "message": secretMessage, "pow": sol})
			},
			status: 400, code: CodePowTooFast,
		},
		{
			name: "replayed challenge",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				b := e.body(t)
				if w := e.post(t, b); w.Code != http.StatusOK {
					t.Fatalf("first = %d %s", w.Code, w.Body)
				}
				return e.post(t, b)
			},
			status: 400, code: CodePowReplay,
		},
		{
			name: "full replay set",
			mod: func(c *Config) {
				svc, err := pow.New(pow.Config{
					Audience: "example.test", Keys: [][]byte{testKey}, MaxNumber: 500,
					Seen: busySeen{}, Now: c.Now,
				})
				if err != nil {
					panic(err)
				}
				c.PoW = svc
			},
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				return e.post(t, map[string]any{
					"name": secretName, "email": secretEmail, "message": secretMessage,
					"pow": e.challenge(t, e.h.pow),
				})
			},
			status: 503, code: CodePowBusy,
		},
		{
			name: "global ceiling",
			mod:  func(c *Config) { c.MailLimiter = limiter(&clock{t: time.Unix(1_800_000_000, 0)}, 0, time.Hour) },
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				return e.post(t, e.body(t))
			},
			status: 503, code: CodeMailLimit,
		},
		{
			name: "send fails",
			mod: func(c *Config) {
				c.Sender = &fakeSender{err: &mail.Error{Stage: "rcpt", Code: 550, Reply: "no mailbox " + secretEmail}}
			},
			run:    func(t *testing.T, e *env) *httptest.ResponseRecorder { return e.post(t, e.body(t)) },
			status: 503, code: CodeSendFailed,
		},
		{
			name: "no keys",
			mod:  func(c *Config) { c.PoW = nil },
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				return e.post(t, map[string]any{"name": secretName, "email": secretEmail, "message": secretMessage})
			},
			status: 503, code: CodeNotConfigured,
		},
		{
			name: "no sender",
			mod:  func(c *Config) { c.Sender = nil },
			run:  func(t *testing.T, e *env) *httptest.ResponseRecorder { return e.post(t, e.body(t)) },

			status: 503, code: CodeNotConfigured,
		},
		{
			name: "wrong method",
			run: func(t *testing.T, e *env) *httptest.ResponseRecorder {
				r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/contact", strings.NewReader("{}"))
				w := httptest.NewRecorder()
				e.h.Submit().ServeHTTP(w, r)
				return w
			},
			status: 405, code: CodeMethodDisallow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, tc.mod)
			w := tc.run(t, e)
			if w.Code != tc.status || code(t, w) != tc.code {
				t.Fatalf("answer = %d %s, want %d %s", w.Code, w.Body, tc.status, tc.code)
			}
			if tc.name != "replayed challenge" && e.sender.count() != 0 {
				t.Fatal("a mail was sent")
			}
			if w.Header().Get("Set-Cookie") != "" {
				t.Fatal("a cookie was set")
			}
			// Every refusal body is free of the visitor's data too.
			if strings.Contains(w.Body.String(), "Quillfeather") || strings.Contains(w.Body.String(), "zora") {
				t.Fatalf("answer echoes the content: %s", w.Body)
			}
			e.assertNoContentInLogs(t)
		})
	}
}

// busySeen is a full replay set.
type busySeen struct{}

func (busySeen) Use(string, time.Time, time.Time) error { return pow.ErrBusy }

func TestFieldErrorsAreCodesByField(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	b := e.body(t)
	b["name"] = "a\r\nBcc: attacker@example.org"
	b["email"] = "zora@bücher.example"
	b["message"] = strings.Repeat("x", 5001)
	w := e.post(t, b)
	var got errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": CodeInvalidChars, "email": CodeNonASCIIEmail, "message": CodeTooLong}
	if w.Code != 400 || len(got.Error.Details) != 3 {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	for k, v := range want {
		if got.Error.Details[k] != v {
			t.Fatalf("details = %v, want %v", got.Error.Details, want)
		}
	}
	if e.sender.count() != 0 {
		t.Fatal("a mail was sent")
	}
}

func TestValidationDoesNotBurnTheSolution(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	b := e.body(t)
	good := b["message"]
	b["message"] = ""
	if w := e.post(t, b); w.Code != 400 || code(t, w) != "invalid_argument" {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	b["message"] = good
	if w := e.post(t, b); w.Code != 200 {
		t.Fatalf("second = %d %s", w.Code, w.Body)
	}
}

func TestHoneypotAnswersLikeSuccess(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	ok := e.post(t, e.body(t))
	b := e.body(t)
	b["website"] = "https://spam.example"
	trap := e.post(t, b)
	if trap.Code != ok.Code || trap.Body.String() != ok.Body.String() {
		t.Fatalf("honeypot %d %q differs from success %d %q", trap.Code, trap.Body, ok.Code, ok.Body)
	}
	if !reflect.DeepEqual(trap.Header(), ok.Header()) {
		t.Fatalf("honeypot headers %v differ from success headers %v", trap.Header(), ok.Header())
	}
	if e.sender.count() != 1 {
		t.Fatalf("sent %d mails, want 1", e.sender.count())
	}
	e.assertNoContentInLogs(t)
}

func TestDecodeFailureDoesNotEchoTheRequest(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"unknown field": `{"leak_Quillfeather_field": 1}`,
		"wrong type":    `{"email_Quillfeather": 1, "name": 5}`,
		"cut off":       `{"name": "Quillfeather"`,
	} {
		e := newEnv(t, nil)
		w := e.post(t, body)
		var got struct {
			Error struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if w.Code != 400 || got.Error.Code != "invalid_argument" || got.Error.Message != "body is not a valid submission" ||
			len(got.Error.Details) != 0 {
			t.Fatalf("%s: answer = %d %s", name, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "Quillfeather") || strings.Contains(w.Body.String(), "name") ||
			strings.Contains(w.Body.String(), "email") {
			t.Fatalf("%s: answer echoes the request: %s", name, w.Body)
		}
		e.assertNoContentInLogs(t)
	}
}

func TestSendSurvivesACancelledRequest(t *testing.T) {
	t.Parallel()
	var sawErr error
	var sawDeadline bool
	e := newEnv(t, func(c *Config) {
		c.Sender = senderFunc(func(ctx context.Context, _ mail.Message) error {
			sawErr = ctx.Err()
			_, sawDeadline = ctx.Deadline()
			return nil
		})
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := e.post(t, e.body(t), func(r *http.Request) { *r = *r.WithContext(ctx) })
	if w.Code != 200 {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if sawErr != nil || !sawDeadline {
		t.Fatalf("send context: err = %v, deadline = %v", sawErr, sawDeadline)
	}
}

type senderFunc func(context.Context, mail.Message) error

func (f senderFunc) Send(ctx context.Context, m mail.Message) error { return f(ctx, m) }

func TestCustomSenderErrorWithTheAddressIsNotLogged(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) {
		c.Sender = senderFunc(func(_ context.Context, m mail.Message) error {
			return fmt.Errorf("relay refused <%s>: %s", m.ReplyTo, m.Body)
		})
	})
	w := e.post(t, e.body(t))
	if w.Code != 503 || code(t, w) != CodeSendFailed {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if !strings.Contains(e.logs.String(), "*fmt.wrapError") && !strings.Contains(e.logs.String(), "*errors.errorString") {
		t.Fatalf("the error type is not logged:\n%s", e.logs)
	}
	e.assertNoContentInLogs(t)
}

func TestPanickingSiteFunctionsAreRecoveredWithoutContent(t *testing.T) {
	t.Parallel()
	for name, mod := range map[string]func(*Config){
		"subject": func(c *Config) { c.Subject = func(r Request) string { panic("bad subject for " + r.Email) } },
		"body":    func(c *Config) { c.Body = func(r Request) string { panic(fmt.Errorf("bad body %q", r.Message)) } },
	} {
		e := newEnv(t, mod)
		w := e.post(t, e.body(t))
		if w.Code != 503 || code(t, w) != CodeSendFailed || e.sender.count() != 0 {
			t.Fatalf("%s: answer = %d %s", name, w.Code, w.Body)
		}
		if !strings.Contains(e.logs.String(), "panicked with") {
			t.Fatalf("%s: the panic is not logged:\n%s", name, e.logs)
		}
		e.assertNoContentInLogs(t)
	}
}

// asErr is an error whose As method tries to write text into any target.
type asErr struct{}

func (asErr) Error() string { return secretEmail }

func (asErr) As(target any) bool {
	switch t := target.(type) {
	case *string:
		*t = secretEmail
		return true
	case **mail.Error:
		*t = &mail.Error{Stage: secretEmail, Code: 7}
		return true
	}
	return false
}

func TestSendErrorLogsOnlyFixedText(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		err  error
		want string
	}{
		"invalid message":   {fmt.Errorf("%w: "+secretEmail+" "+secretMessage, mail.ErrInvalid), "mail: invalid message"},
		"no starttls":       {fmt.Errorf("%w for "+secretName, mail.ErrNoStartTLS), "mail: server does not offer STARTTLS"},
		"no auth":           {fmt.Errorf("%w for "+secretName, mail.ErrNoAuth), "mail: server offers no usable AUTH"},
		"deadline":          {fmt.Errorf("dial "+secretEmail+": %w", context.DeadlineExceeded), "send timed out"},
		"unknown stage":     {&mail.Error{Stage: secretEmail, Code: 5}, "stage unknown code 5"},
		"known stage":       {&mail.Error{Stage: "data", Code: 451, Reply: secretEmail}, "stage data code 451"},
		"custom As":         {asErr{}, "stage unknown code 7"},
		"typed nil":         {(*mail.Error)(nil), "send failed with *mail.Error"},
		"plain error":       {errors.New(secretEmail), "send failed with *errors.errorString"},
		"wrapped mail.Err.": {fmt.Errorf("relay: %w", &mail.Error{Stage: "rcpt", Code: 550}), "stage rcpt code 550"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, func(c *Config) { c.Sender = &fakeSender{err: tc.err} })
			w := e.post(t, e.body(t))
			if w.Code != 503 || code(t, w) != CodeSendFailed {
				t.Fatalf("answer = %d %s", w.Code, w.Body)
			}
			if !strings.Contains(e.logs.String(), tc.want) {
				t.Fatalf("log lacks %q:\n%s", tc.want, e.logs)
			}
			e.assertNoContentInLogs(t)
		})
	}
}

func TestPanickingSenderIsRecoveredWithoutContent(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) {
		c.Sender = senderFunc(func(_ context.Context, m mail.Message) error {
			panic(fmt.Errorf("boom for %s: %s", m.ReplyTo, m.Body))
		})
	})
	w := e.post(t, e.body(t))
	if w.Code != 503 || code(t, w) != CodeSendFailed {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if !strings.Contains(e.logs.String(), "Sender.Send panicked with") {
		t.Fatalf("the panic is not logged:\n%s", e.logs)
	}
	e.assertNoContentInLogs(t)
}

func TestAbortHandlerPanicPassesThrough(t *testing.T) {
	t.Parallel()
	for name, mod := range map[string]func(*Config){
		"sender": func(c *Config) {
			c.Sender = senderFunc(func(context.Context, mail.Message) error { panic(http.ErrAbortHandler) })
		},
		"subject": func(c *Config) { c.Subject = func(Request) string { panic(http.ErrAbortHandler) } },
		"body":    func(c *Config) { c.Body = func(Request) string { panic(http.ErrAbortHandler) } },
	} {
		e := newEnv(t, mod)
		b := e.body(t)
		var got any
		func() {
			defer func() { got = recover() }()
			e.post(t, b)
		}()
		if got != http.ErrAbortHandler { //nolint:errorlint // the sentinel itself is re-panicked
			t.Fatalf("%s: recovered %v, want http.ErrAbortHandler", name, got)
		}
	}
}

func TestHoneypotComesBeforeTheClientLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	for range 20 {
		w := e.post(t, map[string]any{"website": "x"})
		if w.Code != 200 {
			t.Fatalf("honeypot = %d %s", w.Code, w.Body)
		}
	}
	if w := e.post(t, e.body(t)); w.Code != 200 {
		t.Fatalf("real submission after honeypots = %d %s", w.Code, w.Body)
	}
}

func TestClientLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	for i := range 5 {
		// An invalid submission counts as an attempt.
		if w := e.post(t, map[string]any{"name": ""}); w.Code != 400 {
			t.Fatalf("attempt %d = %d %s", i, w.Code, w.Body)
		}
	}
	w := e.post(t, e.body(t))
	if w.Code != 429 || code(t, w) != "too_many_requests" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("sixth = %d %s", w.Code, w.Body)
	}
	// Another client has its own budget.
	if w := e.post(t, e.body(t), from("198.51.100.7:1")); w.Code != 200 {
		t.Fatalf("other client = %d %s", w.Code, w.Body)
	}
	// Time refills it.
	e.clock.advance(3 * time.Minute)
	if w := e.post(t, e.body(t)); w.Code != 200 {
		t.Fatalf("after wait = %d %s", w.Code, w.Body)
	}
	e.assertNoContentInLogs(t)
}

func TestForwardedHeaderIsIgnoredWithoutTrustedProxy(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	for i := range 5 {
		e.post(t, map[string]any{"name": ""}, header("X-Forwarded-For", "10.0.0."+string(rune('1'+i))))
	}
	if w := e.post(t, e.body(t), header("X-Forwarded-For", "10.9.9.9")); w.Code != 429 {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
}

func TestGlobalCeilingIsNeverRefunded(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) {
		c.MailLimiter = nil
		c.Sender = &fakeSender{err: errors.New("smtp down")}
	})
	e.h.mails = limiter(e.clock, 2, time.Hour)
	for range 2 {
		if w := e.post(t, e.body(t)); w.Code != 503 || code(t, w) != CodeSendFailed {
			t.Fatalf("answer = %d %s", w.Code, w.Body)
		}
	}
	// Failed sends took the tokens. The next one is refused before a send.
	if w := e.post(t, e.body(t)); w.Code != 503 || code(t, w) != CodeMailLimit {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
}

func TestCeilingWarningIsThrottledAndHoldsNoContent(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) { c.MailLimiter = limiter(&clock{t: time.Unix(1_800_000_000, 0)}, 0, time.Hour) })
	count := func() int { return strings.Count(e.logs.String(), `"level":"WARN"`) }
	for range 3 {
		e.post(t, e.body(t), from("198.51.100.1:1"))
	}
	if count() != 1 {
		t.Fatalf("%d warnings, want 1:\n%s", count(), e.logs)
	}
	e.clock.advance(2 * time.Minute)
	e.post(t, e.body(t), from("198.51.100.2:1"))
	if count() != 2 {
		t.Fatalf("%d warnings, want 2", count())
	}
	e.assertNoContentInLogs(t)
}

func TestSendFailureLogsTheCauseNotTheContent(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) {
		c.Sender = &fakeSender{err: &mail.Error{Stage: "rcpt", Code: 550, Reply: "no mailbox " + secretEmail}}
	})
	w := e.post(t, e.body(t))
	if w.Code != 503 || code(t, w) != CodeSendFailed {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if !strings.Contains(e.logs.String(), "stage rcpt code 550") {
		t.Fatalf("the cause is not logged:\n%s", e.logs)
	}
	e.assertNoContentInLogs(t)
}

func TestHeaderInjectionIsRefused(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a\r\nBcc: x@example.org", "a\nb", "a\rb", "a\x00b", "a\x1bb"} {
		e := newEnv(t, nil)
		b := e.body(t)
		b["name"] = name
		w := e.post(t, b)
		if w.Code != 400 || e.sender.count() != 0 {
			t.Fatalf("name %q = %d %s, sent %d", name, w.Code, w.Body, e.sender.count())
		}
	}
	e := newEnv(t, nil)
	b := e.body(t)
	b["email"] = "a@example.org\r\nBcc: x@example.org"
	if w := e.post(t, b); w.Code != 400 || e.sender.count() != 0 {
		t.Fatalf("email = %d %s", w.Code, w.Body)
	}
}

func TestSubjectFromTheSiteIsCheckedByMail(t *testing.T) {
	t.Parallel()
	real := &mailBuilder{}
	e := newEnv(t, func(c *Config) {
		c.Sender = real
		c.Subject = func(r Request) string { return "From " + r.Name }
	})
	b := e.body(t)
	b["name"] = "Zora"
	if w := e.post(t, b); w.Code != 200 {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if real.err != nil {
		t.Fatal(real.err)
	}
}

// mailBuilder runs the real message builder without a connection.
type mailBuilder struct{ err error }

func (m *mailBuilder) Send(_ context.Context, msg mail.Message) error {
	_, m.err = mail.Build(msg)
	return m.err
}

func TestTrustedOriginIsAccepted(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(c *Config) { c.TrustedOrigins = []string{"https://app.example.test"} })
	w := e.post(t, e.body(t), header("Sec-Fetch-Site", ""), header("Origin", "https://app.example.test"))
	if w.Code != 200 {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
}

func TestNewRefusesABadConfig(t *testing.T) {
	t.Parallel()
	for name, mod := range map[string]func(*Config){
		"bad origin": func(c *Config) { c.TrustedOrigins = []string{"example.test/"} },
		"bad from":   func(c *Config) { c.From = "not an address" },
		"no to":      func(c *Config) { c.To = nil },
		"to injects": func(c *Config) { c.To = []string{"a@example.org\r\nBcc: x@example.org"} },
	} {
		cfg := Config{Sender: &fakeSender{}, From: "a@example.test", To: []string{"b@example.test"}}
		mod(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestNotConfiguredStillStarts(t *testing.T) {
	t.Parallel()
	h, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if h.Configured() {
		t.Fatal("Configured")
	}
	w := httptest.NewRecorder()
	h.Challenge().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/challenge", http.NoBody))
	if w.Code != 503 || code(t, w) != CodeNotConfigured {
		t.Fatalf("challenge = %d %s", w.Code, w.Body)
	}
}

func (e *env) getChallenge(t *testing.T, addr string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/challenge", http.NoBody)
	r.RemoteAddr = addr
	w := httptest.NewRecorder()
	e.h.Challenge().ServeHTTP(w, r)
	return w
}

func TestChallengeIsSignedAndLimitedPerClient(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := e.getChallenge(t, "203.0.113.9:1")
	var c pow.Challenge
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || w.Code != 200 {
		t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
	if c.Algorithm != pow.Algorithm || len(c.Signature) != 64 || c.Salt == "" {
		t.Fatalf("challenge = %+v", c)
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", w.Header())
	}
	for range 9 {
		if w := e.getChallenge(t, "203.0.113.9:1"); w.Code != 200 {
			t.Fatalf("answer = %d", w.Code)
		}
	}
	w = e.getChallenge(t, "203.0.113.9:1")
	if w.Code != 429 || code(t, w) != "too_many_requests" {
		t.Fatalf("eleventh = %d %s", w.Code, w.Body)
	}
	if w := e.getChallenge(t, "203.0.113.10:1"); w.Code != 200 {
		t.Fatalf("other client = %d", w.Code)
	}
	e.clock.advance(20 * time.Second)
	if w := e.getChallenge(t, "203.0.113.9:1"); w.Code != 200 {
		t.Fatalf("after wait = %d", w.Code)
	}
}

func TestChallengeRefusesOtherMethods(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/challenge", http.NoBody)
	w := httptest.NewRecorder()
	e.h.Challenge().ServeHTTP(w, r)
	if w.Code != 405 || w.Header().Get("Allow") != "GET" {
		t.Fatalf("answer = %d", w.Code)
	}
}

func TestDefaultLimitersAreBounded(t *testing.T) {
	t.Parallel()
	if MaxLimiterKeys != 100_000 || DefaultMaxBodyBytes != 64<<10 {
		t.Fatal("limits changed")
	}
	h, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if h.clients == nil || h.mails == nil || h.maxBody != 64<<10 {
		t.Fatal("defaults missing")
	}
}

// TestNoContentInAnyLogPath drives every path with the visitor's data in the
// body and checks that no log line holds it, with debug lines on.
func TestNoContentInAnyLogPath(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.post(t, e.body(t))                                                                                  // success
	e.post(t, e.body(t), header("Sec-Fetch-Site", "cross-site"))                                          // origin
	e.post(t, e.body(t), header("Content-Type", "text/plain"))                                            // media type
	e.post(t, `{"name":"`+secretName+`","unknown_`+secretMessage+`":1}`)                                  // unknown field
	e.post(t, `{"name":"`+secretName+`", "email": 5}`)                                                    // wrong type
	e.post(t, `{"name":"`+secretName+`"`)                                                                 // cut off
	e.post(t, map[string]any{"name": secretName + "\n", "email": secretEmail, "message": "", "pow": nil}) // fields
	e.post(t, map[string]any{"name": secretName, "email": secretEmail, "message": secretMessage})         // no pow
	b := e.body(t)
	e.post(t, b)
	e.post(t, b) // replay
	b = e.body(t)
	b["website"] = secretMessage
	e.post(t, b) // honeypot
	for range 6 {
		e.post(t, e.body(t), from("198.51.100.99:1")) // client limit
	}
	if e.logs.Len() == 0 {
		t.Fatal("no log output captured")
	}
	e.assertNoContentInLogs(t)

	f := newEnv(t, func(c *Config) { c.Sender = &fakeSender{err: errors.New("smtp: connection refused")} })
	f.post(t, f.body(t)) // failed send
	if !strings.Contains(f.logs.String(), "send failed with *errors.errorString") {
		t.Fatalf("cause missing:\n%s", f.logs)
	}
	f.assertNoContentInLogs(t)
}
