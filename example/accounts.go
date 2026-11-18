// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/account/accounttest"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/ratelimit"
	"github.com/ostendorf-cloud/govue/session"
)

// accountIssuer is the name an authenticator app groups the TOTP entry under.
const accountIssuer = "govue example"

// accountPrefix is where the account routes live, under the base URL the Vue
// client defaults to.
const accountPrefix = "/api/v1/account"

// buildAccounts wires local accounts: the service over an in memory store, the
// handlers with the session callbacks, and the stricter rate limit of the
// routes that check a password.
//
// The store is the conformance fake, so every account is gone on a restart. A
// product writes an adapter over its own tables and runs the accounttest suite
// against it. account imports nothing from session: this function is where
// the two meet, in three callbacks.
func (a *app) buildAccounts() error {
	hasher, err := account.NewHasher(account.HasherConfig{})
	if err != nil {
		return fmt.Errorf("example: password hasher: %w", err)
	}

	service, err := account.NewService(account.ServiceConfig{
		Store:  accounttest.NewMemoryStore(accounttest.MemoryConfig{}),
		Hasher: hasher,
		Issuer: accountIssuer,
		// A password change ends every session of the account, device
		// sessions included, because EndAll asks both stores.
		EndSessions: func(ctx context.Context, accountID string) error {
			return a.sessions.EndAll(ctx, accountID)
		},
		Logger: a.logger,
		Now:    a.cfg.now,
	})
	if err != nil {
		return fmt.Errorf("example: account service: %w", err)
	}

	handlers, err := account.NewHandlers(account.HandlerConfig{
		Service: service,
		// The account id is the session's subject, whatever the login path.
		// The memory store's ids keep their plain form, since this example
		// runs no OIDC account next to them. A product that does prefixes one
		// of the two, for example "local:", because both share the subject
		// namespace that EndAll keys on.
		StartSession: func(ctx context.Context, w http.ResponseWriter, r *http.Request, id account.Identity) error {
			// A login does not end the session the request already carries,
			// so logging in as B while logged in as A would leave A alive.
			// A bearer request is left alone: it gets no cookie session
			// anyway, and End would sign its device out.
			if r.Header.Get("Authorization") == "" {
				if err := a.sessions.End(ctx, w, r); err != nil {
					return err
				}
			}
			_, err := a.sessions.Start(ctx, w, session.Session{Subject: id.ID, Username: id.Login})
			return err
		},
		EndSession: a.sessions.End,
		CurrentIdentity: func(r *http.Request) (account.Identity, bool, error) {
			if err := session.ErrorFromContext(r.Context()); err != nil {
				return account.Identity{}, false, err
			}
			s, ok := session.FromContext(r.Context())
			if !ok {
				return account.Identity{}, false, nil
			}
			return account.Identity{ID: s.Subject, Login: s.Username}, true, nil
		},
		// The same origins the CSRF protector trusts.
		TrustedOrigins: a.cfg.allowedOrigins,
	})
	if err != nil {
		return fmt.Errorf("example: account handlers: %w", err)
	}
	a.accounts = handlers

	a.authLimiter = ratelimit.New(a.cfg.authRateLimitBurst, a.cfg.authRateLimitPer, maxRateLimitKeys)
	authRateLimit, err := middleware.NewRateLimit(middleware.RateLimitConfig{Limiter: a.authLimiter, ClientIP: a.clientIPs})
	if err != nil {
		return fmt.Errorf("example: auth rate limit: %w", err)
	}
	a.authRateLimit = authRateLimit

	return nil
}

// mountAccounts registers the account routes. route is the chain every
// handler runs in, and checked adds the stricter limit in front of the routes
// that check a password: login, TOTP confirmation, password change and signup.
// A guess costs an Argon2id derivation and the hasher has few slots, so one
// client must not be able to use them all.
//
// Signup is mounted only when the application opens registration, which is
// the product's decision. A closed signup is a route that does not exist.
func (a *app) mountAccounts(mux *http.ServeMux, route, checked func(http.Handler) http.Handler) {
	mux.Handle("POST "+accountPrefix+"/login", checked(a.accounts.Login()))
	mux.Handle("POST "+accountPrefix+"/confirm", checked(a.accounts.ConfirmTOTP()))
	mux.Handle("POST "+accountPrefix+"/password", checked(a.accounts.ChangePassword()))
	mux.Handle("POST "+accountPrefix+"/logout", route(a.accounts.Logout()))
	if a.cfg.allowSignup {
		mux.Handle("POST "+accountPrefix+"/signup", checked(a.accounts.Signup()))
	}
}
