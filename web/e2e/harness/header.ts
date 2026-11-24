// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import './header.css'

import { createApp } from 'vue'

import { createGovueI18n } from '../../src/i18n/plugin'
import HeaderApp from './HeaderApp.vue'

// The narrow header of a site: the real components, English, nothing stored.
createApp(HeaderApp)
  .use(
    createGovueI18n({
      locale: 'en',
      storage: { kind: 'custom', get: () => undefined, set: () => undefined },
    }),
  )
  .mount('#app')
