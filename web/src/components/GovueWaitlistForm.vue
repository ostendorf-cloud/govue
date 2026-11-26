<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  computed,
  inject,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  useId,
  warn,
  watchEffect,
} from 'vue'

import type { ApiClient } from '../api/client'
import { useForm, type FieldErrors } from '../composables/useForm'
import { isBearerClient } from '../composables/usePasskey'
import { languageKey, useGovueText } from '../i18n/plugin'
import { createChallengeSolver } from '../lib/pow'
import { codePoints } from './contactForm'
import GovueButton from './GovueButton.vue'
import GovueCheckbox from './GovueCheckbox.vue'
import GovueField from './GovueField.vue'
import GovueInput from './GovueInput.vue'
import { headingTag, type HeadingLevel } from './headingLevel'
import { WAITLIST_LIMITS, localizeWaitlistError } from './waitlistForm'

// The form talks to the Request and Challenge routes of the Go optin package.
// Its body is exactly email, consent, language, website and pow: optin refuses
// any other key, so the consent text version stays on the server in
// optin.Config.ConsentVersion and is never sent from here.
const props = withDefaults(
  defineProps<{
    /**
     * The client of createClient. The form never sets a cookie, reads one or
     * sends a CSRF token. Create the client with `credentials: 'omit'` and
     * without `csrfToken`, so the browser attaches no cookie of the site
     * either. A bearer client is refused: the form renders nothing for it.
     */
    client: ApiClient
    /**
     * The consent text, plain. The `consent` slot wins when both are set.
     * govue ships no consent text, and the optin handler always needs the
     * box ticked: without a text from the site the form renders nothing and
     * warns in development. Keep it in step with optin.Config.ConsentVersion
     * on the server, which is what the server stores with the entry.
     */
    consentLabel?: string
    /** The site's privacy page. Linked under the fields when set. */
    privacyHref?: string
    /** The challenge route, relative to the client's base URL. */
    challengePath?: string
    /** The request route (optin.Handler.Request), relative to the client's base URL. */
    requestPath?: string
    /**
     * How long to wait after the challenge arrived before the request goes
     * out, in milliseconds. At least the server's proof of work minimum age
     * (pow.DefaultMinAge is 3 s) plus a margin.
     */
    minAgeMs?: number
    /** The level of the title, 2 to 6. Level 1 is the page's own heading. */
    headingLevel?: HeadingLevel
    disabled?: boolean
  }>(),
  {
    consentLabel: undefined,
    privacyHref: undefined,
    challengePath: '/waitlist/challenge',
    requestPath: '/waitlist',
    minAgeMs: 3500,
    headingLevel: 2,
    disabled: false,
  },
)

const emit = defineEmits<{
  /**
   * The server accepted the request. It answers the same for every address,
   * so this says nothing about whether a mail went out.
   */
  requested: []
}>()

const slots = defineSlots<{
  /** The heading. Default: the catalogue's title. */
  title?: () => unknown
  /** The consent label, for markup such as a link. There is no default text. */
  consent?: () => unknown
}>()

const { t } = useGovueText()
// The page language goes along as a hint for the site's confirmation mail
// (optin.ConfirmationData.Language). Without the language layer the key is
// left out.
const languageState = inject(languageKey, null)

// A bearer client would send the device token to a public route. A consent
// box without a text is a consent the visitor cannot give. Both fail closed,
// and both follow the props, so a text that arrives late unblocks the form.
const bearer = computed(() => isBearerClient(props.client))
const consentMissing = () => !props.consentLabel && !slots.consent
const blocked = () => bearer.value || consentMissing()
watchEffect(() => {
  if (bearer.value) {
    warn(
      'GovueWaitlistForm refuses a bearer client: it would send the device token to a public route.',
    )
  }
  if (consentMissing()) {
    warn(
      'GovueWaitlistForm needs a consent text from the site. Set the consentLabel prop or fill the consent slot.',
    )
  }
})

const FIELDS = ['email', 'consent'] as const
const headingId = useId()
const honeypotId = useId()
const unsupportedId = `${headingId}-unsupported`

// The server render and a page without JavaScript keep the submit button
// disabled: a native submit would put the address into a GET query string.
// A disabled default button also stops the implicit submit of Enter.
const mounted = ref(false)
// Whether this browser can run the proof of work: a module Worker and
// crypto.subtle, which only a secure context has. Read after mount, so the
// server render touches neither.
const supported = ref(true)
onMounted(() => {
  supported.value =
    typeof Worker === 'function' && typeof globalThis.crypto?.subtle?.digest === 'function'
  mounted.value = true
})

const solver = createChallengeSolver(props.client, {
  path: props.challengePath,
  minAgeMs: props.minAgeMs,
})
// A visitor who leaves while the worker searches does not leave it running,
// and a request on its way is cancelled: no `requested` after unmount.
const submitAbort = new AbortController()
let unmounted = false
onBeforeUnmount(() => {
  unmounted = true
  submitAbort.abort()
  solver.dispose()
})

const done = ref(false)
const phase = ref<'solving' | 'sending'>('solving')
const formEl = ref<HTMLFormElement | null>(null)
const statusEl = ref<HTMLElement | null>(null)
const problemEl = ref<HTMLElement | null>(null)
const titleTag = computed(() => headingTag(props.headingLevel))

