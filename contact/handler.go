// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package contact

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/clientip"
	"github.com/ostendorf-cloud/govue/csrf"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/mail"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/pow"
	"github.com/ostendorf-cloud/govue/ratelimit"
)

// The stable codes of the top level error of a refusal, next to the codes
// of the api package that the handlers also use: invalid_argument,
// unsupported_media_type, request_too_large and too_many_requests.
const (
	CodeCrossOrigin    = "cross_origin"
	CodeNotConfigured  = "not_configured"
	CodePowRequired    = "pow_required"
	CodePowInvalid     = "pow_invalid"
	CodePowExpired     = "pow_expired"
	CodePowTooFast     = "pow_too_fast"
	CodePowReplay      = "pow_replay"
	CodePowBusy        = "pow_busy"
	CodeMailLimit      = "mail_limit"
	CodeSendFailed     = "send_failed"
	CodeMethodDisallow = "method_not_allowed"
)

// Defaults of [Config].
const (
	// DefaultMaxBodyBytes is the body cap of a submission.
	DefaultMaxBodyBytes int64 = 64 << 10
	// DefaultSendTimeout bounds one send, over the dial and the conversation.
	DefaultSendTimeout = 30 * time.Second
	// MaxLimiterKeys bounds the keys of the limiters this package builds.
	MaxLimiterKeys = 100_000

	// DefaultClientBurst and DefaultClientPer are the submissions of one client.
	DefaultClientBurst = 5
	DefaultClientPer   = 10 * time.Minute
	// DefaultMailBurst and DefaultMailPer are the mails of the whole process.
	DefaultMailBurst = 50
	DefaultMailPer   = time.Hour
	// DefaultChallengeBurst and DefaultChallengePer are the challenges one
	// client may fetch.
	DefaultChallengeBurst = 10
	DefaultChallengePer   = 20 * time.Second
)

// warnEvery spaces the warning that the mail ceiling is reached.
const warnEvery = time.Minute

// mailKey is the one key of the global mail limiter.
const mailKey = "mail"

// Config configures a [Handler]. Every limiter is per process. With several
// replicas the budgets multiply.
type Config struct {
	// PoW issues and verifies the challenges. Nil means the keys are not
	// configured: both routes answer 503 not_configured and the process starts.
	PoW *pow.Service
	// Sender sends the mail. Nil means SMTP is not configured: Submit answers
	// 503 not_configured. A typed nil pointer in the interface is not nil.
	// Send must not panic and must not put visitor data in its error text. A
	// panic is recovered here and logged by the type of its value only, and an
	// error is logged by its type unless it is a *mail.Error or one of the
	// mail sentinels.
	Sender mail.Sender
	// From is the fixed sender, on the site's own domain. To is the fixed
	// recipient list. Both are required when Sender is set, and both are
	// checked by [New]. Neither ever comes from a request.
	From string
	To   []string

	// Limiter limits Submit per client. Nil means 5 per 10 minutes and
	// [MaxLimiterKeys] keys.
	//
	// The default allows 30 submissions per hour per key, and the default
	// MailLimiter ceiling is 50 per hour. Only a verified proof of work takes a
	// ceiling token, so a handful of addresses can keep the form refused: four
	// IPv4 addresses or one IPv6 /62 (the key is the /64) that solve 50
	// challenges per hour are enough. A site that expects this lowers Limiter
	// and raises MailLimiter. A full limiter evicts an arbitrary active bucket
	// and so fails open for that key, and one IPv6 /48 holds 65536 /64 keys
	// that can fill a limiter of [MaxLimiterKeys].
	Limiter *ratelimit.Limiter
	// MailLimiter is the global ceiling on sent mails. A mail takes a token
	// whether or not the send succeeds, so a failing server cannot be used to
	// retry without limit. Nil means 50 per hour. The ceiling is the control
	// against a flood of mail and also the way a few valid clients lock the
	// form out for everyone, see Limiter. The refusal logs one throttled
	// warning per minute, which a site should watch.
	MailLimiter *ratelimit.Limiter
	// ChallengeLimiter limits the challenge route per client. Nil means 10 per
	// 20 seconds and [MaxLimiterKeys] keys.
	ChallengeLimiter *ratelimit.Limiter
	// ClientIP resolves the client address. Nil trusts no proxy header.
	ClientIP *clientip.Resolver

	// TrustedOrigins are cross origin callers Submit accepts, for a frontend
	// on another host. Empty trusts the own origin only.
	TrustedOrigins []string

	// Logger receives the log lines. They hold codes, the stage and code of a
	// mail error and type names, never a name, an address or message text.
	// That holds for this package only: a Subject, Body or Sender of the site
	// must keep to it. Nil means the logger of the request context.
	Logger *slog.Logger

	// Subject and Body build the mail from a valid request. Nil means a fixed
	// subject without the name and a body with the three fields. The subject
	// goes through the mail package's checks, and so does everything else.
	// Both run after the ceiling token is taken, so a refused message burns a
	// token. They must not panic and must not put visitor input unchecked into
	// a header. A panic is recovered here, logged by its type only, and answers
	// 503 send_failed, because the value of a panic can hold visitor content.
	Subject func(Request) string
	Body    func(Request) string

	// MaxBodyBytes is the body cap. Zero means [DefaultMaxBodyBytes].
	MaxBodyBytes int64
	// SendTimeout bounds a send. Zero means [DefaultSendTimeout]. The send is
	// not cancelled when the visitor leaves. Keep it below the server's write
	// timeout (30 s by default in httpserver), or a slow send can finish after
	// the answer can no longer be written. A send still running at shutdown can
	// be cut off when the shutdown timeout (20 s by default) ends.
	SendTimeout time.Duration
	// Now is the clock of the warning throttle. Nil means time.Now.
	Now func() time.Time
}

