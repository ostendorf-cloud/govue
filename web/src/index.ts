// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The public surface of @ostendorf-cloud/govue.
 *
 * The stylesheets are not imported here: an app imports the base stylesheet and
 * at most one theme itself, so it decides the order and nothing is pulled in
 * that it did not ask for.
 *
 * This foundation is the token contract, the theme files, the theming composable,
 * the API client, the overlay components, the layout components, and the form
 * input components: a field wrapper, the text, select, checkbox, switch and
 * textarea controls, a button, and the useForm composable that ties them to a
 * submit.
 */

export { themeNames, useTheme } from './composables/useTheme'
export type { ColorMode, ThemeName, ThemeOptions, ThemeState } from './composables/useTheme'

// The API client, which is the only place an app calls fetch.
export { createClient } from './api/client'
export type {
  ApiClient,
  BearerAuthOptions,
  BearerClient,
  BearerMemoryAuthOptions,
  BearerStorageAuthOptions,
  ClientOptions,
  CookieAuthOptions,
  DeviceSession,
  RequestOptions,
  SignInCredentials,
  SignInOptions,
} from './api/client'
// The bearer mode of a native app: the token storage contract and its default.
export { createMemoryTokenStorage } from './api/tokenStorage'
export type { TokenStorage } from './api/tokenStorage'
export { ApiError, ErrorCode, isApiError } from './api/errors'
export type { ApiErrorPayload, ErrorCodeValue } from './api/errors'
export type { Enrolment, LoginResult, Passkey, SignupResult } from './api/account'

export {
  isBearerClient,
  isPasskeySupported,
  PasskeyError,
  usePasskey,
} from './composables/usePasskey'
export type {
  PasskeyErrorCode,
  PasskeyLoginOptions,
  PasskeyOptions,
  PasskeyState,
  PasskeyStatus,
} from './composables/usePasskey'

export { useToasts } from './composables/useToasts'
export type { Toast, ToastInput, ToastKind, ToastState } from './composables/useToasts'

export { useForm } from './composables/useForm'
export type { FieldErrors, FormOptions, FormState, Touched } from './composables/useForm'

// The language layer: the plugin, the composables and the catalogue registry.
export { createGovueI18n, languageKey, pickLocale, useGovueText, useLanguage } from './i18n/plugin'
export type {
  ExistingI18n,
  GovueI18n,
  GovueI18nOptions,
  LanguageState,
  NamedValues,
} from './i18n/plugin'
export { catalogues, DEFAULT_LOCALE, isLocaleCode, localeCodes } from './i18n/catalogues'
export type { LocaleCode } from './i18n/catalogues'
export type { MessageTree } from './i18n/messages'
export {
  hreflangLinks,
  localeFromPath,
  localizedPath,
  localizeRoutes,
  sitemapXml,
  validateRoutes,
} from './i18n/routes'
export type { LocalizedRoute, RouteDef } from './i18n/routes'
// The legal pages: paths, route records and footer links. No legal text.
export { LEGAL_PAGES, legalLinks, legalPath, legalRouteDefs, legalRoutes } from './i18n/legal'
export type { LegalLink, LegalPage, LegalPages, LegalRouteRecord } from './i18n/legal'
export { LANGUAGE_STORAGE_KEY } from './i18n/languageStorage'
export type { LanguageStorageOption } from './i18n/languageStorage'

export { default as GovueBadge } from './components/GovueBadge.vue'
export type { BadgeKind } from './components/GovueBadge.vue'
export { default as GovueButton } from './components/GovueButton.vue'
export { default as GovueCard } from './components/GovueCard.vue'
export type { CardAs, CardHeadingLevel } from './components/GovueCard.vue'
export type { HeadingLevel } from './components/headingLevel'
export { default as GovueCheckbox } from './components/GovueCheckbox.vue'
export { default as GovueConfirmDialog } from './components/GovueConfirmDialog.vue'
export { default as GovueDialog } from './components/GovueDialog.vue'
export { default as GovueEmptyState } from './components/GovueEmptyState.vue'
export { default as GovueErrorState } from './components/GovueErrorState.vue'
export { default as GovueField } from './components/GovueField.vue'
export { default as GovueFooter } from './components/GovueFooter.vue'
export type { FooterLink } from './components/GovueFooter.vue'
export { default as GovueHeader } from './components/GovueHeader.vue'
export { default as GovueInput } from './components/GovueInput.vue'
export { default as GovueLanguageSwitch } from './components/GovueLanguageSwitch.vue'
export { default as GovueLegalPage } from './components/GovueLegalPage.vue'
export { default as GovueLoginForm } from './components/GovueLoginForm.vue'
export { default as GovueNav } from './components/GovueNav.vue'
export type { NavItem } from './components/GovueNav.vue'
export { default as GovuePasskeyButton } from './components/GovuePasskeyButton.vue'
export { default as GovuePage } from './components/GovuePage.vue'
export type { PageWidth } from './components/GovuePage.vue'
export { default as GovueSelect } from './components/GovueSelect.vue'
export { default as GovueSignupForm } from './components/GovueSignupForm.vue'
export { default as GovueSkeleton } from './components/GovueSkeleton.vue'
export type { SkeletonWidth } from './components/GovueSkeleton.vue'
export { default as GovueSkipLink } from './components/GovueSkipLink.vue'
export { default as GovueSwitch } from './components/GovueSwitch.vue'
export { default as GovueTextarea } from './components/GovueTextarea.vue'
export { default as GovueThemeSwitch } from './components/GovueThemeSwitch.vue'
export { THEME_STORAGE_KEY, THEME_ATTRIBUTE, MODE_ATTRIBUTE } from './composables/themeStorage'
export { default as GovueToastHost } from './components/GovueToastHost.vue'
export { default as GovueTotpEnrolment } from './components/GovueTotpEnrolment.vue'
