<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createClient } from '../src/api/client'
import GovueContactForm from '../src/components/GovueContactForm.vue'
import GovueFooter from '../src/components/GovueFooter.vue'
import GovuePage from '../src/components/GovuePage.vue'
import GovueSkipLink from '../src/components/GovueSkipLink.vue'
import { useLanguage } from '../src/i18n/plugin'

// The example's contact page: the form against the routes of the Go contact
// handlers, which a site mounts under its API prefix. The form is stateless, so
// the client sends no cookie and no CSRF token.
const client = createClient({ credentials: 'omit' })
const { locale } = useLanguage()
</script>

<template>
  <GovueSkipLink />
  <GovuePage title="govue example">
    <GovueContactForm :client="client" :privacy-href="`/${locale}/privacy`" require-consent />
  </GovuePage>
  <GovueFooter :legal="['imprint', 'privacy', 'terms']" />
</template>
