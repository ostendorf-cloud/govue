// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * A fake of the Request and Challenge handlers of the Go optin package for
 * tests, written from optin/handler.go and optin/mail.go of govue PR #270. It
 * is the contact fake with the body and the field check of optin:
 *
 * - the body keys are `email`, `consent`, `language`, `website` and `pow`,
 *   and any other key is refused as unknown with 400 invalid_argument, as
 *   api.DecodeLimit does. The consent text version is no key of the body:
 *   optin.Config.ConsentVersion holds it on the server,
 * - the consent box must be true, always: a missing or false one is the field
 *   error `consent: required`, next to the address check,
 * - the address is trimmed and checked for required, too_long (254 runes),
 *   invalid_chars, non_ascii_email and invalid,
 * - the order, the proof of work, the honeypot and the answers are the ones of
 *   the contact fake, which match optin's Request.
 *
 * It is a fake and proves only itself.
 */

import { FakeContactServer, type FakeContactOptions } from './contactServerFake'

const WAITLIST_KEYS = ['email', 'consent', 'language', 'website', 'pow']

/** optin.MaxEmailRunes. */
const MAX_EMAIL = 254

export type FakeWaitlistOptions = Omit<FakeContactOptions, 'requireConsent'>

export class FakeWaitlistServer extends FakeContactServer {
  protected override readonly bodyKeys: readonly string[] = WAITLIST_KEYS

  constructor(options: FakeWaitlistOptions = {}) {
    super({ ...options, requireConsent: true })
  }

  protected override validate(sub: Record<string, unknown>): Record<string, string> | undefined {
    const out: Record<string, string> = {}
    const email = typeof sub.email === 'string' ? sub.email.trim() : ''
    if (email === '') {
      out.email = 'required'
    } else if ([...email].length > MAX_EMAIL) {
      out.email = 'too_long'
    } else if ([...email].some((c) => /\p{Cc}|\p{Cf}/u.test(c))) {
      out.email = 'invalid_chars'
    } else if ([...email].some((c) => c.charCodeAt(0) > 127)) {
      out.email = 'non_ascii_email'
    } else if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) {
      out.email = 'invalid'
    }
    if (sub.consent !== true) {
      out.consent = 'required'
    }
    return Object.keys(out).length > 0 ? out : undefined
  }
}
