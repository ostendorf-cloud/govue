<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import type { SignupResult } from '../api/account'
import type { ApiClient } from '../api/client'
import { useForm } from '../composables/useForm'
import { useGovueText } from '../i18n/plugin'
import { localizeError, MIN_PASSWORD_LENGTH, passwordTooShort, requiredErrors } from './accountForm'
import GovueButton from './GovueButton.vue'
import GovueField from './GovueField.vue'
import GovueInput from './GovueInput.vue'

const props = withDefaults(
  defineProps<{
    /** The client of createClient. The form never assumes a cookie. */
    client: ApiClient
    /** The signup route, relative to the client's base URL. */
    path?: string
    disabled?: boolean
  }>(),
  { path: '/account/signup', disabled: false },
)

const emit = defineEmits<{
  /** The account exists and its TOTP secret waits for a confirming code. The
   *  result carries the enrolment, which the server shows once: hand it to
   *  GovueTotpEnrolment. */
  signedUp: [result: SignupResult]
}>()

const { t } = useGovueText()

const form = useForm({
  initial: { login: '', password: '' },
  validate: (values) => {
    const errors = requiredErrors(values, ['login', 'password'], t)
    if (!errors.password && passwordTooShort(values.password)) {
      errors.password = t('govue.account.error.passwordTooShort', { min: MIN_PASSWORD_LENGTH })
    }
    return errors
  },
  submit: async (values) => {
    let result: SignupResult
    try {
      result = await props.client.post<SignupResult>(props.path, { ...values })
    } catch (caught) {
      throw localizeError(caught, t)
    }
    form.values.password = ''
    emit('signedUp', result)
  },
})

const { values, submitting, problem, requestId } = form
const locked = computed(() => props.disabled || submitting.value)
</script>

<template>
  <form
    class="flex flex-col gap-4"
    novalidate
    :aria-label="t('govue.account.signup.title')"
    :aria-busy="submitting ? 'true' : undefined"
    @submit.prevent="form.handleSubmit()"
  >
    <GovueField
      :label="t('govue.account.field.login')"
      :error="form.errorOf('login')"
      required
      :disabled="locked"
    >
      <GovueInput v-model="values.login" autocomplete="username" @blur="form.blur('login')" />
    </GovueField>

    <GovueField
      :label="t('govue.account.field.password')"
      :hint="t('govue.account.field.passwordHint', { min: MIN_PASSWORD_LENGTH })"
      :error="form.errorOf('password')"
      required
      :disabled="locked"
    >
      <GovueInput
        v-model="values.password"
        type="password"
        autocomplete="new-password"
        @blur="form.blur('password')"
      />
    </GovueField>

    <div v-if="problem" role="alert" class="flex flex-col gap-1 text-sm text-danger-text">
      <p>{{ problem }}</p>
      <p v-if="requestId" class="text-text-muted">
        {{ t('govue.error.requestId') }}: <code class="font-mono">{{ requestId }}</code>
      </p>
    </div>

    <GovueButton type="submit" variant="primary" block :disabled="disabled" :loading="submitting">
      {{ t('govue.account.signup.submit') }}
    </GovueButton>
  </form>
</template>
