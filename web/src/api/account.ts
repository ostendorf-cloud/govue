// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The answers of the Go account package's handlers. The server may add fields
 * in a later release, a token for a native app among them, so a client reads
 * the names it knows and ignores the rest. That is what the index signature
 * says.
 */

/** The 200 answer of the login and the TOTP confirm handlers. */
export interface LoginResult {
  login: string
  [field: string]: unknown
}

/**
 * A registered passkey as the passkey handlers list it. The id is base64url,
 * the times are RFC 3339, and `last_used_at` is null before the first login.
 */
export interface Passkey {
  id: string
  name: string
  created_at: string
  last_used_at: string | null
  [field: string]: unknown
}

/** The TOTP enrolment a signup answers with, once. */
export interface Enrolment {
  /** The base32 secret, for an app that cannot scan. */
  secret: string
  /** The otpauth URI of the same secret. */
  uri: string
}

/** The 201 answer of the signup handler. */
export interface SignupResult {
  login: string
  enrolment: Enrolment
  [field: string]: unknown
}
