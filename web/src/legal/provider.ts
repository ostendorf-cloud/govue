// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { inject, type App, type InjectionKey } from 'vue'

import { localeCodes } from '../i18n/catalogues'
import type { LegalProvider } from './types'

export const legalProviderKey: InjectionKey<LegalProvider> = Symbol('govue-legal-provider')

const required = ['name', 'street', 'postalCode', 'city', 'phone', 'email', 'vatId'] as const

/**
 * assertProvider returns the provider when every field the law asks for is
 * filled in, and throws naming the first field that is not. A page with a
 * blank imprint line is worse than a failed start.
 */
export function assertProvider(provider: LegalProvider): LegalProvider {
  for (const field of required) {
    if (typeof provider[field] !== 'string' || provider[field].trim() === '') {
      throw new Error(`govue: legal provider: ${field} is empty`)
    }
  }
  for (const code of localeCodes) {
    if (!provider.disputeResolution?.[code]?.trim()) {
      throw new Error(`govue: legal provider: disputeResolution has no ${code} text`)
    }
  }
  return provider
}

/**
 * createGovueLegal passes the provider in once per site. The imprint and the
 * privacy page read it, so no page types it.
 *
 *   app.use(createGovueLegal({ name: '...', ... }))
 */
export function createGovueLegal(provider: LegalProvider): { install: (app: App) => void } {
  const checked = assertProvider(provider)
  return {
    install(app) {
      app.provide(legalProviderKey, checked)
    },
  }
}

/** useLegalProvider is the pages' door to the configuration. */
export function useLegalProvider(override?: LegalProvider): LegalProvider {
  if (override) {
    return assertProvider(override)
  }
  const provider = inject(legalProviderKey, null)
  if (!provider) {
    throw new Error('govue: install the legal provider first: app.use(createGovueLegal(provider))')
  }
  return provider
}
