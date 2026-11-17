// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import "errors"

// ErrNotFound is what a [Store] returns, wrapped or not, when a lookup matches
// no account or a conditional write changes no row. The second case is how a
// replayed TOTP code is refused, so this package treats ErrNotFound from a
// write as a refused login, never as a missing account.
var ErrNotFound = errors.New("account: not found")

// ErrConflict is what [Store.Create] returns, wrapped or not, when the login is
// already taken.
var ErrConflict = errors.New("account: login already taken")

// ErrInvalidCredentials is the one failure a login answers with. No such
// account, an account without a password or a confirmed TOTP secret, a wrong
// password and a wrong, missing or replayed code all answer identically.
// Telling them apart would tell an attacker which part of a guess was right.
var ErrInvalidCredentials = errors.New("account: invalid login, password or code")
