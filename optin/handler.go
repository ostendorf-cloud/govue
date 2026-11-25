// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package optin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
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

// The stable codes of a refusal, next to the codes of the api package that
// the handlers also use: invalid_argument, unsupported_media_type,
// request_too_large, too_many_requests and internal.
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
	CodeTokenInvalid   = "token_invalid"
	CodeBusy           = "busy"
	CodeMethodDisallow = "method_not_allowed"
)

// Defaults of [Config].
const (
	// DefaultPendingTTL is the life of an unconfirmed entry.
	DefaultPendingTTL = 48 * time.Hour
	// DefaultMaxSendsPerAddress bounds the confirmation mails of one
	// pending period.
	DefaultMaxSendsPerAddress = 3
	// DefaultResendInterval is the least time between two confirmation mails
	// to one address.
	DefaultResendInterval = 10 * time.Minute
	// DefaultPurgeInterval is the period of RunPurge.
	DefaultPurgeInterval = time.Hour
	// DefaultMaxBodyBytes is the body cap of every route.
	DefaultMaxBodyBytes int64 = 16 << 10
	// DefaultSendTimeout bounds one send.
	DefaultSendTimeout = 10 * time.Second
	// MaxLimiterKeys bounds the keys of the limiters this package builds.
	MaxLimiterKeys = 100_000

	// DefaultClientBurst and DefaultClientPer are the requests of one client.
	DefaultClientBurst = 5
	DefaultClientPer   = 10 * time.Minute
	// DefaultMailBurst and DefaultMailPer are the confirmation mails of the
	// whole process.
	DefaultMailBurst = 50
	DefaultMailPer   = time.Hour
	// DefaultChallengeBurst and DefaultChallengePer are the challenges of one
	// client.
	DefaultChallengeBurst = 10
	DefaultChallengePer   = 20 * time.Second
	// DefaultConfirmBurst and DefaultConfirmPer are the confirm calls of one
	// client.
	DefaultConfirmBurst = 10
	DefaultConfirmPer   = time.Minute
)

// maxURLBytes bounds a configured link so the List-Unsubscribe value with its
// token stays far below the header limit of the mail package.
const maxURLBytes = 512

// mailKey is the one key of the global mail limiter.
const mailKey = "mail"

// warnEvery spaces the warning that the mail ceiling is reached.
const warnEvery = time.Minute

// Content is the subject and the plain text body of one mail. The site
// writes both.
type Content struct {
	Subject string
	Body    string
}

// ConfirmationData is what the site's confirmation mail function gets.
type ConfirmationData struct {
	// Address is the recipient.
	Address string
	// Language is the page language of the request, a hint. It may be empty.
	Language string
	// ConfirmURL is the link that confirms. The body must contain it exactly.
	ConfirmURL string
	// UnsubscribeURL is the link that removes the address and stops every
	// further mail, the "this was not me" link. The body must contain it
	// exactly.
	UnsubscribeURL string
	// Expires is when ConfirmURL stops working.
	Expires time.Time
}

// MailingData is what the site's mailing function gets for one recipient.
type MailingData struct {
	Address        string
	Language       string
	ConsentVersion string
	Confirmed      time.Time
	// UnsubscribeURL is the link that removes the address. The body must
	// contain it exactly.
	UnsubscribeURL string
}

