<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import type { LoginResult } from '../api/account'
import type { ApiClient } from '../api/client'
import { useForm } from '../composables/useForm'
import { useGovueText } from '../i18n/plugin'
import { localizeError, requiredErrors } from './accountForm'
import GovueButton from './GovueButton.vue'
import GovueField from './GovueField.vue'
import GovueInput from './GovueInput.vue'

const props = withDefaults(
  defineProps<{
    /** The client of createClient. The form never assumes a cookie. */
    client: ApiClient
    /** The login route, relative to the client's base URL. */
    path?: string
    disabled?: boolean
  }>(),
  { path: '/account/login', disabled: false },
)

const emit = defineEmits<{
  /** The login went through. The answer is whatever the server sent, a token
   *  for a native app included once the server has one. */
  success: [result: LoginResult]
}>()

const { t } = useGovueText()

const form = useForm({
  initial: { login: '', password: '', code: '' },
  validate: (values) => requiredErrors(values, ['login', 'password', 'code'], t),
  submit: async (values) => {
    let result: LoginResult
    try {
      result = await props.client.post<LoginResult>(props.path, { ...values })
    } catch (caught) {
      throw localizeError(caught, t)
    }
    // Neither the password nor a used code has a reason to stay in the page.
    form.values.password = ''
    form.values.code = ''
    emit('success', result)
  },
})

const { values, submitting, problem, requestId } = form
const locked = computed(() => props.disabled || submitting.value)
</script>

<template>
  <form
    class="flex flex-col gap-4"
    novalidate
    :aria-label="t('govue.account.login.title')"
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

    <!-- A failure that is not about one field. It is announced when it
         appears, and the request id is there for a bug report. -->
    <div v-if="problem" role="alert" class="flex flex-col gap-1 text-sm text-danger-text">
      <p>{{ problem }}</p>
      <p v-if="requestId" class="text-text-muted">
        {{ t('govue.error.requestId') }}: <code class="font-mono">{{ requestId }}</code>
      </p>
    </div>

    <GovueButton type="submit" variant="primary" block :disabled="disabled" :loading="submitting">
      {{ t('govue.account.login.submit') }}
    </GovueButton>
  </form>
</template>
