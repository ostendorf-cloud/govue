// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package account gives an application local accounts with a password and a
// TOTP second factor, against the application's own tables.
//
// This package owns no schema. Applications already differ in how they store
// an account, so a consumer satisfies [Store] with an adapter over what it has.
// Adopting this package is an adapter, not a data migration.
//
// So far the package holds the contract only: [Identity], [Store], [Record]
// and the errors. Password hashing, TOTP and the login logic follow on top of
// it.
//
// # One identity, whatever the path
//
// [Identity] is what every login path converges on. Its ID is the consumer's
// own account id, opaque here and never a provider's subject claim, so a
// session keys on one identifier however the login happened. A passkey login,
// added by a separate package, yields the same Identity as a password and
// TOTP login.
//
// # The store exposes only what the logic calls
//
// [Store] has the six methods the logic needs for login, TOTP enrolment and
// password change, and nothing else. There is no method for a provider
// identity, a connected account or a tenant, so no code here can reach one.
// That makes the boundary structural rather than a check somebody could
// forget. See the [Store] doc comment before adding a method.
//
// # Replay is refused in the store
//
// A TOTP code is good once. [Store.AdvanceTOTPCounter] is a conditional write:
// the store compares and writes in one statement and answers [ErrNotFound]
// when the counter did not move. Of two logins racing with the same code,
// exactly one passes.
//
// # One answer for every failed login
//
// Every refused login answers [ErrInvalidCredentials], whatever was wrong, so
// the answer never tells which part of a guess was right.
package account
