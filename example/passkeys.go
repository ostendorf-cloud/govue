// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/account/accounttest"
	"github.com/ostendorf-cloud/govue/passkey"
	"github.com/ostendorf-cloud/govue/passkey/passkeytest"
	"github.com/ostendorf-cloud/govue/session"
)

// passkeyPrefix is where the passkey routes live, below the account routes.
const passkeyPrefix = accountPrefix + "/passkeys"

// buildPasskeys wires passkeys next to the local accounts when PASSKEY_RP_ID
// is set, and does nothing otherwise. The stores are the in memory fakes, so
// every passkey and every pending challenge is gone on a restart. A product
// writes an adapter over its own tables and runs the passkeytest suite
// against it.
//
// A bad relying party, an origin that is not https or not below the RP id,
// is refused here, so the application does not start.
func (a *app) buildPasskeys(accounts *accounttest.MemoryStore) error {
	if a.cfg.passkeyRPID == "" {
		return nil
	}

	store := passkeytest.NewMemoryStore()
	handlers, err := passkey.New(passkey.Config{
		RPID:          a.cfg.passkeyRPID,
		RPDisplayName: a.cfg.passkeyRPName,
		Origins:       a.cfg.allowedOrigins,
		Store:         store,
		// The same function as the account login, so both start the same
		// session for the same Identity.
		StartSession: a.startSession,
		CurrentAccount: func(r *http.Request) (account.Identity, time.Time, bool, error) {
			if err := session.ErrorFromContext(r.Context()); err != nil {
				return account.Identity{}, time.Time{}, false, err
			}
			s, ok := session.FromContext(r.Context())
			// Passkeys work with the cookie session only. A device session
			// of a native app cannot register one: the challenge cookie is
			// host only and SameSite=Strict, and a passkey login starts a
			// cookie session.
			if !ok || s.Credential != session.CredentialCookie {
				return account.Identity{}, time.Time{}, false, nil
			}
			// IssuedAt is the login, which a rotation does not move. That is
			// the time the reauth window counts from.
			return account.Identity{ID: s.Subject, Login: s.Username}, s.IssuedAt, true, nil
		},
		OtherFactors: func(ctx context.Context, id account.Identity) (int, error) {
			return otherFactors(ctx, accounts, id)
		},
		// The same origins the CSRF protector and the account handlers trust.
		TrustedOrigins: a.cfg.allowedOrigins,
		AllowPlainHTTP: a.cfg.allowPlainHTTP,
		Now:            a.cfg.now,
	})
	if err != nil {
		return fmt.Errorf("example: passkey handlers: %w", err)
	}
	a.passkeys = handlers
	a.passkeyStore = store

	return nil
}

// otherFactors is 1 when the account can log in with its password and a
// confirmed TOTP secret, and 0 otherwise. The last passkey of an account with
// 0 cannot be deleted.
func otherFactors(ctx context.Context, accounts account.Store, id account.Identity) (int, error) {
	rec, err := accounts.FindByLogin(ctx, id.Login)
	switch {
	case errors.Is(err, account.ErrNotFound):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("example: other factors: %w", err)
	case rec.ID != id.ID:
		// The login now names another account. Count nothing for this one.
		return 0, nil
	case rec.PasswordHash != "" && rec.TOTPCounter > 0:
		return 1, nil
	default:
		return 0, nil
	}
}

// mountPasskeys registers the passkey routes when passkeys are on. The begin
// and finish routes sit behind checked, the stricter limit of the routes that
// check a credential: a begin stores a challenge and a finish verifies a
// signature, and the challenge store is bounded, so one client must not fill
// it. List and delete need the general limit only.
func (a *app) mountPasskeys(mux *http.ServeMux, route, checked func(http.Handler) http.Handler) {
	if a.passkeys == nil {
		return
	}
	mux.Handle("POST "+passkeyPrefix+"/register/begin", checked(a.passkeys.RegisterBegin()))
	mux.Handle("POST "+passkeyPrefix+"/register/finish", checked(a.passkeys.RegisterFinish()))
	mux.Handle("POST "+passkeyPrefix+"/login/begin", checked(a.passkeys.LoginBegin()))
	mux.Handle("POST "+passkeyPrefix+"/login/finish", checked(a.passkeys.LoginFinish()))
	mux.Handle("GET "+passkeyPrefix, route(a.passkeys.List()))
	mux.Handle("POST "+passkeyPrefix+"/delete", route(a.passkeys.Delete()))
}
