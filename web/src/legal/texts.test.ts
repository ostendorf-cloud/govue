// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { localeCodes } from '../i18n/catalogues'
import {
  contactForm,
  rateLimit,
  serverLogs,
  sessionCookie,
  umami,
  type ContactFormOptions,
} from './blocks'
import { tx } from './helpers'
import {
  controllerDefinition,
  generalAfterControllerSections,
  generalBeforeControllerSections,
  glanceSections,
  hostingSections,
} from './privacyText'
import type { DataSection, LegalSection, LocalizedText } from './types'

const shared: LegalSection[] = [
  ...glanceSections,
  ...hostingSections,
  ...generalBeforeControllerSections,
  ...generalAfterControllerSections,
]

const blocks: Array<[string, DataSection]> = [
  ['serverLogs', serverLogs()],
  ['sessionCookie', sessionCookie()],
  ['contactForm', contactForm()],
  ['umami', umami()],
  ['rateLimit', rateLimit()],
]

function texts(sections: LegalSection[]): LocalizedText[] {
  return sections.flatMap((s) => [
    s.heading,
    ...s.blocks.flatMap((b) => (b.kind === 'p' ? [b.text] : b.items)),
  ])
}

describe('the shared privacy texts', () => {
  it('has a German and an English text for every heading, paragraph and list item', () => {
    for (const text of [...texts(shared), controllerDefinition]) {
      for (const code of localeCodes) {
        expect(text[code]?.trim(), JSON.stringify(text).slice(0, 80)).toBeTruthy()
      }
    }
  })

  it('has an English text that is not a copy of the German one', () => {
    for (const text of texts(shared)) {
      expect(text.en).not.toBe(text.de)
    }
  })

  it('has unique anchors across the shared parts and the blocks', () => {
    const ids = [...shared, ...blocks.map(([, b]) => b)].map((s) => s.id)

    expect(new Set(ids).size).toBe(ids.length)
  })

  it('covers the four shared parts the issue names', () => {
    const ids = shared.map((s) => s.id)

    expect(ids).toEqual(
      expect.arrayContaining([
        'glance-general',
        'hosting-own',
        'general-legal-grounds',
        'rights-objection',
        'rights-complaint',
        'rights-portability',
        'rights-access',
        'rights-restriction',
      ]),
    )
  })

  it('sets the Art. 21 objection notices in capitals', () => {
    const objection = shared.find((s) => s.id === 'rights-objection')

    expect(objection?.blocks.every((b) => b.kind === 'p' && b.upper)).toBe(true)
  })
})

describe('the optional blocks', () => {
  it.each(blocks)('%s states what it stores, in both languages', (_name, block) => {
    expect(block.stores.length).toBeGreaterThan(0)
    for (const item of block.stores) {
      for (const code of localeCodes) {
        expect(item[code].trim()).not.toBe('')
      }
    }
  })

  it.each(blocks)('%s states for how long, in both languages', (_name, block) => {
    for (const code of localeCodes) {
      expect(block.retention[code].trim()).not.toBe('')
    }
  })

  it.each(blocks)(
    '%s renders its stored items and its retention into the page text',
    (_n, block) => {
      for (const item of block.stores) {
        expect(block.blocks.some((b) => b.kind === 'list' && b.items.includes(item))).toBe(true)
      }
      expect(block.blocks.some((b) => b.kind === 'p' && b.text === block.retention)).toBe(true)
    },
  )

  it.each(blocks)('%s names no third party by default', (_name, block) => {
    const all = JSON.stringify(block)

    expect(all).not.toMatch(/Hetzner|Meta Platforms|Instagram/)
  })

  it('takes the log retention from the option', () => {
    expect(serverLogs({ retentionDays: 7 }).retention.de).toContain('nach 7 Tagen')
    expect(serverLogs({ retentionDays: 7 }).retention.en).toContain('after 7 days')
    expect(serverLogs().retention.en).toContain('after 30 days')
  })

  it('takes the statistics retention and the backup period from the options', () => {
    const block = umami({ retentionMonths: 6, backupDays: 3 })

    expect(block.retention.en).toContain('after 6 months')
    expect(block.retention.en).toContain('further 3 days')
    expect(umami().retention.en).toContain('after 14 months')
  })

  it('adds a backup note only when the site gives one', () => {
    const note = tx('Backups liegen bei Example Backup.', 'Backups are held by Example Backup.')
    const withNote = JSON.stringify(umami({ backupNote: note }).blocks)

    expect(withNote).toContain('Example Backup')
    expect(JSON.stringify(umami().blocks)).not.toContain('Example Backup')
  })

  it('adds a mail host note only when the site gives one', () => {
    const options: ContactFormOptions = {
      mailHostNote: tx('Der Server steht bei Example Host.', 'The server runs at Example Host.'),
    }

    expect(JSON.stringify(contactForm(options).blocks)).toContain('Example Host')
    expect(JSON.stringify(contactForm().blocks)).not.toContain('Example Host')
  })

  it('refers to the session cookie only when the site uses that block', () => {
    expect(JSON.stringify(contactForm({ withSessionCookie: true }).blocks)).toContain(
      'Sitzungs-Cookie',
    )
    expect(JSON.stringify(contactForm().blocks)).not.toContain('Sitzungs-Cookie')
  })

  it('refers to the contact form limits only when the site uses that block', () => {
    expect(JSON.stringify(rateLimit({ withContactForm: true }).blocks)).toContain('Contact form')
    expect(JSON.stringify(rateLimit().blocks)).not.toContain('Contact form')
  })

  it('takes the cookie trigger from the option', () => {
    const block = sessionCookie({
      trigger: tx('wenn Sie sich anmelden', 'you sign in'),
    })

    expect(JSON.stringify(block.blocks)).toContain('only once you sign in')
  })
})
