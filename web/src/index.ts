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
export type { ApiClient, ClientOptions, RequestOptions } from './api/client'
export { ApiError, ErrorCode, isApiError } from './api/errors'
export type { ApiErrorPayload, ErrorCodeValue } from './api/errors'

export { useToasts } from './composables/useToasts'
export type { Toast, ToastInput, ToastKind, ToastState } from './composables/useToasts'

export { useForm } from './composables/useForm'
export type { FieldErrors, FormOptions, FormState, Touched } from './composables/useForm'

export { default as GovueBadge } from './components/GovueBadge.vue'
export type { BadgeKind } from './components/GovueBadge.vue'
export { default as GovueButton } from './components/GovueButton.vue'
export { default as GovueCard } from './components/GovueCard.vue'
export type { CardAs } from './components/GovueCard.vue'
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
export { default as GovueNav } from './components/GovueNav.vue'
export type { NavItem } from './components/GovueNav.vue'
export { default as GovuePage } from './components/GovuePage.vue'
export type { PageWidth } from './components/GovuePage.vue'
export { default as GovueSelect } from './components/GovueSelect.vue'
export { default as GovueSkeleton } from './components/GovueSkeleton.vue'
export type { SkeletonWidth } from './components/GovueSkeleton.vue'
export { default as GovueSkipLink } from './components/GovueSkipLink.vue'
export { default as GovueSwitch } from './components/GovueSwitch.vue'
export { default as GovueTextarea } from './components/GovueTextarea.vue'
export { default as GovueToastHost } from './components/GovueToastHost.vue'
