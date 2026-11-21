// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package devicehttp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/internal/bearer"
	"github.com/ostendorf-cloud/govue/logging"
	"github.com/ostendorf-cloud/govue/session"
)

// The challenges of RFC 6750 section 3.1, the same two as session.Middleware
// sends: the bare one for a request without a token and invalid_token for a
// token that fails.
const (
	challengeNoToken = "Bearer"
	challengeInvalid = `Bearer error="invalid_token"`
)

// Messages the handlers send. A cause class has exactly one.
const (
	messageInvalidCredentials = "the login, the password or the code is not correct"
	messageInvalidToken       = "the token is not valid"
	messageUnauthenticated    = "authentication required"
	messageUnknownDevice      = "unknown device"
	messageRefused            = "no new token was issued, keep the token you have"
	messageStale              = "the account changed meanwhile, please sign in again"
	messageBusy               = "the server is busy, please try again shortly"
	messageUnavailable        = "device sign in is not available right now"
	messageCrossOrigin        = "cross origin request refused"
)

// DetailInvalid is the value of the details map of a refused request: a token
// a client maps to its own text, not a sentence.
const DetailInvalid = "invalid"

// DefaultMaxBodyBytes is the body limit of every handler when
// [HandlerConfig.MaxBodyBytes] is zero.
const DefaultMaxBodyBytes int64 = 16 << 10

// BusyRetryAfter is the Retry-After the handlers send with a 503 for
// [ErrBusy].
const BusyRetryAfter = 2 * time.Second

// Codes of the handlers, next to the codes of the api package. A client may
// branch on them.
const (
	// CodeInvalidCredentials is the one code of a refused issue.
	CodeInvalidCredentials = "invalid_credentials"
	// CodeRotationRefused is a rotation that issued no token while the
	// presented token stays valid.
	CodeRotationRefused = "rotation_refused"
	// CodeStale is an issue that nothing was written for, because the account
	// ended its sessions after the credential check.
	CodeStale = "stale"
)

var (
	// ErrInvalidCredentials is what [HandlerConfig.VerifyCredentials] returns
	// for every refusal: an unknown login, a wrong password, a wrong or missing
	// code, a disabled account. The handler answers it with one body.
	ErrInvalidCredentials = errors.New("devicehttp: credentials not valid")

	// ErrBusy is what [HandlerConfig.VerifyCredentials] returns when it has no
	// capacity to check, so that the answer says nothing about the guess. The
	// handler answers 503 with Retry-After.
	ErrBusy = errors.New("devicehttp: verification is busy")
)

// Credentials is what an app sends to get a token. The names are those of
// account.CredentialsRequest. A product with other factors maps them in its
// callback or leaves a field empty.
type Credentials struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// IssueRequest is the body of [Handlers.Issue].
type IssueRequest struct {
	Credentials
	// DeviceName is a display label such as the model of the phone. It is
	// optional and never trusted.
	DeviceName string `json:"device_name"`
}

// RotateRequest is the body of [Handlers.Rotate].
type RotateRequest struct {
	// Nonce is the client's random value for this rotate attempt. A retry of
	// the same attempt sends the same one.
	Nonce string `json:"nonce"`
}

// TokenResult is the body of a successful issue or rotation. It is the one
// place a token is sent, and it is sent once. Fields may be added later, so a
// client reads the names it knows and ignores the rest.
type TokenResult struct {
	// Token is the bearer token. The server keeps its hash only.
	Token string `json:"token"`
	// TokenID is the id of the device session, the one that [DeviceInfo.ID]
	// lists. It is a handle, never a credential.
	TokenID string `json:"token_id"`
	// ExpiresAt is the end of the device session. A rotation never moves it.
	ExpiresAt time.Time `json:"expires_at"`
}

// DeviceInfo is one device of the caller, as [Handlers.List] shows it. It
// carries no token and no hash.
type DeviceInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	ExpiresAt time.Time `json:"expires_at"`
	// Current is true for the device that makes the request, and false for
	// every device when a cookie session lists them.
	Current bool `json:"current"`
}

