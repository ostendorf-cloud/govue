// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/*
 * The shared parts of a privacy policy: data protection at a glance, hosting,
 * general notes and mandatory information, and the rights of the data subject.
 *
 * Origin: the wording is carried over from the privacy policy of ostendorf.tech.
 * Its owner is the provider of every site built on govue, and the provider
 * allows its reuse here, see NOTICE.md. The German text is binding and the
 * English text is a translation.
 *
 * These texts are not legal advice. Every site needs its own legal review.
 */

import { p, section, tx, ul } from './helpers'
import type { LegalSection, LocalizedText } from './types'

/** Part 1, without the sentences that belong to one site. */
export const glanceSections: LegalSection[] = [
  section(
    'glance-general',
    ['Allgemeine Hinweise', 'General notes'],
    [
      p(
        'Die folgenden Hinweise geben einen einfachen Überblick darüber, was mit Ihren personenbezogenen Daten passiert, wenn Sie diese Website besuchen. Personenbezogene Daten sind alle Daten, mit denen Sie persönlich identifiziert werden können. Ausführliche Informationen zum Thema Datenschutz entnehmen Sie unserer unter diesem Text aufgeführten Datenschutzerklärung.',
        'The following notes give a simple overview of what happens to your personal data when you visit this website. Personal data is any data that can be used to identify you personally. For detailed information on data protection, please see our privacy policy set out below this text.',
      ),
    ],
  ),
  section(
    'glance-collection',
    ['Datenerfassung auf dieser Website', 'Data collection on this website'],
    [],
  ),
  section(
    'glance-responsible',
    [
      'Wer ist verantwortlich für die Datenerfassung auf dieser Website?',
      'Who is responsible for data collection on this website?',
    ],
    [
      p(
        'Die Datenverarbeitung auf dieser Website erfolgt durch den Websitebetreiber. Dessen Kontaktdaten können Sie dem Abschnitt „Hinweis zur verantwortlichen Stelle“ in dieser Datenschutzerklärung entnehmen.',
        'Data processing on this website is carried out by the website operator. You can find the operator\'s contact details in the section "Information about the controller" in this privacy policy.',
      ),
    ],
    4,
  ),
  section(
    'glance-how',
    ['Wie erfassen wir Ihre Daten?', 'How do we collect your data?'],
    [
      p(
        'Ihre Daten werden zum einen dadurch erhoben, dass Sie uns diese mitteilen. Hierbei kann es sich z. B. um Daten handeln, die Sie in ein Formular eingeben.',
        'Your data is collected, firstly, through information you provide to us. This may, for example, be data you enter into a form.',
      ),
      p(
        'Andere Daten werden automatisch beim Besuch der Website durch unsere IT-Systeme erfasst. Das sind vor allem technische Daten (z. B. Internetbrowser, Betriebssystem oder Uhrzeit des Seitenaufrufs). Die Erfassung dieser Daten erfolgt automatisch, sobald Sie diese Website betreten.',
        'Other data is collected automatically by our IT systems when you visit the website. This is mainly technical data (for example, your internet browser, operating system or the time of the page request). This data is collected automatically as soon as you access this website.',
      ),
    ],
    4,
  ),
  section(
    'glance-purpose',
    ['Wofür nutzen wir Ihre Daten?', 'What do we use your data for?'],
    [
      p(
        'Ein Teil der Daten wird erhoben, um eine fehlerfreie Bereitstellung der Website zu gewährleisten. Sofern über die Website Verträge geschlossen oder angebahnt werden können, werden die übermittelten Daten auch für Vertragsangebote, Bestellungen oder sonstige Auftragsanfragen verarbeitet.',
        'Part of the data is collected to ensure the website is provided without errors. To the extent contracts can be concluded or initiated through the website, the data transmitted is also processed for contract offers, orders or other service enquiries.',
      ),
    ],
    4,
  ),
  section(
    'glance-rights',
    [
      'Welche Rechte haben Sie bezüglich Ihrer Daten?',
      'What rights do you have regarding your data?',
    ],
    [
      p(
        'Sie haben jederzeit das Recht, unentgeltlich Auskunft über Herkunft, Empfänger und Zweck Ihrer gespeicherten personenbezogenen Daten zu erhalten. Sie haben außerdem ein Recht, die Berichtigung oder Löschung dieser Daten zu verlangen. Wenn Sie eine Einwilligung zur Datenverarbeitung erteilt haben, können Sie diese Einwilligung jederzeit für die Zukunft widerrufen. Außerdem haben Sie das Recht, unter bestimmten Umständen die Einschränkung der Verarbeitung Ihrer personenbezogenen Daten zu verlangen. Des Weiteren steht Ihnen ein Beschwerderecht bei der zuständigen Aufsichtsbehörde zu.',
        'You have the right at any time to obtain information, free of charge, about the origin, recipients and purpose of your stored personal data. You also have the right to demand that this data be corrected or deleted. If you have given consent to data processing, you can withdraw that consent at any time for the future. You also have the right, under certain circumstances, to demand that the processing of your personal data be restricted. Furthermore, you have the right to lodge a complaint with the competent supervisory authority.',
      ),
      p(
        'Hierzu sowie zu weiteren Fragen zum Thema Datenschutz können Sie sich jederzeit an uns wenden.',
        'You are welcome to contact us at any time regarding this and any further questions about data protection.',
      ),
    ],
    4,
  ),
]

