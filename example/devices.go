// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ostendorf-cloud/govue/account"
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
		// No IssueTrustedOrigins: the web origins of ALLOWED_ORIGINS feed the
		// CSRF protector and a CORS policy with credentials, and an app origin
		// must never be on those lists. A separate app origin list arrives with
		// step 5 of #117, see docs/concepts/govue.md.
	})
	if err != nil {
		return fmt.Errorf("example: device handlers: %w", err)
	}
	a.devices = handlers

	return nil
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