// Config configures a [Handler]. Every limiter is per process. With several
// replicas the budgets multiply, the per address limit in the Store does not.
type Config struct {
	// Secret derives the address key and the unsubscribe key. At least
	// [MinSecretLen] bytes. Nil or empty means not configured: the routes
	// answer 503 not_configured and the process starts. Changing it changes
	// every entry key, so existing entries and suppression markers are no
	// longer found.
	Secret []byte
	// PoW issues and verifies the challenges of Request. Nil means not
	// configured.
	PoW *pow.Service
	// Sender sends the mail. Nil means not configured. Send must not panic
	// and must not put the address in its error text.
	Sender mail.Sender
	// From is the fixed sender on the site's own domain. Required with Sender.
	From string
	// Store keeps the entries. Required.
	Store Store

	// ConsentVersion names the consent text the site's form shows, for
	// example "2026-11-25". It is stored with every request. Required. The
	// site changes it whenever the text changes.
	ConsentVersion string

	// ConfirmURL is the site's confirmation page, UnsubscribeURL its
	// unsubscribe page, OneClickURL the absolute URL of the Unsubscribe
	// route for the List-Unsubscribe header (RFC 8058). The package appends
	// "?token=" and the token. Each is required, https, with a host, no
	// query, no fragment, no user info, no "=" and at most 512 bytes.
	ConfirmURL     string
	UnsubscribeURL string
	OneClickURL    string

	// Confirmation builds the confirmation mail. Required. The body must
	// contain ConfirmURL and UnsubscribeURL of its data exactly, or the mail
	// is not sent. New calls it once with sample data. It must not panic, a
	// panic is recovered and the mail is not sent.
	Confirmation func(ConfirmationData) Content

	// PendingTTL is the life of an unconfirmed entry and its links. Zero
	// means [DefaultPendingTTL].
	PendingTTL time.Duration
	// MaxSendsPerAddress bounds the confirmation mails per pending period.
	// Zero means [DefaultMaxSendsPerAddress].
	MaxSendsPerAddress int
	// ResendInterval is the least time between two confirmation mails to one
	// address. Zero means [DefaultResendInterval].
	ResendInterval time.Duration
	// PurgeInterval is the period of RunPurge. Zero means
	// [DefaultPurgeInterval].
	PurgeInterval time.Duration

	// Limiter limits Request per client. Nil means 5 per 10 minutes.
	Limiter *ratelimit.Limiter
	// MailLimiter is the global ceiling. Every valid request takes a token
	// before its state is known, so the ceiling tells nothing about one
	// address. Nil means 50 per hour.
	MailLimiter *ratelimit.Limiter
	// ChallengeLimiter limits Challenge per client. Nil means 10 per 20
	// seconds.
	ChallengeLimiter *ratelimit.Limiter
	// ConfirmLimiter limits Confirm per client. Nil means 10 per minute.
	ConfirmLimiter *ratelimit.Limiter
	// ClientIP resolves the client address. Nil trusts no proxy header.
	ClientIP *clientip.Resolver
	// TrustedOrigins are cross origin callers the JSON routes accept.
	TrustedOrigins []string

	// Logger receives the log lines. They hold codes and type names, never
	// an address or a token. Nil means the logger of the request context.
	Logger *slog.Logger
	// MaxBodyBytes is the body cap. Zero means [DefaultMaxBodyBytes].
	MaxBodyBytes int64
	// SendTimeout bounds one send. Zero means [DefaultSendTimeout].
	SendTimeout time.Duration
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Rand is the source of the confirmation tokens. Nil means crypto/rand.
	// It must be safe for concurrent use.
	Rand io.Reader
}

// Handler serves the waitlist. It is safe for concurrent use.
type Handler struct {
	keys           keys
	pow            *pow.Service
	sender         mail.Sender
	from           string
	store          Store
	consentVersion string
	confirmURL     string
	unsubscribeURL string
	oneClickURL    string
	confirmation   func(ConfirmationData) Content
	ttl            time.Duration
	maxSends       int
	resend         time.Duration
	purgeEvery     time.Duration
	clients        *ratelimit.Limiter
	mails          *ratelimit.Limiter
	clientIP       *clientip.Resolver
	origin         *csrf.Protector
	logger         *slog.Logger
	maxBody        int64
	timeout        time.Duration
	now            func() time.Time
	rand           io.Reader
	configured     bool

	challenge http.Handler
	confirm   http.Handler

	inflight sync.WaitGroup

	mu       sync.Mutex
	lastWarn time.Time
}

// ErrNotConfigured is returned by the methods that need the secret, the
// sender or the keys when one of them is missing.
var ErrNotConfigured = errors.New("optin: not configured")