// DeviceList is the body of [Handlers.List].
type DeviceList struct {
	Devices []DeviceInfo `json:"devices"`
}

// HandlerConfig configures [Handlers]. Sessions and VerifyCredentials are
// required.
type HandlerConfig struct {
	// Sessions is the manager with a device store configured. Required.
	Sessions *session.Manager

	// VerifyCredentials checks the credentials of an issue and returns the
	// subject the device session belongs to, the same value as the Subject of
	// the cookie sessions, so that one EndAll reaches both. Required. The
	// handler has no credential check of its own, and imports no account
	// package.
	//
	// It returns [ErrInvalidCredentials] for every refusal and [ErrBusy] when
	// it has no capacity. Any other error is answered as an opaque 500. The
	// subject must not be empty on success.
	//
	// The handler takes the time of the check before it calls this function,
	// as ADR 0001 requires, so a password change that lands during a slow
	// verification leaves no device behind. A callback must therefore read the
	// stored credential only after it is called, and never cache the result of
	// an earlier check.
	VerifyCredentials func(ctx context.Context, c Credentials) (subject string, err error)

	// DeviceName is an optional naming policy. It gets the validated name the
	// app sent, which may be empty, and returns the name to store, for example
	// a default for an empty one. An error is answered as 400. The result is
	// validated again before the credentials are checked: a name the session
	// package would refuse is a bug of the policy and answers an opaque 500,
	// with no credential checked and no code consumed. Nil stores the name as
	// sent.
	//
	// A name is a label the app chose and is never trusted. It may carry
	// U+2028, U+3164, trailing spaces or markup such as "<script>". The JSON
	// is safe. A UI renders a name as text, never as HTML.
	DeviceName func(requested string) (string, error)

	// IssueTrustedOrigins are the cross origin callers the origin check of
	// [Handlers.Issue] accepts, each a scheme and host without a trailing
	// slash. Zero trusts this origin only. They apply to Issue and to no
	// other route: the cookie paths of List and Revoke never trust a cross
	// origin. The list is the application's list of app origins. It must
	// never be the list of csrf.Protector.TrustOrigin or of the web CORS
	// policy, see the package doc.
	IssueTrustedOrigins []string

	// MaxBodyBytes limits a request body. Zero is [DefaultMaxBodyBytes].
	MaxBodyBytes int64

	// Now is the clock of the credential check. It has to be the clock of
	// session.Config.Now, because the store compares the two. Zero is
	// [time.Now].
	Now func() time.Time
}

// Handlers are the HTTP handlers of the device token sessions. Build it once
// at startup and mount the handlers. It is safe for concurrent use.
type Handlers struct {
	sessions     *session.Manager
	verify       func(ctx context.Context, c Credentials) (string, error)
	name         func(string) (string, error)
	cross        *http.CrossOriginProtection // trusts this origin only
	issueCross   *http.CrossOriginProtection // trusts IssueTrustedOrigins too
	maxBodyBytes int64
	now          func() time.Time
}

