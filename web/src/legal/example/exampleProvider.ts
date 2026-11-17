// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { tx } from '../helpers'
import type { LegalProvider } from '../types'

/**
 * A made up provider for the example site and the tests. Every value is a
 * placeholder. A real provider's data is passed in by the site and never
 * committed here.
 */
export const exampleProvider: LegalProvider = {
  name: 'Example Provider',
  street: '1 Example Street',
  postalCode: '00000',
  city: 'Example City',
  country: tx('Deutschland', 'Germany'),
  phone: '+49 000 0000000',
  email: 'legal@example.invalid',
  vatId: 'DE000000000',
  disputeResolution: tx(
    'Wir sind weder verpflichtet noch bereit, an einem Streitbeilegungsverfahren vor einer Verbraucherschlichtungsstelle teilzunehmen.',
    'We are neither obliged nor willing to take part in dispute resolution proceedings before a consumer arbitration board.',
  ),
}