// New validates cfg. A missing Secret, PoW or Sender is not an error: the
// routes answer 503 until they are set. Everything else that is missing or
// malformed is an error that names the field, never its value.
func New(cfg Config) (*Handler, error) {
	h := &Handler{
		pow:            cfg.PoW,
		sender:         cfg.Sender,
		from:           cfg.From,
		store:          cfg.Store,
		consentVersion: cfg.ConsentVersion,
		confirmURL:     cfg.ConfirmURL,
		unsubscribeURL: cfg.UnsubscribeURL,
		oneClickURL:    cfg.OneClickURL,
		confirmation:   cfg.Confirmation,
		ttl:            orDuration(cfg.PendingTTL, DefaultPendingTTL),
		maxSends:       cfg.MaxSendsPerAddress,
		resend:         orDuration(cfg.ResendInterval, DefaultResendInterval),
		purgeEvery:     orDuration(cfg.PurgeInterval, DefaultPurgeInterval),
		clients:        cfg.Limiter,
		mails:          cfg.MailLimiter,
		clientIP:       cfg.ClientIP,
		logger:         cfg.Logger,
		maxBody:        cfg.MaxBodyBytes,
		timeout:        orDuration(cfg.SendTimeout, DefaultSendTimeout),
		now:            cfg.Now,
		rand:           cfg.Rand,
	}
	var problems []string
	if len(cfg.Secret) > 0 && len(cfg.Secret) < MinSecretLen {
		problems = append(problems, fmt.Sprintf("Secret must be at least %d bytes", MinSecretLen))
	}
	if h.store == nil {
		problems = append(problems, "Store is required")
	}
	if strings.TrimSpace(h.consentVersion) == "" {
		problems = append(problems, "ConsentVersion is required")
	} else if _, err := mail.Build(probeMessage(probeAddress, h.consentVersion)); err != nil {
		problems = append(problems, "ConsentVersion must be printable text")
	}
	for _, u := range []struct{ name, value string }{
		{"ConfirmURL", h.confirmURL}, {"UnsubscribeURL", h.unsubscribeURL}, {"OneClickURL", h.oneClickURL},
	} {
		if !validLink(u.value) {
			problems = append(problems, u.name+" must be an https URL with a host and no query, fragment, user info or '=', at most 512 bytes")
		}
	}
	if h.confirmation == nil {
		problems = append(problems, "Confirmation is required")
	}
	if h.maxSends < 0 || cfg.PendingTTL < 0 || cfg.ResendInterval < 0 || cfg.PurgeInterval < 0 || cfg.SendTimeout < 0 {
		problems = append(problems, "durations and counts must not be negative")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("optin: invalid config: %s", strings.Join(problems, "; "))
	}

	if h.maxSends == 0 {
		h.maxSends = DefaultMaxSendsPerAddress
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
	confirms := cfg.ConfirmLimiter
	if confirms == nil {
		confirms = ratelimit.New(DefaultConfirmBurst, DefaultConfirmPer, MaxLimiterKeys)
	}
	if h.clientIP == nil {
		h.clientIP = clientip.NewResolver(nil)
	}
	if h.maxBody <= 0 {
		h.maxBody = DefaultMaxBodyBytes
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.rand == nil {
		h.rand = rand.Reader
	}
	if len(cfg.Secret) > 0 {
		h.keys = deriveKeys(cfg.Secret)
	}

	origin, err := csrf.New(func(*http.Request) (string, bool) { return "", false })
	if err != nil {
		return nil, fmt.Errorf("optin: origin check: %w", err)
	}
	for _, o := range cfg.TrustedOrigins {
		if err := origin.TrustOrigin(o); err != nil {
			return nil, fmt.Errorf("optin: %w", err)
		}
	}
	h.origin = origin

	if h.sender != nil {
		if _, err := mail.Build(probeMessage(h.from, "x")); err != nil {
			return nil, fmt.Errorf("optin: From: %w", err)
		}
	}
	if err := h.probeConfirmation(); err != nil {
		return nil, err
	}
	h.configured = len(cfg.Secret) > 0 && h.pow != nil && h.sender != nil && h.from != ""

	limit, err := middleware.NewRateLimit(middleware.RateLimitConfig{Limiter: challenges, ClientIP: h.clientIP})
	if err != nil {
		return nil, fmt.Errorf("optin: challenge limit: %w", err)
	}
	h.challenge = limit.Middleware(http.HandlerFunc(h.serveChallenge))
	confirmLimit, err := middleware.NewRateLimit(middleware.RateLimitConfig{Limiter: confirms, ClientIP: h.clientIP})
	if err != nil {
		return nil, fmt.Errorf("optin: confirm limit: %w", err)
	}
	h.confirm = confirmLimit.Middleware(handle(h, h.serveConfirm))

	if !h.configured && h.logger != nil {
		h.logger.Warn("optin not configured: routes answer 503",
			"secret", len(cfg.Secret) > 0, "pow", h.pow != nil, "sender", h.sender != nil)
	}
	return h, nil
}

func orDuration(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// probeAddress is the placeholder address of a startup check.
const probeAddress = "probe@example.org"

// probeMessage is a message that only checks one From and one subject.
func probeMessage(from, subject string) mail.Message {
	return mail.Message{From: from, To: []string{probeAddress}, Subject: subject, Body: "x"}
}

// validLink accepts an absolute https URL with a host and nothing that would
// make the token ambiguous or the List-Unsubscribe header encoded.
func validLink(raw string) bool {
	if raw == "" || len(raw) > maxURLBytes || strings.ContainsAny(raw, "=?#<>,\"' \t\r\n\\") {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x21 || raw[i] > 0x7e {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Opaque == ""
}

// probeConfirmation calls the site's function once with sample data, so a
// body without the links fails at startup and not on the first visitor.
func (h *Handler) probeConfirmation() error {
	data := ConfirmationData{
		Address:        "probe@example.org",
		ConfirmURL:     h.confirmURL + "?token=" + strings.Repeat("A", confirmTokenLen),
		UnsubscribeURL: h.unsubscribeURL + "?token=" + strings.Repeat("A", unsubscribeTokenLen),
		Expires:        time.Unix(0, 0).UTC(),
	}
	var c Content
	if cause := guard("Confirmation", func() { c = h.confirmation(data) }); cause != nil {
		return fmt.Errorf("optin: Confirmation: %w", cause)
	}
	if err := checkContent(c, data.ConfirmURL, data.UnsubscribeURL); err != nil {
		return fmt.Errorf("optin: Confirmation: %w", err)
	}
	return nil
}

// Configured reports whether the secret, the keys and the sender are set.
func (h *Handler) Configured() bool { return h.configured }

// Challenge returns the GET handler that issues a proof of work challenge,
// limited per client.
func (h *Handler) Challenge() http.Handler { return h.challenge }

// Request returns the POST handler of the form. It checks, in this order:
// the origin, the configuration, the content type and body cap, the
// honeypot, the per client limit, the address and the consent box, the proof
// of work and the global ceiling. Then it updates the entry and answers 200
// {"ok":true}, the same for every address. A confirmation mail, if one is
// due, is sent after the answer.
func (h *Handler) Request() http.Handler { return handle(h, h.serveRequest) }

// Confirm returns the POST handler of the confirmation page. The body is
// {"token": "..."}. It is limited per client.
func (h *Handler) Confirm() http.Handler { return h.confirm }

// Unsubscribe returns the POST handler that removes an address and keeps a
// suppression marker. It takes a JSON body {"token": "..."} from the site's
// page, with the origin check, or the RFC 8058 one click form body
// List-Unsubscribe=One-Click with the token in the query, which a mail
// provider sends from its own servers.
func (h *Handler) Unsubscribe() http.Handler { return handle(h, h.serveUnsubscribe) }

func handle(h *Handler, fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			h.fail(w, r, err)
		}
	})
}

type okBody struct {
	OK bool `json:"ok"`
}

// ok writes the one success answer. A failed write means the client is gone.
func ok(w http.ResponseWriter) error {
	_ = api.Write(w, http.StatusOK, okBody{OK: true})
	return nil
}

func (h *Handler) serveChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.fail(w, r, methodNotAllowed(w, http.MethodGet))
		return
	}
	if !h.configured {
		h.fail(w, r, notConfigured())
		return
	}
	c, err := h.pow.Create()
	if err != nil {
		h.fail(w, r, api.Internal(fmt.Errorf("optin: challenge: %w", err)))
		return
	}
	if err := api.Write(w, http.StatusOK, c); err != nil {
		h.log(r).DebugContext(r.Context(), "optin: write failed")
	}
}

