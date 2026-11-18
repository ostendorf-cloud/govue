// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package passkey

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/api"
	"github.com/ostendorf-cloud/govue/logging"
)

// The name of the challenge cookie. The __Host- prefix makes a browser refuse
// it unless it is Secure, host only and on path /, so a sibling subdomain
// cannot plant one. Without TLS the prefix is dropped with the Secure
// attribute, as session.Config.AllowPlainHTTP does it.
const (
	ChallengeCookieName      = "__Host-govue-passkey"
	PlainChallengeCookieName = "govue-passkey"
)

// MaxNameLength is the longest passkey label, counted in characters.
const MaxNameLength = 64

const (
	messageInvalidPasskey = "the passkey could not be registered"
	messageInvalidLogin   = "the passkey could not be verified"
)

// The values of the details map of a refused name. They are tokens a client
// maps to its own texts, as in the account package.
const (
	DetailInvalid = "invalid"
	DetailTooLong = "too_long"
)

// BeginRequest is the body of both begin handlers: an empty object. It has no
// field, so the strict decoder refuses any.
type BeginRequest struct{}

// BeginResult is the 200 body of both begin handlers. PublicKey is the
// WebAuthn options object, ready for PublicKeyCredential.parseCreationOptionsFromJSON
// or parseRequestOptionsFromJSON.
type BeginResult struct {
	PublicKey any `json:"public_key"`
}

// RegisterFinishRequest is the body of [Handlers.RegisterFinish].
type RegisterFinishRequest struct {
	// Credential is the JSON of PublicKeyCredential.toJSON() after create.
	Credential json.RawMessage `json:"credential"`

	// Name is the optional label of the passkey.
	Name string `json:"name"`
}

// RegisterResult is the 201 body of [Handlers.RegisterFinish].
type RegisterResult struct {
	Passkey Info `json:"passkey"`
}

// LoginFinishRequest is the body of [Handlers.LoginFinish].
type LoginFinishRequest struct {
	// Credential is the JSON of PublicKeyCredential.toJSON() after get.
	Credential json.RawMessage `json:"credential"`
}

