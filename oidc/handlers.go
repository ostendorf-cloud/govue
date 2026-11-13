// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/ostendorf-cloud/govue/logging"
)

// DefaultNextParam is the query parameter [Handlers.Login] reads the post login
// target from when [HandlerConfig.NextParam] is empty.
const DefaultNextParam = "next"

// LoginStore keeps a login in flight between the redirect to the identity
// provider and the callback that comes back from it.
//
// It is an interface, and a small one, because this package imports nothing
// from a session package and is not going to start: a login that has not
// happened yet has no session to live in. A *session.Codec satisfies it as it
// is, with no adapter in between, which is what makes wiring these handlers up
// three lines rather than a file. An application that signs its login cookie
// some other way implements these three methods instead.
//
// Read is expected to clear the login whatever the outcome. A login is good for
// exactly one callback, and *session.Codec does that already.
type LoginStore interface {
	SetLoginCookie(w http.ResponseWriter, value any) error
	ReadLoginCookie(w http.ResponseWriter, r *http.Request, into any) error
	ClearLoginCookie(w http.ResponseWriter)
}

// HandlerConfig configures [Handlers]. Provider, Logins and StartSession are
// required; every other field is secure when left zero.
type HandlerConfig struct {
	// Provider is the issuer and client registration the login runs against.
	// Required.
	Provider *Provider

	// Logins keeps the login in flight. Required.
	Logins LoginStore

	// StartSession is where the application turns a verified [Identity] into
	// its own session, typically one call to a session manager's Start.
	// Required: this package has no session of its own and must not guess what
	// belongs in one.
	//
	// An error it returns is answered as [ErrProvider] and never reaches the
	// browser as itself. That is the right answer for what usually fails here,
	// a session store that could not be written, and it is why an account that
	// authenticated fine but may not use this application is better refused by
	// the application's own authorisation check than by an error from here: a
	// refusal is not something the browser should be told to retry against.
	StartSession func(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity) error

	// Success is where the browser goes when the login carried no target of its
	// own. Zero is "/".
	Success string

	// NextParam is the query parameter [Handlers.Login] reads the post login
	// target from. Zero is [DefaultNextParam]. An empty string after defaulting
	// is impossible; set NoNext to turn the parameter off.
	NextParam string

	// NoNext ignores the query parameter entirely, so every login ends at
	// Success. It is for an application that decides where a login lands
	// itself and would rather not carry a value from a query string at all.
	NoNext bool

	// OnError replaces what a refused login is answered with, for an
	// application that answers HTML instead of plain text. Zero is 503 for
	// [ErrProvider] and 400 for everything else, each a short sentence with no
	// cause in it.
	//
	// The error is one of the two opaque ones, never a cause: the cause is
	// already in the log by the time this is called.
	OnError func(w http.ResponseWriter, r *http.Request, err error)
}

// Handlers is the pair of HTTP handlers an authorization code login needs, with
// the order of the steps and the single use of the login already decided.
//
// It is safe for concurrent use. Build it once at startup and mount the two
// handlers.
type Handlers struct {
	provider  *Provider
	logins    LoginStore
	start     func(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity) error
	success   string
	nextParam string
	noNext    bool
	onError   func(w http.ResponseWriter, r *http.Request, err error)
}