// requestBody is the JSON body of Request. Website is the honeypot.
type requestBody struct {
	Email    string        `json:"email"`
	Consent  bool          `json:"consent"`
	Language string        `json:"language"`
	Website  string        `json:"website"`
	PoW      *pow.Solution `json:"pow"`
}

// tokenBody is the JSON body of Confirm and Unsubscribe.
type tokenBody struct {
	Token string `json:"token"`
}

// precheck runs the method, origin, configuration and content type checks a
// JSON route shares.
func (h *Handler) precheck(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, http.MethodPost)
	}
	if err := h.origin.Check(r); err != nil {
		return api.New(http.StatusForbidden, CodeCrossOrigin, "cross origin request refused")
	}
	if !h.configured {
		return notConfigured()
	}
	if !isMediaType(r, "application/json") {
		return api.New(http.StatusUnsupportedMediaType, api.CodeUnsupportedMediaType,
			"content type must be application/json")
	}
	return nil
}

func isMediaType(r *http.Request, want string) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == want
}

func (h *Handler) serveRequest(w http.ResponseWriter, r *http.Request) error {
	if err := h.precheck(w, r); err != nil {
		return err
	}
	var body requestBody
	if err := api.DecodeLimit(r, &body, h.maxBody); err != nil {
		return decodeFailure(err)
	}
	if body.Website != "" {
		// The honeypot answers like a success and does nothing.
		return ok(w)
	}
	if allowed, wait := h.clients.Take(clientip.Key(h.clientIP.Resolve(r))); !allowed {
		return api.TooManyRequests(wait)
	}
	address := strings.TrimSpace(body.Email)
	fields := map[string]any{}
	if code := checkAddress(address); code != "" {
		fields[FieldEmail] = code
	}
	if !body.Consent {
		fields[FieldConsent] = CodeRequired
	}
	if len(fields) > 0 {
		return api.Invalid("some fields are invalid").WithDetails(fields)
	}
	if body.PoW == nil {
		return coded(api.Invalid("proof of work is required"), CodePowRequired)
	}
	if err := h.pow.Verify(*body.PoW); err != nil {
		return powFailure(err)
	}
	// The ceiling is taken before the state of the address is known, so an
	// exhausted ceiling refuses every address alike.
	if allowed, wait := h.mails.Take(mailKey); !allowed {
		h.warnCeiling(r)
		e := api.Unavailable("the form is busy, please try again later").Exposed()
		e.Code, e.RetryAfter = CodeMailLimit, wait
		return e
	}
	// A token is made for every request, whatever the state, so the work
	// before the answer does not depend on the address.
	token, hash, err := newConfirmToken(h.rand)
	if err != nil {
		return api.Internal(err)
	}
	now := h.now().Round(0)
	key := h.keys.addressKey(address)
	language := cleanLanguage(body.Language)
	var rcpt *recipient
	err = h.store.Update(r.Context(), key, func(e *Entry) error {
		rcpt = nil
		switch {
		case e.State == StateNone, e.State == StatePending && !now.Before(e.Created.Add(h.ttl)):
			*e = Entry{
				Key: key, State: StatePending, Address: address, Language: language,
				ConsentVersion: h.consentVersion, Created: now, ConfirmHash: hash, Sends: 1, LastSent: now,
			}
		case e.State == StatePending:
			if e.Sends >= h.maxSends || now.Sub(e.LastSent) < h.resend {
				return nil
			}
			e.Address, e.Language, e.ConsentVersion = address, language, h.consentVersion
			e.ConfirmHash, e.LastSent = hash, now
			e.Sends++
		default:
			// Confirmed or suppressed: nothing is written and nothing is sent.
			return nil
		}
		rc, err := newRecipient(*e, kindConfirmation)
		if err != nil {
			return err
		}
		rcpt = &rc
		return nil
	})
	switch {
	case errors.Is(err, ErrStoreFull):
		// The same answer as for every other address. Nothing is sent.
		h.log(r).WarnContext(r.Context(), "optin: store is full, request dropped")
		return ok(w)
	case err != nil:
		return api.Internal(fmt.Errorf("optin: store: %w", err))
	}
	if rcpt != nil {
		h.sendConfirmationLater(r, *rcpt, token)
	}
	return ok(w)
}