// NewHandlers validates cfg and returns the handlers. Like the other
// constructors of this module it names every problem at once.
func NewHandlers(cfg HandlerConfig) (*Handlers, error) {
	var problems []error
	if cfg.Sessions == nil {
		problems = append(problems, errors.New("sessions is required"))
	}
	if cfg.VerifyCredentials == nil {
		problems = append(problems, errors.New("verify credentials is required"))
	}
	if cfg.MaxBodyBytes < 0 {
		problems = append(problems, fmt.Errorf("max body bytes %d is negative", cfg.MaxBodyBytes))
	}
	issueCross := http.NewCrossOriginProtection()
	for i, origin := range cfg.IssueTrustedOrigins {
		// A refused origin is not shown, and the error of AddTrustedOrigin,
		// which quotes it, is not wrapped: it may hold a credential.
		if issueCross.AddTrustedOrigin(origin) != nil {
			problems = append(problems, fmt.Errorf("trusted origin %d must be scheme://host[:port] "+
				"without a path, a query or a fragment (the value is not shown)", i+1))
		}
	}
	// A manager without a device store would fail every issue after the
	// credentials were checked, and the code consumed. AuthenticateDevice
	// looks at the store before the token, so an empty token reaches nothing
	// else.
	if cfg.Sessions != nil {
		if _, err := cfg.Sessions.AuthenticateDevice(context.Background(), ""); errors.Is(err, session.ErrDevicesDisabled) {
			problems = append(problems, errors.New("the session manager has no device store"))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("devicehttp: handlers: %w", err)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Handlers{
		sessions:     cfg.Sessions,
		verify:       cfg.VerifyCredentials,
		name:         cfg.DeviceName,
		cross:        http.NewCrossOriginProtection(),
		issueCross:   issueCross,
		maxBodyBytes: cmp.Or(cfg.MaxBodyBytes, DefaultMaxBodyBytes),
		now:          now,
	}, nil
}

// Issue starts a device session after a verified sign in and answers 201 with
// a [TokenResult]. It is the one place a token is created, and the token is in
// this answer only.
//
// The caller proves itself with the credentials in the body, checked by
// [HandlerConfig.VerifyCredentials]. This handler does not read a bearer token
// or a cookie: a valid token with an empty body is a 401, and with the
// credentials of another account it issues for that account. It neither reads
// nor sets a cookie. In the real chain the middleware in front still acts on
// the headers before this handler runs: a junk, expired, revoked or doubled
// Authorization header gets its 401 even with valid credentials, a cookie
// request without a CSRF token gets the protector's 403, and a valid pending
// token is confirmed by the session middleware as on any request.
//
// The method, the origin, the content type, the size and the shape of the body
// and the device name are checked before the credentials, so a refused request
// costs no verification and says nothing about them. A policy result that is
// not a valid name is a bug of the application and answers an opaque 500 at
// the same place, before any credential is checked, so no code is consumed.
// Every refusal of the callback answers 401 with one body.
//
// The route needs a per client rate limit in front of it. See the package
// documentation.
func (h *Handlers) Issue() http.Handler {
	return h.route(http.MethodPost, h.issueCross, true, func(w http.ResponseWriter, r *http.Request) error {
		var req IssueRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}
		name, err := h.deviceName(req.DeviceName)
		if err != nil {
			return err
		}

		// The time of the check is taken before the verification reads the
		// stored credential. ADR 0001 explains why the order matters.
		checkedAt := h.now()
		subject, err := h.verify(r.Context(), req.Credentials)
		switch {
		case err == nil:
		case errors.Is(err, ErrInvalidCredentials):
			return api.New(http.StatusUnauthorized, CodeInvalidCredentials, messageInvalidCredentials)
		case errors.Is(err, ErrBusy):
			return busy()
		default:
			return api.Internal(fmt.Errorf("devicehttp: verify credentials: %w", err))
		}
		if subject == "" {
			return api.Internal(errors.New("devicehttp: verify credentials: the subject is empty"))
		}

		token, device, err := h.sessions.IssueDevice(r.Context(), subject, name, checkedAt)
		switch {
		case err == nil:
		case errors.Is(err, session.ErrStaleAuthentication):
			return api.New(http.StatusConflict, CodeStale, messageStale)
		case errors.Is(err, session.ErrInvalidDeviceName):
			// The name was checked before the credentials. Reaching this is a
			// disagreement between the two rules, a bug.
			return api.Internal(err)
		case errors.Is(err, session.ErrDeviceStoreFull):
			return api.Unavailable(messageUnavailable).Exposed().WithCause(err)
		default:
			return api.Internal(err)
		}

		logging.FromContext(r.Context()).InfoContext(r.Context(), "devicehttp: device token issued",
			"device_id", device.ID, "subject", device.Subject)

		return api.Write(w, http.StatusCreated, TokenResult{Token: token, TokenID: device.ID, ExpiresAt: device.ExpiresAt})
	})
}

// Rotate replaces the token of the calling device and answers 200 with a
// [TokenResult]. The device id and its expiry stay as they were.
//
// It is authenticated by the bearer token itself and by nothing else: a
// request with a cookie and no token is a 401, and a cookie is never read. The
// rules of the rotation, the grace for the old token, the minimum interval and
// the nonce, are those of session.Manager.RotateDevice. A rotation that issues
// no token while the presented token stays valid answers 409 with
// [CodeRotationRefused], which an app takes quietly.
func (h *Handlers) Rotate() http.Handler {
	return h.route(http.MethodPost, h.cross, false, func(w http.ResponseWriter, r *http.Request) error {
		token, err := bearerToken(w, r)
		if err != nil {
			return err
		}
		var req RotateRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}

		next, device, err := h.sessions.RotateDevice(r.Context(), token, req.Nonce)
		switch {
		case err == nil:
		case errors.Is(err, session.ErrInvalidNonce):
			return api.Invalid("the nonce is not valid").WithDetails(map[string]any{"nonce": DetailInvalid})
		case errors.Is(err, session.ErrRotateRefused):
			return api.New(http.StatusConflict, CodeRotationRefused, messageRefused)
		case errors.Is(err, session.ErrDeviceToken):
			return invalidToken(w)
		default:
			return api.Internal(err)
		}

		return api.Write(w, http.StatusOK, TokenResult{Token: next, TokenID: device.ID, ExpiresAt: device.ExpiresAt})
	})
}