const form = useForm({
  initial: { email: '', consent: false, website: '' },
  validate: (values) => {
    const errors: FieldErrors<typeof values> = {}
    if (values.email.trim() === '') {
      errors.email = t('govue.waitlist.error.required')
    } else if (codePoints(values.email.trim()) > WAITLIST_LIMITS.email) {
      errors.email = t('govue.waitlist.error.tooLong', { max: WAITLIST_LIMITS.email })
    }
    if (!values.consent) {
      errors.consent = t('govue.waitlist.error.consentRequired')
    }
    return errors
  },
  submit: async (values) => {
    if (values.website.trim() !== '') {
      // A filled honeypot is a bot. It sees the same answer a person sees, and
      // nothing is solved or sent for it.
      done.value = true
      return
    }
    phase.value = 'solving'
    let pow
    try {
      pow = await solver.solved()
    } catch (caught) {
      throw localizeWaitlistError(caught, t, FIELDS)
    }
    phase.value = 'sending'
    try {
      await props.client.post(
        props.requestPath,
        {
          email: values.email,
          // Always true here, the check above refuses a send without it. The
          // server records it as the client's claim next to its consent
          // version and refuses a missing or false one.
          consent: values.consent,
          ...(languageState ? { language: languageState.locale.value } : {}),
          website: '',
          pow,
        },
        { skipUnauthenticatedHook: true, signal: submitAbort.signal },
      )
    } catch (caught) {
      // The solution is spent whatever the answer was. A fresh challenge is
      // ready by the time the visitor sends again.
      solver.prepare()
      throw localizeWaitlistError(caught, t, FIELDS)
    }
    if (unmounted) {
      return
    }
    // Done is set here and nowhere else: after the server said yes.
    done.value = true
    emit('requested')
  },
})

const { values, submitting, problem, requestId } = form
const locked = computed(() => props.disabled || submitting.value)

const status = computed(() => {
  if (done.value) return t('govue.waitlist.success')
  if (submitting.value) {
    return phase.value === 'solving' ? t('govue.waitlist.solving') : t('govue.waitlist.sending')
  }
  return ''
})

/**
 * onSubmit moves focus to where the visitor has to look next: the status
 * after the request (the form is gone, and so is the focused button), the
 * first field with an error after a refused check, the failure text after a
 * failed request.
 */
async function onSubmit(): Promise<void> {
  if (!mounted.value || !supported.value) {
    return
  }
  const ok = await form.handleSubmit()
  await nextTick()
  if (ok) {
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
  done.value = false
  await nextTick()
  formEl.value?.querySelector<HTMLElement>('input[name="email"]')?.focus()
}
</script>

<template>
  <p v-if="blocked()" role="alert" class="text-base text-text">
    {{ t('govue.waitlist.unavailable') }}
  </p>
  <section v-else class="flex flex-col gap-4" :aria-labelledby="headingId">
    <component :is="titleTag" :id="headingId" class="text-xl font-semibold text-text">
      <slot name="title">{{ t('govue.waitlist.title') }}</slot>
    </component>

    <noscript>
      <p class="text-base text-text">{{ t('govue.waitlist.noscript') }}</p>
    </noscript>

    <!-- Always in the page, so a change of its text is announced. It takes
         focus after the request, because the form that had it is gone. -->
    <p
      ref="statusEl"
      role="status"
      aria-live="polite"
      tabindex="-1"
      :class="status ? 'text-base text-text' : 'sr-only'"
    >
      {{ status }}
    </p>

    <div v-if="done" class="flex flex-col items-start gap-2">
      <GovueButton variant="secondary" @click="again">
        {{ t('govue.waitlist.another') }}
      </GovueButton>
    </div>

    <form
      v-else
      ref="formEl"
      class="relative flex flex-col gap-4"
      novalidate
      :aria-labelledby="headingId"
      :aria-busy="submitting ? 'true' : undefined"
      @focusin="supported && solver.prepare()"
      @submit.prevent="onSubmit"
    >
      <GovueField
        :label="t('govue.waitlist.field.email')"
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

      <!-- The honeypot sits off screen, never display none or hidden: a bot
           that skips an invisible field by that test still fills this one. -->
      <div class="absolute -left-[9999px] size-px overflow-hidden" aria-hidden="true">
        <label :for="honeypotId">{{ t('govue.waitlist.field.honeypot') }}</label>
        <input
          :id="honeypotId"
          v-model="values.website"
          type="text"
          name="website"
          tabindex="-1"
          autocomplete="off"
        />
      </div>

      <div class="flex flex-col gap-1.5">
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
        {{ t('govue.waitlist.privacyIntro') }}
        <a
          :href="privacyHref"
          class="inline-flex min-h-11 items-center text-accent-text underline"
          >{{ t('govue.waitlist.privacy') }}</a
        >
      </p>

      <p v-if="!supported" :id="unsupportedId" class="text-sm text-danger-text">
        {{ t('govue.waitlist.unsupported') }}
      </p>

      <div
        v-if="problem"
        ref="problemEl"
        role="alert"
        tabindex="-1"
        class="flex flex-col gap-1 text-sm text-danger-text"
      >
        <p>{{ t('govue.waitlist.failure') }} {{ problem }}</p>
        <p v-if="requestId" class="text-text-muted">
          {{ t('govue.error.requestId') }}: <code class="font-mono">{{ requestId }}</code>
        </p>
      </div>

      <GovueButton
        type="submit"
        variant="primary"
        block
        :disabled="disabled || !mounted || !supported"
        :aria-describedby="supported ? undefined : unsupportedId"
        :loading="submitting"
      >
        {{ t('govue.waitlist.submit') }}
      </GovueButton>
    </form>
  </section>
</template>