// sendConfirmationLater sends the confirmation mail after the answer, so the
// time of the SMTP conversation never shows in the answer.
func (h *Handler) sendConfirmationLater(r *http.Request, rc recipient, token string) {
	logger := h.log(r)
	ctx := context.WithoutCancel(r.Context())
	h.inflight.Add(1)
	go func() {
		defer h.inflight.Done()
		if err := h.sendConfirmation(ctx, rc, token); err != nil {
			logger.ErrorContext(ctx, "optin: confirmation mail not sent", "error", err.Error())
		}
	}()
}

func (h *Handler) sendConfirmation(ctx context.Context, rc recipient, token string) error {
	data := ConfirmationData{
		Address:        rc.address,
		Language:       rc.language,
		ConfirmURL:     h.confirmURL + "?token=" + token,
		UnsubscribeURL: h.unsubscribeURL + "?token=" + h.keys.unsubscribeToken(rc.key),
		Expires:        rc.created.Add(h.ttl),
	}
	var c Content
	if cause := guard("Confirmation", func() { c = h.confirmation(data) }); cause != nil {
		return cause
	}
	return h.deliver(ctx, rc, c, data.ConfirmURL)
}

// Wait blocks until every confirmation mail in flight has been handed to the
// sender, or ctx ends. A site calls it at shutdown after the server stopped
// taking requests.
func (h *Handler) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		h.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("optin: wait: %w", ctx.Err())
	}
}