// newWebAuthn builds the library's relying party from a validated config.
// The library confines itself to this package: no exported name carries one
// of its types.
func newWebAuthn(cfg Config) (*webauthn.WebAuthn, error) {
	timeout := webauthn.TimeoutConfig{Timeout: cfg.ChallengeTTL, TimeoutUVD: cfg.ChallengeTTL}

	return webauthn.New(&webauthn.Config{
		RPID:                  cfg.RPID,
		RPDisplayName:         cfg.RPDisplayName,
		RPOrigins:             cfg.Origins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
		// The expiry is this package's, through the challenge store and
		// Config.Now. The library's own expiry reads time.Now and stays off.
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
}

// user is the library's view of an account during one ceremony.
type user struct {
	handle []byte
	name   string
	creds  []webauthn.Credential
}

// WebAuthnID is the random user handle, never the account id.
func (u *user) WebAuthnID() []byte { return u.handle }

// WebAuthnName is the login, which an authenticator shows next to the passkey.
func (u *user) WebAuthnName() string { return u.name }

// WebAuthnDisplayName is the login as well: the account package has no
// display name.
func (u *user) WebAuthnDisplayName() string { return u.name }

// WebAuthnCredentials are the account's passkeys.
func (u *user) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// libraryCredentials decodes the stored credentials. The stored SignCount is
// the truth, not the copy inside Data, so the library compares a login's
// counter with the value the compare and set guards.
func libraryCredentials(creds []Credential) ([]webauthn.Credential, error) {
	out := make([]webauthn.Credential, 0, len(creds))
	for _, c := range creds {
		var wc webauthn.Credential
		if err := json.Unmarshal(c.Data, &wc); err != nil {
			return nil, fmt.Errorf("passkey: decode credential: %w", err)
		}
		wc.ID = c.ID
		wc.Authenticator.SignCount = c.SignCount
		wc.Authenticator.CloneWarning = false
		out = append(out, wc)
	}

	return out, nil
}

// RegisterBegin starts registering a passkey for the logged in account and
// answers 200 with a [BeginResult] and the challenge cookie. It needs a login
// within [Config.ReauthWindow]: 401 without a session, 403 with
// [CodeReauthenticationRequired] for an older one. The options ask for a
// discoverable credential, user verification and no attestation, and list
// the account's passkeys so an authenticator does not register twice.
func (h *Handlers) RegisterBegin() http.Handler {
	return h.handle(http.MethodPost, func(w http.ResponseWriter, r *http.Request) error {
		id, err := h.recent(r)
		if err != nil {
			return err
		}
		var req BeginRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}

		stored, err := h.cfg.Store.Credentials(r.Context(), id.ID)
		if err != nil {
			return api.Internal(fmt.Errorf("passkey: list credentials: %w", err))
		}
		creds, err := libraryCredentials(stored)
		if err != nil {
			return api.Internal(err)
		}
		handle, err := userHandle(stored)
		if err != nil {
			return api.Internal(err)
		}

		creation, session, err := h.wa.BeginRegistration(
			&user{handle: handle, name: id.Login, creds: creds},
			webauthn.WithExclusions(webauthn.Credentials(creds).CredentialDescriptors()),
		)
		if err != nil {
			return api.Internal(fmt.Errorf("passkey: begin registration: %w", err))
		}

		if err := h.putChallenge(w, r, CeremonyRegistration, id.ID, session); err != nil {
			return err
		}

		return api.Write(w, http.StatusOK, BeginResult{PublicKey: creation.Response})
	})
}

// RegisterFinish verifies the new credential against the challenge of
// [Handlers.RegisterBegin], stores it and answers 201 with a
// [RegisterResult]. Of two first registrations racing from two browsers,
// each with its own new user handle, exactly one succeeds. The other gets the
// one refusal and begins again with the handle the account now has. The challenge is taken, and the cookie cleared, before
// anything else is checked, so it is gone whatever the outcome.
//
// It needs the same account that began and a login within
// [Config.ReauthWindow]. Every refused ceremony answers 400 with
// [CodeInvalidPasskey] and one body: no challenge, an expired or used one,
// one of another account or of a login, a wrong origin or RP id hash, no user
// verification, a malformed credential and a credential id already stored.
func (h *Handlers) RegisterFinish() http.Handler {
	return h.handle(http.MethodPost, func(w http.ResponseWriter, r *http.Request) error {
		challenge, found, err := h.takeChallenge(w, r)
		if err != nil {
			return err
		}
		id, err := h.recent(r)
		if err != nil {
			return err
		}
		var req RegisterFinishRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}
		if err := checkName(req.Name); err != nil {
			return err
		}

		refused := func(cause error) error {
			logging.FromContext(r.Context()).DebugContext(r.Context(),
				"passkey: registration refused", "account", id.ID, "error", cause.Error())

			return api.New(http.StatusBadRequest, CodeInvalidPasskey, messageInvalidPasskey)
		}

		if !found || challenge.Ceremony != CeremonyRegistration || challenge.AccountID != id.ID {
			return refused(errors.New("no matching registration challenge"))
		}
		session, err := h.session(challenge)
		if err != nil {
			return refused(err)
		}
		parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
		if err != nil {
			return refused(err)
		}
		cred, err := h.wa.CreateCredential(&user{handle: session.UserID, name: id.Login}, session, parsed)
		if err != nil {
			return refused(err)
		}

		// An account has one user handle. A registration begun before another
		// one finished carries a handle the account no longer has: refuse it,
		// and the person begins again with the stored handle. The store makes
		// the same check atomic with the insert, see Store.AddCredential.
		current, err := h.cfg.Store.Credentials(r.Context(), id.ID)
		if err != nil {
			return api.Internal(fmt.Errorf("passkey: list credentials: %w", err))
		}
		for _, c := range current {
			if !bytes.Equal(c.UserHandle, session.UserID) {
				return refused(errors.New("the account has another user handle"))
			}
		}

		data, err := json.Marshal(cred)
		if err != nil {
			return api.Internal(fmt.Errorf("passkey: encode credential: %w", err))
		}
		stored := Credential{
			ID:         cred.ID,
			AccountID:  id.ID,
			UserHandle: session.UserID,
			Name:       req.Name,
			SignCount:  cred.Authenticator.SignCount,
			Data:       data,
			CreatedAt:  h.cfg.Now(),
		}
		switch err := h.cfg.Store.AddCredential(r.Context(), stored); {
		case errors.Is(err, ErrConflict):
			return refused(err)
		case err != nil:
			return api.Internal(fmt.Errorf("passkey: add credential: %w", err))
		}

		return api.Write(w, http.StatusCreated, RegisterResult{Passkey: info(stored)})
	})
}

