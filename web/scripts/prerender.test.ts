// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { injectIntoTemplate as injectFromEntry } from '../src/entry-server.ts'
import { injectIntoTemplate, main, parseArgs, prerender } from './prerender.mjs'

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

  it('fails when render resolves to something other than a string', async () => {
    const entry = write('entry.mjs', 'export const render = async () => 42\n')
    const template = write('index.html', '<!--app-html-->')
    const out = join(dir, 'o.html')

    await expect(prerender({ entry, template, out })).rejects.toThrow(/must resolve to a string/)
    expect(existsSync(out)).toBe(false)
  })

  it('creates the directory of the output file', async () => {
    const entry = write('entry.mjs', 'export const render = async () => "<p>x</p>"\n')
    const template = write('index.html', '<!--app-html-->')
    const out = join(dir, 'deep', 'er', 'page.html')

    await prerender({ entry, template, out })

    expect(readFileSync(out, 'utf8')).toBe('<p>x</p>')
  })

  it('fails on a second run over its own output, the marker being consumed', async () => {
    const entry = write('entry.mjs', 'export const render = async () => "<p>x</p>"\n')
    const template = write('index.html', '<!--app-html-->')

    await prerender({ entry, template, out: template })

    await expect(prerender({ entry, template, out: template })).rejects.toThrow(/placeholder/)
  })
})

describe('main', () => {
  let stderr: string[]

  beforeEach(() => {
    stderr = []
    vi.spyOn(process.stderr, 'write').mockImplementation((chunk) => {
      stderr.push(String(chunk))
      return true
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('exits 1 when a required flag is missing', async () => {
    expect(await main(['--entry', 'a.js'])).toBe(1)
    expect(stderr.join('')).toContain('are all required')
  })

  it('exits 1 on an unknown flag', async () => {
    expect(await main(['--nope'])).toBe(1)
    expect(stderr.join('')).toContain('unknown argument')
  })

  it('exits 0 and writes the page', async () => {
    const entry = write('entry.mjs', 'export const render = async () => "<p>ok</p>"\n')
    const template = write('index.html', '<!--app-html-->')
    const out = join(dir, 'o.html')

    expect(await main(['--entry', entry, '--template', template, '--out', out])).toBe(0)
    expect(readFileSync(out, 'utf8')).toBe('<p>ok</p>')
  })

  it('exits 1 when the page fails to render, so a broken page fails the build', async () => {
    const entry = write(
      'entry.mjs',
      'export const render = async () => { throw new Error("boom") }\n',
    )
    const template = write('index.html', '<!--app-html-->')
    const out = join(dir, 'o.html')

    expect(await main(['--entry', entry, '--template', template, '--out', out])).toBe(1)
    expect(stderr.join('')).toContain('boom')
    expect(existsSync(out)).toBe(false)
  })
})

describe('parity with injectIntoTemplate in the render entry', () => {
  const cases: [string, string, string | undefined][] = [
    ['<div><!--app-html--></div>', '<p>x</p>', undefined],
    ['a{{ssr}}b', 'X', '{{ssr}}'],
    ['<!--app-html-->', '<p>$&$1</p>', undefined],
    ['<html></html>', 'x', undefined],
    ['<!--app-html--><!--app-html-->', 'x', undefined],
    ['abc', 'x', ''],
  ]

  it.each(cases)('treats %j the same in both copies', (template, html, placeholder) => {
    const run = (fn: typeof injectIntoTemplate) => {
      try {
        return { ok: fn(template, html, placeholder) }
      } catch (error) {
        return { error: (error as Error).message }
      }
    }

    expect(run(injectIntoTemplate)).toEqual(run(injectFromEntry))
  })
})
