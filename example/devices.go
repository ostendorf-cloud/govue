// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ostendorf-cloud/govue/account"
	"github.com/ostendorf-cloud/govue/middleware"
	"github.com/ostendorf-cloud/govue/session/devicehttp"
)

// authPrefix is where the device token routes live, next to the account routes.
const authPrefix = "/api/v1/auth"

// buildDevices wires the device token handlers of a native app to the local
// accounts. devicehttp imports no account package and account imports no
// session package: this function is where the two meet, in one callback that
// maps the answer of Service.Login to the two errors devicehttp knows.
//
// The token is issued after the same check as the cookie login: login,
// password and a TOTP code. The code is consumed, so each issue needs the next
// one.
func (a *app) buildDevices(service *account.Service) error {
	handlers, err := devicehttp.NewHandlers(devicehttp.HandlerConfig{
		Sessions: a.sessions,
		VerifyCredentials: func(ctx context.Context, c devicehttp.Credentials) (string, error) {
			id, err := service.Login(ctx, c.Login, c.Password, c.Code)
			if err != nil {
				return "", mapLoginError(err)
			}
			// The account id is the subject of every session, so one EndAll
			// at a password change reaches the cookie and the device ones.
			return id.ID, nil
		},
		// The same clock as the session manager, because the store compares
		// the time of the credential check with the time of an EndAll.
		Now: a.cfg.now,
		// The app origins of APP_ORIGINS, and only those. The web origins of
		// ALLOWED_ORIGINS feed the CSRF protector and a CORS policy with
		// credentials, and never go here. Empty without APP_ORIGINS, which
		// leaves Issue trusting its own origin only.
		IssueTrustedOrigins: a.appOrigins.List(),
	})
	if err != nil {
		return fmt.Errorf("example: device handlers: %w", err)
	}
	a.devices = handlers

	return nil
}

// buildAppOrigins turns APP_ORIGINS into the typed list and the app CORS
// handler. It does nothing without APP_ORIGINS.
//
// This is the one place the lists meet, and it runs at startup: the app list
// is checked against the web list and against the origins handed to
// csrf.TrustOrigin, which here are the same ALLOWED_ORIGINS. An application
// with its own trusted origins passes them too. An overlap fails startup and
// names the origin. The app list reaches Issue and the app CORS handler and
// nothing else.
func (a *app) buildAppOrigins() error {
	if len(a.cfg.appOrigins) == 0 {
		if len(a.cfg.appSchemes) > 0 {
			return errors.New("example: APP_SCHEMES is set without APP_ORIGINS")
		}

		return nil
	}

	origins, err := middleware.NewAppOrigins(a.cfg.appOrigins, a.cfg.appSchemes...)
	if err != nil {
		return fmt.Errorf("example: APP_ORIGINS: %w", err)
	}
	if err := origins.RejectOverlap(a.cfg.allowedOrigins, a.cfg.allowedOrigins); err != nil {
		return fmt.Errorf("example: APP_ORIGINS: %w", err)
	}
	appCORS, err := middleware.NewAppCORS(origins)
	if err != nil {
		return fmt.Errorf("example: app CORS: %w", err)
	}
	// An app calls the issue route from its own origin, which is cross site, so
	// the origin half of the CSRF protector would refuse it. The one route is
	// exempt from that half, and only that. Issue checks the origin itself
	// against the app origins, and the token half stays.
	if err := a.protector.ExemptOrigin(http.MethodPost, authPrefix+"/token"); err != nil {
		return fmt.Errorf("example: %w", err)
	}
	a.appOrigins = origins
	a.appCORS = appCORS

	return nil
}

// corsMiddleware is the CORS part of the route chain. With app origins, a
// request whose Origin is on the app list is answered by the app handler,
// without credentials. Every other request goes on to the web policy, which
// never sees an app origin because the lists are disjoint.
func (a *app) corsMiddleware() middleware.Middleware {
	if a.appCORS == nil {
		return a.cors.Middleware
	}

	return func(next http.Handler) http.Handler {
		return a.appCORS.Middleware(a.cors.Middleware(next))
	}
}

// mapLoginError turns what account.Service.Login returns into the two errors
// devicehttp knows. Anything else passes on and is answered as an opaque 500.
func mapLoginError(err error) error {
	switch {
	case errors.Is(err, account.ErrInvalidCredentials):
		return devicehttp.ErrInvalidCredentials
	case errors.Is(err, account.ErrHashingBusy):
		return devicehttp.ErrBusy
	}

	return err
}

// mountDevices registers the device token routes. route is the chain every
// handler runs in. Issuing a token checks credentials, so it sits behind
// checked, the stricter limit of the routes that cost an Argon2id derivation,
// and is pinned with a 429 in the tests. The other routes cost a store lookup
// and need the general limit only.
//
// Rotate and the sign out of the calling device take a bearer token only. List
// and the revocation of one device take a cookie session or a token, so the
// web app can end a lost phone.
func (a *app) mountDevices(mux *http.ServeMux, route, checked func(http.Handler) http.Handler) {
	mux.Handle("POST "+authPrefix+"/token", checked(a.devices.Issue()))
	mux.Handle("POST "+authPrefix+"/token/rotate", route(a.devices.Rotate()))
	mux.Handle("DELETE "+authPrefix+"/token", route(a.devices.SignOut()))
	mux.Handle("GET "+authPrefix+"/devices", route(a.devices.List()))
	mux.Handle("DELETE "+authPrefix+"/devices/{id}", route(a.devices.Revoke()))
}