// SignOut ends the device session the bearer token belongs to and answers 204.
// It needs a bearer token and never touches a cookie: the cookie session of the
// same browser, if there is one, stays as it is. Signing out the same token a
// second time is a 401.
func (h *Handlers) SignOut() http.Handler {
	return h.route(http.MethodDelete, h.cross, false, func(w http.ResponseWriter, r *http.Request) error {
		if _, err := bearerToken(w, r); err != nil {
			return err
		}
		// Manager.End signs the device out on a bearer request and never
		// reads or clears the cookie, whatever the request carries.
		err := h.sessions.End(r.Context(), w, r)
		switch {
		case err == nil:
		case errors.Is(err, session.ErrDeviceToken):
			return invalidToken(w)
		default:
			return api.Internal(err)
		}

		api.NoContent(w)

		return nil
	})
}

// List answers 200 with the caller's own devices as a [DeviceList], most
// recently seen first: id, name, created, last seen and expires, never a token
// or a hash. The caller is a cookie session or a device, so the web app can
// list the phones of a person and a phone can list its siblings.
//
// It reads the session [session.Manager.Middleware] put into the request
// context. Without the middleware every request is a 401.
func (h *Handlers) List() http.Handler {
	return h.route(http.MethodGet, h.cross, false, func(w http.ResponseWriter, r *http.Request) error {
		caller, err := caller(r)
		if err != nil {
			return err
		}
		devices, err := h.sessions.ListDevices(r.Context(), caller.Subject)
		if err != nil {
			return api.Internal(err)
		}

		out := DeviceList{Devices: make([]DeviceInfo, 0, len(devices))}
		for _, d := range devices {
			out.Devices = append(out.Devices, DeviceInfo{
				ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt, LastSeen: d.LastSeen, ExpiresAt: d.ExpiresAt,
				Current: caller.Credential == session.CredentialDevice && d.ID == caller.DeviceID,
			})
		}

		return api.Write(w, http.StatusOK, out)
	})
}

// Revoke ends one device of the caller, by the id in the route's {id}, and
// answers 204. An id of another account answers exactly like an unknown id:
// the same status, body and headers, and nothing of the other account ends.
// The caller is a cookie session or a device. A cookie request goes through
// the origin check of this handler, which trusts this origin only, and the
// application's CSRF check.
//
// Any device token of an account can list and revoke every device of the
// account, itself included. A device that holds an old token inside the grace
// of a rotation can do the same, see the package documentation.
func (h *Handlers) Revoke() http.Handler {
	return h.route(http.MethodDelete, h.cross, false, func(w http.ResponseWriter, r *http.Request) error {
		caller, err := caller(r)
		if err != nil {
			return err
		}
		id := r.PathValue("id")

		err = h.sessions.RevokeDevice(r.Context(), caller.Subject, id)
		switch {
		case err == nil:
		case errors.Is(err, session.ErrNotFound):
			return api.NotFound(messageUnknownDevice)
		default:
			return api.Internal(err)
		}

		logging.FromContext(r.Context()).InfoContext(r.Context(), "devicehttp: device revoked by its owner",
			"device_id", id, "subject", caller.Subject)
		api.NoContent(w)

		return nil
	})
}