/** Part 2. */
export const hostingSections: LegalSection[] = [
  section(
    'hosting-own',
    ['Hosting auf eigenen Servern', 'Hosting on our own servers'],
    [
      p(
        'Wir hosten die Inhalte unserer Website selbst. Die Website läuft auf eigenen Servern, die wir in unseren eigenen Räumlichkeiten in Deutschland betreiben. Einen externen Hosting-Anbieter setzen wir für die Website nicht ein; die beim Besuch der Website anfallenden Daten werden auf diesen Servern verarbeitet.',
        'We host the content of our website ourselves. The website runs on our own servers, which we operate on our own premises in Germany. We do not use an external hosting provider for the website; the data generated when the website is visited is processed on these servers.',
      ),
      p(
        'Der Betrieb erfolgt auf Grundlage von Art. 6 Abs. 1 lit. f DSGVO. Wir haben ein berechtigtes Interesse an einer möglichst zuverlässigen und datensparsamen Darstellung unserer Website.',
        'This is carried out on the basis of Art. 6(1)(f) GDPR. We have a legitimate interest in presenting our website as reliably and economically, in terms of data, as possible.',
      ),
    ],
  ),
]

/** Part 3 up to the controller notice, which the page builds from the provider. */
export const generalBeforeControllerSections: LegalSection[] = [
  section(
    'general-protection',
    ['Datenschutz', 'Data protection'],
    [
      p(
        'Die Betreiber dieser Seiten nehmen den Schutz Ihrer persönlichen Daten sehr ernst. Wir behandeln Ihre personenbezogenen Daten vertraulich und entsprechend den gesetzlichen Datenschutzvorschriften sowie dieser Datenschutzerklärung.',
        'The operators of these pages take the protection of your personal data very seriously. We treat your personal data as confidential and in accordance with the statutory data protection regulations and this privacy policy.',
      ),
      p(
        'Wenn Sie diese Website benutzen, werden verschiedene personenbezogene Daten erhoben. Personenbezogene Daten sind Daten, mit denen Sie persönlich identifiziert werden können. Die vorliegende Datenschutzerklärung erläutert, welche Daten wir erheben und wofür wir sie nutzen. Sie erläutert auch, wie und zu welchem Zweck das geschieht.',
        'When you use this website, various personal data is collected. Personal data is any data that can be used to identify you personally. This privacy policy explains what data we collect and what we use it for. It also explains how and for what purpose this happens.',
      ),
      p(
        'Wir weisen darauf hin, dass die Datenübertragung im Internet (z. B. bei der Kommunikation per E-Mail) Sicherheitslücken aufweisen kann. Ein lückenloser Schutz der Daten vor dem Zugriff durch Dritte ist nicht möglich.',
        'We point out that data transmission over the internet (for example, when communicating by email) can have security gaps. Complete protection of data against access by third parties is not possible.',
      ),
    ],
  ),
]

/** The sentence that closes the controller notice. */
export const controllerDefinition: LocalizedText = tx(
  'Verantwortliche Stelle ist die natürliche oder juristische Person, die allein oder gemeinsam mit anderen über die Zwecke und Mittel der Verarbeitung von personenbezogenen Daten (z. B. Namen, E-Mail-Adressen o. Ä.) entscheidet.',
  'The controller is the natural or legal person who alone, or jointly with others, determines the purposes and means of the processing of personal data (such as names, email addresses, and the like).',
)

