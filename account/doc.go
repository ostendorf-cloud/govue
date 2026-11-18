// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package account gives an application local accounts with a password and a
// TOTP second factor, against the application's own tables.
//
// This package owns no schema. Applications already differ in how they store
// an account, so a consumer satisfies [Store] with an adapter over what it has.
// Adopting this package is an adapter, not a data migration.
//
// The package holds the contract, [Identity], [Store], [Record] and the
// errors, the two checks a login is built from, Argon2id password hashing in
// [Hasher] and TOTP in [VerifyTOTPCode], and [Service], which combines them
// with the store: signup, TOTP confirmation, login and password change. The
// HTTP handlers on top of it are not part of the package yet.
//
// The accounttest package holds the conformance suite every adapter runs
// against its own storage, and an in memory store for tests.
//
// # The service
//
// [Service.SignUp] creates an account with an unconfirmed TOTP secret.
// [Service.ConfirmTOTP] confirms it with a code, gated on the password,
// since no session exists yet. Only then can [Service.Login] succeed, which
// checks the password and the code in one call. [Service.ChangePassword]
// stores the new hash first and then calls the application's EndSessions
// callback, in that order. The package imports nothing from session.
//
// [Store.SetPasswordHash] is a compare and set on the hash the caller read.
// Of two password changes racing from one password exactly one wins, and the
// other answers [ErrStale]. A rehash after a login never puts back a password
// that was changed meanwhile.
//
// SignUp answers [ErrConflict] for a taken login. That tells anyone whether
// an account exists. See [Service.SignUp] for what an application can do
// about it.
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
// the answer never tells which part of a guess was right. It also costs the
// same work: one Argon2id derivation and the same HMACs, against empty
// values when no account matched.
package account
