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
    passkey: {
      login: { label: 'Mit Passkey anmelden' },
      enrol: { label: 'Passkey hinzufügen' },
      busy: 'Warte auf deinen Passkey',
      cancelled: 'Die Passkey-Abfrage wurde geschlossen. Es hat sich nichts geändert.',
      error: {
        reauthentication_required: 'Bitte melde dich erneut an und versuche es noch einmal.',
        invalid_passkey: 'Der Passkey konnte nicht hinzugefügt werden. Bitte versuche es erneut.',
        invalid_credentials: 'Der Passkey konnte nicht geprüft werden.',
        last_factor:
          'Das ist deine letzte Möglichkeit zur Anmeldung, sie kann nicht entfernt werden.',
        not_found: 'Dieser Passkey existiert nicht mehr.',
        unsupported: 'Dieser Browser unterstützt keine Passkeys.',
        unauthenticated: 'Du bist nicht angemeldet. Bitte melde dich an und versuche es erneut.',
        invalid_argument:
          'Die Anfrage war nicht gültig. Bitte prüfe deine Eingabe und versuche es erneut.',
        permission_denied:
          'Die Anfrage wurde abgelehnt. Bitte lade die Seite neu und versuche es erneut.',
        rate_limited: 'Zu viele Versuche. Bitte warte einen Moment und versuche es erneut.',
        unavailable: 'Der Server ist ausgelastet. Bitte versuche es gleich noch einmal.',
        network: 'Keine Verbindung zum Server. Bitte prüfe dein Netzwerk und versuche es erneut.',
        already_registered: 'Dieses Gerät hat bereits einen Passkey für dein Konto.',
        busy: 'Eine andere Passkey-Abfrage ist offen.',
        bearer_unsupported: 'Passkeys sind in dieser App nicht verfügbar.',
        failed: 'Etwas ist schiefgegangen. Bitte versuche es erneut.',
      },
    },
    account: {
      field: {
        login: 'Login',
        password: 'Passwort',
        code: 'Code aus deiner Authenticator-App',
        codeHint: 'Sechs Ziffern. Ein Code gilt nur einmal.',
        passwordHint: 'Mindestens {min} Zeichen.',
      },
      login: {
        title: 'Anmelden',
        submit: 'Anmelden',
      },
      signup: {
        title: 'Konto erstellen',
        submit: 'Konto erstellen',
      },
      totp: {
        title: 'Authenticator-App einrichten',
        intro:
          'Füge dieses Konto mit dem Geheimnis oder dem Link unten in eine Authenticator-App ein und gib dann den Code ein, den die App anzeigt.',
        secret: 'Geheimnis',
        uri: 'Einrichtungslink',
        openApp: 'In Authenticator-App öffnen',
        submit: 'Code bestätigen',
        lost: 'Dieses Geheimnis wird nur einmal angezeigt. Bewahre es auf, bis du einen Code bestätigt hast.',
      },
      error: {
        required: 'Bitte fülle dieses Feld aus.',
        invalidCredentials: 'Login, Passwort oder Code sind nicht korrekt.',
        busy: 'Der Server ist ausgelastet. Bitte versuche es gleich noch einmal.',
        rateLimited: 'Zu viele Versuche. Bitte warte einen Moment und versuche es erneut.',
        taken: 'Dieser Login ist bereits vergeben.',
        loginInvalid: 'Dieser Login ist nicht gültig.',
        passwordTooShort: 'Das Passwort braucht mindestens {min} Zeichen.',
        passwordTooLong: 'Das Passwort ist zu lang.',
        crossOrigin:
          'Die Anfrage wurde abgelehnt. Bitte lade die Seite neu und versuche es erneut.',
        signupRefused:
          'Die Registrierung ist nicht offen, oder die Anfrage wurde abgelehnt. Bitte lade die Seite neu und versuche es erneut.',
        network: 'Keine Verbindung zum Server. Bitte prüfe dein Netzwerk und versuche es erneut.',
        generic: 'Etwas ist schiefgegangen. Bitte versuche es erneut.',
      },
    },
  },
}

export default de
