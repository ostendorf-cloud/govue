// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import "context"

// Record is one local account as the [Store] holds it: the fields this package
// reads, and nothing a user table usually carries besides.
//
// An empty PasswordHash or TOTPSecret means the account has none, for example
// an account a consumer created through OIDC alone. Such an account cannot log
// in through this package, whatever password somebody guesses for it.
//
// An account that must not log in, because it is disabled, pending or
// otherwise held back by the consumer, answers [ErrNotFound] from
// [Store.FindByLogin] or comes back with an empty PasswordHash. This package
// has no status field to check, so the adapter is where such an account is
// refused.
type Record struct {
	// ID is the consumer's account id, opaque here. [Store.Create] fills it
	// and ignores any value the caller set.
	ID string

	// Login is the unique name the person logs in with, usually an email
	// address. The store decides uniqueness, and a store that folds case
	// folds it the same way in FindByLogin and Create.
	Login string

	// PasswordHash is the encoded password hash, for example an Argon2id PHC
	// string. The store keeps it as given and never interprets it.
	PasswordHash string

	// TOTPSecret is the base32 TOTP secret. A store may encrypt it at rest,
	// as long as FindByLogin hands back the plain value.
	TOTPSecret string

	// TOTPCounter is the RFC 6238 counter of the last code this account
	// consumed. Zero means no code was ever consumed, which is also what an
	// unconfirmed TOTP secret looks like: [Store.ConfirmTOTP] writes the
	// counter of the confirming code, so a secret counts as confirmed exactly
	// when TOTPCounter is above zero. Counter zero is the first thirty
	// seconds of 1970, so no live code ever has it, and no separate
	// confirmed column is needed.
	//
	// Zero means unconfirmed only for an account created through
	// [Store.Create]. An adapter over a table that existed before adoption
	// maps a NULL counter on such a row to 1, for example with
	// COALESCE(totp_last_counter, 1) in its reads and conditional writes,
	// or backfills 1 once. Counter 1 is 1970 as well, so no live code is
	// refused. Without that, an adopted account that never logged in reads
	// as unconfirmed, and its login is refused with the right password and
	// code. Create writes an explicit 0, never NULL.
	TOTPCounter int64
}

// Store is what this package needs from a consumer's storage: the six calls
// its logic makes for login, TOTP enrolment and password change, and no more.
// FindByLogin, Create and AdvanceTOTPCounter serve signup and login.
// ConfirmTOTP and ReplaceTOTPSecret serve TOTP enrolment, and SetPasswordHash
// serves a password change. The interface is defined here, at the consumer of
// the data, and a consumer satisfies it with an adapter over its own tables.
// This package owns no schema.
//
// What is deliberately absent is the point. There is no method for a provider
// identity, a connected account, a tenant, a role or a list of accounts. Code
// in this package cannot reach what the interface does not offer, so a login
// can never be matched against a provider account somebody connected for
// another purpose. That guarantee is structural, not a check somebody could
// forget. Adding a method here, whatever the intended use, removes it for
// every consumer at once.
//
// The methods that take an id act on whatever account the adapter lets that
// id reach. That a call never acts outside the account it was asked about is
// structural only where the adapter enforces scope, through row level
// security or a tenant. Without that, it rests on the caller passing the right
// id.
//
// Scoping is the adapter's job. A consumer with row level security or tenants
// reads its scope from ctx inside the adapter. Nothing here carries it. An
// adapter that expects a scope and finds none in ctx fails closed: it returns
// an error and never falls back to an unscoped query.
//
// The logic looks an account up by login, also where it already holds an
// [Identity], for example to confirm TOTP or change a password. It must then
// check that the record's ID equals the Identity's ID before it writes, so a
// login that now names a different account is refused rather than followed.
//
// An implementation is safe for concurrent use. It returns [ErrNotFound],
// wrapped or not, for a login or an id it does not hold, and for a conditional
// write that changed nothing. Any other error is a failure of the store.
type Store interface {
	// FindByLogin returns the account with this login, or ErrNotFound. It runs
	// before any account scope exists, so an adapter with row level security
	// reaches the row through a narrow lookup, not a widened policy. An
	// account that must not log in answers ErrNotFound or comes back with an
	// empty PasswordHash; see [Record].
	FindByLogin(ctx context.Context, login string) (Record, error)

	// Create stores a new account and returns it with ID filled. It ignores
	// any ID the caller set and stores TOTPCounter as zero whatever the
	// caller set, so a new account always starts with an unconfirmed TOTP
	// secret. A login that is already taken returns ErrConflict. The other
	// fields are stored as given.
	Create(ctx context.Context, rec Record) (Record, error)

	// AdvanceTOTPCounter sets the account's TOTPCounter to counter only when
	// the stored value is above zero and lower than counter, and returns
	// ErrNotFound when no row changed. The comparison and the write are one
	// statement, a conditional UPDATE in SQL. That is what refuses a replayed
	// code: of two logins racing with the same code, exactly one advances the
	// counter. Reading the counter and then writing the whole record cannot
	// do that. The stored value has to be above zero, so a login can never
	// confirm a secret by advancing it.
	AdvanceTOTPCounter(ctx context.Context, id string, counter int64) error

	// ConfirmTOTP confirms the account's TOTP secret by setting TOTPCounter
	// to counter, the counter of the code that confirmed it, so that code
	// cannot log in afterwards. secret is the secret the code was verified
	// against. It writes only when the stored secret is exactly secret, the
	// stored counter is zero and counter is above zero, in one statement,
	// and returns ErrNotFound when no row changed. Binding the write to the
	// secret means a ReplaceTOTPSecret landing between verify and confirm
	// makes the confirm fail, instead of confirming a secret the person
	// never saw.
	ConfirmTOTP(ctx context.Context, id, secret string, counter int64) error

	// ReplaceTOTPSecret replaces an unconfirmed TOTP secret with secret. It
	// writes only while the stored counter is zero, and returns ErrNotFound
	// when no row changed. It exists for the person who lost the enrolment
	// response before confirming: without it they could neither confirm nor
	// log in, and Create would refuse their login as taken. Once a secret is
	// confirmed it can no longer be replaced through this call.
	//
	// No session exists before the first login, so the logic gates this call
	// on the password. An unconfirmed account is therefore password only:
	// the password alone can replace the secret, confirm it and own the
	// account. For a fresh signup that is the accepted window between
	// signup and confirm. An adopted account avoids it through the counter
	// mapping described on [Record.TOTPCounter].
	ReplaceTOTPSecret(ctx context.Context, id, secret string) error

	// SetPasswordHash replaces the account's password hash with newHash and
	// nothing else. It is a compare and set: it writes only when the stored
	// hash is not empty and equals oldHash, the hash the caller read and
	// verified, in one statement, a conditional UPDATE in SQL. It returns
	// ErrNotFound when no row changed: the hash moved since the caller read
	// it, the account has no password, or the account is missing.
	//
	// That is what keeps a concurrent password change from being lost. Of two
	// writes from the same old hash, exactly one succeeds, so two racing
	// password changes cannot both win, and a rehash after a login never puts
	// back a password that was changed meanwhile. Writing the hash without
	// the comparison cannot do that. Refusing an empty stored hash means this
	// package can never give a password to an account that has none, such as
	// one created through OIDC alone.
	SetPasswordHash(ctx context.Context, id, oldHash, newHash string) error
}
