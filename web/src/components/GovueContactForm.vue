<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, ref, useId } from 'vue'

import type { ApiClient } from '../api/client'
import { useForm, type FieldErrors } from '../composables/useForm'
import { useGovueText } from '../i18n/plugin'
import { createChallengeSolver } from '../lib/pow'
import { CONTACT_LIMITS, codePoints, localizeContactError } from './contactForm'
import GovueButton from './GovueButton.vue'
import GovueCheckbox from './GovueCheckbox.vue'
import GovueField from './GovueField.vue'
import GovueInput from './GovueInput.vue'
import GovueTextarea from './GovueTextarea.vue'

const props = withDefaults(
  defineProps<{
    /**
     * The client of createClient. The form is stateless: it reads and writes
     * no cookie and sends no CSRF token. Create the client for it with
     * `credentials: 'omit'` and no `csrfToken`, so the browser attaches no
     * cookie of the site either.
     */
    client: ApiClient
    /** The site's privacy page. Linked under the fields when set. */
    privacyHref?: string
    /** Show a consent checkbox that has to be ticked before the form is sent. */
    requireConsent?: boolean
    /** The challenge route, relative to the client's base URL. */
    challengePath?: string
    /** The submit route, relative to the client's base URL. */
    submitPath?: string
    disabled?: boolean
  }>(),
  {
    privacyHref: undefined,
    requireConsent: false,
    challengePath: '/contact/challenge',
    submitPath: '/contact',
    disabled: false,
  },
)

const emit = defineEmits<{
  /** The server accepted the message. Never emitted for a failure. */
  sent: []
}>()

defineSlots<{
  /** The heading. Default: the catalogue's title. */
  title?: () => unknown
  /** The consent label, for markup such as a link. Default: the catalogue's text. */
  consent?: () => unknown
}>()

const { t } = useGovueText()

const FIELDS = ['name', 'email', 'message', 'consent'] as const
const headingId = useId()
const honeypotId = useId()

// No Worker and no crypto.subtle until prepare() runs, which only a focus does,
// so the form renders on the server.
const solver = createChallengeSolver(props.client, { path: props.challengePath })

const sent = ref(false)

const form = useForm({
  initial: { name: '', email: '', message: '', consent: false, website: '' },
  validate: (values) => {
    const errors: FieldErrors<typeof values> = {}
    for (const field of ['name', 'email', 'message'] as const) {
      const value = values[field]
      if (value.trim() === '') {
        errors[field] = t('govue.contact.error.required')
      } else if (codePoints(value) > CONTACT_LIMITS[field]) {
        errors[field] = t('govue.contact.error.tooLong')
      }
    }
    if (props.requireConsent && !values.consent) {
      errors.consent = t('govue.contact.error.consentRequired')
    }
    return errors
  },
  submit: async (values) => {
    if (values.website.trim() !== '') {
      // A filled honeypot is a bot. It sees the same success a person sees, and
      // nothing is sent or solved for it.
      sent.value = true
      return
    }
    let pow
    try {
      pow = await solver.solved()
    } catch (caught) {
      throw localizeContactError(caught, t, FIELDS)
    }
    try {
      await props.client.post(
        props.submitPath,
        {
          name: values.name,
          email: values.email,
          message: values.message,
          consent: values.consent,
          website: '',
          pow,
        },
        { skipUnauthenticatedHook: true },
      )
    } catch (caught) {
      // The solution is spent whatever the answer was. A fresh challenge is
      // ready by the time the visitor sends again.
      solver.prepare()
      throw localizeContactError(caught, t, FIELDS)
    }
    // Success is set here and nowhere else: after the server said yes.
    sent.value = true
    emit('sent')
  },
})

const { values, submitting, problem, requestId } = form
const locked = computed(() => props.disabled || submitting.value)

const status = computed(() => {
  if (sent.value) return t('govue.contact.success')
  if (submitting.value) return t('govue.contact.sending')
  return ''
})

