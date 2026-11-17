// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { main, parseArgs, prerender } from './prerender.ts'

let dir: string

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'govue-prerender-'))
})

afterEach(() => {
  rmSync(dir, { recursive: true, force: true })
})

function write(name: string, content: string): string {
  const path = join(dir, name)
  writeFileSync(path, content)
  return path
}

describe('parseArgs', () => {
  it('reads every flag', () => {
    expect(
      parseArgs([
        '--entry',
        'a.js',
        '--template',
        't.html',
        '--out',
        'o.html',
        '--placeholder',
        'X',
      ]),
    ).toEqual({ entry: 'a.js', template: 't.html', out: 'o.html', placeholder: 'X', help: false })
  })

  it('tolerates a bare double dash', () => {
    expect(parseArgs(['--', '--help']).help).toBe(true)
  })

  it('rejects an unknown flag', () => {
    expect(() => parseArgs(['--nope'])).toThrow(/unknown argument/)
  })
})

describe('prerender', () => {
  it('writes the rendered page into the template', async () => {
    const entry = write('entry.mjs', 'export const render = async () => "<h1>Hi</h1>"\n')
    const template = write('index.html', '<body><!--app-html--></body>')
    const out = join(dir, 'out.html')

    await prerender({ entry, template, out })

    expect(readFileSync(out, 'utf8')).toBe('<body><h1>Hi</h1></body>')
  })

  it('fails when the entry exports no render function', async () => {
    const entry = write('entry.mjs', 'export const other = 1\n')
    const template = write('index.html', '<!--app-html-->')

    await expect(prerender({ entry, template, out: join(dir, 'o.html') })).rejects.toThrow(
      /must export a render function/,
    )
  })

  it('fails and writes nothing when the template has no placeholder', async () => {
    const entry = write('entry.mjs', 'export const render = async () => "x"\n')
    const template = write('index.html', '<body></body>')
    const out = join(dir, 'o.html')

    await expect(prerender({ entry, template, out })).rejects.toThrow(/placeholder/)
    expect(() => readFileSync(out)).toThrow()
  })
})

describe('main', () => {
  it('exits 1 when a required flag is missing', async () => {
    expect(await main(['--entry', 'a.js'])).toBe(1)
  })
})
