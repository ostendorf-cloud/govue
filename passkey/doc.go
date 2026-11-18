// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package passkey gives an account of the account package passkeys: WebAuthn
// credentials a person registers while logged in and then logs in with,
// next to the password and TOTP login and never instead of it.
//
// This package owns no schema. A consumer satisfies [Store] with an adapter
// over its own tables, and the passkeytest package holds the conformance
// suite every adapter runs and an in memory store.
//
// # Relying party
//
// [Config.RPID] is required configuration and never derived from a request's
// Host header. Startup refuses an RP id that is not the host or a parent of
// the host of every one of [Config.Origins], and an origin that is not https,
// with http allowed on localhost only. The RP id cannot change later: every
// passkey is bound to it, so a change loses all of them.
//
// # Challenges
//
// A challenge is single use. The begin handlers store it in a
// [ChallengeStore] under a random id and send that id in an HttpOnly cookie.
// The finish handlers take it, which deletes it in the same step, whatever
// the outcome. A registration challenge is bound to the account that began
// it, a login challenge to the browser that holds the cookie, and neither
// finishes the other ceremony. [MemoryChallengeStore] is the bounded default
// for one process.
//
// # Accounts
//
// The package imports account for [account.Identity] only. A passkey login
// hands the application the same Identity a password and TOTP login does,
// through a StartSession callback with the signature of
// account.HandlerConfig.StartSession, so the application passes one function
// to both. account does not import this package, so an application without
// passkeys does not pull in the WebAuthn library. Like account, this package
// imports nothing from session.
//
// The user handle stored on the authenticator is 32 random bytes per
// account, not the account id. See [Credential].
//
// Deleting the last passkey of an account is refused when
// [Config.OtherFactors] reports no other way to log in. Recovery codes are
// not part of this version: an account that loses its TOTP device and every
// passkey is locked out.
package passkey
