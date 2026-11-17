// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { MessageTree } from './messages'

/** govue's own visitor-facing strings, German. Every key lives under govue. */
const de: MessageTree = {
  govue: {
    skipLink: 'Zum Inhalt springen',
    button: {
      loading: 'Wird ausgeführt',
    },
    confirm: {
      confirm: 'Löschen',
      cancel: 'Abbrechen',
    },
    empty: {
      title: 'Noch nichts vorhanden',
      description: 'Hier gibt es noch keine Einträge.',
    },
    error: {
      title: 'Etwas ist schiefgegangen',
      message: 'Die Anfrage ist fehlgeschlagen. Bitte erneut versuchen.',
      retry: 'Erneut versuchen',
      requestId: 'Anfrage-ID',
    },
    field: {
      required: '(Pflichtfeld)',
    },
    footer: {
      label: 'Fußbereich',
    },
    nav: {
      label: 'Hauptmenü',
      menu: 'Menü',
    },
    toast: {
      success: 'Erfolg',
      info: 'Hinweis',
      error: 'Fehler',
      dismiss: 'Schließen',
    },
    theme: {
      label: 'Darstellung',
      system: 'System',
      light: 'Hell',
      dark: 'Dunkel',
    },
    language: {
      label: 'Sprache',
    },
    legal: {
      bindingNotice:
        'Die deutsche Fassung ist rechtlich verbindlich. Diese englische Fassung ist eine Übersetzung.',
      viewGerman: 'Deutsche Fassung ansehen',
      imprint: {
        title: 'Impressum',
        contact: 'Kontakt',
        phone: 'Telefon:',
        email: 'E-Mail:',
        vatHeading: 'Umsatzsteuer-ID',
        vatText: 'Umsatzsteuer-Identifikationsnummer gemäß §27a Umsatzsteuergesetz: {vatId}',
        disputeHeading: 'Verbraucherstreitbeilegung',
      },
      privacy: {
        title: 'Datenschutzerklärung',
        glance: 'Datenschutz auf einen Blick',
        hosting: 'Hosting',
        general: 'Allgemeine Hinweise und Pflichtinformationen',
        collection: 'Datenerfassung auf dieser Website',
        controller: 'Hinweis zur verantwortlichen Stelle',
        controllerIntro:
          'Die verantwortliche Stelle für die Datenverarbeitung auf dieser Website ist:',
      },
    },
  },
}

export default de
