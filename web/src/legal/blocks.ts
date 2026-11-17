// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/*
 * Optional building blocks for the part "data collection on this website".
 * Each one is a section that states what it stores and for how long. A site
 * uses a block only when its own code does what the text says, and sets the
 * options to what its code does. Wording origin and limits: see privacyText.ts.
 */

import { p, tx } from './helpers'
import type { DataSection, LocalizedText } from './types'

/** Paragraph blocks of a block's own text, from stored items and retention. */
function build(
  id: string,
  heading: [string, string],
  intro: Array<[string, string]>,
  stores: LocalizedText[],
  storesLead: [string, string],
  retention: LocalizedText,
  outro: Array<[string, string]> = [],
): DataSection {
  return {
    id,
    level: 3,
    heading: tx(heading[0], heading[1]),
    stores,
    retention,
    blocks: [
      ...intro.map(([de, en]) => p(de, en)),
      p(storesLead[0], storesLead[1]),
      { kind: 'list', items: stores },
      { kind: 'p', text: retention },
      ...outro.map(([de, en]) => p(de, en)),
    ],
  }
}

export interface ServerLogsOptions {
  /** Days the log files are kept. Default 30. It must match the real rotation. */
  retentionDays?: number
}

/** Server log files, as the govue access log writes them. */
export function serverLogs(options: ServerLogsOptions = {}): DataSection {
  const days = options.retentionDays ?? 30
  return build(
    'server-logs',
    ['Server-Log-Dateien', 'Server log files'],
    [
      [
        'Bei jedem Aufruf dieser Website schreibt unser Webserver automatisch eine Protokollzeile.',
        'For every request to this website, our web server automatically writes one log line.',
      ],
    ],
    [
      tx('Zeitpunkt der Anfrage', 'the time of the request'),
      tx('IP-Adresse', 'the IP address'),
      tx(
        'eine zufällige, von unserem Server vergebene Anfrage-Kennung, mit der sich zusammengehörige Protokollzeilen einer einzelnen Anfrage zuordnen lassen',
        'a random request id our server assigns, so that log lines belonging to one request can be matched to each other',
      ),
      tx('Anfragemethode (z. B. GET oder POST)', 'the request method (for example, GET or POST)'),
      tx(
        'was angefragt wurde, als internes Muster der Adresse und nicht als vollständige aufgerufene Adresse mit allen enthaltenen Parametern',
        'what was requested, as an internal address pattern and never as the full address requested with all its parameters',
      ),
      tx(
        'HTTP-Statuscode und übertragene Datenmenge',
        'the HTTP status code and the amount of data transferred',
      ),
      tx('Bearbeitungsdauer der Anfrage', 'how long the request took to answer'),
    ],
    ['Darin festgehalten werden:', 'It records:'],
    tx(
      `Die Log-Dateien werden nur für einen begrenzten Zeitraum aufbewahrt und spätestens nach ${days} Tagen automatisch gelöscht. Log-Dateien, deren weitere Aufbewahrung zu Beweiszwecken erforderlich ist, sind bis zur endgültigen Klärung des jeweiligen Vorfalls von der Löschung ausgenommen.`,
      `The log files are retained for a limited period only and are automatically deleted after ${days} days at the latest. Log files whose further retention is required for evidentiary purposes are excluded from deletion until the incident in question has been finally clarified.`,
    ),
    [
      [
        'Nicht erfasst werden dabei der Browsertyp Ihres Geräts (User-Agent) und eine etwaige Referrer-URL. Eine Zusammenführung dieser Daten mit anderen Datenquellen wird nicht vorgenommen.',
        "Your device's browser type (user agent) and any referrer URL are not recorded as part of this. This data is not merged with other data sources.",
      ],
      [
        'Die Erfassung dieser Daten erfolgt auf Grundlage von Art. 6 Abs. 1 lit. f DSGVO. Der Websitebetreiber hat ein berechtigtes Interesse an der technisch fehlerfreien Darstellung, der Sicherheit und der Optimierung seiner Website sowie an der Abwehr von Missbrauch – hierzu müssen die Server-Log-Files erfasst werden.',
        'This data is collected on the basis of Art. 6(1)(f) GDPR. The website operator has a legitimate interest in the technically error-free presentation, security and optimisation of its website, and in defending against misuse; the server log files must be collected for this purpose.',
      ],
    ],
  )
}

