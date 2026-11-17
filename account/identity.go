// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

// Identity is what every login path converges on, and the only thing a login
// returns. A password and TOTP login and a passkey login for the same account
// yield the same Identity, so nothing above this package branches on how a
// login happened.
//
// It carries no tenant, no role, no groups and no login method. Those belong to
// the consumer, which adds them when it builds its own session from an
// Identity.
type Identity struct {
	// ID is the consumer's own account id, opaque to this package. It is never
	// parsed here and never filled with a provider's subject claim, which is
	// what lets a session, and session.Manager.EndAll, key on one identifier
	// whichever path the login took. A consumer with an OIDC path maps the
	// provider subject to this same id itself.
	ID string

	// Login is the name the person typed to log in, as the store holds it.
	Login string
}
