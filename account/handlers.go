// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"

	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/logging"
)

// DefaultMaxBodyBytes is the body limit of every handler when
// [HandlerConfig.MaxBodyBytes] is zero. The longest legitimate body is a
// password of [MaxPasswordLength] bytes next to a login of [MaxLoginLength]
// bytes, which JSON escaping can at most multiply by six.
const DefaultMaxBodyBytes int64 = 16 << 10

// HashingBusyRetryAfter is the Retry-After the handlers send with a 503 for
// [ErrHashingBusy].
const HashingBusyRetryAfter = 2 * time.Second

// The error codes of the handlers, next to the codes of the api package.
// Codes are stable and machine readable, and a client may branch on them.
const (
	// CodeInvalidCredentials is the one code of a failed login or TOTP
	// confirmation, and of a wrong current password in a password change.
	CodeInvalidCredentials = "invalid_credentials"

	// CodeStale is a password change that nothing was written for, because the
	// account changed during the call.
	CodeStale = "stale"

	// CodePasswordChangeUnknown is a password change whose outcome is not
	// known. The person logs in with the new password to find out.
	CodePasswordChangeUnknown = "password_change_unknown"
)

// Messages the handlers send. A failed login has exactly one, whatever the
// cause.
const (
	messageInvalidCredentials = "the login, the password or the code is not correct"
	messageWrongCurrent       = "the current password is not correct"
	messageBusy               = "the server is busy, please try again shortly"
	messageCrossOrigin        = "cross origin request refused"
	messageSignupClosed       = "signup is not available"
	messageLoginTaken         = "this login is already taken"
	messageStale              = "the account changed meanwhile, please try again"
	messageUnknown            = "the password change may not have happened, log in with the new password to find out"
)

// The values of the details map of a refused signup or password change. They
// are tokens a client maps to its own texts, not sentences.
const (
	DetailInvalid  = "invalid"
	DetailTooShort = "too_short"
	DetailTooLong  = "too_long"
)

// SignupRequest is the body of the signup handler.
type SignupRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// CredentialsRequest is the body of the login and the TOTP confirm handlers:
// the login, the password and a code from the authenticator app.
type CredentialsRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// PasswordChangeRequest is the body of the password change handler.
type PasswordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// EnrolmentResult is the enrolment a signup answers with, once. The secret is
// never handed out again.
type EnrolmentResult struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

// SignupResult is the 201 body of the signup handler. Fields may be added in
// a later release, so a client reads the names it knows and ignores the rest.
type SignupResult struct {
	Login     string          `json:"login"`
	Enrolment EnrolmentResult `json:"enrolment"`
}

// LoginResult is the 200 body of the login and the TOTP confirm handlers. The
// session is whatever [HandlerConfig.StartSession] started, usually a cookie.
// Fields may be added in a later release, a token for a native app among
// them, so a client reads the names it knows and ignores the rest.
type LoginResult struct {
	Login string `json:"login"`
}

// PasswordChangeResult is the 200 body of the password change handler, for
// the outcomes where the password changed. Fields may be added in a later
// release.
type PasswordChangeResult struct {
	// PasswordChanged is true in every 200 answer.
	PasswordChanged bool `json:"password_changed"`

	// SessionsEnded says whether every session of the account was ended. When
	// false the change stands, and the sessions opened with the old password
	// may still be live until the application retries ending them.
	SessionsEnded bool `json:"sessions_ended"`
}

// HandlerConfig configures [Handlers]. Every field except AllowSignup,
// TrustedOrigins and MaxBodyBytes is required.
type HandlerConfig struct {
	// Service is the account logic the handlers call. Required. Its
	// EndSessions callback is what a password change ends the sessions with.
	Service *Service

	// StartSession is where the application turns a verified [Identity] into
	// its own session, typically one call to a session manager's Start. It is
	// called after a successful login and after a successful TOTP
	// confirmation, which proved both factors, and never otherwise. Required:
	// this package has no session of its own and imports nothing from the
	// session package.
	//
	// An error it returns is answered with a 500 that carries no cause. The
	// code of a login is consumed by then, so the person logs in again with
	// the next code.
	StartSession func(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity) error

	// EndSession ends the one session the request carries, for the logout
	// handler. A request without a session is not an error. Required. Ending
	// every session of an account is [ServiceConfig.EndSessions].
	EndSession func(ctx context.Context, w http.ResponseWriter, r *http.Request) error

	// CurrentIdentity returns the account the request is logged in as, and
	// false for a request without a session. An error means the session store
	// could not answer. The password change handler needs it. Required.
	CurrentIdentity func(r *http.Request) (Identity, bool, error)

	// AllowSignup decides whether a signup may go on, for a closed
	// registration, an invite check or a limit. Nil allows every signup, and
	// a product that offers none mounts no signup handler at all. A returned
	// *api.Error is answered as it is. Any other error is answered as 403
	// without its text.
	AllowSignup func(r *http.Request) error

	// TrustedOrigins are the cross origin callers every handler accepts, each a
	// scheme and host without a trailing slash, for a frontend served from
	// another host. Zero trusts this origin only.
	TrustedOrigins []string

	// MaxBodyBytes limits a request body. Zero is [DefaultMaxBodyBytes].
	MaxBodyBytes int64
}