export interface SessionCookieOptions {
  /**
   * When the cookie is set, as a fragment that follows "erst dann," in German and
   * "only once" in English. The German one starts with "wenn", the English one with
   * "you". Default: the visitor uses the contact form.
   */
  trigger?: LocalizedText
}

/** One technical session cookie, as the govue session and csrf packages set it. */
export function sessionCookie(options: SessionCookieOptions = {}): DataSection {
  const trigger =
    options.trigger ??
    tx(
      'wenn Sie das Kontaktformular benutzen. Es wird gesetzt, sobald Sie ein Feld des Formulars auswählen',
      'you use the contact form. It is set as soon as you select a field of the form',
    )
  return build(
    'session-cookie',
    ['Cookies', 'Cookies'],
    [
      [
        `Diese Website setzt ein einziges Cookie: ein technisches Sitzungs-Cookie, und zwar erst dann, ${trigger.de}. Wenn Sie nur lesen und das Formular nicht benutzen, erhalten Sie kein Cookie. Es dient dazu, unserem Server zu ermöglichen, das Formular vor Cross-Site-Request-Forgery-Angriffen (CSRF) zu schützen; es wird nicht zu Tracking, Analyse oder Werbung verwendet und nicht an Dritte übermittelt.`,
        `This website sets a single cookie: a technical session cookie, and only once ${trigger.en}. If you only read the pages and do not use the form, you receive no cookie at all. It exists to let our server protect the form against cross-site request forgery (CSRF) attacks; it is not used for tracking, analytics or advertising, and is not transmitted to third parties.`,
      ],
    ],
    [
      tx(
        'eine zufällige, nicht auf Sie persönlich zurückführbare Kennung',
        'a random identifier that cannot be traced back to you personally',
      ),
    ],
    ['Das Cookie enthält ausschließlich:', 'The cookie contains only:'],
    tx(
      'Das Cookie ist ein Sitzungs-Cookie ohne eigenes Speicherdauer-Attribut: Ihr Browser verwirft es, sobald Sie ihn schließen. Zusätzlich ist es so gesetzt, dass kein Skript auf der Seite es auslesen kann (HttpOnly), dass es nur über eine verschlüsselte Verbindung übertragen wird (Secure) und dass es nicht an eine andere Website mitgesendet wird (SameSite=Lax).',
      'The cookie is a session cookie with no storage-duration attribute of its own: your browser discards it once you close it. It is also set so that no script on the page can read it (HttpOnly), so that it is only transmitted over an encrypted connection (Secure), and so that it is not sent along to another website (SameSite=Lax).',
    ),
    [
      [
        'Nach unserer Einschätzung ist dieses Cookie technisch notwendig im Sinne von § 25 Abs. 2 Nr. 2 TDDDG, weil ohne ein solches Sitzungs-Cookie das Formular nicht wirksam vor Cross-Site-Request-Forgery geschützt werden kann; eine Einwilligung ist deshalb nicht erforderlich, und wir zeigen kein Cookie-Banner an. Darüber hinaus speichern wir keine weiteren Informationen in Ihrem Endgerät, greifen nicht auf dort gespeicherte Informationen zu und setzen auch kein Device-Fingerprinting im Browser ein.',
        'In our assessment, this cookie is technically necessary within the meaning of Section 25(2) no. 2 TDDDG, because the form cannot be effectively protected against cross-site request forgery without a session cookie of this kind; consent is therefore not required, and we do not display a cookie banner. Beyond this, we do not store any further information on your device, do not access information stored there, and do not use device fingerprinting in the browser either.',
      ],
    ],
  )
}

export interface ContactFormOptions {
  /**
   * One sentence about where the mail server runs, when a third party hosts it.
   * Left out by default, so the text names no third party.
   */
  mailHostNote?: LocalizedText
  /** The form is protected by the session cookie block. Default false. */
  withSessionCookie?: boolean
}