// Handler serves the contact form. It is safe for concurrent use.
type Handler struct {
	pow        *pow.Service
	sender     mail.Sender
	from       string
	to         []string
	clients    *ratelimit.Limiter
	mails      *ratelimit.Limiter
	clientIP   *clientip.Resolver
	origin     *csrf.Protector
	logger     *slog.Logger
	subject    func(Request) string
	body       func(Request) string
	maxBody    int64
	timeout    time.Duration
	now        func() time.Time
	challenge  http.Handler
	configured bool

	mu       sync.Mutex
	lastWarn time.Time
}

// New validates cfg. Missing keys or a missing sender are not an error: the
// handlers answer 503 until they are set, so a site boots without them. A bad
// From, To or trusted origin is an error.
func New(cfg Config) (*Handler, error) {
	h := &Handler{
		pow:      cfg.PoW,
		sender:   cfg.Sender,
		from:     cfg.From,
		to:       append([]string(nil), cfg.To...),
		clients:  cfg.Limiter,
		mails:    cfg.MailLimiter,
		clientIP: cfg.ClientIP,
		logger:   cfg.Logger,
		subject:  cfg.Subject,
		body:     cfg.Body,
		maxBody:  cfg.MaxBodyBytes,
		timeout:  cfg.SendTimeout,
		now:      cfg.Now,
	}
	if h.clients == nil {
		h.clients = ratelimit.New(DefaultClientBurst, DefaultClientPer, MaxLimiterKeys)
	}
	if h.mails == nil {
		h.mails = ratelimit.New(DefaultMailBurst, DefaultMailPer, 1)
	}
	challenges := cfg.ChallengeLimiter
	if challenges == nil {
		challenges = ratelimit.New(DefaultChallengeBurst, DefaultChallengePer, MaxLimiterKeys)
	}
	if h.clientIP == nil {
		h.clientIP = clientip.NewResolver(nil)
	}
	if h.subject == nil {
		h.subject = func(Request) string { return "New message from the contact form" }
	}
	if h.body == nil {
		h.body = defaultBody
	}
	if h.maxBody <= 0 {
		h.maxBody = DefaultMaxBodyBytes
	}
	if h.timeout <= 0 {
		h.timeout = DefaultSendTimeout
	}
	if h.now == nil {
		h.now = time.Now
	}

	origin, err := csrf.New(func(*http.Request) (string, bool) { return "", false })
	if err != nil {
		return nil, fmt.Errorf("contact: origin check: %w", err)
	}
	for _, o := range cfg.TrustedOrigins {
		if err := origin.TrustOrigin(o); err != nil {
			return nil, fmt.Errorf("contact: %w", err)
		}
	}
	h.origin = origin

	if h.sender != nil {
		// A dry run with the site's own envelope, so a bad From or To fails
		// at startup and not on the first visitor.
		if _, err := mail.Build(mail.Message{From: h.from, To: h.to, Subject: "x", Body: "x"}); err != nil {
			return nil, fmt.Errorf("contact: envelope: %w", err)
		}
	}
	h.configured = h.pow != nil && h.sender != nil && h.from != "" && len(h.to) > 0

	limit, err := middleware.NewRateLimit(middleware.RateLimitConfig{Limiter: challenges, ClientIP: h.clientIP})
	if err != nil {
		return nil, fmt.Errorf("contact: challenge limit: %w", err)
	}
	h.challenge = limit.Middleware(http.HandlerFunc(h.serveChallenge))

	if !h.configured && h.logger != nil {
		h.logger.Warn("contact form not configured: routes answer 503",
			"pow", h.pow != nil, "sender", h.sender != nil)
	}
	return h, nil
}

// Configured reports whether keys, sender and envelope are all set. When it
// is false both routes answer 503 not_configured.
func (h *Handler) Configured() bool { return h.configured }

