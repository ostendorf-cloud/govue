import { describe, expect, it, vi } from 'vitest'

import type { ApiError } from './useForm'
import { useForm } from './useForm'

interface Wish {
  title: string
  price: string
}

/**
 * A stand-in for whatever a future HTTP client rejects with. It is still a
 * real Error, which is what the rest of this package's lint rules expect from
 * anything thrown or rejected, and `isApiError` only cares that `code` and
 * `message` are strings, which an Error subclass satisfies just as well as a
 * plain object would.
 */
class TestApiError extends Error implements ApiError {
  code: string
  details?: Record<string, unknown>
  requestId?: string

  constructor(init: ApiError) {
    super(init.message)
    this.code = init.code
    this.details = init.details
    this.requestId = init.requestId
  }
}

function newForm(submit: (values: Wish) => Promise<void> | void) {
  return useForm<Wish>({
    initial: { title: '', price: '' },
    validate: (values) => {
      const errors: { title?: string; price?: string } = {}
      if (values.title.trim() === '') {
        errors.title = 'Please enter a title.'
      }
      if (values.price !== '' && Number.isNaN(Number(values.price))) {
        errors.price = 'Please enter an amount.'
      }
      return errors
    },
    submit,
  })
}

describe('useForm', () => {
  it('submits the values when everything is valid', async () => {
    const submit = vi.fn()
    const form = newForm(submit)
    form.values.title = 'Lamp'

    await expect(form.handleSubmit()).resolves.toBe(true)

    expect(submit).toHaveBeenCalledWith({ title: 'Lamp', price: '' })
    expect(form.problem.value).toBe('')
  })

  it('refuses to submit an invalid form and marks the fields touched', async () => {
    const submit = vi.fn()
    const form = newForm(submit)

    await expect(form.handleSubmit()).resolves.toBe(false)

    expect(submit).not.toHaveBeenCalled()
    expect(form.errors.value.title).toBe('Please enter a title.')
    expect(form.errorOf('title')).toBe('Please enter a title.')
    expect(form.hasErrors.value).toBe(true)
  })

  it('shows an error only once its field was touched', () => {
    const form = newForm(vi.fn())

    expect(form.errorOf('title')).toBeUndefined()

    form.blur('title')
    expect(form.errorOf('title')).toBe('Please enter a title.')
  })

  it('validates one field on blur, not the whole form', () => {
    const form = newForm(vi.fn())
    form.values.price = 'free'

    form.blur('price')

    expect(form.errors.value.price).toBe('Please enter an amount.')
    // The empty title is wrong too, but the user has not been there yet.
    expect(form.errorOf('title')).toBeUndefined()
  })

  it('clears a field error once it is fixed', () => {
    const form = newForm(vi.fn())
    form.blur('title')
    expect(form.errors.value.title).toBeTruthy()

    form.values.title = 'Lamp'
    form.blur('title')

    expect(form.errors.value.title).toBeUndefined()
    expect(form.hasErrors.value).toBe(false)
  })

  it('puts the server field errors on the inputs and keeps the input', async () => {
    const form = newForm(() =>
      Promise.reject(
        new TestApiError({
          code: 'invalid_argument',
          message: 'two fields are wrong',
          details: { title: 'already on this list', unknown_field: 'ignored' },
          requestId: 'req-7',
        }),
      ),
    )
    form.values.title = 'Lamp'

    await expect(form.handleSubmit()).resolves.toBe(false)

    expect(form.errors.value.title).toBe('already on this list')
    expect(form.errorOf('title')).toBe('already on this list')
    // The field already says it, so there is no second message.
    expect(form.problem.value).toBe('')
    expect(form.requestId.value).toBe('req-7')
    // Nothing the user typed is thrown away.
    expect(form.values.title).toBe('Lamp')
  })

  it('shows one message with the request id for a failure without fields', async () => {
    const form = newForm(() =>
      Promise.reject(
        new TestApiError({
          code: 'internal',
          message: 'internal error',
          requestId: 'req-9',
        }),
      ),
    )
    form.values.title = 'Lamp'

    await form.handleSubmit()

    expect(form.problem.value).toBe('internal error')
    expect(form.requestId.value).toBe('req-9')
    expect(form.hasErrors.value).toBe(false)
  })

  it('reports an unexpected failure without leaking it', async () => {
    const form = newForm(() => {
      throw new TypeError('cannot read properties of undefined')
    })
    form.values.title = 'Lamp'

    await form.handleSubmit()

    expect(form.problem.value).toBe('Something went wrong. Please try again.')
    expect(form.problem.value).not.toContain('undefined')
  })

  it('is in flight while submitting and refuses a second submit', async () => {
    let release = () => {}
    const form = newForm(
      () =>
        new Promise<void>((resolve) => {
          release = resolve
        }),
    )
    form.values.title = 'Lamp'

    const first = form.handleSubmit()
    expect(form.submitting.value).toBe(true)
    await expect(form.handleSubmit()).resolves.toBe(false)

    release()
    await expect(first).resolves.toBe(true)
    expect(form.submitting.value).toBe(false)
  })

  it('resets to the initial values', async () => {
    const form = newForm(vi.fn())
    form.values.title = 'Lamp'
    form.blur('price')
    await form.handleSubmit()

    form.reset()

    expect(form.values).toEqual({ title: '', price: '' })
    expect(form.hasErrors.value).toBe(false)
    expect(form.errorOf('price')).toBeUndefined()
    expect(form.problem.value).toBe('')
  })
})