/** A contact form that mails the message and protects itself with proof of work. */
export function contactForm(options: ContactFormOptions = {}): DataSection {
  const host = options.mailHostNote
  const cookie = options.withSessionCookie
    ? tx(
        ' Das technische Sitzungs-Cookie (siehe Abschnitt „Cookies“) schützt dabei auch dieses Formular vor Cross-Site-Request-Forgery.',
        ' The technical session cookie (see the "Cookies" section) also protects this form against cross-site request forgery.',
      )
    : tx('', '')
  return build(
    'contact-form',
    ['Kontaktformular', 'Contact form'],
    [
      [
        'Wenn Sie uns per Kontaktformular Anfragen zukommen lassen, werden Ihre Angaben aus dem Anfrageformular inklusive der von Ihnen dort angegebenen Kontaktdaten zwecks Bearbeitung der Anfrage und für den Fall von Anschlussfragen bei uns gespeichert. Diese Daten geben wir nicht ohne Ihre Einwilligung weiter.',
        'If you send us enquiries using the contact form, your details from the enquiry form, including the contact details you provide there, are stored with us for the purpose of processing the enquiry and in case of follow-up questions. We do not pass on this data without your consent.',
      ],
      [
        `Ihre Angaben werden von unserem Webserver nicht in einer Datenbank gespeichert, sondern als E-Mail über unseren eigenen Mailserver an unser Postfach gesendet.${host ? ` ${host.de}` : ''} Die Übertragung vom Webserver zum Mailserver erfolgt verschlüsselt.`,
        `Your details are not stored by our web server in a database, but sent as an email via our own mail server to our mailbox.${host ? ` ${host.en}` : ''} Transmission from the web server to the mail server is encrypted.`,
      ],
      [
        `Zum Schutz vor Spam löst Ihr Browser beim Ausfüllen des Formulars eine kleine Rechenaufgabe (sogenannter Proof of Work). Dazu ruft er bei unserem Server eine zufällige Aufgabe ab und sendet die Lösung zusammen mit dem Formular zurück. Dabei werden selbst keine weiteren Cookies gesetzt, keine weiteren Informationen in Ihrem Endgerät gespeichert und keine Dienste von Drittanbietern eingebunden.${cookie.de} Um Missbrauch zu verhindern, verarbeitet unser Server außerdem Ihre IP-Adresse kurzzeitig im Arbeitsspeicher, um die Zahl der Anfragen je Adresse zu begrenzen; dafür wird sie nicht dauerhaft gespeichert. Rechtsgrundlage hierfür ist unser berechtigtes Interesse am Schutz vor Spam und Missbrauch (Art. 6 Abs. 1 lit. f DSGVO).`,
        `To protect against spam, your browser solves a small computational task (a so-called proof of work) when you fill in the form. To do this, it requests a random task from our server and sends the solution back together with the form. This itself sets no further cookies, stores no further information on your device, and uses no third-party services for this purpose.${cookie.en} To prevent misuse, our server also processes your IP address briefly in memory in order to limit the number of requests per address; it is not stored permanently for this purpose. The legal basis for this is our legitimate interest in protection against spam and misuse (Art. 6(1)(f) GDPR).`,
      ],
      [
        'Die Verarbeitung dieser Daten erfolgt auf Grundlage von Art. 6 Abs. 1 lit. b DSGVO, sofern Ihre Anfrage mit der Erfüllung eines Vertrags zusammenhängt oder zur Durchführung vorvertraglicher Maßnahmen erforderlich ist. In allen übrigen Fällen beruht die Verarbeitung auf unserem berechtigten Interesse an der effektiven Bearbeitung der an uns gerichteten Anfragen (Art. 6 Abs. 1 lit. f DSGVO) oder auf Ihrer Einwilligung (Art. 6 Abs. 1 lit. a DSGVO) sofern diese abgefragt wurde; die Einwilligung ist jederzeit widerrufbar.',
        'Processing of this data is carried out on the basis of Art. 6(1)(b) GDPR, where your enquiry relates to the performance of a contract or is necessary to carry out pre-contractual measures. In all other cases, processing is based on our legitimate interest in the effective handling of enquiries addressed to us (Art. 6(1)(f) GDPR), or on your consent (Art. 6(1)(a) GDPR) where this has been requested; consent can be withdrawn at any time.',
      ],
    ],
    [
      tx('Name', 'name'),
      tx('E-Mail-Adresse', 'email address'),
      tx('Nachricht', 'message'),
      tx('IP-Adresse, kurzzeitig im Arbeitsspeicher', 'IP address, briefly in memory'),
    ],
    ['Gespeichert werden:', 'What is stored:'],
    tx(
      'Die von Ihnen im Kontaktformular eingegebenen Daten verbleiben bei uns, bis Sie uns zur Löschung auffordern, Ihre Einwilligung zur Speicherung widerrufen oder der Zweck für die Datenspeicherung entfällt (z. B. nach abgeschlossener Bearbeitung Ihrer Anfrage). Zwingende gesetzliche Bestimmungen – insbesondere Aufbewahrungsfristen – bleiben unberührt.',
      'The data you enter in the contact form remains with us until you ask us to delete it, withdraw your consent to its storage, or the purpose for storing the data no longer applies (for example, after your enquiry has been fully processed). Mandatory statutory provisions, in particular retention periods, remain unaffected.',
    ),
  )
}

