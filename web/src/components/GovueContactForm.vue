<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, nextTick, onBeforeUnmount, ref, useId, warn, watchEffect } from 'vue'

import type { ApiClient } from '../api/client'
import { useForm, type FieldErrors } from '../composables/useForm'
import { isBearerClient } from '../composables/usePasskey'
import { useGovueText } from '../i18n/plugin'
import { createChallengeSolver } from '../lib/pow'
import { CONTACT_LIMITS, codePoints, localizeContactError } from './contactForm'
import GovueButton from './GovueButton.vue'
import GovueCheckbox from './GovueCheckbox.vue'
import GovueField from './GovueField.vue'
import GovueInput from './GovueInput.vue'
import GovueTextarea from './GovueTextarea.vue'
import { headingTag, type HeadingLevel } from './headingLevel'

const props = withDefaults(
  defineProps<{
    /**
     * The client of createClient. The form never sets a cookie, reads one or
     * sends a CSRF token, and it never touches the client's options. What the
     * browser attaches to a request is the client's setting, and that is the
     * caller's duty: create the client with `credentials: 'omit'` and without
     * `csrfToken`, so the browser attaches no cookie of the site either. A
     * client with the default `credentials: 'same-origin'` sends the site's
     * cookies to the contact routes. A bearer client (a native app's device
     * token) is refused: the form renders nothing for it, as it would send the
     * token to a public route.
     */
    client: ApiClient
    /** The site's privacy page. Linked under the fields when set. */
    privacyHref?: string
    /**
     * Show a consent checkbox that has to be ticked before the form is sent.
     * govue ships no consent text: the site supplies it through `consentLabel`
     * or the `consent` slot. Without one the form refuses to render, with a
     * warning in development. The request then carries `consent` with the
     * state of the box, which the Go handler checks with
     * `contact.Config.RequireConsent` and hands to the site's mail body. Set
     * both together. Without this prop the request carries no `consent`.
     */
    requireConsent?: boolean
    /** The consent text, plain. The `consent` slot wins when both are set. */
    consentLabel?: string
    /** The challenge route, relative to the client's base URL. */
    challengePath?: string
    /** The submit route, relative to the client's base URL. */
    submitPath?: string
    /**
     * How long to wait after the challenge arrived before the message goes
     * out, in milliseconds. It must be at least the server's proof of work
     * minimum age (pow.DefaultMinAge is 3 s) plus a margin: a shorter wait is
     * refused as too fast. Raise it when the server's MinAge is raised.
     */
    minAgeMs?: number
    /** The level of the title, 2 to 6. Level 1 is the page's own heading. */
    headingLevel?: HeadingLevel
    disabled?: boolean
  }>(),
  {
    privacyHref: undefined,
    requireConsent: false,
    consentLabel: undefined,
    challengePath: '/contact/challenge',
    submitPath: '/contact',
    minAgeMs: 3500,
    headingLevel: 2,
    disabled: false,
  },
)

const emit = defineEmits<{
  /** The server accepted the message. Never emitted for a failure. */
  sent: []
}>()

const slots = defineSlots<{
  /** The heading. Default: the catalogue's title. */
  title?: () => unknown
  /** The consent label, for markup such as a link. There is no default text. */
  consent?: () => unknown
}>()

const { t } = useGovueText()

// A bearer client would send the device token to the contact routes. A
// required consent without a text would be a checkbox with no label, or a
// consent the visitor cannot give. Both fail closed: no field is rendered, a
// short alert says the form is not available, and the development warning
// names the reason. They follow the props, so a consent text that arrives
// late unblocks the form. The slot is read in the render, which tracks it.
const bearer = computed(() => isBearerClient(props.client))
const consentMissing = () => props.requireConsent && !props.consentLabel && !slots.consent
const blocked = () => bearer.value || consentMissing()
watchEffect(() => {
  if (bearer.value) {
    warn(
      'GovueContactForm refuses a bearer client: it would send the device token to a public route.',
    )
  }
  if (consentMissing()) {
    warn(
      'GovueContactForm: requireConsent needs a consent text from the site. Set the consentLabel prop or fill the consent slot.',
    )
  }
})

const FIELDS = ['name', 'email', 'message', 'consent'] as const
const headingId = useId()
const honeypotId = useId()

