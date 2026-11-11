/**
 * The public surface of @ostendorf-cloud/web-kit.
 *
 * The stylesheets are not imported here: an app imports the base stylesheet and
 * at most one theme itself, so it decides the order and nothing is pulled in
 * that it did not ask for.
 *
 * No component exists yet. This foundation is the token contract, the theme
 * files, the composable that switches between them and the API client; the
 * Wk* components are built on it one at a time.
 */

export { themeNames, useTheme } from './composables/useTheme'
export type { ColorMode, ThemeName, ThemeOptions, ThemeState } from './composables/useTheme'

// The API client, which is the only place an app calls fetch.
export { createClient } from './api/client'
export type { ApiClient, ClientOptions, RequestOptions } from './api/client'
export { ApiError, ErrorCode, isApiError } from './api/errors'
export type { ApiErrorPayload, ErrorCodeValue } from './api/errors'
