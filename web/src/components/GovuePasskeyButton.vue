<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { nextTick, onMounted, ref } from 'vue'

import type { LoginResult, Passkey } from '../api/account'
import type { ApiClient } from '../api/client'
import {
  isBearerClient,
  isPasskeySupported,
  usePasskey,
  type PasskeyError,
} from '../composables/usePasskey'
import { useGovueText } from '../i18n/plugin'
import GovueButton from './GovueButton.vue'

const props = withDefaults(
  defineProps<{
    /**
     * The client of createClient, as the account forms take it. The passkey
     * routes need the session cookie (enrolment) and the challenge cookie of
     * the begin answer, so the client sends credentials: 'same-origin' or
     * 'include'. The challenge cookie is SameSite=Strict and host only.
     */
    client: ApiClient
    /** What the button does: log in with a passkey or add one to the account. */
    mode: 'login' | 'enrol'
    /** The label of a new passkey, enrolment only. Sent as it is. */
    name?: string
    /** The route prefix, relative to the client's base URL. */
    path?: string
    disabled?: boolean
  }>(),
  { name: undefined, path: undefined, disabled: false },
)

const emit = defineEmits<{
  /** A login went through (mode login) or a passkey was added (mode enrol). */
  success: [result: LoginResult | Passkey]
  /** The person dismissed the prompt. Nothing changed. */
  cancelled: []
  /** The ceremony failed. The component already shows the text. */
  error: [error: PasskeyError]
}>()

const { t } = useGovueText()
const passkey = usePasskey({ client: () => props.client, path: () => props.path })

// False until mounted, so a prerendered page and the first client render agree
// and the button appears only where the browser can run a ceremony. A client
// with a device token cannot, so the button is not offered to it either.
const supported = ref(false)
onMounted(() => {
  supported.value = isPasskeySupported() && !isBearerClient(props.client)
})

const button = ref<{ $el: unknown } | null>(null)

// A button that turns disabled while it works drops the focus. When the
// ceremony is over and nothing else has it, the button gets it back.
async function restoreFocus(): Promise<void> {
  await nextTick()
  const element: unknown = button.value?.$el
  if (
    element instanceof HTMLElement &&
    (document.activeElement === null || document.activeElement === document.body)
  ) {
    element.focus()
  }
}

async function run(): Promise<void> {
  if (passkey.busy.value) {
    return
  }
  try {
    const result = props.mode === 'enrol' ? await passkey.enrol(props.name) : await passkey.login()
    if (result === undefined) {
      emit('cancelled')
      return
    }
    emit('success', result)
  } catch (caught) {
    // The error is in passkey.error and shown below.
    emit('error', caught as PasskeyError)
  } finally {
    await restoreFocus()
  }
}
</script>

<template>
  <div v-if="supported" class="flex flex-col gap-2">
    <!-- The busy text is announced by the live region below, once. The
         button's own sr-only loading text is emptied so it is not read twice. -->
    <GovueButton
      ref="button"
      variant="secondary"
      :disabled="disabled"
      :loading="passkey.busy.value"
      loading-label=""
      @click="run"
    >
      {{ t(mode === 'enrol' ? 'govue.passkey.enrol.label' : 'govue.passkey.login.label') }}
    </GovueButton>

    <!-- One live region that is always there, so a screen reader hears a
         change in it: the busy text and the quiet note of a dismissed prompt. -->
    <div aria-live="polite" class="text-sm">
      <p v-if="passkey.busy.value" class="sr-only">{{ t('govue.passkey.busy') }}</p>
      <p v-else-if="passkey.status.value === 'cancelled'" class="text-text-muted">
        {{ t('govue.passkey.cancelled') }}
      </p>
    </div>

    <!-- A failure is an alert, as in the account forms. -->
    <p v-if="passkey.error.value" role="alert" class="text-sm text-danger-text">
      {{ t(`govue.passkey.error.${passkey.error.value.code}`) }}
    </p>
  </div>
</template>