// Handlers are the HTTP handlers of local accounts: signup, TOTP
// confirmation, login, password change and logout, as JSON over POST. Build
// it once at startup and mount the handlers. It is safe for concurrent use.
//
// Each handler refuses a method other than POST, a request that
// net/http.CrossOriginProtection refuses, and a body that is not one JSON
// object of the documented fields under the application/json content type, up
// to the body limit. The origin check lives in the handler, so mounting it
// without a CSRF middleware in front does not leave a login open to a cross
// site form. A session bound CSRF token is the application's middleware, as
// for any other unsafe route.
type Handlers struct {
	service      *Service
	start        func(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity) error
	end          func(ctx context.Context, w http.ResponseWriter, r *http.Request) error
	current      func(r *http.Request) (Identity, bool, error)
	allowSignup  func(r *http.Request) error
	cross        *http.CrossOriginProtection
	maxBodyBytes int64
}

// NewHandlers validates cfg and returns the handlers. Like the other
// constructors of this module it names every problem at once.
func NewHandlers(cfg HandlerConfig) (*Handlers, error) {
	var problems []error
	if cfg.Service == nil {
		problems = append(problems, errors.New("service is required"))
	}
	if cfg.StartSession == nil {
		problems = append(problems, errors.New("start session is required"))
	}
	if cfg.EndSession == nil {
		problems = append(problems, errors.New("end session is required"))
	}
	if cfg.CurrentIdentity == nil {
		problems = append(problems, errors.New("current identity is required"))
	}
	if cfg.MaxBodyBytes < 0 {
		problems = append(problems, fmt.Errorf("max body bytes %d is negative", cfg.MaxBodyBytes))
	}

	cross := http.NewCrossOriginProtection()
	for _, origin := range cfg.TrustedOrigins {
		if err := cross.AddTrustedOrigin(origin); err != nil {
			problems = append(problems, fmt.Errorf("trusted origin %q: %w", origin, err))
		}
	}

	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("account handlers: %w", err)
	}

	limit := cfg.MaxBodyBytes
	if limit == 0 {
		limit = DefaultMaxBodyBytes
	}

	return &Handlers{
		service:      cfg.Service,
		start:        cfg.StartSession,
		end:          cfg.EndSession,
		current:      cfg.CurrentIdentity,
		allowSignup:  cfg.AllowSignup,
		cross:        cross,
		maxBodyBytes: limit,
	}, nil
}

// Signup creates an account and answers 201 with a [SignupResult], the
// enrolment of the new TOTP secret shown once. It starts no session: the
// account cannot log in before [Handlers.ConfirmTOTP].
//
// A login that is taken answers 409. That is an account existence oracle, as
// [Service.SignUp] documents, so put the rate limit of the login route in
// front of it as well.
func (h *Handlers) Signup() http.Handler {
	return h.post(func(w http.ResponseWriter, r *http.Request) error {
		if h.allowSignup != nil {
			if err := h.allowSignup(r); err != nil {
				var apiErr *api.Error
				if errors.As(err, &apiErr) {
					return apiErr
				}

				return api.PermissionDenied(messageSignupClosed).WithCause(err)
			}
		}

		var req SignupRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}

		id, enrolment, err := h.service.SignUp(r.Context(), req.Login, req.Password)
		if err != nil {
			return signupError(err)
		}

		return api.Write(w, http.StatusCreated, SignupResult{
			Login:     id.Login,
			Enrolment: EnrolmentResult(enrolment),
		})
	})
}

// ConfirmTOTP confirms the TOTP secret of a fresh account with a code, gated
// on the password, and starts a session through [HandlerConfig.StartSession],
// since the call proved both factors. It answers 200 with a [LoginResult].
// Every refusal answers exactly like a refused login.
func (h *Handlers) ConfirmTOTP() http.Handler {
	return h.post(func(w http.ResponseWriter, r *http.Request) error {
		var req CredentialsRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}

		id, err := h.service.ConfirmTOTP(r.Context(), req.Login, req.Password, req.Code)
		if err != nil {
			return credentialsError(err)
		}

		return h.startAndAnswer(w, r, id)
	})
}

// Login checks login, password and code in one call, starts a session through
// [HandlerConfig.StartSession] and answers 200 with a [LoginResult]. There is
// no half logged in state between two requests.
//
// Every refusal answers 401 with one body: an unknown login, a wrong password,
// a wrong, missing or replayed code, an unconfirmed secret and a disabled
// account. A server without capacity to hash answers 503 with Retry-After,
// which says nothing about the guess. No session is started on any failure.
func (h *Handlers) Login() http.Handler {
	return h.post(func(w http.ResponseWriter, r *http.Request) error {
		var req CredentialsRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}

		id, err := h.service.Login(r.Context(), req.Login, req.Password, req.Code)
		if err != nil {
			return credentialsError(err)
		}

		return h.startAndAnswer(w, r, id)
	})
}

