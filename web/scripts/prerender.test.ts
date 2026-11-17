// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { join, relative } from 'node:path'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { injectIntoTemplate as injectFromEntry } from '../src/entry-server.ts'
import { injectIntoTemplate, main, parseArgs, prerender, prerenderFiles } from './prerender.mjs'

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

/** An entry whose files function returns the given list as JSON. */
function filesEntry(list: unknown): string {
  return write('files.mjs', `export const files = async () => (${JSON.stringify(list)})\n`)
}

/** Every file under a directory, relative and sorted. */
function tree(root: string): string[] {
  if (!existsSync(root)) return []
  return readdirSync(root, { recursive: true, withFileTypes: true })
    .filter((d) => !d.isDirectory())
    .map((d) => relative(root, join(d.parentPath, d.name)))
    .sort()
}

describe('parseArgs with --out-dir', () => {
  it('reads the output directory', () => {
    expect(parseArgs(['--entry', 'a.js', '--template', 't.html', '--out-dir', 'dist'])).toEqual({
      entry: 'a.js',
      template: 't.html',
      outDir: 'dist',
      help: false,
    })
  })
})

describe('prerenderFiles', () => {
  it('writes every file of the bundle into the directory', async () => {
    const entry = write(
      'files.mjs',
      `export const files = async (template) => [
  { path: 'de/index.html', content: template + ' de' },
  { path: 'de/impressum/index.html', content: 'impressum' },
  { path: 'sitemap.xml', content: '<urlset/>' },
]
`,
    )
    const template = write('index.html', '<t>')
    const outDir = join(dir, 'dist')

    await prerenderFiles({ entry, template, outDir })

    expect(tree(outDir)).toEqual(['de/impressum/index.html', 'de/index.html', 'sitemap.xml'])
    expect(readFileSync(join(outDir, 'de/index.html'), 'utf8')).toBe('<t> de')
    expect(readFileSync(join(outDir, 'de/impressum/index.html'), 'utf8')).toBe('impressum')
    expect(readFileSync(join(outDir, 'sitemap.xml'), 'utf8')).toBe('<urlset/>')
  })

  it('writes a decoded non ASCII path as it is', async () => {
    const entry = filesEntry([{ path: 'de/über/index.html', content: 'ü' }])
    const outDir = join(dir, 'dist')

    await prerenderFiles({ entry, template: write('index.html', ''), outDir })

    expect(readFileSync(join(outDir, 'de', 'über', 'index.html'), 'utf8')).toBe('ü')
  })

  it('overwrites a file of an earlier run and keeps every other file', async () => {
    const outDir = join(dir, 'dist')
    mkdirSync(join(outDir, 'de'), { recursive: true })
    writeFileSync(join(outDir, 'de', 'index.html'), 'an older and longer page')
    writeFileSync(join(outDir, 'index.html'), 'shell')
    const entry = filesEntry([{ path: 'de/index.html', content: 'new' }])

    await prerenderFiles({ entry, template: join(outDir, 'index.html'), outDir })

    expect(readFileSync(join(outDir, 'de', 'index.html'), 'utf8')).toBe('new')
    expect(readFileSync(join(outDir, 'index.html'), 'utf8')).toBe('shell')
  })

  it('fails when the entry exports no files function', async () => {
    const entry = write('files.mjs', 'export const render = async () => ""\n')

    await expect(
      prerenderFiles({ entry, template: write('index.html', ''), outDir: join(dir, 'dist') }),
    ).rejects.toThrow(/must export a files function/)
  })

  const badLists: [string, unknown, RegExp][] = [
    ['a result that is not an array', { path: 'a', content: 'x' }, /must resolve to an array/],
    ['an entry that is not an object', ['a'], /\{ path, content \}/],
    ['a path that is not a string', [{ path: 1, content: 'x' }], /\{ path, content \}/],
    ['content that is not a string', [{ path: 'a', content: 1 }], /\{ path, content \}/],
    ['an empty path', [{ path: '', content: 'x' }], /empty/],
    ['an absolute path', [{ path: '/etc/x', content: 'x' }], /relative/],
    ['a drive letter', [{ path: 'C:/x', content: 'x' }], /relative/],
    ['a parent segment', [{ path: 'de/../../x', content: 'x' }], /segment/],
    ['a leading parent segment', [{ path: '../x', content: 'x' }], /segment/],
    ['a dot segment', [{ path: './x', content: 'x' }], /segment/],
    ['an empty segment', [{ path: 'de//x', content: 'x' }], /segment/],
    ['a trailing slash', [{ path: 'de/', content: 'x' }], /segment/],
    ['a backslash', [{ path: 'de\\..\\x', content: 'x' }], /backslash/],
    ['a NUL byte', [{ path: 'de/x\u0000.html', content: 'x' }], /NUL/],
    [
      'a duplicate',
      [
        { path: 'de/index.html', content: 'a' },
        { path: 'de/index.html', content: 'b' },
      ],
      /more than once/,
    ],
    [
      'a duplicate that differs in case only',
      [
        { path: 'de/A/index.html', content: 'a' },
        { path: 'de/a/index.html', content: 'b' },
      ],
      /more than once/,
    ],
    [
      'a file where another needs a directory',
      [
        { path: 'de/index.html', content: 'a' },
        { path: 'de', content: 'b' },
      ],
      /directory/,
    ],
  ]

  it.each(badLists)('refuses %s and writes nothing', async (_name, list, message) => {
    const outDir = join(dir, 'dist')
    // A first, valid entry: nothing may be written before every path passed.
    const entry = filesEntry(
      Array.isArray(list) ? [{ path: 'first.html', content: 'x' }, ...(list as unknown[])] : list,
    )

    await expect(
      prerenderFiles({ entry, template: write('index.html', ''), outDir }),
    ).rejects.toThrow(message)
    expect(tree(outDir)).toEqual([])
    expect(tree(dir)).toEqual(['files.mjs', 'index.html'])
  })

  it('refuses a symlinked directory inside the output and writes nothing', async () => {
    const outDir = join(dir, 'dist')
    const elsewhere = join(dir, 'elsewhere')
    mkdirSync(outDir)
    mkdirSync(elsewhere)
    symlinkSync(elsewhere, join(outDir, 'de'))
    const entry = filesEntry([
      { path: 'first.html', content: 'x' },
      { path: 'de/index.html', content: 'x' },
    ])

    await expect(
      prerenderFiles({ entry, template: write('index.html', ''), outDir }),
    ).rejects.toThrow(/symbolic link/)
    expect(tree(elsewhere)).toEqual([])
    expect(existsSync(join(outDir, 'first.html'))).toBe(false)
  })

  it('refuses to write through a symlinked file and leaves its target alone', async () => {
    const outDir = join(dir, 'dist')
    mkdirSync(outDir)
    const target = write('target.txt', 'keep')
    symlinkSync(target, join(outDir, 'sitemap.xml'))
    const entry = filesEntry([{ path: 'sitemap.xml', content: 'x' }])

    await expect(
      prerenderFiles({ entry, template: write('index.html', ''), outDir }),
    ).rejects.toThrow(/symbolic link/)
    expect(readFileSync(target, 'utf8')).toBe('keep')
    expect(lstatSync(join(outDir, 'sitemap.xml')).isSymbolicLink()).toBe(true)
  })

  it('refuses a path whose directory is a file in the output', async () => {
    const outDir = join(dir, 'dist')
    mkdirSync(outDir)
    writeFileSync(join(outDir, 'de'), 'a file')
    const entry = filesEntry([{ path: 'de/index.html', content: 'x' }])

    await expect(
      prerenderFiles({ entry, template: write('index.html', ''), outDir }),
    ).rejects.toThrow(/"de\/index.html" needs .* to be a directory/)
  })

  it('refuses a path that is a directory in the output', async () => {
    const outDir = join(dir, 'dist')
    mkdirSync(join(outDir, 'de'), { recursive: true })
    const entry = filesEntry([{ path: 'de', content: 'x' }])

    await expect(
      prerenderFiles({ entry, template: write('index.html', ''), outDir }),
    ).rejects.toThrow(/"de" is a directory/)
  })
})