// LoginBegin starts a discoverable login and answers 200 with a
// [BeginResult] and the challenge cookie. No session and no login name are
// needed. The options require user verification and list no credential.
func (h *Handlers) LoginBegin() http.Handler {
	return h.handle(http.MethodPost, func(w http.ResponseWriter, r *http.Request) error {
		var req BeginRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}

		assertion, session, err := h.wa.BeginDiscoverableLogin(
			webauthn.WithUserVerification(protocol.VerificationRequired),
		)
		if err != nil {
			return api.Internal(fmt.Errorf("passkey: begin login: %w", err))
		}

		if err := h.putChallenge(w, r, CeremonyLogin, "", session); err != nil {
			return err
		}

		return api.Write(w, http.StatusOK, BeginResult{PublicKey: assertion.Response})
	})
}

// LoginFinish verifies the assertion against the challenge of
// [Handlers.LoginBegin], starts a session through [Config.StartSession] and
// answers 200 with an [account.LoginResult], the body of a password login.
// The challenge is taken and the cookie cleared first, whatever the outcome.
//
// Every refusal answers 401 with [account.CodeInvalidCredentials] and one
// body: no challenge, an expired or used one, one of a registration, an
// unknown credential or user handle, a disabled account, a wrong origin or RP
// id hash, a bad signature, no user verification and a sign count that did
// not move forward, which is how a cloned authenticator shows. A store or a
// session failure is a 500, since that is no answer about the passkey.
func (h *Handlers) LoginFinish() http.Handler {
	return h.handle(http.MethodPost, func(w http.ResponseWriter, r *http.Request) error {
		challenge, found, err := h.takeChallenge(w, r)
		if err != nil {
			return err
		}
		var req LoginFinishRequest
		if err := h.decode(r, &req); err != nil {
			return err
		}

		refused := func(cause error) error {
			logging.FromContext(r.Context()).DebugContext(r.Context(),
				"passkey: login refused", "error", cause.Error())

			return api.New(http.StatusUnauthorized, account.CodeInvalidCredentials, messageInvalidLogin)
		}

		if !found || challenge.Ceremony != CeremonyLogin {
			return refused(errors.New("no matching login challenge"))
		}
		session, err := h.session(challenge)
		if err != nil {
			return refused(err)
		}
		parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
		if err != nil {
			return refused(err)
		}

		var (
			who      account.Identity
			stored   []Credential
			storeErr error
		)
		lookup := func(_, handle []byte) (webauthn.User, error) {
			who, stored, storeErr = h.cfg.Store.FindByUserHandle(r.Context(), handle)
			if storeErr != nil {
				return nil, storeErr
			}
			creds, err := libraryCredentials(stored)
			if err != nil {
				storeErr = err
				return nil, err
			}

			return &user{handle: handle, name: who.Login, creds: creds}, nil
		}

		_, cred, err := h.wa.ValidatePasskeyLogin(lookup, session, parsed)
		if storeErr != nil && !errors.Is(storeErr, ErrNotFound) {
			return api.Internal(fmt.Errorf("passkey: find by user handle: %w", storeErr))
		}
		if err != nil {
			return refused(err)
		}
		if cred.Authenticator.CloneWarning {
			return refused(errors.New("sign count did not move forward"))
		}

		var old *Credential
		for i := range stored {
			if string(stored[i].ID) == string(cred.ID) {
				old = &stored[i]
			}
		}
		if old == nil || old.AccountID != who.ID {
			return refused(errors.New("credential not in the account's list"))
		}

		data, err := json.Marshal(cred)
		if err != nil {
			return api.Internal(fmt.Errorf("passkey: encode credential: %w", err))
		}
		next := *old
		next.SignCount = cred.Authenticator.SignCount
		next.LastUsedAt = h.cfg.Now()
		next.Data = data
		switch err := h.cfg.Store.UpdateCredential(r.Context(), next, old.SignCount); {
		case errors.Is(err, ErrNotFound):
			// Another login with the same counter value won the race.
			return refused(err)
		case err != nil:
			return api.Internal(fmt.Errorf("passkey: update credential: %w", err))
		}

		if err := h.cfg.StartSession(r.Context(), w, r, who); err != nil {
			return api.Internal(fmt.Errorf("passkey: start session: %w", err))
		}

		return api.Write(w, http.StatusOK, account.LoginResult{Login: who.Login})
	})
}

