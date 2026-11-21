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
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/passkey"
	"github.com/ostendorf-cloud/govue/passkey/passkeytest"
	"github.com/ostendorf-cloud/govue/ratelimit"
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
		// The RP id is PASSKEY_RP_ID and the origins are ALLOWED_ORIGINS.
		return fmt.Errorf("example: passkey handlers (PASSKEY_RP_ID, ALLOWED_ORIGINS): %w", err)
	}
	a.passkeys = handlers
	a.passkeyStore = store

	// A bucket of its own, not the account limiter's. A frontend that starts
	// a conditional autofill login on every page view calls login begin far
	// more often than anyone types a password, and sharing the bucket would
	// then answer 429 to the password login of the same client. The limit is
	// the product's decision, PASSKEY_RATE_LIMIT_BURST and _PER here.
	limiter := ratelimit.New(a.cfg.passkeyRateLimitBurst, a.cfg.passkeyRateLimitPer, maxRateLimitKeys)
	limit, err := middleware.NewRateLimit(middleware.RateLimitConfig{Limiter: limiter, ClientIP: a.clientIPs})
	if err != nil {
		return fmt.Errorf("example: passkey rate limit: %w", err)
	}
	a.passkeyRateLimit = limit

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
// and finish routes sit behind the passkey limit, in the place the account
// limit has in the chain of the account routes: a begin stores a challenge and
// a finish verifies a signature, and the challenge store is bounded, so one
// client must not fill it. List and delete need the general limit only.
func (a *app) mountPasskeys(mux *http.ServeMux, route func(http.Handler) http.Handler) {
	if a.passkeys == nil {
		return
	}
	checked := func(h http.Handler) http.Handler {
		return middleware.Chain(h, a.corsMiddleware(), a.rateLimit.Middleware, a.passkeyRateLimit.Middleware,
			a.protector.Middleware, a.sessions.Middleware)
	}
	mux.Handle("POST "+passkeyPrefix+"/register/begin", checked(a.passkeys.RegisterBegin()))
	mux.Handle("POST "+passkeyPrefix+"/register/finish", checked(a.passkeys.RegisterFinish()))
	mux.Handle("POST "+passkeyPrefix+"/login/begin", checked(a.passkeys.LoginBegin()))
	mux.Handle("POST "+passkeyPrefix+"/login/finish", checked(a.passkeys.LoginFinish()))
	mux.Handle("GET "+passkeyPrefix, route(a.passkeys.List()))
	mux.Handle("POST "+passkeyPrefix+"/delete", route(a.passkeys.Delete()))
}