// errTokenInvalid is the one refusal of a token, whatever the reason.
var errTokenInvalid = errors.New("optin: token invalid")

func tokenInvalid() error {
	return coded(api.Invalid("the link is not valid"), CodeTokenInvalid)
}

func (h *Handler) serveConfirm(w http.ResponseWriter, r *http.Request) error {
	if err := h.precheck(w, r); err != nil {
		return err
	}
	var body tokenBody
	if err := api.DecodeLimit(r, &body, h.maxBody); err != nil {
		return decodeFailure(err)
	}
	hash, valid := parseConfirmToken(body.Token)
	if !valid {
		return tokenInvalid()
	}
	key, err := h.store.FindByConfirmHash(r.Context(), hash)
	switch {
	case errors.Is(err, ErrNotFound):
		return tokenInvalid()
	case err != nil:
		return api.Internal(fmt.Errorf("optin: store: %w", err))
	}
	now := h.now().Round(0)
	err = h.store.Update(r.Context(), key, func(e *Entry) error {
		if e.State != StatePending ||
			subtle.ConstantTimeCompare(e.ConfirmHash[:], hash[:]) != 1 ||
			!now.Before(e.Created.Add(h.ttl)) {
			return errTokenInvalid
		}
		e.State, e.Confirmed = StateConfirmed, now
		e.ConfirmHash, e.Sends, e.LastSent = TokenHash{}, 0, time.Time{}
		return nil
	})
	switch {
	case errors.Is(err, errTokenInvalid):
		return tokenInvalid()
	case err != nil:
		return api.Internal(fmt.Errorf("optin: store: %w", err))
	}
	return ok(w)
}

func (h *Handler) serveUnsubscribe(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return methodNotAllowed(w, http.MethodPost)
	}
	var token string
	if isMediaType(r, "application/x-www-form-urlencoded") || isMediaType(r, "multipart/form-data") {
		// RFC 8058 one click: a mail provider posts from its own servers,
		// not from a page, so there is no origin to check. The token in the
		// query is the authority, and its MAC is checked before the store.
		if !h.configured {
			return notConfigured()
		}
		r.Body = http.MaxBytesReader(w, r.Body, h.maxBody)
		if err := r.ParseMultipartForm(h.maxBody); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			return api.Invalid("body is not a valid one click request")
		}
		if r.PostForm.Get("List-Unsubscribe") != "One-Click" {
			return api.Invalid("body is not a valid one click request")
		}
		token = r.URL.Query().Get("token")
	} else {
		if err := h.precheck(w, r); err != nil {
			return err
		}
		var body tokenBody
		if err := api.DecodeLimit(r, &body, h.maxBody); err != nil {
			return decodeFailure(err)
		}
		token = body.Token
	}
	key, valid := h.keys.parseUnsubscribeToken(token)
	if !valid {
		return tokenInvalid()
	}
	if err := h.suppress(r.Context(), key); err != nil {
		if errors.Is(err, ErrStoreFull) {
			e := api.Unavailable("please try again later").Exposed()
			e.Code = CodeBusy
			return e
		}
		return api.Internal(err)
	}
	return ok(w)
}