// ChangePassword changes the password of the logged in account and answers
// with the outcome of [Service.ChangePassword]:
//
//   - 200 with SessionsEnded true: the password changed and every session
//     ended, the caller's own included. The person logs in again.
//   - 200 with SessionsEnded false: the password changed, ending the sessions
//     failed. The change stands.
//   - 409 with [CodeStale]: nothing changed, try again.
//   - 500 with [CodePasswordChangeUnknown]: the change may not have happened.
//
// A request without a session answers 401, a wrong current password 403 with
// [CodeInvalidCredentials], a new password that breaks the policy 400 with the
// field in the details, and a server without hashing capacity 503.
func (h *Handlers) ChangePassword() http.Handler {
	return h.post(func(w http.ResponseWriter, r *http.Request) error {
		id, ok, err := h.current(r)
		if err != nil {
			return api.Unavailable("session store is unavailable").WithCause(err)
		}
		if !ok {
			return api.Unauthenticated("log in first")
		}

		var req PasswordChangeRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}

		err = h.service.ChangePassword(r.Context(), id, req.CurrentPassword, req.NewPassword)

		switch {
		case err == nil:
			return api.Write(w, http.StatusOK, PasswordChangeResult{PasswordChanged: true, SessionsEnded: true})
		case errors.Is(err, ErrSessionsNotEnded):
			// The change committed. The cause is the callback's. It goes to
			// the log and never to the client.
			logging.FromContext(r.Context()).ErrorContext(r.Context(),
				"account: password changed, ending the sessions failed", "error", err.Error())

			return api.Write(w, http.StatusOK, PasswordChangeResult{PasswordChanged: true, SessionsEnded: false})
		case errors.Is(err, ErrStale):
			return api.New(http.StatusConflict, CodeStale, messageStale)
		case errors.Is(err, ErrInvalidCredentials):
			return api.New(http.StatusForbidden, CodeInvalidCredentials, messageWrongCurrent)
		case errors.Is(err, ErrPasswordTooShort):
			return api.Invalid("the new password is too short").WithDetails(map[string]any{"new_password": DetailTooShort})
		case errors.Is(err, ErrPasswordTooLong):
			return api.Invalid("the new password is too long").WithDetails(map[string]any{"new_password": DetailTooLong})
		case errors.Is(err, ErrHashingBusy):
			return busy()
		default:
			return api.New(http.StatusInternalServerError, CodePasswordChangeUnknown, messageUnknown).
				Exposed().WithCause(err)
		}
	})
}

// Logout ends the session the request carries through
// [HandlerConfig.EndSession] and answers 204. A request without a session is
// answered the same way.
func (h *Handlers) Logout() http.Handler {
	return h.post(func(w http.ResponseWriter, r *http.Request) error {
		if err := h.end(r.Context(), w, r); err != nil {
			return api.Internal(fmt.Errorf("account: end session: %w", err))
		}

		api.NoContent(w)

		return nil
	})
}

// post wraps fn in the checks every handler makes before it reads anything:
// the method, then the origin.
func (h *Handlers) post(fn api.HandlerFunc) http.Handler {
	return api.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)

			return api.New(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
		if err := h.cross.Check(r); err != nil {
			return api.PermissionDenied(messageCrossOrigin).WithCause(err)
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

// startAndAnswer starts the session of a verified identity and answers.
func (h *Handlers) startAndAnswer(w http.ResponseWriter, r *http.Request, id Identity) error {
	if err := h.start(r.Context(), w, r, id); err != nil {
		return api.Internal(fmt.Errorf("account: start session: %w", err))
	}

	return api.Write(w, http.StatusOK, LoginResult{Login: id.Login})
}

// credentialsError maps what Login and ConfirmTOTP return. Every refusal is
// one error, so every refusal is one status and one body.
func credentialsError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		return api.New(http.StatusUnauthorized, CodeInvalidCredentials, messageInvalidCredentials)
	case errors.Is(err, ErrHashingBusy):
		return busy()
	default:
		return api.Internal(err)
	}
}

// signupError maps what SignUp returns.
func signupError(err error) error {
	switch {
	case errors.Is(err, ErrLoginInvalid):
		return api.Invalid("the login is not valid").WithDetails(map[string]any{"login": DetailInvalid})
	case errors.Is(err, ErrPasswordTooShort):
		return api.Invalid("the password is too short").WithDetails(map[string]any{"password": DetailTooShort})
	case errors.Is(err, ErrPasswordTooLong):
		return api.Invalid("the password is too long").WithDetails(map[string]any{"password": DetailTooLong})
	case errors.Is(err, ErrConflict):
		return api.Conflict(messageLoginTaken)
	case errors.Is(err, ErrHashingBusy):
		return busy()
	default:
		return api.Internal(err)
	}
}

// busy is the 503 of a server without hashing capacity. Its message is chosen
// here and safe to send, hence Exposed.
func busy() error {
	err := api.Unavailable(messageBusy).Exposed()
	err.RetryAfter = HashingBusyRetryAfter

	return err
}
