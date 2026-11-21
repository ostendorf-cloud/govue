// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The bundling test of the proof of work worker. A site consumes govue as a
// package: a real directory under node_modules, never a symlink into the
// source tree, with the TypeScript and Vue sources inside. This builds a tiny
// consumer app against such a copy and checks that the bundler found the
// `new Worker(new URL('./pow.worker.ts', import.meta.url))` call inside the
// package and emitted the worker as its own file with the solver in it.
//
// The copy is the offline equivalent of the pnpm git dependency layout: the
// package.json and the files it lists, under node_modules/@ostendorf-cloud.
// pnpm's content addressed store puts the same directory behind a symlink,
// and Vite resolves a symlink to its real path, so the module ids are the
// same ones.

import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { build } from 'vite'
import { afterAll, beforeAll, describe, expect, it } from 'vitest'

const webDir = resolve(fileURLToPath(new URL('..', import.meta.url)))

let dir: string
let files: Array<{ fileName: string; type: string; code: string }>

beforeAll(async () => {
  dir = mkdtempSync(join(tmpdir(), 'govue-consumer-'))
  const modules = join(dir, 'node_modules')
  mkdirSync(modules)

  // The consumer's own dependencies, shared with this package's install.
  for (const name of readdirSync(join(webDir, 'node_modules'))) {
    if (name === '.bin' || name.startsWith('.')) continue
    symlinkSync(join(webDir, 'node_modules', name), join(modules, name))
  }

  // The package as a git dependency delivers it: package.json and the sources.
  const pkg = join(modules, '@ostendorf-cloud', 'govue')
  mkdirSync(pkg, { recursive: true })
  cpSync(join(webDir, 'package.json'), join(pkg, 'package.json'))
  cpSync(join(webDir, 'src'), join(pkg, 'src'), {
    recursive: true,
    filter: (source) => !/\.test\.ts$/.test(source),
  })

  writeFileSync(
    join(dir, 'entry.ts'),
    [
      "import { createChallengeSolver, GovueContactForm } from '@ostendorf-cloud/govue'",
      'export { GovueContactForm }',
      'export const solver = createChallengeSolver({} as never)',
      'solver.prepare()',
      '',
    ].join('\n'),
  )

  const result = await build({
    root: dir,
    configFile: false,
    logLevel: 'silent',
    plugins: [vue()],
    build: { write: false, rollupOptions: { input: join(dir, 'entry.ts') } },
  })
  const outputs = Array.isArray(result) ? result : [result as { output: unknown[] }]
  files = outputs.flatMap((o) =>
    (
      o as { output: Array<{ fileName: string; type: string; code?: string; source?: unknown }> }
    ).output.map((f) => ({
      fileName: f.fileName,
      type: f.type,
      code: typeof f.code === 'string' ? f.code : typeof f.source === 'string' ? f.source : '',
    })),
  )
})

afterAll(() => {
  rmSync(dir, { recursive: true, force: true })
})

describe('the worker in a consumer build', () => {
  it('emits the worker as its own file with the solver in it', () => {
    const worker = files.find((f) => /pow\.worker/.test(f.fileName))
    expect(worker, files.map((f) => f.fileName).join(', ')).toBeDefined()
    expect(worker?.code).toContain('crypto.subtle')
    expect(worker?.code).toContain('SHA-256')
  })

  it('points the main bundle at the emitted file, not at the source', () => {
    const worker = files.find((f) => /pow\.worker/.test(f.fileName))
    const main = files.find((f) => f.type === 'chunk' && /new Worker/.test(f.code))
    expect(main).toBeDefined()
    expect(main?.code).toContain(worker?.fileName.split('/').pop() ?? '')
    expect(main?.code).not.toContain('pow.worker.ts')
  })

  it('keeps the network out of the worker file', () => {
    const worker = files.find((f) => /pow\.worker/.test(f.fileName))
    for (const word of [
      'fetch(',
      'XMLHttpRequest',
      'document.cookie',
      'localStorage',
      'WebSocket',
    ]) {
      expect(worker?.code, word).not.toContain(word)
    }
  })
})