// NewHandlers validates the configuration and fills in the defaults. Like [New]
// it names every problem at once and is the kind of error read by whoever
// deployed the application, not by a browser.
func NewHandlers(cfg HandlerConfig) (*Handlers, error) {
	var problems []error
	if cfg.Provider == nil {
		problems = append(problems, errors.New("provider is required"))
	}
	if cfg.Logins == nil {
		problems = append(problems, errors.New("login store is required"))
	}
	if cfg.StartSession == nil {
		problems = append(problems, errors.New("start session is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("oidc handlers: %w", err)
	}

	h := &Handlers{
		provider:  cfg.Provider,
		logins:    cfg.Logins,
		start:     cfg.StartSession,
		success:   cfg.Success,
		nextParam: cfg.NextParam,
		noNext:    cfg.NoNext,
		onError:   cfg.OnError,
	}
	if h.success == "" {
		h.success = "/"
	}
	if h.nextParam == "" {
		h.nextParam = DefaultNextParam
	}
	if h.onError == nil {
		h.onError = respondOpaque
	}
	return h, nil
}

// Login starts a login: it generates the state, the nonce and the PKCE
// verifier, keeps them in the login store and redirects the browser to the
// identity provider.
//
// The response carries Cache-Control: no-store. The redirect URL holds the
// state and the nonce of one browser's login attempt, and a cache or a back
// button handing it to the next visitor would hand them that attempt.
func (h *Handlers) Login() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !onlyRead(w, r) {
			return
		}
		ctx := r.Context()

		var next string
		if !h.noNext {
			next = localPath(r.URL.Query().Get(h.nextParam))
		}

		login, err := NewLogin(next)
		if err != nil {
			h.refuse(w, r, ErrProvider, "the login state could not be generated", err)
			return
		}
		if err := h.logins.SetLoginCookie(w, login); err != nil {
			h.refuse(w, r, ErrProvider, "the login could not be stored", err)
			return
		}

		target, err := h.provider.AuthCodeURL(ctx, login)
		if err != nil {
			// The login was already stored, and this attempt is over. Leaving
			// the cookie behind would mean a browser carrying the state of a
			// login it never started.
			h.logins.ClearLoginCookie(w)
			// AuthCodeURL has already written the cause to the log.
			h.onError(w, r, err)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

// Callback finishes a login: it reads the login the browser carries, checks
// what the provider sent back against it, exchanges the authorization code and
// hands the verified identity to [HandlerConfig.StartSession].
//
// The login cookie is read and cleared before the code is exchanged, which is
// the order that matters. A browser, a cache or an attacker replaying the same
// callback URL finds no login to finish, so the state, the nonce and the PKCE
// verifier are good for one attempt only, and a failure anywhere after that
// leaves the browser with no login cookie rather than one it could send again.
//
// Nothing about why a login was refused reaches the browser. Which step failed
// is in the log.
func (h *Handlers) Callback() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !onlyRead(w, r) {
			return
		}
		ctx := r.Context()

		var login Login
		if err := h.logins.ReadLoginCookie(w, r, &login); err != nil {
			h.refuse(w, r, ErrLogin, "the login cookie is missing or does not verify", err)
			return
		}

		query := r.URL.Query()
		if code := query.Get("error"); code != "" {
			// The provider refused the login itself: consent declined, an
			// account that may not use this client, an interaction it needed
			// and could not have. Both fields come from the provider and are
			// shortened before they reach the log, and neither is ever part of
			// the answer.
			h.log(ctx, slog.LevelWarn, "the provider refused the login",
				"provider_error", oauthError(code, query.Get("error_description")))
			h.onError(w, r, ErrLogin)
			return
		}
		if !login.MatchesState(query.Get("state")) {
			// This callback is not the one this browser's login asked for.
			h.log(ctx, slog.LevelWarn, "the state does not match the login")
			h.onError(w, r, ErrLogin)
			return
		}

		identity, err := h.provider.Exchange(ctx, query.Get("code"), login)
		if err != nil {
			// Exchange has already written the cause to the log.
			h.onError(w, r, err)
			return
		}
		if err := h.start(ctx, w, r, identity); err != nil {
			// The account is who it says it is and the application still could
			// not give it a session, which in practice is a session store that
			// cannot be written. It is not something the browser is told.
			h.refuse(w, r, ErrProvider, "the session could not be started", err)
			return
		}

		target := h.success
		// Next was signed by this application, but the value inside it came
		// from a query string in the first place, so it is checked again rather
		// than trusted because it survived a signature.
		if checked := localPath(login.Next); checked != "" {
			target = checked
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

// refuse writes the cause of a refusal this file detected itself to the log and
// answers with the opaque error. The two failures the [Provider] reports have
// already been logged by the time they get here and go straight to the
// responder instead.
func (h *Handlers) refuse(w http.ResponseWriter, r *http.Request, public error, reason string, cause error) {
	level := slog.LevelWarn
	if errors.Is(public, ErrProvider) {
		level = slog.LevelError
	}
	h.log(r.Context(), level, reason, "error", cause.Error())
	h.onError(w, r, public)
}

// log writes one line through the request's logger. No request_id key is added
// here: that is middleware.RequestID's job, and its absence is not papered over
// with an invented one.
func (h *Handlers) log(ctx context.Context, level slog.Level, reason string, args ...any) {
	logging.FromContext(ctx).Log(ctx, level, "oidc login refused", append([]any{"reason", reason}, args...)...)
}

// respondOpaque is what a refused login is answered with unless
// [HandlerConfig.OnError] replaces it: one sentence, no cause, and the status
// the two errors mean. [ErrProvider] is 503 because the application cannot
// reach its identity provider and starting over will not help; everything else
// is 400 because this one attempt is over and the browser can try again.
func respondOpaque(w http.ResponseWriter, _ *http.Request, err error) {
	if errors.Is(err, ErrProvider) {
		http.Error(w, "the login service is unavailable, please try again later", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "the login could not be completed, please start over", http.StatusBadRequest)
}

// onlyRead answers anything but GET and HEAD with 405 and reports whether the
// request may go on.
//
// The check is in the handler rather than left to the route pattern, so that
// mounting the handler as "/login" is as safe as mounting it as "GET /login".
// Both are reasonable ways to write a route and only one of them has the method
// in it.
func onlyRead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// localPath returns target when it is a path on this site, and an empty string
// for everything else.
//
// A redirect target that comes from whoever started the login is an open
// redirect waiting to happen: a login link that lands on somebody else's site
// carries this application's reputation and a believable moment to ask for a
// password again. So only a path is accepted, and every form that parses as
// something other than a path on this host is dropped.
//
// Dropping it is not an error. It is untrusted input from a query string, and
// refusing the whole login over it would turn a stray parameter into a user who
// cannot log in; the login finishes at [HandlerConfig.Success] instead.
func localPath(target string) string {
	// A single leading slash is a path on this host. Two of them, or a slash
	// and a backslash, are a protocol relative URL: "//evil.example" sends the
	// browser to another host, and a browser normalises the backslash form into
	// it. A backslash anywhere else is not something a path on this site needs
	// and is a known way to disagree with a parser about where the host ends.
	// The second character is checked by index rather than with a prefix
	// helper: the length guard keeps that index from reading past a
	// one-character string, and the resulting shape is the one CodeQL's
	// go/bad-redirect-check rule is documented to recognise as sufficient.
	if !strings.HasPrefix(target, "/") ||
		(len(target) > 1 && (target[1] == '/' || target[1] == '\\')) ||
		strings.Contains(target, `\`) {
		return ""
	}
	// A control character does not belong in a URL and is how a header or a log
	// line gets split in two.
	for _, r := range target {
		if unicode.IsControl(r) {
			return ""
		}
	}
	// The parse is the last word: anything that still comes out carrying a
	// scheme or a host is not a path, whatever it looked like.
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return ""
	}
	return target
}