describe('main with --out-dir', () => {
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

  it('exits 0 and writes the files', async () => {
    const entry = filesEntry([{ path: 'de/index.html', content: 'ok' }])
    const template = write('index.html', '')
    const outDir = join(dir, 'dist')

    expect(await main(['--entry', entry, '--template', template, '--out-dir', outDir])).toBe(0)
    expect(readFileSync(join(outDir, 'de', 'index.html'), 'utf8')).toBe('ok')
  })

  it('exits 1 and names the path the bundle got wrong', async () => {
    const entry = filesEntry([{ path: '../escape.html', content: 'x' }])
    const template = write('index.html', '')
    const outDir = join(dir, 'dist')

    expect(await main(['--entry', entry, '--template', template, '--out-dir', outDir])).toBe(1)
    expect(stderr.join('')).toContain('"../escape.html"')
    expect(existsSync(join(dir, 'escape.html'))).toBe(false)
  })

  it('exits 1 when the bundle fails, so a broken page fails the build', async () => {
    const entry = write(
      'files.mjs',
      'export const files = async () => { throw new Error("boom") }\n',
    )
    const template = write('index.html', '')

    expect(
      await main(['--entry', entry, '--template', template, '--out-dir', join(dir, 'dist')]),
    ).toBe(1)
    expect(stderr.join('')).toContain('boom')
  })

  it('exits 1 for --out together with --out-dir', async () => {
    expect(
      await main(['--entry', 'a.js', '--template', 't.html', '--out', 'o', '--out-dir', 'd']),
    ).toBe(1)
    expect(stderr.join('')).toContain('either --out or --out-dir')
  })

  it('exits 1 for --placeholder with --out-dir', async () => {
    expect(
      await main([
        '--entry',
        'a.js',
        '--template',
        't.html',
        '--out-dir',
        'd',
        '--placeholder',
        'X',
      ]),
    ).toBe(1)
    expect(stderr.join('')).toContain('--placeholder applies to --out only')
  })

  it('exits 1 without --out and --out-dir', async () => {
    expect(await main(['--entry', 'a.js', '--template', 't.html'])).toBe(1)
    expect(stderr.join('')).toContain('are all required')
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
    ['aaa', 'x', 'aa'],
  ]

  it('counts overlapping matches as more than one', () => {
    expect(() => injectIntoTemplate('aaa', 'x', 'aa')).toThrow('more than once')
    expect(() => injectFromEntry('aaa', 'x', 'aa')).toThrow('more than once')
  })

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
