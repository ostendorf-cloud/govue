// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import * as fs from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, beforeEach, expect, it, vi } from 'vitest'

// The second guard of --out-dir: a symbolic link that appears between the
// last check and the write. No JavaScript runs in that window, so the test
// simulates the race: lstat answers "missing" for the file and creates the
// link at the same moment.
const race = vi.hoisted(() => ({ path: '', link: '', seen: 0 }))

vi.mock('node:fs', async (importOriginal) => {
  const actual = await importOriginal<typeof fs>()
  return {
    ...actual,
    lstatSync: ((path: fs.PathLike, options?: fs.StatSyncOptions) => {
      // The first lstat of the file is the check before any write, the
      // second the check right before the write: the race goes there.
      if (race.path !== '' && String(path) === race.path && ++race.seen === 2) {
        actual.symlinkSync(race.link, race.path)
        race.path = ''
        throw Object.assign(new Error('ENOENT: simulated'), { code: 'ENOENT' })
      }
      return actual.lstatSync(path, options)
    }) as typeof fs.lstatSync,
  }
})

const { prerenderFiles } = await import('./prerender.mjs')

let dir: string

beforeEach(() => {
  dir = fs.mkdtempSync(join(tmpdir(), 'govue-prerender-race-'))
})

afterEach(() => {
  fs.rmSync(dir, { recursive: true, force: true })
})

it('does not write through a symbolic link created after the last check', async () => {
  const outDir = join(dir, 'dist')
  fs.mkdirSync(outDir)
  const target = join(dir, 'target.txt')
  fs.writeFileSync(target, 'keep')
  const entry = join(dir, 'files.mjs')
  fs.writeFileSync(
    entry,
    "export const files = async () => [{ path: 'page.html', content: 'x' }]\n",
  )
  const template = join(dir, 'index.html')
  fs.writeFileSync(template, '')
  race.path = join(outDir, 'page.html')
  race.link = target

  await expect(prerenderFiles({ entry, template, outDir })).rejects.toThrow(/ELOOP|symbolic/)
  expect(fs.readFileSync(target, 'utf8')).toBe('keep')
})
