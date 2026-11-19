// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createApp } from 'vue'

import { createGovueI18n } from '../../src/i18n/plugin'
import PasskeyApp from './PasskeyApp.vue'

// The real components and the real client against whatever the spec routes
// the passkey requests to.
createApp(PasskeyApp)
  .use(
    createGovueI18n({
      locale: 'en',
      storage: { kind: 'custom', get: () => undefined, set: () => undefined },
    }),
  )
  .mount('#app')
