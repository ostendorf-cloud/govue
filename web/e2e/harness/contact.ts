// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createApp } from 'vue'

import { createClient } from '../../src/api/client'
import GovueContactForm from '../../src/components/GovueContactForm.vue'
import { createGovueI18n } from '../../src/i18n/plugin'

// The real form, the real client and the real worker file, served by Vite.
// Whatever answers /api/v1/contact is up to the spec.
const client = createClient({ credentials: 'omit' })

createApp(GovueContactForm, {
  client,
  privacyHref: '/privacy',
  requireConsent: true,
  consentLabel: 'I agree that my details are used to answer my message.',
})
  .use(
    createGovueI18n({
      locale: 'en',
      storage: { kind: 'custom', get: () => undefined, set: () => undefined },
    }),
  )
  .mount('#app')
