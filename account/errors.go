// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import "errors"

// ErrNotFound is what a [Store] returns, wrapped or not, in three cases. From
// a lookup it means no account matched. From a conditional write,
// [Store.AdvanceTOTPCounter], [Store.ConfirmTOTP] or [Store.ReplaceTOTPSecret],
// it means the write did not happen: the counter was not advanced or the
// secret not confirmed or replaced, because the code was replayed, the
// account was in the wrong state or the account is missing. From
// [Store.SetPasswordHash] it means the account is missing. This package
// treats ErrNotFound from a conditional write as a refusal, never as proof
// that the account is gone.
var ErrNotFound = errors.New("account: not found")

// ErrConflict is what [Store.Create] returns, wrapped or not, when the login is
// already taken.
var ErrConflict = errors.New("account: login already taken")

// ErrInvalidCredentials is what every refused login answers with. No such
// account, an account without a password or a confirmed TOTP secret, a wrong
// password and a wrong, missing or replayed code all answer identically.
// Telling them apart would tell an attacker which part of a guess was right.
// A login that could not be decided, because the store failed or the server
// had no capacity to hash, is not a refusal and answers with its own error.
var ErrInvalidCredentials = errors.New("account: invalid login, password or code")