export interface UmamiOptions {
  /** Months the statistics are kept. Default 14. It must match Umami's setting. */
  retentionMonths?: number
  /** Days a backup copy keeps the data beyond that. Default 15. */
  backupDays?: number
  /**
   * One sentence naming where backups are stored, when a third party holds them.
   * Left out by default.
   */
  backupNote?: LocalizedText
}

/** Visitor statistics with a self hosted Umami, reported by the server. */
export function umami(options: UmamiOptions = {}): DataSection {
  const months = options.retentionMonths ?? 14
  const days = options.backupDays ?? 15
  const note = options.backupNote
  return build(
    'umami',
    ['Besucherstatistik mit Umami', 'Visitor statistics with Umami'],
    [
      [
        `Um zu verstehen, welche Inhalte unserer Website gelesen werden und wie Besucher zu uns finden, erstellen wir eine Besucherstatistik mit der Open-Source-Software Umami. Umami betreiben wir selbst auf unseren eigenen Servern.${note ? ` ${note.de}` : ' Die Daten werden nicht an Dritte übermittelt.'}`,
        `To understand which content on our website is read and how visitors find us, we compile visitor statistics using the open-source software Umami. We operate Umami ourselves on our own servers.${note ? ` ${note.en}` : ' The data is not transmitted to third parties.'}`,
      ],
      [
        'Umami läuft nicht in Ihrem Browser: Es wird kein Skript geladen, kein Cookie gesetzt und nichts auf Ihrem Endgerät gespeichert oder ausgelesen. Stattdessen meldet unser Webserver jeden Aufruf einer Seite an Umami, mit den Angaben, die Ihr Browser beim Aufruf ohnehin an uns übermittelt.',
        'Umami does not run in your browser: no script is loaded, no cookie is set, and nothing is stored on or read from your device. Instead, our web server reports every page request to Umami, using the information your browser transmits to us in any case when making the request.',
      ],
      [
        'Aus der IP-Adresse ermittelt Umami Land, Region und Stadt. Aus IP-Adresse, User-Agent, einem geheimen Schlüssel und dem laufenden Monat bildet Umami eine pseudonyme Kennung, um die Seitenaufrufe eines Besuchs zusammenzufassen; die Kennung wechselt deshalb jeden Monat. Die IP-Adresse und den vollständigen User-Agent speichert Umami nicht, sondern nur die daraus abgeleiteten Angaben (Land, Region, Stadt, Browser, Betriebssystem) zusammen mit der pseudonymen Kennung. Eine Zusammenführung mit anderen Daten oder eine Identifizierung einzelner Besucher findet nicht statt.',
        'Umami derives the country, region and city from the IP address. From the IP address, the user agent, a secret key and the current month, Umami forms a pseudonymous identifier to group together the page views of one visit; the identifier therefore changes every month. Umami does not store the IP address or the full user agent, only the information derived from them (country, region, city, browser, operating system) together with the pseudonymous identifier. No merging with other data, and no identification of individual visitors, takes place.',
      ],
    ],
    [
      tx(
        'aufgerufene Seite (ohne angehängte Parameter) und ihr Titel',
        'the page requested (without any attached parameters) and its title',
      ),
      tx(
        'Referrer URL ohne Parameter, sofern Sie von einer anderen Website kommen und Ihr Browser sie übermittelt',
        'the referrer URL without parameters, where you come from another website and your browser transmits it',
      ),
      tx('bevorzugte Sprache Ihres Browsers', "your browser's preferred language"),
      tx(
        'Browsertyp und Browserversion sowie verwendetes Betriebssystem (User-Agent)',
        'browser type and version, and operating system used (user agent)',
      ),
      tx('IP-Adresse', 'IP address'),
    ],
    ['Gemeldet werden:', 'The following is reported:'],
    tx(
      `Die Statistikdaten werden nach ${months} Monaten automatisch gelöscht. In den Sicherungskopien der Datenbank bleiben sie danach in der Regel noch bis zu ${days} Tage erhalten.`,
      `The statistics data is automatically deleted after ${months} months. In the backup copies of the database, it typically remains for up to a further ${days} days afterwards.`,
    ),
    [
      [
        'Nicht erfasst werden Aufrufe, bei denen Ihr Browser das Signal „Do Not Track“ (DNT) oder „Global Privacy Control“ (GPC) sendet, sowie Aufrufe, die Ihr Browser nur vorab lädt.',
        'Requests where your browser sends the "Do Not Track" (DNT) or "Global Privacy Control" (GPC) signal are not recorded, nor are requests your browser merely preloads.',
      ],
      [
        'Die Verarbeitung erfolgt auf Grundlage von Art. 6 Abs. 1 lit. f DSGVO. Wir haben ein berechtigtes Interesse daran, die Nutzung unserer Website statistisch auszuwerten, um unser Angebot zu verbessern, ohne dabei Daten unserer Besucher an Dritte weiterzugeben. Sie können der Verarbeitung jederzeit widersprechen, indem Sie in Ihrem Browser „Do Not Track“ oder „Global Privacy Control“ aktivieren, oder indem Sie uns kontaktieren.',
        'This processing is carried out on the basis of Art. 6(1)(f) GDPR. We have a legitimate interest in statistically evaluating the use of our website in order to improve our offering, without passing on our visitors\' data to third parties in doing so. You can object to this processing at any time by activating "Do Not Track" or "Global Privacy Control" in your browser, or by contacting us.',
      ],
    ],
  )
}

