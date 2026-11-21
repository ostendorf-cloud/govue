// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/internal/configtext"
)

// The error codes of the handlers, next to the codes of the api package and
// [account.CodeInvalidCredentials], which a refused passkey login answers.
// Codes are stable and machine readable, and a client may branch on them.
const (
	// CodeReauthenticationRequired is a session whose login is older than
	// [Config.ReauthWindow], for a step that needs a recent one. The person
	// logs in again and retries.
	CodeReauthenticationRequired = "reauthentication_required"

	// CodeLastFactor is a delete that would leave the account without a way
	// to log in.
	CodeLastFactor = "last_factor"

	// CodeInvalidPasskey is the one code of a refused registration.
	CodeInvalidPasskey = "invalid_passkey"
)

const (
	messageCrossOrigin = "cross origin request refused"
	messageLogInFirst  = "log in first"
	messageReauth      = "log in again to change your passkeys"
	messageLastFactor  = "the last sign in factor cannot be removed"
	messageNotFound    = "no such passkey"
)

// Info is one passkey as the list and the registration answer it.
// Fields may be added in a later release.
type Info struct {
	// ID is the credential id in base64url without padding.
	ID string `json:"id"`

	// Name is the label the person gave it, possibly empty.
	Name string `json:"name"`

	// CreatedAt is when it was registered.
	CreatedAt time.Time `json:"created_at"`

	// LastUsedAt is the last login with it, null before the first.
	LastUsedAt *time.Time `json:"last_used_at"`
}

// ListResult is the 200 body of [Handlers.List].
type ListResult struct {
	Passkeys []Info `json:"passkeys"`
}

// DeleteRequest is the body of [Handlers.Delete].
type DeleteRequest struct {
	// ID is the credential id in base64url without padding.
	ID string `json:"id"`
}

// Handlers are the HTTP handlers of passkeys. Build them once at startup
// with [New] and mount them. They are safe for concurrent use.
//
// Like the account handlers, each one checks the method, then the origin
// with net/http.CrossOriginProtection, then a strict JSON body. The origin
// check lives in the handler for the reason the account handlers give: a
// passkey login has no session to bind a CSRF token to, and mounting it
// behind a middleware somebody forgot must not leave it open to a cross site
// request. A session bound CSRF token stays the application's middleware.
type Handlers struct {
	cfg        Config
	challenges ChallengeStore
	cross      *http.CrossOriginProtection
	wa         *webauthn.WebAuthn
}

// New validates cfg and returns the handlers. It names every problem at once.
func New(cfg Config) (*Handlers, error) {
	var problems []error

	valid, err := cfg.validate()
	if err != nil {
		problems = append(problems, err)
	}

	cross := http.NewCrossOriginProtection()
	for _, origin := range cfg.TrustedOrigins {
		// The error of AddTrustedOrigin quotes the origin, and the one of
		// url.Parse inside it quotes it again, so neither is wrapped.
		if cross.AddTrustedOrigin(origin) != nil {
			problems = append(problems, fmt.Errorf("trusted origin %s must be scheme://host[:port] "+
				"without a path, a query or a fragment", configtext.Quote(origin)))
		}
	}

	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("passkey: %w", err)
	}

	// The library checks the RP id and the origins once more, with its own
	// rules. A refusal there is a startup error as well.
	wa, err := newWebAuthn(valid)
	if err != nil {
		return nil, fmt.Errorf("passkey: relying party: %w", err)
	}

	challenges := valid.Challenges
	if challenges == nil {
		mem, err := NewMemoryChallengeStore(MemoryChallengeConfig{Now: valid.Now})
		if err != nil {
			return nil, fmt.Errorf("passkey: %w", err)
		}
		challenges = mem
	}

	return &Handlers{cfg: valid, challenges: challenges, cross: cross, wa: wa}, nil
}

// List answers 200 with the passkeys of the logged in account as a
// [ListResult], and 401 without a session. It is a GET and needs no recent
// login.
func (h *Handlers) List() http.Handler {
	return h.handle(http.MethodGet, func(w http.ResponseWriter, r *http.Request) error {
		id, _, err := h.current(r)
		if err != nil {
			return err
		}

		creds, err := h.cfg.Store.Credentials(r.Context(), id.ID)
		if err != nil {
			return api.Internal(fmt.Errorf("passkey: list credentials: %w", err))
		}

		out := ListResult{Passkeys: make([]Info, 0, len(creds))}
		for _, c := range creds {
			out.Passkeys = append(out.Passkeys, info(c))
		}

		return api.Write(w, http.StatusOK, out)
	})
}

