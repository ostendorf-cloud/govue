/**
 * The public surface of @ostendorf-cloud/web-kit.
 *
 * The stylesheets are not imported here: an app imports the base stylesheet and
 * at most one theme itself, so it decides the order and nothing is pulled in
 * that it did not ask for.
 *
 * This foundation is the token contract, the theme files, the theming composable,
 * the API client, the overlay components, and the form input components: a field
 * wrapper, the text, select, checkbox, switch and textarea controls, a button, and
 * the useForm composable that ties them to a submit. Layout components are a later
 * issue.
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

export { default as WkButton } from './components/WkButton.vue'
export { default as WkCheckbox } from './components/WkCheckbox.vue'
export { default as WkConfirmDialog } from './components/WkConfirmDialog.vue'
export { default as WkDialog } from './components/WkDialog.vue'
export { default as WkEmptyState } from './components/WkEmptyState.vue'
export { default as WkErrorState } from './components/WkErrorState.vue'
export { default as WkField } from './components/WkField.vue'
export { default as WkInput } from './components/WkInput.vue'
export { default as WkSelect } from './components/WkSelect.vue'
export { default as WkSwitch } from './components/WkSwitch.vue'
export { default as WkTextarea } from './components/WkTextarea.vue'
export { default as WkToastHost } from './components/WkToastHost.vue'
