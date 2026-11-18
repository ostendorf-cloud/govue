// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package account gives an application local accounts with a password and a
// TOTP second factor, against the application's own tables.
//
// This package owns no schema. Applications already differ in how they store
// an account, so a consumer satisfies [Store] with an adapter over what it has.
// Adopting this package is an adapter, not a data migration.
//
// So far the package holds the contract, [Identity], [Store], [Record] and the
// errors, and the two checks a login is built from: Argon2id password
// hashing in [Hasher] and TOTP in [VerifyTOTPCode]. The login logic that
// combines them with the store follows on top of it.
//
// # Password hashing
//
// [Hasher] writes Argon2id in the PHC string format and reads the parameters
// of a stored hash back from it, so a later parameter change keeps older
// hashes verifiable and [PasswordCheck.NeedsRehash] says when to rewrite one.
// Every [Hasher.VerifyPassword] call runs one derivation, also for an unknown
// account, which passes the empty string.
//
// The cap on concurrent derivations is global to the Hasher: one slot by
// default, [HasherConfig.MaxConcurrentHashes], with a wait of
// [DefaultHashWait]. Sustained login traffic from anyone answers
// [ErrHashingBusy] for everyone. Put a per client rate limit in front of the
// login route, as the example application does with the ratelimit package
// and middleware.NewRateLimit.
//
// # TOTP
//
// [VerifyTOTPCode] accepts one step either side of now and returns the
// counter of the matched step, the highest if a code matches two. The caller
// consumes it with [Store.AdvanceTOTPCounter], and only that conditional
// write refuses a replayed code.
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