// Delete deletes one passkey of the logged in account and answers 204. It
// needs a login within [Config.ReauthWindow], else 403 with
// [CodeReauthenticationRequired]. A passkey that is not the account's
// answers 404, whoever holds it. The last passkey of an account for which
// [Config.OtherFactors] reports zero answers 409 with [CodeLastFactor]: the
// account would have no way left to log in.
func (h *Handlers) Delete() http.Handler {
	return h.handle(http.MethodPost, func(w http.ResponseWriter, r *http.Request) error {
		id, err := h.recent(r)
		if err != nil {
			return err
		}

		var req DeleteRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}
		credID, err := base64.RawURLEncoding.DecodeString(req.ID)
		if err != nil || len(credID) == 0 {
			return api.NotFound(messageNotFound)
		}

		others, err := h.cfg.OtherFactors(r.Context(), id)
		if err != nil {
			return api.Internal(fmt.Errorf("passkey: other factors: %w", err))
		}
		keepOne := others <= 0

		err = h.cfg.Store.DeleteCredential(r.Context(), id.ID, credID, keepOne)
		switch {
		case err == nil:
			api.NoContent(w)

			return nil
		case !errors.Is(err, ErrNotFound):
			return api.Internal(fmt.Errorf("passkey: delete credential: %w", err))
		case !keepOne:
			return api.NotFound(messageNotFound)
		}

		// Nothing was deleted with keepOne set: either the passkey is not the
		// account's, or it is its last one.
		owned, err := h.owns(r, id.ID, credID)
		if err != nil {
			return err
		}
		if owned {
			return api.New(http.StatusConflict, CodeLastFactor, messageLastFactor)
		}

		return api.NotFound(messageNotFound)
	})
}

// owns reports whether the account holds the credential id.
func (h *Handlers) owns(r *http.Request, accountID string, credID []byte) (bool, error) {
	creds, err := h.cfg.Store.Credentials(r.Context(), accountID)
	if err != nil {
		return false, api.Internal(fmt.Errorf("passkey: list credentials: %w", err))
	}
	for _, c := range creds {
		if string(c.ID) == string(credID) {
			return true, nil
		}
	}

	return false, nil
}

// current returns the logged in account and when it logged in, or the error
// to answer with.
func (h *Handlers) current(r *http.Request) (account.Identity, time.Time, error) {
	id, at, ok, err := h.cfg.CurrentAccount(r)
	if err != nil {
		return account.Identity{}, time.Time{}, api.Unavailable("session store is unavailable").WithCause(err)
	}
	if !ok || id.ID == "" {
		return account.Identity{}, time.Time{}, api.Unauthenticated(messageLogInFirst)
	}

	return id, at, nil
}

// recent is current with the login no older than the reauth window. A login
// time that is zero or in the future fails closed: the session cannot say
// when the person last proved who they are.
func (h *Handlers) recent(r *http.Request) (account.Identity, error) {
	id, at, err := h.current(r)
	if err != nil {
		return account.Identity{}, err
	}
	now := h.cfg.Now()
	if at.IsZero() || at.After(now) || now.Sub(at) > h.cfg.ReauthWindow {
		return account.Identity{}, api.New(http.StatusForbidden, CodeReauthenticationRequired, messageReauth)
	}

	return id, nil
}

// handle wraps fn in the checks every handler makes before it reads
// anything: the method, then the origin.
func (h *Handlers) handle(method string, fn api.HandlerFunc) http.Handler {
	return api.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if r.Method != method {
			w.Header().Set("Allow", method)

			return api.New(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
		if err := h.cross.Check(r); err != nil {
			return api.PermissionDenied(messageCrossOrigin).WithCause(err)
		}

		return fn(w, r)
	})
}

// decode reads one JSON object into into, as the account handlers do: the
// content type has to be application/json, and api.DecodeLimit refuses a
// body over the limit, an unknown field and a second value.
func (h *Handlers) decode(r *http.Request, into any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return api.New(http.StatusUnsupportedMediaType, api.CodeUnsupportedMediaType,
			"content type must be application/json")
	}

	return api.DecodeLimit(r, into, h.cfg.MaxBodyBytes)
}

// info is the wire form of a credential.
func info(c Credential) Info {
	out := Info{
		ID:        base64.RawURLEncoding.EncodeToString(c.ID),
		Name:      c.Name,
		CreatedAt: c.CreatedAt.UTC(),
	}
	if !c.LastUsedAt.IsZero() {
		used := c.LastUsedAt.UTC()
		out.LastUsedAt = &used
	}

	return out
}
