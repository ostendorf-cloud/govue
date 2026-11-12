// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, reactive, ref, type ComputedRef, type Ref } from 'vue'

/**
 * What a submit callback may reject with, once some future HTTP client has
 * parsed the api package's error envelope and camelCased request_id. No such
 * client exists in this repository yet, so useForm only describes the shape
 * it is prepared to handle; it never builds or imports one.
 */
export interface ApiError {
  code: string
  message: string
  details?: Record<string, unknown>
  requestId?: string
}

/** isApiError duck types rather than checking a class, since the error could
 *  come from any future client implementation that agrees on this shape. */
function isApiError(value: unknown): value is ApiError {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as Record<string, unknown>).code === 'string' &&
    typeof (value as Record<string, unknown>).message === 'string'
  )
}

/** FieldErrors maps a field name to the message shown under its input. */
export type FieldErrors<T> = Partial<Record<keyof T & string, string>>

/** Touched records which fields the user has already left. */
export type Touched<T> = Partial<Record<keyof T & string, boolean>>

export interface FormOptions<T extends object> {
  /** The values the form starts with, and what reset goes back to. */
  initial: T
  /** Client side checks. Called on blur for one field and on submit for all. */
  validate?: (values: T) => FieldErrors<T>
  /** What submitting does. An ApiError's field details land on the inputs. */
  submit: (values: T) => Promise<void> | void
}

export interface FormState<T extends object> {
  values: T
  /** The current message per field, whether its field was touched or not. */
  errors: Readonly<Ref<FieldErrors<T>>>
  touched: Readonly<Ref<Touched<T>>>
  /** True while submit is in flight; disable the submit button with it. */
  submitting: Readonly<Ref<boolean>>
  /** The one message for a failure that is not about a single field. */
  problem: Readonly<Ref<string>>
  /** The request id of that failure, so a report can name it. */
  requestId: Readonly<Ref<string>>
  /** Whether any field currently has an error. */
  hasErrors: ComputedRef<boolean>
  /** Marks the field touched and validates it; call it on blur. */
  blur: (field: keyof T & string) => void
  /** Validates everything and submits. Returns whether it went through. */
  handleSubmit: () => Promise<boolean>
  reset: () => void
  /** The error of a field, but only once it has been touched. */
  errorOf: (field: keyof T & string) => string | undefined
}

/**
 * useForm is the four states a form has: editing, in flight, refused and done.
 *
 * Validation runs on blur and on submit, never on every keystroke, and a
 * failure keeps everything the user typed.
 */
export function useForm<T extends object>(options: FormOptions<T>): FormState<T> {
  const values = reactive(structuredClone(options.initial)) as T
  // The two maps are refs rather than reactive objects: a generic key type
  // cannot index a Reactive<Partial<Record<...>>>, and replacing the whole map
  // is what both of them actually do.
  const errors = ref<FieldErrors<T>>({}) as Ref<FieldErrors<T>>
  const touched = ref<Touched<T>>({}) as Ref<Touched<T>>
  const submitting = ref(false)
  const problem = ref('')
  const requestId = ref('')

  function runValidation(): FieldErrors<T> {
    return options.validate?.(values) ?? {}
  }

  return {
    values,
    errors,
    touched,
    submitting,
    problem,
    requestId,
    hasErrors: computed(() => Object.values(errors.value).some((message) => Boolean(message))),
    blur: (field) => {
      touched.value = { ...touched.value, [field]: true }
      const found = runValidation()
      // Only this field's result is taken, so leaving the first input does not
      // light up the whole form.
      const next = { ...errors.value }
      if (found[field]) {
        next[field] = found[field]
      } else {
        delete next[field]
      }
      errors.value = next
    },
    handleSubmit: async () => {
      if (submitting.value) {
        return false
      }
      problem.value = ''
      requestId.value = ''

      const found = runValidation()
      const fields = Object.keys(found) as Array<keyof T & string>
      if (fields.length > 0) {
        errors.value = found
        const nextTouched = { ...touched.value }
        for (const field of fields) {
          nextTouched[field] = true
        }
        touched.value = nextTouched
        return false
      }
      errors.value = {}

      submitting.value = true
      try {
        await options.submit(values)
        return true
      } catch (caught) {
        if (isApiError(caught)) {
          const serverErrors: FieldErrors<T> = {}
          const nextTouched = { ...touched.value }
          for (const [field, message] of Object.entries(caught.details ?? {})) {
            if (field in values && typeof message === 'string') {
              const key = field as keyof T & string
              serverErrors[key] = message
              nextTouched[key] = true
            }
          }
          errors.value = serverErrors
          touched.value = nextTouched
          // A field error is already shown at the field; anything else needs
          // the one message, with the id that identifies the log line.
          problem.value = Object.keys(serverErrors).length > 0 ? '' : caught.message
          requestId.value = caught.requestId ?? ''
        } else {
          // Never show the raw error: it could be anything, including a
          // message that leaks implementation detail.
          problem.value = 'Something went wrong. Please try again.'
        }
        return false
      } finally {
        submitting.value = false
      }
    },
    reset: () => {
      Object.assign(values, structuredClone(options.initial))
      errors.value = {}
      touched.value = {}
      problem.value = ''
      requestId.value = ''
    },
    errorOf: (field) => (touched.value[field] ? errors.value[field] : undefined),
  }
}