// Challenge returns the GET handler that issues a signed challenge. It is
// limited per client already, so the route needs no limiter of its own.
func (h *Handler) Challenge() http.Handler { return h.challenge }

// Submit returns the POST handler that validates a submission and sends the
// mail. It checks, in this order: the origin, the configuration, the content
// type and body cap, the honeypot, the per client limit, the fields, the
// proof of work, the global mail ceiling, and then it sends.
func (h *Handler) Submit() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h.submit(w, r); err != nil {
			h.fail(w, r, err)
		}
	})
}

// submission is the JSON body. Website is the honeypot, a field a person
// never sees and never fills.
type submission struct {
	Request
	Website string        `json:"website"`
	PoW     *pow.Solution `json:"pow"`
}

type okBody struct {
	OK bool `json:"ok"`
}

func (h *Handler) serveChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.fail(w, r, methodNotAllowed(w, http.MethodGet))
		return
	}
	if h.pow == nil {
		h.fail(w, r, notConfigured())
		return
	}
	c, err := h.pow.Create()
	if err != nil {
		h.fail(w, r, api.Internal(fmt.Errorf("contact: challenge: %w", err)))
		return
	}
	if err := api.Write(w, http.StatusOK, c); err != nil {
		h.log(r).DebugContext(r.Context(), "contact: write failed")
	}
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, http.MethodPost)
	}
	if err := h.origin.Check(r); err != nil {
		return api.New(http.StatusForbidden, CodeCrossOrigin, "cross origin request refused")
	}
	if !h.configured {
		return notConfigured()
	}
	// api.DecodeLimit lets a missing content type pass, a public form does not.
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return api.New(http.StatusUnsupportedMediaType, api.CodeUnsupportedMediaType,
			"content type must be application/json")
	}
	var sub submission
	if err := api.DecodeLimit(r, &sub, h.maxBody); err != nil {
		return decodeFailure(err)
	}
	if sub.Website != "" {
		// The honeypot answers like a success and does nothing. It is no signal.
		return h.ok(w)
	}
	ok, wait := h.clients.Take(clientip.Key(h.clientIP.Resolve(r)))
	if !ok {
		return api.TooManyRequests(wait)
	}
	req := Normalize(sub.Request)
	if fields := Validate(req); fields != nil {
		details := make(map[string]any, len(fields))
		for k, v := range fields {
			details[k] = v
		}
		return api.Invalid("some fields are invalid").WithDetails(details)
	}
	if sub.PoW == nil {
		return coded(api.Invalid("proof of work is required"), CodePowRequired)
	}
	if err := h.pow.Verify(*sub.PoW); err != nil {
		return powFailure(err)
	}
	if ok, wait := h.mails.Take(mailKey); !ok {
		h.warnCeiling(r)
		e := api.Unavailable("the form is busy, please try again later").Exposed()
		e.Code, e.RetryAfter = CodeMailLimit, wait
		return e
	}
	var msg mail.Message
	if cause := guard("a Subject or Body function", func() { msg = h.message(req) }); cause != nil {
		return sendFailed(cause)
	}
	// The visitor leaving does not cancel a send that may already be on the
	// wire. The timeout ends it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), h.timeout)
	defer cancel()
	var sendErr error
	if cause := guard("Sender.Send", func() { sendErr = h.sender.Send(ctx, msg) }); cause != nil {
		return sendFailed(cause)
	}
	if sendErr != nil {
		return sendFailed(safeCause(sendErr))
	}
	return h.ok(w)
}

// message builds the mail from the site's functions.
func (h *Handler) message(req Request) mail.Message {
	return mail.Message{
		From:    h.from,
		To:      h.to,
		ReplyTo: req.Email,
		Subject: h.subject(req),
		Body:    h.body(req),
		Headers: map[string]string{"Auto-Submitted": "auto-generated"},
	}
}

// guard runs fn and turns a panic into a cause that names the type of the
// panic value and nothing else: the value can hold the visitor's content, and
// the Recover middleware would log it. http.ErrAbortHandler is another
// handler's signal to abort the response and is re-panicked unchanged, as
// middleware.Recover does.
func guard(what string, fn func()) (cause error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
			panic(p)
		}
		cause = loggedCause(what + " panicked with " + fmt.Sprintf("%T", p))
	}()
	fn()
	return nil
}

// loggedCause is a cause whose text is safe for a log line. Only this
// package builds one, from fixed text and type names.
type loggedCause string

func (c loggedCause) Error() string { return string(c) }

// sendFailed answers 503 send_failed with a cause that is already safe.
func sendFailed(cause error) error {
	e := api.Unavailable("the message could not be sent").WithCause(cause).Exposed()
	e.Code = CodeSendFailed
	return e
}