// No Worker and no crypto.subtle until prepare() runs, which only a focus does,
// so the form renders on the server.
const solver = createChallengeSolver(props.client, {
  path: props.challengePath,
  minAgeMs: props.minAgeMs,
})
// A visitor who leaves while the worker searches does not leave it running,
// and a send that is still on its way is cancelled: no `sent` after unmount.
const submitAbort = new AbortController()
let unmounted = false
onBeforeUnmount(() => {
  unmounted = true
  submitAbort.abort()
  solver.dispose()
})

const sent = ref(false)
const formEl = ref<HTMLFormElement | null>(null)
const statusEl = ref<HTMLElement | null>(null)
const problemEl = ref<HTMLElement | null>(null)
const titleTag = computed(() => headingTag(props.headingLevel))

const form = useForm({
  initial: { name: '', email: '', message: '', consent: false, website: '' },
  validate: (values) => {
    const errors: FieldErrors<typeof values> = {}
    for (const field of ['name', 'email', 'message'] as const) {
      const value = values[field]
      if (value.trim() === '') {
        errors[field] = t('govue.contact.error.required')
      } else if (codePoints(value) > CONTACT_LIMITS[field]) {
        errors[field] = t('govue.contact.error.tooLong', { max: CONTACT_LIMITS[field] })
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
          // The state of the box, only when the form shows one. The server
          // records it as the client's claim (contact.Request.Consent) and,
          // with RequireConsent, refuses a missing or false one.
          ...(props.requireConsent ? { consent: values.consent } : {}),
          website: '',
          pow,
        },
        { skipUnauthenticatedHook: true, signal: submitAbort.signal },
      )
    } catch (caught) {
      // The solution is spent whatever the answer was. A fresh challenge is
      // ready by the time the visitor sends again.
      solver.prepare()
      throw localizeContactError(caught, t, FIELDS)
    }
    if (unmounted) {
      return
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

/**
 * onSubmit moves focus to where the visitor has to look next: the success
 * text after a send (the form is gone, and so is the focused button), the
 * first field with an error after a refused check, the failure text after a
 * failed send.
 */
async function onSubmit(): Promise<void> {
  const done = await form.handleSubmit()
  await nextTick()
  if (done) {
    statusEl.value?.focus()
    return
  }
  const invalid = formEl.value?.querySelector<HTMLElement>('[aria-invalid="true"]')
  if (invalid) {
    invalid.focus()
  } else {
    problemEl.value?.focus()
  }
}

async function again(): Promise<void> {
  form.reset()
  sent.value = false
  await nextTick()
  formEl.value?.querySelector<HTMLElement>('input[name="name"]')?.focus()
}
</script>

<template>
  <p v-if="blocked()" role="alert" class="text-base text-text">
    {{ t('govue.contact.unavailable') }}
  </p>
  <section v-else class="flex flex-col gap-4" :aria-labelledby="headingId">
    <component :is="titleTag" :id="headingId" class="text-xl font-semibold text-text">
      <slot name="title">{{ t('govue.contact.title') }}</slot>
    </component>

    <!-- Always in the page, so a change of its text is announced. It takes
         focus after a send, because the form that had it is gone by then. -->
    <p
      ref="statusEl"
      role="status"
      aria-live="polite"
      tabindex="-1"
      :class="status ? 'text-base text-text' : 'sr-only'"
    >
      {{ status }}
    </p>

    <div v-if="sent" class="flex flex-col items-start gap-2">
      <GovueButton variant="secondary" @click="again">
        {{ t('govue.contact.another') }}
      </GovueButton>
    </div>

    <form
      v-else
      ref="formEl"
      class="relative flex flex-col gap-4"
      novalidate
      :aria-labelledby="headingId"
      :aria-busy="submitting ? 'true' : undefined"
      @focusin="solver.prepare()"
      @submit.prevent="onSubmit"
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
          :label="consentLabel ?? ''"
          :disabled="locked"
          :aria-invalid="form.errorOf('consent') ? 'true' : undefined"
          :aria-describedby="form.errorOf('consent') ? `${headingId}-consent-error` : undefined"
          @change="form.blur('consent')"
        >
          <slot name="consent">{{ consentLabel }}</slot>
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

      <div
        v-if="problem"
        ref="problemEl"
        role="alert"
        tabindex="-1"
        class="flex flex-col gap-1 text-sm text-danger-text"
      >
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
