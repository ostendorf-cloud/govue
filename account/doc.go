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
// with the store: signup, TOTP confirmation, login and password change.
// [Handlers] puts JSON over HTTP on top of the service.
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
// SignUp answers [ErrConflict] for a taken login. With this flow an account
// existence oracle exists, and an application cannot hide it by faking a
// success, since the confirm step tells again. A product that must hide it
// defers account creation until the person proved control of the address,
// which SignUp does not support. See [Service.SignUp].
//
// The service does not normalise logins: letter case, Unicode forms and
// confusable characters are the adapter's or the application's job.
//
// # HTTP handlers
//
// [NewHandlers] returns [Handlers] with one http.Handler per step: Signup,
// ConfirmTOTP, Login, ChangePassword and Logout. A product mounts the ones
// it offers, each on a POST route. A product without open registration
// mounts no signup handler, and [HandlerConfig.AllowSignup] decides per
// request where registration is open but conditional.
//
// Like the oidc handlers they take callbacks, so this package imports nothing
// from session: [HandlerConfig.StartSession] turns a verified [Identity] into
// a session, [HandlerConfig.EndSession] ends the one session of a logout,
// [HandlerConfig.CurrentIdentity] reads the account of a request, and the
// service's EndSessions callback ends every session after a password change.
//
// Requests and answers are JSON objects with named fields, never a bare
// value: [SignupRequest], [CredentialsRequest] and [PasswordChangeRequest] in,
// [SignupResult], [LoginResult] and [PasswordChangeResult] out, errors in the
// shape of the api package. Fields may be added to an answer in a later
// release, for a token a native app can use instead of a cookie, so a client
// reads the names it knows and ignores the rest. A request never takes a
// field this package does not name.
//
// Every handler checks, in this order, the method (POST only), the origin
// with net/http.CrossOriginProtection, and the body. A request with a
// Sec-Fetch-Site of cross-site or same-site, or an Origin other than this host
// or [HandlerConfig.TrustedOrigins], is refused with 403, with or without a
// session. A request that carries neither header passes, as it does for the
// csrf package: that is every client that is not a browser. The session bound
// CSRF token stays the application's middleware, as for any unsafe route.
// The body has to be application/json, at most [DefaultMaxBodyBytes] unless
// configured, one object, with no unknown field.
//
// A refused login, and a refused TOTP confirmation, answer 401 with
// [CodeInvalidCredentials] and one message, whether the login is unknown, the
// password or the code wrong or replayed, the secret unconfirmed or the
// account disabled. A server without capacity to hash answers 503 with
// Retry-After instead, which says nothing about the guess. Neither the
// password, a code, a secret nor a hash reaches the log or an answer, and no
// session is started on any failure.
//
// Four routes need a per client rate limit in front of them: signup, login,
// TOTP confirmation and password change. Each costs an Argon2id derivation and
// the hasher has one slot by default, so without a limit anyone keeps it busy
// and everybody gets 503. Confirmation needs no session. Password change
// answers 403 for a wrong current password and 200 for the right one, so a
// stolen session can test passwords as fast as the hasher allows. Signup
// answers 409 for a taken login and so is an account existence oracle. See
// [Service.SignUp].
//
// A login does not end a session the request already carries. The
// application ends it in StartSession before it starts the new one.
//
// Bodies are decoded by Go's encoding/json with unknown fields refused. Field
// names match case insensitively, of two equal keys the last wins, and invalid
// UTF-8 is replaced by U+FFFD. A proxy or a WAF that parses JSON differently
// is out of scope. For the same reason AllowSignup, which runs before the body
// is read, cannot take an invite code from it: an invite check reads a header,
// a cookie or a query parameter.
//
// The ids of local accounts and the subjects of an OIDC login share the
// namespace of the session's subject, which EndAll keys on. A product that runs
// both prefixes one of them, for example "local:" before the account id, or an
// OIDC subject could end up equal to a local id and share its sessions.
//
// A password change answers with the four outcomes of
// [Service.ChangePassword]: 200 with sessions_ended true, 200 with
// sessions_ended false when the change stands but ending the sessions failed,
// 409 [CodeStale] when nothing changed, and 500 [CodePasswordChangeUnknown]
// when the change may not have happened. Every session of the account ends
// with it, the caller's own included, and the handler starts none.
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
// the answer never tells which part of a guess was right. It also costs one
// Argon2id derivation and the same HMACs, against empty values when no
// account matched. The derivation runs at today's parameters without a
// usable hash and at the stored parameters for an existing account with a
// password, so after a parameter change an account still on the old
// parameters costs what those say until a login rehashes it.
package account
