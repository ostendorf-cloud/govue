<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The example site's privacy page: the shared parts from govue, the optional
// blocks the example site's own code backs, and one section of its own.
import GovuePrivacyPage from '../../components/GovuePrivacyPage.vue'
import { contactForm, rateLimit, serverLogs, sessionCookie, umami } from '../blocks'
import { p as legalParagraph, section as legalSection } from '../helpers'

const sections = [
  sessionCookie(),
  serverLogs(),
  rateLimit({ withContactForm: true }),
  umami(),
  contactForm({ withSessionCookie: true }),
  legalSection(
    'example-newsletter',
    ['Beispiel: eigener Abschnitt', 'Example: a section of its own'],
    [
      legalParagraph(
        'Dieser Abschnitt gehört nur zu dieser Beispielseite.',
        'This section belongs to this example site only.',
      ),
    ],
  ),
]
</script>

<template>
  <GovuePrivacyPage german-href="/de/privacy" :sections="sections">
    <template #intro>
      Example Site sets one technical cookie and uses no third-party services.
    </template>
  </GovuePrivacyPage>
</template>