// stages are the stage names of the mail package, the only ones a log line
// may carry. Any other value logs as "unknown".
var stages = map[string]bool{
	"dial": true, "tls": true, "greeting": true, "ehlo": true, "starttls": true,
	"auth": true, "mail": true, "rcpt": true, "data": true, "send": true,
}

// safeCause is the text of a send error that may reach a log: the allowed
// stage and the code of a *mail.Error, the fixed text of a mail sentinel, and
// for any other error the Go type name only, because a custom Sender's error
// can hold the visitor's address. It never calls Error on the error, and it
// survives an error whose methods panic, such as a typed nil *mail.Error.
func safeCause(err error) (cause error) {
	defer func() {
		if recover() != nil {
			cause = loggedCause(fmt.Sprintf("send failed with %T", err))
		}
	}()
	var me *mail.Error
	switch {
	case errors.As(err, &me) && me != nil:
		stage := "unknown"
		if stages[me.Stage] {
			stage = me.Stage
		}
		return loggedCause(fmt.Sprintf("mail: smtp stage %s code %d", stage, me.Code))
	case errors.Is(err, mail.ErrInvalid):
		return loggedCause(mail.ErrInvalid.Error())
	case errors.Is(err, mail.ErrNoStartTLS):
		return loggedCause(mail.ErrNoStartTLS.Error())
	case errors.Is(err, mail.ErrNoAuth):
		return loggedCause(mail.ErrNoAuth.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return loggedCause("send timed out")
	}
	return loggedCause(fmt.Sprintf("send failed with %T", err))
}

// ok writes the one success answer, which the honeypot shares. A failed
// write means the client is gone and has no second way to be told.
func (h *Handler) ok(w http.ResponseWriter) error {
	_ = api.Write(w, http.StatusOK, okBody{OK: true})
	return nil
}

// coded sets the code of a freshly built error.
func coded(e *api.Error, code string) *api.Error {
	e.Code = code
	return e
}

// defaultBody is the plain text body when Config.Body is nil.
func defaultBody(r Request) string {
	return "Name: " + r.Name + "\nEmail: " + r.Email + "\n\n" + r.Message + "\n"
}

// decodeFailure keeps the size and media type answers of the api package and
// replaces every other text, which can name a field of the request.
func decodeFailure(err error) error {
	e := api.AsError(err)
	switch e.Status {
	case http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		return api.New(e.Status, e.Code, e.Message)
	default:
		return api.Invalid("body is not a valid submission")
	}
}

func powFailure(err error) error {
	switch {
	case errors.Is(err, pow.ErrExpired):
		return coded(api.Invalid("the challenge expired"), CodePowExpired)
	case errors.Is(err, pow.ErrTooFast):
		return coded(api.Invalid("the challenge was solved too fast"), CodePowTooFast)
	case errors.Is(err, pow.ErrReplay):
		return coded(api.Invalid("the challenge was used before"), CodePowReplay)
	case errors.Is(err, pow.ErrBusy):
		e := api.Unavailable("the form is busy, please try again later").Exposed()
		e.Code = CodePowBusy
		return e
	default:
		return coded(api.Invalid("the proof of work is invalid"), CodePowInvalid)
	}
}

func notConfigured() error {
	e := api.Unavailable("the contact form is not available").Exposed()
	e.Code = CodeNotConfigured
	return e
}

func methodNotAllowed(w http.ResponseWriter, allow string) error {
	w.Header().Set("Allow", allow)
	return api.New(http.StatusMethodNotAllowed, CodeMethodDisallow, "method not allowed")
}

// fail writes the error shape. A refusal logs its code and status only. An
// error with a cause logs the cause, which by construction holds no field of
// the form: a send error is the mail package's, naming a stage and a code.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	e := api.AsError(err)
	logger := h.log(r)
	if errors.Unwrap(e) != nil {
		logger.ErrorContext(r.Context(), "contact: request failed", "code", e.Code, "status", e.Status, "error", e.Error())
	} else {
		logger.DebugContext(r.Context(), "contact: request refused", "code", e.Code, "status", e.Status)
	}
	api.WriteError(w, r, e)
}

// warnCeiling logs that the global mail ceiling is reached, at most once per
// minute, with no content.
func (h *Handler) warnCeiling(r *http.Request) {
	h.mu.Lock()
	now := h.now()
	due := h.lastWarn.IsZero() || now.Sub(h.lastWarn) >= warnEvery
	if due {
		h.lastWarn = now
	}
	h.mu.Unlock()
	if due {
		h.log(r).WarnContext(r.Context(), "contact: global mail ceiling reached, submissions are refused")
	}
}

func (h *Handler) log(r *http.Request) *slog.Logger {
	if h.logger != nil {
		return h.logger
	}
	return logging.FromContext(r.Context())
}
