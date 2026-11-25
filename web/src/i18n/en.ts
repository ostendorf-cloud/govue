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
      compact: {
        system: 'Follow the system',
        light: 'Light mode',
        dark: 'Dark mode',
      },
      // The name of the cycle button: the group label, the current mode and
      // the one a press moves to, each a compact name.
      cycle: '{label}: {current}. Next: {next}',
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
        invalid_argument: 'The request was not valid. Please check your input and try again.',
        permission_denied: 'The request was refused. Please reload the page and try again.',
        rate_limited: 'Too many attempts. Please wait a moment and try again.',
        unavailable: 'The server is busy. Please try again in a moment.',
        network: 'No connection to the server. Please check your network and try again.',
        already_registered: 'This device already has a passkey for your account.',
        busy: 'Another passkey prompt is open.',
        bearer_unsupported: 'Passkeys are not available in this app.',
        failed: 'Something went wrong. Please try again.',
      },
    },
    legal: {
      link: {
        imprint: 'Imprint',
        privacy: 'Privacy',
        terms: 'Terms',
      },
      title: {
        imprint: 'Imprint',
        privacy: 'Privacy policy',
        terms: 'Terms and conditions',
      },
      back: 'Back to the start page',
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
    contact: {
      title: 'Contact',
      field: {
        name: 'Name',
        email: 'Email address',
        message: 'Message',
        honeypot: 'Leave this field empty',
      },
      wait: {
        minute: 'in about a minute',
        minutes: 'in about {n} minutes',
        hour: 'in about an hour',
        hours: 'in about {n} hours',
      },
      privacy: 'Privacy policy',
      privacyIntro: 'How your details are handled:',
      submit: 'Send message',
      sending: 'Sending your message',
      success: 'Thank you. Your message was sent.',
      another: 'Write another message',
      failure: 'Sending failed.',
      unavailable: 'The contact form is not available.',
      error: {
        required: 'Please fill in this field.',
        tooLong: 'This is too long. At most {max} characters are allowed.',
        invalidChars: 'This contains characters that are not allowed.',
        nonAsciiEmail:
          'Please use an email address without umlauts or other special characters, such as ä, ö, ü, ß or é.',
        invalid: 'This is not valid.',
        consentRequired: 'Please agree before sending.',
        invalidRequest: 'The form could not be accepted. Please check your input and try again.',
        notConfigured: 'The contact form is not set up yet. Please try again later.',
        powRequired: 'The spam check is missing. Nothing was sent. Please send the form again.',
        powInvalid: 'The spam check failed. Nothing was sent. Please send the form again.',
        powExpired: 'The spam check ran out of time. Nothing was sent. Please send the form again.',
        powTooFast: 'The form was sent too fast. Please wait a moment and send it again.',
        powReplay: 'The spam check was already used. Nothing was sent. Please send the form again.',
        powBusy: 'The server is busy right now. Nothing was sent. Please try again in a moment.',
        powBusyWait: 'The server is busy right now. Nothing was sent. Please try again {wait}.',
        mailLimit:
          'The form has reached its limit of messages for now. Nothing was sent. Please try again later.',
        mailLimitWait:
          'The form has reached its limit of messages for now. Nothing was sent. Please try again {wait}.',
        sendFailed:
          'Your message may not have been sent. Please check before you send it again or write by another way.',
        rateLimited: 'Too many messages. Please wait a few minutes and try again.',
        rateLimitedWait: 'Too many messages. Please try again {wait}.',
        tooLarge: 'The message is too large to send.',
        refused: 'The request was refused. Please reload the page and try again.',
        solver:
          'The spam check could not run in this browser. Nothing was sent. Please reload the page or try another browser.',
        network: 'No connection to the server. Please check your network and try again.',
        generic: 'Something went wrong. Please try again.',
      },
    },
    waitlist: {
      title: 'Waitlist',
      field: {
        email: 'Email address',
        honeypot: 'Leave this field empty',
      },
      privacy: 'Privacy policy',
      privacyIntro: 'How your details are handled:',
      submit: 'Join the waitlist',
      solving: 'Running the spam check in your browser. This takes a few seconds.',
      sending: 'Sending your request',
      success:
        'Almost done. Please check your inbox and confirm your address with the link in the mail. An address that is already on the list gets no new mail.',
      another: 'Use another address',
      failure: 'The request failed.',
      unavailable: 'The waitlist form is not available.',
      noscript:
        'This form needs JavaScript, because it runs a spam check in your browser. Please turn on JavaScript to join the waitlist.',
      unsupported:
        'This browser cannot run the spam check this form needs. Please use a current browser to join the waitlist.',
      error: {
        required: 'Please enter your email address.',
        tooLong: 'This is too long. At most {max} characters are allowed.',
        invalidChars: 'This contains characters that are not allowed.',
        nonAsciiEmail:
          'Please use an email address without umlauts or other special characters, such as ä, ö, ü, ß or é.',
        invalid: 'This is not a valid email address.',
        consentRequired: 'Please agree before you join.',
        invalidRequest: 'The form could not be accepted. Please check your input and try again.',
        notConfigured: 'The waitlist is not set up yet. Please try again later.',
        powRequired: 'The spam check is missing. Nothing was sent. Please send the form again.',
        powInvalid: 'The spam check failed. Nothing was sent. Please send the form again.',
        powExpired: 'The spam check ran out of time. Nothing was sent. Please send the form again.',
        powTooFast: 'The form was sent too fast. Please wait a moment and send it again.',
        powReplay: 'The spam check was already used. Nothing was sent. Please send the form again.',
        powBusy: 'The server is busy right now. Nothing was sent. Please try again in a moment.',
        powBusyWait: 'The server is busy right now. Nothing was sent. Please try again {wait}.',
        mailLimit:
          'The waitlist has reached its limit of mails for now. Nothing was sent. Please try again later.',
        mailLimitWait:
          'The waitlist has reached its limit of mails for now. Nothing was sent. Please try again {wait}.',
        rateLimited: 'Too many requests. Please wait a few minutes and try again.',
        rateLimitedWait: 'Too many requests. Please try again {wait}.',
        tooLarge: 'The request is too large to send.',
        refused: 'The request was refused. Please reload the page and try again.',
        solver:
          'The spam check could not run in this browser. Nothing was sent. Please reload the page or try another browser.',
        network: 'No connection to the server. Please check your network and try again.',
        generic: 'Something went wrong. Please try again.',
      },
    },
  },
}

export default en
