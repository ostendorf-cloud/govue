// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { MessageTree } from './messages'

/**
 * govue's own visitor-facing strings, German in the formal register: the
 * visitor is addressed as "Sie". It has exactly the keys of de.ts, and a text
 * that addresses nobody is the same as there. It is not a language of its
 * own: createGovueI18n({ formality: 'formal' }) merges it into the locale de,
 * so the language switch, lang and the routes stay de.
 */
const deFormal: MessageTree = {
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
      compact: {
        system: 'Dem System folgen',
        light: 'Heller Modus',
        dark: 'Dunkler Modus',
      },
      // Addresses nobody, so it is the text of de.ts.
      cycle: '{label}: {current}. Als Nächstes: {next}',
    },
    language: {
      label: 'Sprache',
    },
    passkey: {
      login: { label: 'Mit Passkey anmelden' },
      enrol: { label: 'Passkey hinzufügen' },
      busy: 'Warten auf Ihren Passkey',
      cancelled: 'Die Passkey-Abfrage wurde geschlossen. Es hat sich nichts geändert.',
      error: {
        reauthentication_required:
          'Bitte melden Sie sich erneut an und versuchen Sie es noch einmal.',
        invalid_passkey:
          'Der Passkey konnte nicht hinzugefügt werden. Bitte versuchen Sie es erneut.',
        invalid_credentials: 'Der Passkey konnte nicht geprüft werden.',
        last_factor:
          'Das ist Ihre letzte Möglichkeit zur Anmeldung, sie kann nicht entfernt werden.',
        not_found: 'Dieser Passkey existiert nicht mehr.',
        unsupported: 'Dieser Browser unterstützt keine Passkeys.',
        unauthenticated:
          'Sie sind nicht angemeldet. Bitte melden Sie sich an und versuchen Sie es erneut.',
        invalid_argument:
          'Die Anfrage war nicht gültig. Bitte prüfen Sie Ihre Eingabe und versuchen Sie es erneut.',
        permission_denied:
          'Die Anfrage wurde abgelehnt. Bitte laden Sie die Seite neu und versuchen Sie es erneut.',
        rate_limited:
          'Zu viele Versuche. Bitte warten Sie einen Moment und versuchen Sie es erneut.',
        unavailable: 'Der Server ist ausgelastet. Bitte versuchen Sie es gleich noch einmal.',
        network:
          'Keine Verbindung zum Server. Bitte prüfen Sie Ihr Netzwerk und versuchen Sie es erneut.',
        already_registered: 'Dieses Gerät hat bereits einen Passkey für Ihr Konto.',
        busy: 'Eine andere Passkey-Abfrage ist offen.',
        bearer_unsupported: 'Passkeys sind in dieser App nicht verfügbar.',
        failed: 'Etwas ist schiefgegangen. Bitte versuchen Sie es erneut.',
      },
    },
    legal: {
      link: {
        imprint: 'Impressum',
        privacy: 'Datenschutz',
        terms: 'AGB',
      },
      title: {
        imprint: 'Impressum',
        privacy: 'Datenschutzerklärung',
        terms: 'Allgemeine Geschäftsbedingungen',
      },
      back: 'Zurück zur Startseite',
    },
    account: {
      field: {
        login: 'Login',
        password: 'Passwort',
        code: 'Code aus Ihrer Authenticator-App',
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
          'Fügen Sie dieses Konto mit dem Geheimnis oder dem Link unten in eine Authenticator-App ein und geben Sie dann den Code ein, den die App anzeigt.',
        secret: 'Geheimnis',
        uri: 'Einrichtungslink',
        openApp: 'In Authenticator-App öffnen',
        submit: 'Code bestätigen',
        lost: 'Dieses Geheimnis wird nur einmal angezeigt. Bewahren Sie es auf, bis Sie einen Code bestätigt haben.',
      },
      error: {
        required: 'Bitte füllen Sie dieses Feld aus.',
        invalidCredentials: 'Login, Passwort oder Code sind nicht korrekt.',
        busy: 'Der Server ist ausgelastet. Bitte versuchen Sie es gleich noch einmal.',
        rateLimited:
          'Zu viele Versuche. Bitte warten Sie einen Moment und versuchen Sie es erneut.',
        taken: 'Dieser Login ist bereits vergeben.',
        loginInvalid: 'Dieser Login ist nicht gültig.',
        passwordTooShort: 'Das Passwort braucht mindestens {min} Zeichen.',
        passwordTooLong: 'Das Passwort ist zu lang.',
        crossOrigin:
          'Die Anfrage wurde abgelehnt. Bitte laden Sie die Seite neu und versuchen Sie es erneut.',
        signupRefused:
          'Die Registrierung ist nicht offen, oder die Anfrage wurde abgelehnt. Bitte laden Sie die Seite neu und versuchen Sie es erneut.',
        network:
          'Keine Verbindung zum Server. Bitte prüfen Sie Ihr Netzwerk und versuchen Sie es erneut.',
        generic: 'Etwas ist schiefgegangen. Bitte versuchen Sie es erneut.',
      },
    },
    contact: {
      title: 'Kontakt',
      field: {
        name: 'Name',
        email: 'E-Mail-Adresse',
        message: 'Nachricht',
        honeypot: 'Dieses Feld bitte leer lassen',
      },
      wait: {
        minute: 'in etwa einer Minute',
        minutes: 'in etwa {n} Minuten',
        hour: 'in etwa einer Stunde',
        hours: 'in etwa {n} Stunden',
      },
      privacy: 'Datenschutzerklärung',
      privacyIntro: 'So gehen wir mit Ihren Angaben um:',
      submit: 'Nachricht senden',
      sending: 'Ihre Nachricht wird gesendet',
      success: 'Danke. Ihre Nachricht wurde gesendet.',
      another: 'Weitere Nachricht schreiben',
      failure: 'Das Senden ist fehlgeschlagen.',
      unavailable: 'Das Kontaktformular ist nicht verfügbar.',
      error: {
        required: 'Bitte füllen Sie dieses Feld aus.',
        tooLong: 'Das ist zu lang. Erlaubt sind höchstens {max} Zeichen.',
        invalidChars: 'Das enthält Zeichen, die nicht erlaubt sind.',
        nonAsciiEmail:
          'Bitte nutzen Sie eine E-Mail-Adresse ohne Umlaute und andere Sonderzeichen, etwa ä, ö, ü, ß oder é.',
        invalid: 'Das ist nicht gültig.',
        consentRequired: 'Bitte stimmen Sie zu, bevor Sie senden.',
        invalidRequest:
          'Das Formular konnte nicht angenommen werden. Bitte prüfen Sie Ihre Eingaben und versuchen Sie es erneut.',
        notConfigured:
          'Das Kontaktformular ist noch nicht eingerichtet. Bitte versuchen Sie es später erneut.',
        powRequired:
          'Die Spam-Prüfung fehlt. Es wurde nichts gesendet. Bitte senden Sie das Formular erneut.',
        powInvalid:
          'Die Spam-Prüfung ist fehlgeschlagen. Es wurde nichts gesendet. Bitte senden Sie das Formular erneut.',
        powExpired:
          'Die Spam-Prüfung ist abgelaufen. Es wurde nichts gesendet. Bitte senden Sie das Formular erneut.',
        powTooFast:
          'Das Formular wurde zu schnell gesendet. Bitte warten Sie einen Moment und senden Sie es erneut.',
        powReplay:
          'Die Spam-Prüfung wurde schon verwendet. Es wurde nichts gesendet. Bitte senden Sie das Formular erneut.',
        powBusy:
          'Der Server ist gerade ausgelastet. Es wurde nichts gesendet. Bitte versuchen Sie es gleich erneut.',
        powBusyWait:
          'Der Server ist gerade ausgelastet. Es wurde nichts gesendet. Bitte versuchen Sie es {wait} erneut.',
        mailLimit:
          'Das Formular hat sein Limit an Nachrichten vorerst erreicht. Es wurde nichts gesendet. Bitte versuchen Sie es später erneut.',
        mailLimitWait:
          'Das Formular hat sein Limit an Nachrichten vorerst erreicht. Es wurde nichts gesendet. Bitte versuchen Sie es {wait} erneut.',
        sendFailed:
          'Ihre Nachricht wurde möglicherweise nicht gesendet. Bitte prüfen Sie das, bevor Sie sie erneut senden, oder schreiben Sie auf einem anderen Weg.',
        rateLimited:
          'Zu viele Nachrichten. Bitte warten Sie ein paar Minuten und versuchen Sie es erneut.',
        rateLimitedWait: 'Zu viele Nachrichten. Bitte versuchen Sie es {wait} erneut.',
        tooLarge: 'Die Nachricht ist zu groß zum Senden.',
        refused:
          'Die Anfrage wurde abgelehnt. Bitte laden Sie die Seite neu und versuchen Sie es erneut.',
        solver:
          'Die Spam-Prüfung konnte in diesem Browser nicht laufen. Es wurde nichts gesendet. Bitte laden Sie die Seite neu oder nutzen Sie einen anderen Browser.',
        network:
          'Keine Verbindung zum Server. Bitte prüfen Sie Ihr Netzwerk und versuchen Sie es erneut.',
        generic: 'Etwas ist schiefgegangen. Bitte versuchen Sie es erneut.',
      },
    },
  },
}

export default deFormal