function again(): void {
  form.reset()
  sent.value = false
}
</script>

<template>
  <section class="flex flex-col gap-4" :aria-labelledby="headingId">
    <h2 :id="headingId" class="text-xl font-semibold text-text">
      <slot name="title">{{ t('govue.contact.title') }}</slot>
    </h2>

    <!-- Always in the page, so a change of its text is announced. -->
    <p role="status" aria-live="polite" :class="status ? 'text-base text-text' : 'sr-only'">
      {{ status }}
    </p>

    <div v-if="sent" class="flex flex-col items-start gap-2">
      <GovueButton variant="secondary" @click="again">
        {{ t('govue.contact.another') }}
      </GovueButton>
    </div>

    <form
      v-else
      class="relative flex flex-col gap-4"
      novalidate
      :aria-labelledby="headingId"
      :aria-busy="submitting ? 'true' : undefined"
      @focusin="solver.prepare()"
      @submit.prevent="form.handleSubmit()"
    >
      <GovueField
        :label="t('govue.contact.field.name')"
        :error="form.errorOf('name')"
        required
        :disabled="locked"
      >
        <GovueInput
          v-model="values.name"
          name="name"
          autocomplete="name"
          @blur="form.blur('name')"
        />
      </GovueField>

      <GovueField
        :label="t('govue.contact.field.email')"
        :error="form.errorOf('email')"
        required
        :disabled="locked"
      >
        <GovueInput
          v-model="values.email"
          type="email"
          name="email"
          autocomplete="email"
          inputmode="email"
          @blur="form.blur('email')"
        />
      </GovueField>

      <GovueField
        :label="t('govue.contact.field.message')"
        :error="form.errorOf('message')"
        required
        :disabled="locked"
      >
        <GovueTextarea
          v-model="values.message"
          name="message"
          :rows="6"
          @blur="form.blur('message')"
        />
      </GovueField>

      <!-- The honeypot sits off screen, never display none or hidden: a bot
           that skips an invisible field by that test still fills this one. -->
      <div class="absolute -left-[9999px] size-px overflow-hidden" aria-hidden="true">
        <label :for="honeypotId">{{ t('govue.contact.field.honeypot') }}</label>
        <input
          :id="honeypotId"
          v-model="values.website"
          type="text"
          name="website"
          tabindex="-1"
          autocomplete="off"
        />
      </div>

      <div v-if="requireConsent" class="flex flex-col gap-1.5">
        <GovueCheckbox
          v-model="values.consent"
          :label="t('govue.contact.consent')"
          :disabled="locked"
          :aria-invalid="form.errorOf('consent') ? 'true' : undefined"
          :aria-describedby="form.errorOf('consent') ? `${headingId}-consent-error` : undefined"
          @change="form.blur('consent')"
        >
          <slot name="consent">{{ t('govue.contact.consent') }}</slot>
        </GovueCheckbox>
        <p
          v-if="form.errorOf('consent')"
          :id="`${headingId}-consent-error`"
          role="alert"
          class="text-sm text-danger-text"
        >
          {{ form.errorOf('consent') }}
        </p>
      </div>

      <p v-if="privacyHref" class="text-sm text-text-muted">
        {{ t('govue.contact.privacyIntro') }}
        <a
          :href="privacyHref"
          class="inline-flex min-h-11 items-center text-accent-text underline"
          >{{ t('govue.contact.privacy') }}</a
        >
      </p>

      <div v-if="problem" role="alert" class="flex flex-col gap-1 text-sm text-danger-text">
        <p>{{ t('govue.contact.failure') }} {{ problem }}</p>
        <p v-if="requestId" class="text-text-muted">
          {{ t('govue.error.requestId') }}: <code class="font-mono">{{ requestId }}</code>
        </p>
      </div>

      <GovueButton type="submit" variant="primary" block :disabled="disabled" :loading="submitting">
        {{ t('govue.contact.submit') }}
      </GovueButton>
    </form>
  </section>
</template>
