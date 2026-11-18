<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import type { Enrolment, LoginResult } from '../api/account'
import type { ApiClient } from '../api/client'
import { useForm } from '../composables/useForm'
import { useGovueText } from '../i18n/plugin'
import { localizeError, requiredErrors } from './accountForm'
import GovueButton from './GovueButton.vue'
import GovueField from './GovueField.vue'
import GovueInput from './GovueInput.vue'

const props = withDefaults(
  defineProps<{
    /**
     * The client of createClient. The form never assumes a cookie. Its
     * request sets skipUnauthenticatedHook. When a person can be logged in
     * while the form is used, the client needs csrfToken, or the server
     * answers 403 and the form shows a refused request.
     */
    client: ApiClient
    /** The login the enrolment belongs to. */
    login: string
    /**
     * What the signup answered with. It is shown here and nowhere else. The
     * parent drops it once `confirmed` is emitted, since the secret is shown
     * once and has no reason to stay in memory.
     */
    enrolment: Enrolment
    /** The confirm route, relative to the client's base URL. */
    path?: string
    disabled?: boolean
  }>(),
  { path: '/account/confirm', disabled: false },
)

const emit = defineEmits<{
  /** The code matched and the secret is active. The server has started a
   *  session when it is a cookie one, and the answer is what the login form
   *  would have emitted. */
  confirmed: [result: LoginResult]
}>()

const { t } = useGovueText()

// The password is asked for again: no session exists before the first
// confirmation, so the password gates it, and nothing then has to carry it
// from the signup form to here.
const form = useForm({
  initial: { password: '', code: '' },
  validate: (values) => requiredErrors(values, ['password', 'code'], t),
  submit: async (values) => {
    let result: LoginResult
    try {
      result = await props.client.post<LoginResult>(
        props.path,
        { login: props.login, ...values },
        { skipUnauthenticatedHook: true },
      )
    } catch (caught) {
      throw localizeError(caught, t)
    }
    form.values.password = ''
    form.values.code = ''
    emit('confirmed', result)
  },
})

const { values, submitting, problem, requestId } = form
const locked = computed(() => props.disabled || submitting.value)

// The link opens an authenticator app on a phone. Only an otpauth link is
// rendered as one, whatever the server sent.
const appLink = computed(() =>
  props.enrolment.uri.startsWith('otpauth://') ? props.enrolment.uri : undefined,
)
</script>

<template>
  <form
    class="flex flex-col gap-4"
    novalidate
    :aria-label="t('govue.account.totp.title')"
    :aria-busy="submitting ? 'true' : undefined"
    @submit.prevent="form.handleSubmit()"
  >
    <!-- The login for the password manager, which saves the password next to
         a username field. Hidden from the eye and from a screen reader, which
         has the login from the page it is on. -->
    <input
      type="text"
      class="sr-only"
      tabindex="-1"
      aria-hidden="true"
      autocomplete="username"
      readonly
      :value="login"
    />

    <p class="text-base text-text">{{ t('govue.account.totp.intro') }}</p>

    <!-- Read only inputs, so the value can be selected and copied and a
         screen reader names it. No QR code: that would be a library. -->
    <GovueField :label="t('govue.account.totp.secret')">
      <GovueInput :model-value="enrolment.secret" readonly />
    </GovueField>

    <GovueField :label="t('govue.account.totp.uri')">
      <GovueInput :model-value="enrolment.uri" readonly />
    </GovueField>

    <a
      v-if="appLink"
      :href="appLink"
      class="inline-flex min-h-11 items-center text-base text-accent-text underline"
    >
      {{ t('govue.account.totp.openApp') }}
    </a>

    <p class="text-sm text-text-muted">{{ t('govue.account.totp.lost') }}</p>

    <GovueField
      :label="t('govue.account.field.password')"
      :error="form.errorOf('password')"
      required
      :disabled="locked"
    >
      <GovueInput
        v-model="values.password"
        type="password"
        autocomplete="current-password"
        @blur="form.blur('password')"
      />
    </GovueField>

    <GovueField
      :label="t('govue.account.field.code')"
      :hint="t('govue.account.field.codeHint')"
      :error="form.errorOf('code')"
      required
      :disabled="locked"
    >
      <GovueInput
        v-model="values.code"
        autocomplete="one-time-code"
        inputmode="numeric"
        @blur="form.blur('code')"
      />
    </GovueField>

    <div v-if="problem" role="alert" class="flex flex-col gap-1 text-sm text-danger-text">
      <p>{{ problem }}</p>
      <p v-if="requestId" class="text-text-muted">
        {{ t('govue.error.requestId') }}: <code class="font-mono">{{ requestId }}</code>
      </p>
    </div>

    <GovueButton type="submit" variant="primary" block :disabled="disabled" :loading="submitting">
      {{ t('govue.account.totp.submit') }}
    </GovueButton>
  </form>
</template>