/** Part 3 after the controller notice, and the rights of the data subject. */
export const generalAfterControllerSections: LegalSection[] = [
  section(
    'general-storage',
    ['Speicherdauer', 'Storage period'],
    [
      p(
        'Soweit innerhalb dieser Datenschutzerklärung keine speziellere Speicherdauer genannt wurde, verbleiben Ihre personenbezogenen Daten bei uns, bis der Zweck für die Datenverarbeitung entfällt. Wenn Sie ein berechtigtes Löschersuchen geltend machen oder eine Einwilligung zur Datenverarbeitung widerrufen, werden Ihre Daten gelöscht, sofern wir keine anderen rechtlich zulässigen Gründe für die Speicherung Ihrer personenbezogenen Daten haben (z. B. steuer- oder handelsrechtliche Aufbewahrungsfristen); im letztgenannten Fall erfolgt die Löschung nach Fortfall dieser Gründe.',
        'Unless a more specific storage period is stated within this privacy policy, your personal data will remain with us until the purpose for the data processing no longer applies. If you assert a legitimate request for deletion or withdraw your consent to data processing, your data will be deleted, unless we have other legally permissible grounds for storing your personal data (for example, retention periods under tax or commercial law); in the latter case, deletion will take place once those grounds cease to apply.',
      ),
    ],
  ),
  section(
    'general-legal-grounds',
    [
      'Allgemeine Hinweise zu den Rechtsgrundlagen der Datenverarbeitung auf dieser Website',
      'General notes on the legal grounds for data processing on this website',
    ],
    [
      p(
        'Sofern Sie in die Datenverarbeitung eingewilligt haben, verarbeiten wir Ihre personenbezogenen Daten auf Grundlage von Art. 6 Abs. 1 lit. a DSGVO bzw. Art. 9 Abs. 2 lit. a DSGVO, sofern besondere Datenkategorien nach Art. 9 Abs. 1 DSGVO verarbeitet werden. Im Falle einer ausdrücklichen Einwilligung in die Übertragung personenbezogener Daten in Drittstaaten erfolgt die Datenverarbeitung außerdem auf Grundlage von Art. 49 Abs. 1 lit. a DSGVO. Sofern Sie in die Speicherung von Cookies oder in den Zugriff auf Informationen in Ihr Endgerät (z. B. via Device-Fingerprinting) eingewilligt haben, erfolgt die Datenverarbeitung zusätzlich auf Grundlage von § 25 Abs. 1 TDDDG. Die Einwilligung ist jederzeit widerrufbar. Sind Ihre Daten zur Vertragserfüllung oder zur Durchführung vorvertraglicher Maßnahmen erforderlich, verarbeiten wir Ihre Daten auf Grundlage des Art. 6 Abs. 1 lit. b DSGVO. Des Weiteren verarbeiten wir Ihre Daten, sofern diese zur Erfüllung einer rechtlichen Verpflichtung erforderlich sind auf Grundlage von Art. 6 Abs. 1 lit. c DSGVO. Die Datenverarbeitung kann ferner auf Grundlage unseres berechtigten Interesses nach Art. 6 Abs. 1 lit. f DSGVO erfolgen. Über die jeweils im Einzelfall einschlägigen Rechtsgrundlagen wird in den folgenden Absätzen dieser Datenschutzerklärung informiert.',
        'Where you have given consent to data processing, we process your personal data on the basis of Art. 6(1)(a) GDPR, or Art. 9(2)(a) GDPR where special categories of data under Art. 9(1) GDPR are processed. Where you have given explicit consent to the transfer of personal data to third countries, data processing is additionally carried out on the basis of Art. 49(1)(a) GDPR. Where you have consented to the storage of cookies or to access to information on your device (for example, via device fingerprinting), data processing is additionally carried out on the basis of Section 25(1) TDDDG (the German Telecommunications-Digital-Services-Data-Protection Act). Consent can be withdrawn at any time. Where your data is required for the performance of a contract or to carry out pre-contractual measures, we process your data on the basis of Art. 6(1)(b) GDPR. We further process your data where this is necessary to comply with a legal obligation, on the basis of Art. 6(1)(c) GDPR. Data processing may also be carried out on the basis of our legitimate interest under Art. 6(1)(f) GDPR. The legal grounds relevant in each individual case are set out in the following paragraphs of this privacy policy.',
      ),
    ],
  ),
  section(
    'general-recipients',
    ['Empfänger von personenbezogenen Daten', 'Recipients of personal data'],
    [
      p(
        'Im Rahmen unserer Geschäftstätigkeit arbeiten wir mit verschiedenen externen Stellen zusammen. Dabei ist teilweise auch eine Übermittlung von personenbezogenen Daten an diese externen Stellen erforderlich. Wir geben personenbezogene Daten nur dann an externe Stellen weiter, wenn dies im Rahmen einer Vertragserfüllung erforderlich ist, wenn wir gesetzlich hierzu verpflichtet sind (z. B. Weitergabe von Daten an Steuerbehörden), wenn wir ein berechtigtes Interesse nach Art. 6 Abs. 1 lit. f DSGVO an der Weitergabe haben oder wenn eine sonstige Rechtsgrundlage die Datenweitergabe erlaubt. Beim Einsatz von Auftragsverarbeitern geben wir personenbezogene Daten unserer Kunden nur auf Grundlage eines gültigen Vertrags über Auftragsverarbeitung weiter. Im Falle einer gemeinsamen Verarbeitung wird ein Vertrag über gemeinsame Verarbeitung geschlossen.',
        "In the course of our business activities, we work together with various external parties. This sometimes requires the transfer of personal data to these external parties. We only pass on personal data to external parties where this is necessary for the performance of a contract, where we are legally obliged to do so (for example, passing data to tax authorities), where we have a legitimate interest under Art. 6(1)(f) GDPR in doing so, or where some other legal basis permits the disclosure. Where we use processors, we only pass on our customers' personal data on the basis of a valid data processing agreement. In the case of joint processing, an agreement on joint controllership is concluded.",
      ),
    ],
  ),
  section(
    'general-withdrawal',
    [
      'Widerruf Ihrer Einwilligung zur Datenverarbeitung',
      'Withdrawing your consent to data processing',
    ],
    [
      p(
        'Viele Datenverarbeitungsvorgänge sind nur mit Ihrer ausdrücklichen Einwilligung möglich. Sie können eine bereits erteilte Einwilligung jederzeit widerrufen. Die Rechtmäßigkeit der bis zum Widerruf erfolgten Datenverarbeitung bleibt vom Widerruf unberührt.',
        'Many data processing operations are only possible with your express consent. You can withdraw consent you have already given at any time. The lawfulness of data processing carried out before the withdrawal remains unaffected by the withdrawal.',
      ),
    ],
  ),
  section(
    'rights-objection',
    [
      'Widerspruchsrecht gegen die Datenerhebung in besonderen Fällen sowie gegen Direktwerbung (Art. 21 DSGVO)',
      'Right to object to the collection of data in special cases, and to direct marketing (Art. 21 GDPR)',
    ],
    [
      p(
        'Wenn die Datenverarbeitung auf Grundlage von Art. 6 Abs. 1 lit. e oder f DSGVO erfolgt, haben Sie jederzeit das Recht, aus Gründen, die sich aus Ihrer besonderen Situation ergeben, gegen die Verarbeitung Ihrer personenbezogenen Daten Widerspruch einzulegen; dies gilt auch für ein auf diese Bestimmungen gestütztes Profiling. Die jeweilige Rechtsgrundlage, auf denen eine Verarbeitung beruht, entnehmen Sie dieser Datenschutzerklärung. Wenn Sie Widerspruch einlegen, werden wir Ihre betroffenen personenbezogenen Daten nicht mehr verarbeiten, es sei denn, wir können zwingende schutzwürdige Gründe für die Verarbeitung nachweisen, die Ihre Interessen, Rechte und Freiheiten überwiegen oder die Verarbeitung dient der Geltendmachung, Ausübung oder Verteidigung von Rechtsansprüchen (Widerspruch nach Art. 21 Abs. 1 DSGVO).',
        'Where data processing is based on Art. 6(1)(e) or (f) GDPR, you have the right to object at any time, on grounds relating to your particular situation, to the processing of your personal data; this also applies to profiling based on these provisions. The legal basis on which any processing is based can be found in this privacy policy. If you object, we will no longer process your personal data concerned, unless we can demonstrate compelling legitimate grounds for the processing which override your interests, rights and freedoms, or the processing serves the establishment, exercise or defence of legal claims (objection under Art. 21(1) GDPR).',
        true,
      ),
      p(
        'Werden Ihre personenbezogenen Daten verarbeitet, um Direktwerbung zu betreiben, so haben Sie das Recht, jederzeit Widerspruch gegen die Verarbeitung Sie betreffender personenbezogenen Daten zum Zwecke derartiger Werbung einzulegen; dies gilt auch für das Profiling, soweit es mit solcher Direktwerbung in Verbindung steht. Wenn Sie widersprechen, werden Ihre personenbezogenen Daten anschließend nicht mehr zum Zwecke der Direktwerbung verwendet (Widerspruch nach Art. 21 Abs. 2 DSGVO).',
        'Where your personal data is processed for the purposes of direct marketing, you have the right to object at any time to the processing of personal data concerning you for the purposes of such marketing; this also applies to profiling to the extent it is related to such direct marketing. If you object, your personal data will subsequently no longer be used for the purposes of direct marketing (objection under Art. 21(2) GDPR).',
        true,
      ),
    ],
  ),
  section(
    'rights-complaint',
    [
      'Beschwerderecht bei der zuständigen Aufsichtsbehörde',
      'Right to lodge a complaint with the competent supervisory authority',
    ],
    [
      p(
        'Im Falle von Verstößen gegen die DSGVO steht den Betroffenen ein Beschwerderecht bei einer Aufsichtsbehörde, insbesondere in dem Mitgliedstaat ihres gewöhnlichen Aufenthalts, ihres Arbeitsplatzes oder des Orts des mutmaßlichen Verstoßes zu. Das Beschwerderecht besteht unbeschadet anderweitiger verwaltungsrechtlicher oder gerichtlicher Rechtsbehelfe.',
        'In the event of breaches of the GDPR, data subjects have the right to lodge a complaint with a supervisory authority, in particular in the member state of their habitual residence, place of work, or the place of the alleged breach. This right to complain exists without prejudice to any other administrative or judicial remedy.',
      ),
    ],
  ),
  section(
    'rights-portability',
    ['Recht auf Datenübertragbarkeit', 'Right to data portability'],
    [
      p(
        'Sie haben das Recht, Daten, die wir auf Grundlage Ihrer Einwilligung oder in Erfüllung eines Vertrags automatisiert verarbeiten, an sich oder an einen Dritten in einem gängigen, maschinenlesbaren Format aushändigen zu lassen. Sofern Sie die direkte Übertragung der Daten an einen anderen Verantwortlichen verlangen, erfolgt dies nur, soweit es technisch machbar ist.',
        'You have the right to have data that we process automatically on the basis of your consent or in performance of a contract handed over to you or to a third party in a commonly used, machine-readable format. Where you request the direct transfer of the data to another controller, this will only be done to the extent it is technically feasible.',
      ),
    ],
  ),
  section(
    'rights-access',
    ['Auskunft, Berichtigung und Löschung', 'Access, rectification and erasure'],
    [
      p(
        'Sie haben im Rahmen der geltenden gesetzlichen Bestimmungen jederzeit das Recht auf unentgeltliche Auskunft über Ihre gespeicherten personenbezogenen Daten, deren Herkunft und Empfänger und den Zweck der Datenverarbeitung und ggf. ein Recht auf Berichtigung oder Löschung dieser Daten. Hierzu sowie zu weiteren Fragen zum Thema personenbezogene Daten können Sie sich jederzeit an uns wenden.',
        'Within the scope of the applicable statutory provisions, you have the right at any time to free information about your stored personal data, its origin and recipients, and the purpose of the data processing, and, where applicable, a right to have this data corrected or deleted. You are welcome to contact us at any time regarding this and any further questions about personal data.',
      ),
    ],
  ),
  section(
    'rights-restriction',
    ['Recht auf Einschränkung der Verarbeitung', 'Right to restriction of processing'],
    [
      p(
        'Sie haben das Recht, die Einschränkung der Verarbeitung Ihrer personenbezogenen Daten zu verlangen. Hierzu können Sie sich jederzeit an uns wenden. Das Recht auf Einschränkung der Verarbeitung besteht in folgenden Fällen:',
        'You have the right to request the restriction of the processing of your personal data. You are welcome to contact us at any time for this purpose. The right to restriction of processing exists in the following cases:',
      ),
      ul(
        [
          'Wenn Sie die Richtigkeit Ihrer bei uns gespeicherten personenbezogenen Daten bestreiten, benötigen wir in der Regel Zeit, um dies zu überprüfen. Für die Dauer der Prüfung haben Sie das Recht, die Einschränkung der Verarbeitung Ihrer personenbezogenen Daten zu verlangen.',
          'If you dispute the accuracy of your personal data stored with us, we generally need time to verify this. For the duration of the review, you have the right to demand the restriction of the processing of your personal data.',
        ],
        [
          'Wenn die Verarbeitung Ihrer personenbezogenen Daten unrechtmäßig geschah/geschieht, können Sie statt der Löschung die Einschränkung der Datenverarbeitung verlangen.',
          'If the processing of your personal data was or is unlawful, you may demand the restriction of the data processing instead of its erasure.',
        ],
        [
          'Wenn wir Ihre personenbezogenen Daten nicht mehr benötigen, Sie sie jedoch zur Ausübung, Verteidigung oder Geltendmachung von Rechtsansprüchen benötigen, haben Sie das Recht, statt der Löschung die Einschränkung der Verarbeitung Ihrer personenbezogenen Daten zu verlangen.',
          'If we no longer need your personal data, but you need it to exercise, defend or assert legal claims, you have the right to demand the restriction of the processing of your personal data instead of its erasure.',
        ],
        [
          'Wenn Sie einen Widerspruch nach Art. 21 Abs. 1 DSGVO eingelegt haben, muss eine Abwägung zwischen Ihren und unseren Interessen vorgenommen werden. Solange noch nicht feststeht, wessen Interessen überwiegen, haben Sie das Recht, die Einschränkung der Verarbeitung Ihrer personenbezogenen Daten zu verlangen.',
          'If you have lodged an objection under Art. 21(1) GDPR, a balancing of your interests and ours must take place. As long as it has not yet been determined whose interests prevail, you have the right to demand the restriction of the processing of your personal data.',
        ],
      ),
      p(
        'Wenn Sie die Verarbeitung Ihrer personenbezogenen Daten eingeschränkt haben, dürfen diese Daten – von ihrer Speicherung abgesehen – nur mit Ihrer Einwilligung oder zur Geltendmachung, Ausübung oder Verteidigung von Rechtsansprüchen oder zum Schutz der Rechte einer anderen natürlichen oder juristischen Person oder aus Gründen eines wichtigen öffentlichen Interesses der Europäischen Union oder eines Mitgliedstaats verarbeitet werden.',
        'If you have restricted the processing of your personal data, this data may, other than being stored, only be processed with your consent, or for the establishment, exercise or defence of legal claims, or for the protection of the rights of another natural or legal person, or for reasons of important public interest of the European Union or a member state.',
      ),
    ],
  ),
  section(
    'general-tls',
    ['SSL- bzw. TLS-Verschlüsselung', 'SSL or TLS encryption'],
    [
      p(
        'Diese Seite nutzt aus Sicherheitsgründen und zum Schutz der Übertragung vertraulicher Inhalte, wie zum Beispiel Bestellungen oder Anfragen, die Sie an uns als Seitenbetreiber senden, eine SSL- bzw. TLS-Verschlüsselung. Eine verschlüsselte Verbindung erkennen Sie daran, dass die Adresszeile des Browsers von „http://“ auf „https://“ wechselt und an dem Schloss-Symbol in Ihrer Browserzeile.',
        'For security reasons and to protect the transmission of confidential content, such as orders or enquiries you send to us as the site operator, this site uses SSL or TLS encryption. You can recognise an encrypted connection by the fact that the browser\'s address bar changes from "http://" to "https://" and by the lock symbol in your browser bar.',
      ),
      p(
        'Wenn die SSL- bzw. TLS-Verschlüsselung aktiviert ist, können die Daten, die Sie an uns übermitteln, nicht von Dritten mitgelesen werden.',
        'When SSL or TLS encryption is activated, the data you transmit to us cannot be read by third parties.',
      ),
    ],
  ),
  section(
    'general-advertising',
    ['Widerspruch gegen Werbe-E-Mails', 'Objection to promotional emails'],
    [
      p(
        'Der Nutzung von im Rahmen der Impressumspflicht veröffentlichten Kontaktdaten zur Übersendung von nicht ausdrücklich angeforderter Werbung und Informationsmaterialien wird hiermit widersprochen. Die Betreiber der Seiten behalten sich ausdrücklich rechtliche Schritte im Falle der unverlangten Zusendung von Werbeinformationen, etwa durch Spam-E-Mails, vor.',
        'We hereby object to the use of contact details published in the context of the obligation to provide an imprint for the purpose of sending unsolicited advertising and information material. The operators of these pages expressly reserve the right to take legal action in the event of the unsolicited sending of promotional information, for example through spam emails.',
      ),
    ],
  ),
]