export interface RateLimitOptions {
  /** The contact form has limits of its own, described in its block. Default false. */
  withContactForm?: boolean
}

/** Rate limiting per IP address, held in memory only. */
export function rateLimit(options: RateLimitOptions = {}): DataSection {
  const form = options.withContactForm
  return build(
    'rate-limit',
    ['Ratenbegrenzung', 'Rate limiting'],
    [
      [
        'Um unsere Website vor Überlastung und Missbrauch zu schützen, begrenzen wir, wie viele Anfragen eine einzelne IP-Adresse innerhalb kurzer Zeit an uns stellen kann. Dazu zählt unser Server für jede Adresse, wie viele Anfragen kurz zuvor eingegangen sind; stellt eine Adresse zu viele Anfragen, werden weitere Anfragen von ihr kurzzeitig abgewiesen.',
        'To protect our website against overload and misuse, we limit how many requests a single IP address can make to us within a short period of time. To do this, our server counts, for each address, how many requests it has recently received; where an address makes too many requests, further requests from it are briefly refused.',
      ],
    ],
    [
      tx(
        'IP-Adresse mit der Zahl ihrer jüngsten Anfragen',
        'IP address with its recent request count',
      ),
    ],
    ['Gespeichert wird:', 'What is stored:'],
    tx(
      'Dieser Zähler existiert ausschließlich im Arbeitsspeicher unseres Servers. Er wird nicht auf Festplatte geschrieben und nicht an Dritte übermittelt. Sobald eine Adresse eine Zeit lang keine weiteren Anfragen mehr stellt, verwirft unser Server den zugehörigen Zähler automatisch; eine darüberhinausgehende Aufbewahrung findet nicht statt.',
      "This counter exists only in our server's memory. It is not written to disk and not transmitted to third parties. Once an address has made no further requests for a while, our server automatically discards the associated counter; no further retention takes place.",
    ),
    [
      ...(form
        ? ([
            [
              'Für die Schritte des Kontaktformulars gelten stattdessen eigene Begrenzungen, die im Abschnitt „Kontaktformular“ beschrieben sind. Alle werden getrennt gezählt.',
              'The steps of the contact form have limits of their own instead, described in the "Contact form" section. All are counted separately.',
            ],
          ] as Array<[string, string]>)
        : []),
      [
        'Die Verarbeitung erfolgt auf Grundlage von Art. 6 Abs. 1 lit. f DSGVO. Wir haben ein berechtigtes Interesse daran, unsere Website vor Überlastung und missbräuchlichen Zugriffen zu schützen.',
        'This processing is carried out on the basis of Art. 6(1)(f) GDPR. We have a legitimate interest in protecting our website against overload and abusive access.',
      ],
    ],
  )
}
