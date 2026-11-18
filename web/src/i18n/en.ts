// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { MessageTree } from './messages'

/** govue's own visitor-facing strings, English. Every key lives under govue. */
const en: MessageTree = {
  govue: {
    skipLink: 'Skip to content',
    button: {
      loading: 'Working',
    },
    confirm: {
      confirm: 'Delete',
      cancel: 'Cancel',
    },
    empty: {
      title: 'Nothing here yet',
      description: 'There are no entries to show.',
    },
    error: {
      title: 'Something went wrong',
      message: 'The request failed. Please try again.',
      retry: 'Try again',
      requestId: 'Request id',
    },
    field: {
      required: '(required)',
    },
    footer: {
      label: 'Footer',
    },
    nav: {
      label: 'Main',
      menu: 'Menu',
    },
    toast: {
      success: 'Success',
      info: 'Info',
      error: 'Error',
      dismiss: 'Dismiss',
    },
    theme: {
      label: 'Theme',
      system: 'System',
      light: 'Light',
      dark: 'Dark',
    },
    language: {
      label: 'Language',
    },
    passkey: {
      login: { label: 'Log in with a passkey' },
      enrol: { label: 'Add a passkey' },
      busy: 'Waiting for your passkey',
      cancelled: 'The passkey prompt was closed. Nothing changed.',
      error: {
        reauthentication_required: 'Please log in again, then try once more.',
        invalid_passkey: 'The passkey could not be added. Please try again.',
        invalid_credentials: 'The passkey could not be verified.',
        last_factor: 'This is your last way to log in, so it cannot be removed.',
        not_found: 'This passkey no longer exists.',
        unsupported: 'This browser does not support passkeys.',
        unauthenticated: 'You are not logged in. Please log in and try again.',
        invalid_argument: 'The name is not valid.',
        permission_denied: 'The request was refused. Please reload the page and try again.',
        rate_limited: 'Too many attempts. Please wait a moment and try again.',
        unavailable: 'The server is busy. Please try again in a moment.',
        network: 'No connection to the server. Please check your network and try again.',
        already_registered: 'This device already has a passkey for your account.',
        busy: 'Another passkey prompt is open.',
        failed: 'Something went wrong. Please try again.',
      },
    },
    account: {
      field: {
        login: 'Login',
        password: 'Password',
        code: 'Code from your authenticator app',
        codeHint: 'Six digits. A code works once.',
        passwordHint: 'At least {min} characters.',
      },
      login: {
        title: 'Log in',
        submit: 'Log in',
      },
      signup: {
        title: 'Create an account',
        submit: 'Create account',
      },
      totp: {
        title: 'Set up your authenticator app',
        intro:
          'Add this account to an authenticator app with the secret or the link below, then enter the code the app shows.',
        secret: 'Secret',
        uri: 'Provisioning link',
        openApp: 'Open in authenticator app',
        submit: 'Confirm code',
        lost: 'This secret is shown only once. Keep it until you have confirmed a code.',
      },
      error: {
        required: 'Please fill in this field.',
        invalidCredentials: 'The login, the password or the code is not correct.',
        busy: 'The server is busy. Please try again in a moment.',
        rateLimited: 'Too many attempts. Please wait a moment and try again.',
        taken: 'This login is already taken.',
        loginInvalid: 'This login is not valid.',
        passwordTooShort: 'The password needs at least {min} characters.',
        passwordTooLong: 'The password is too long.',
        crossOrigin: 'The request was refused. Please reload the page and try again.',
        signupRefused:
          'Signup is not open, or the request was refused. Please reload the page and try again.',
        network: 'No connection to the server. Please check your network and try again.',
        generic: 'Something went wrong. Please try again.',
      },
    },
  },
}

export default en