// route wraps fn in the checks every handler makes before it reads anything:
// the method, then the origin. A bearer request skips the origin check unless
// always is set. A browser never attaches an Authorization header by itself, so
// the check guards the requests that ride on ambient credentials, and the
// issue route, which has none and whose answer carries the token.
func (h *Handlers) route(method string, cross *http.CrossOriginProtection, always bool, fn api.HandlerFunc) http.Handler {
	return api.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if r.Method != method {
			w.Header().Set("Allow", method)

			return api.New(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
		if kind, _ := bearer.Classify(r.Header); always || kind == bearer.None {
			if err := cross.Check(r); err != nil {
				return api.PermissionDenied(messageCrossOrigin).WithCause(err)
			}
		}

		return fn(w, r)
	})
}

// decode reads one JSON object into into: the content type has to be
// application/json, and api.DecodeLimit refuses a body over the limit, an
// unknown field and a second value.
func (h *Handlers) decode(r *http.Request, into any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return api.New(http.StatusUnsupportedMediaType, api.CodeUnsupportedMediaType,
			"content type must be application/json")
	}

	return api.DecodeLimit(r, into, h.maxBodyBytes)
}

// deviceName applies the policy and validates the result with the rules of
// session.Manager.IssueDevice, so a name that passes here is never refused
// after the credentials are known to be right.
func (h *Handlers) deviceName(requested string) (string, error) {
	if !validName(requested) {
		return "", invalidName()
	}
	name := requested
	if h.name != nil {
		var err error
		if name, err = h.name(requested); err != nil {
			return "", invalidName()
		}
		if !validName(name) {
			return "", api.Internal(errors.New("devicehttp: the device name policy returned a name the session package refuses"))
		}
	}

	return name, nil
}

func invalidName() error {
	return api.Invalid("the device name is not valid").WithDetails(map[string]any{"device_name": DetailInvalid})
}

// validName is the rule of session.Manager.IssueDevice: bounded, UTF-8, and
// free of control and format characters. A test pins that the two agree.
func validName(name string) bool {
	if len(name) > session.MaxDeviceNameBytes || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}

	return true
}

// bearerToken returns the token of a request that carries exactly one bearer
// Authorization header, and the 401 for every other request, a cookie only
// request included. The challenge is the bare one of RFC 6750 section 3.1.
func bearerToken(w http.ResponseWriter, r *http.Request) (string, error) {
	kind, token := bearer.Classify(r.Header)
	if kind != bearer.Bearer || token == "" {
		w.Header().Set("WWW-Authenticate", challengeNoToken)

		return "", api.Unauthenticated(messageInvalidToken)
	}

	return token, nil
}

// invalidToken is the 401 of a token that does not authenticate.
func invalidToken(w http.ResponseWriter) error {
	w.Header().Set("WWW-Authenticate", challengeInvalid)

	return api.Unauthenticated(messageInvalidToken)
}

// caller returns the session the middleware read, a cookie or a device, and
// the 401 when there is none. A store that could not answer is a 503, so a
// person is not told to sign in again during an outage.
func caller(r *http.Request) (session.Session, error) {
	if err := session.ErrorFromContext(r.Context()); err != nil {
		return session.Session{}, api.Unavailable(messageUnavailable).Exposed().WithCause(err)
	}
	s, ok := session.FromContext(r.Context())
	if !ok || s.Subject == "" {
		return session.Session{}, api.Unauthenticated(messageUnauthenticated)
	}

	return s, nil
}

// busy is the 503 of a verifier without capacity. Its message is chosen here
// and safe to send, hence Exposed.
func busy() error {
	err := api.Unavailable(messageBusy).Exposed()
	err.RetryAfter = BusyRetryAfter

	return err
}
