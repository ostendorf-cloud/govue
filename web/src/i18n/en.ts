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
  },
}

export default en
