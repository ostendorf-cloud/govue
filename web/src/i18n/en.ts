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
    legal: {
      bindingNotice:
        'The German version is legally binding. This English version is a translation.',
      viewGerman: 'View the German version',
      imprint: {
        title: 'Imprint',
        contact: 'Contact',
        phone: 'Phone:',
        email: 'Email:',
        vatHeading: 'VAT ID',
        vatText: 'VAT identification number per Section 27a of the German VAT Act: {vatId}',
        disputeHeading: 'Consumer dispute resolution',
      },
      privacy: {
        title: 'Privacy policy',
        glance: 'Data protection at a glance',
        hosting: 'Hosting',
        general: 'General notes and mandatory information',
        collection: 'Data collection on this website',
        controller: 'Information about the controller',
        controllerIntro: 'The controller responsible for data processing on this website is:',
      },
    },
  },
}

export default en