// suppress replaces the entry of key with a suppression marker that holds the
// key and nothing else.
func (h *Handler) suppress(ctx context.Context, key Key) error {
	if err := h.store.Update(ctx, key, func(e *Entry) error {
		*e = Entry{Key: key, State: StateSuppressed}
		return nil
	}); err != nil {
		return fmt.Errorf("optin: store: %w", err)
	}
	return nil
}

// Suppressed reports whether address carries a suppression marker. An
// import tool of the site checks it before it adds an address anywhere.
func (h *Handler) Suppressed(ctx context.Context, address string) (bool, error) {
	if h.keys.address == nil {
		return false, ErrNotConfigured
	}
	e, err := h.store.Get(ctx, h.keys.addressKey(strings.TrimSpace(address)))
	switch {
	case errors.Is(err, ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("optin: store: %w", err)
	}
	return e.State == StateSuppressed, nil
}

// Unsuppress removes the suppression marker of address, on the person's own
// request to the site. The form cannot do this. An address without a marker
// is left alone.
func (h *Handler) Unsuppress(ctx context.Context, address string) error {
	if h.keys.address == nil {
		return ErrNotConfigured
	}
	err := h.store.Update(ctx, h.keys.addressKey(strings.TrimSpace(address)), func(e *Entry) error {
		if e.State == StateSuppressed {
			e.State = StateNone
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("optin: store: %w", err)
	}
	return nil
}

// Purge deletes every unconfirmed entry whose pending period has ended and
// returns how many it deleted.
func (h *Handler) Purge(ctx context.Context) (int, error) {
	n, err := h.store.DeletePendingBefore(ctx, h.now().Add(-h.ttl))
	if err != nil {
		return n, fmt.Errorf("optin: purge: %w", err)
	}
	return n, nil
}

// RunPurge calls Purge once at the start and then every PurgeInterval until
// ctx ends. A failed purge is logged and retried at the next tick. It returns
// nil when ctx ends.
func (h *Handler) RunPurge(ctx context.Context) error {
	t := time.NewTicker(h.purgeEvery)
	defer t.Stop()
	for {
		if n, err := h.Purge(ctx); err != nil && ctx.Err() == nil {
			h.logOrContext(ctx).WarnContext(ctx, "optin: purge failed", "error", err.Error())
		} else if n > 0 {
			h.logOrContext(ctx).InfoContext(ctx, "optin: purged unconfirmed entries", "count", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// decodeFailure keeps the size and media type answers of the api package and
// replaces every other text, which can name a field of the request.
func decodeFailure(err error) error {
	e := api.AsError(err)
	switch e.Status {
	case http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		return api.New(e.Status, e.Code, e.Message)
	default:
		return api.Invalid("body is not a valid request")
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

func coded(e *api.Error, code string) *api.Error {
	e.Code = code
	return e
}

func notConfigured() error {
	e := api.Unavailable("the waitlist is not available").Exposed()
	e.Code = CodeNotConfigured
	return e
}

func methodNotAllowed(w http.ResponseWriter, allow string) error {
	w.Header().Set("Allow", allow)
	return api.New(http.StatusMethodNotAllowed, CodeMethodDisallow, "method not allowed")
}

// fail writes the error shape. A refusal logs its code and status only. A
// cause is built by this package or is a store error, and never holds an
// address or a token.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	e := api.AsError(err)
	logger := h.log(r)
	if errors.Unwrap(e) != nil {
		logger.ErrorContext(r.Context(), "optin: request failed", "code", e.Code, "status", e.Status, "error", e.Error())
	} else {
		logger.DebugContext(r.Context(), "optin: request refused", "code", e.Code, "status", e.Status)
	}
	api.WriteError(w, r, e)
}

func (h *Handler) warnCeiling(r *http.Request) {
	h.mu.Lock()
	now := h.now()
	due := h.lastWarn.IsZero() || now.Sub(h.lastWarn) >= warnEvery
	if due {
		h.lastWarn = now
	}
	h.mu.Unlock()
	if due {
		h.log(r).WarnContext(r.Context(), "optin: global mail ceiling reached, requests are refused")
	}
}

func (h *Handler) log(r *http.Request) *slog.Logger {
	return h.logOrContext(r.Context())
}

func (h *Handler) logOrContext(ctx context.Context) *slog.Logger {
	if h.logger != nil {
		return h.logger
	}
	return logging.FromContext(ctx)
}