// putChallenge stores the ceremony state and sets the cookie.
func (h *Handlers) putChallenge(w http.ResponseWriter, r *http.Request, ceremony Ceremony, accountID string, session *webauthn.SessionData) error {
	data, err := json.Marshal(session)
	if err != nil {
		return api.Internal(fmt.Errorf("passkey: encode session: %w", err))
	}
	id, err := newChallengeID()
	if err != nil {
		return api.Internal(err)
	}
	c := Challenge{Ceremony: ceremony, AccountID: accountID, Data: data, ExpiresAt: h.cfg.Now().Add(h.cfg.ChallengeTTL)}
	if err := h.challenges.Put(r.Context(), id, c); err != nil {
		return api.Internal(fmt.Errorf("passkey: put challenge: %w", err))
	}

	cookie := h.cookie()
	cookie.Value = id
	cookie.MaxAge = int(h.cfg.ChallengeTTL / time.Second)
	http.SetCookie(w, cookie)

	return nil
}

// takeChallenge clears the cookie and takes the challenge it names. found is
// false for a missing cookie, a missing or used challenge and an expired one.
// Only a failing store is an error.
func (h *Handlers) takeChallenge(w http.ResponseWriter, r *http.Request) (Challenge, bool, error) {
	clear := h.cookie()
	clear.MaxAge = -1
	http.SetCookie(w, clear)

	cookie, err := r.Cookie(h.cookieName())
	if err != nil || cookie.Value == "" {
		return Challenge{}, false, nil
	}
	c, err := h.challenges.Take(r.Context(), cookie.Value)
	switch {
	case errors.Is(err, ErrNotFound):
		return Challenge{}, false, nil
	case err != nil:
		return Challenge{}, false, api.Internal(fmt.Errorf("passkey: take challenge: %w", err))
	}
	// A store that keeps an expired challenge does not get it through.
	if !h.cfg.Now().Before(c.ExpiresAt) {
		return Challenge{}, false, nil
	}

	return c, true, nil
}

// session decodes the library's state of a challenge.
func (h *Handlers) session(c Challenge) (webauthn.SessionData, error) {
	var s webauthn.SessionData
	if err := json.Unmarshal(c.Data, &s); err != nil {
		return webauthn.SessionData{}, fmt.Errorf("passkey: decode session: %w", err)
	}

	return s, nil
}

func (h *Handlers) cookieName() string {
	if h.cfg.AllowPlainHTTP {
		return PlainChallengeCookieName
	}

	return ChallengeCookieName
}

// cookie is the challenge cookie without a value and a Max-Age.
func (h *Handlers) cookie() *http.Cookie {
	return &http.Cookie{
		Name:     h.cookieName(),
		Path:     "/",
		HttpOnly: true,
		Secure:   !h.cfg.AllowPlainHTTP,
		SameSite: http.SameSiteStrictMode,
	}
}

// userHandle returns the account's user handle: the one its passkeys carry,
// or a new random one for its first passkey.
func userHandle(stored []Credential) ([]byte, error) {
	for _, c := range stored {
		if len(c.UserHandle) > 0 {
			return c.UserHandle, nil
		}
	}
	handle := make([]byte, UserHandleBytes)
	if _, err := rand.Read(handle); err != nil {
		return nil, fmt.Errorf("passkey: user handle: %w", err)
	}

	return handle, nil
}

// checkName refuses a label that is too long or holds a character that is
// not printable.
func checkName(name string) error {
	if !utf8.ValidString(name) {
		return api.Invalid("the name is not valid").WithDetails(map[string]any{"name": DetailInvalid})
	}
	if utf8.RuneCountInString(name) > MaxNameLength {
		return api.Invalid("the name is too long").WithDetails(map[string]any{"name": DetailTooLong})
	}
	for _, c := range name {
		if !unicode.IsPrint(c) {
			return api.Invalid("the name is not valid").WithDetails(map[string]any{"name": DetailInvalid})
		}
	}

	return nil
}
