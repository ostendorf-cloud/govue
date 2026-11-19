<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// A harness page for passkey.spec.ts: the real GovuePasskeyButton in both
// modes with the real createClient. No Tailwind, like the other harness page.
import { ref } from 'vue'

import { createClient } from '../../src/api/client'
import GovuePasskeyButton from '../../src/components/GovuePasskeyButton.vue'

const client = createClient()
const result = ref('')
</script>

<template>
  <main>
    <h1>Passkeys</h1>
    <section aria-label="Enrol">
      <GovuePasskeyButton
        :client="client"
        mode="enrol"
        name="Laptop"
        @success="(r) => (result = `enrolled:${(r as { name: string }).name}`)"
        @cancelled="result = 'cancelled'"
      />
    </section>
    <section aria-label="Login">
      <GovuePasskeyButton
        :client="client"
        mode="login"
        @success="(r) => (result = `login:${(r as { login: string }).login}`)"
        @cancelled="result = 'cancelled'"
      />
    </section>
    <output id="result" role="status" aria-label="Result">{{ result }}</output>
  </main>
</template>
