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

// buildAppOrigins turns APP_ORIGINS into the typed list and sets up the issue
// route for it. It does nothing without APP_ORIGINS. buildAppCORS finishes the
// job once the web CORS policy exists.
//
// This is where the app list meets the origins handed to csrf.TrustOrigin,
// which here are the same ALLOWED_ORIGINS as the web CORS list. An application
// with its own trusted origins passes them to RejectOverlap too. An overlap
// fails startup and names the origin. The web CORS list is checked again,
// structurally, by middleware.NewAppCORS in buildAppCORS. The app list reaches
// Issue, the issue route of the protector and the app CORS handler, and
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
		// The error says "app origin N" for an entry of APP_ORIGINS and "app
		// scheme N" for one of APP_SCHEMES.
		return fmt.Errorf("example: APP_ORIGINS and APP_SCHEMES: %w", err)
	}
	if err := origins.RejectOverlap(nil, a.cfg.allowedOrigins); err != nil {
		return fmt.Errorf("example: APP_ORIGINS: %w", err)
	}
	// An app calls the issue route from its own origin, which is cross site, so
	// the origin half of the CSRF protector would refuse it. The app origins
	// are trusted on that one route, and only there, and the token half stays.
	// Issue checks the origin itself against the same list as a second layer.
	if err := a.protector.TrustOriginOnRoute(http.MethodPost, authPrefix+"/token", origins.List()); err != nil {
		return fmt.Errorf("example: %w", err)
	}
	a.appOrigins = origins

	return nil
}

// buildAppCORS builds the one CORS handler of an app mode deployment from the
// app list and the web policy. NewAppCORS refuses an overlap with the web
// policy. It does nothing without APP_ORIGINS.
func (a *app) buildAppCORS() error {
	if len(a.cfg.appOrigins) == 0 {
		return nil
	}
	appCORS, err := middleware.NewAppCORS(a.appOrigins, a.cors)
	if err != nil {
		return fmt.Errorf("example: app CORS: %w", err)
	}
	a.appCORS = appCORS

	return nil
}

// corsMiddleware is the CORS part of the route chain. With app origins it is
// the combined handler, which answers an app origin itself and gives every
// other request to the web policy. Without them it is the web policy.
func (a *app) corsMiddleware() middleware.Middleware {
	if a.appCORS == nil {
		return a.cors.Middleware
	}

	return a.appCORS.Middleware
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
