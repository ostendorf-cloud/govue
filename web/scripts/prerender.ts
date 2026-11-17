// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Build time pre-rendering of one page. A thin wrapper: load a built SSR
// bundle, call its render function, put the markup into a template file and
// write the result. What decides whether the markup is right lives in
// ../src/entry-server.ts, which has its own tests.
//
// The bundle is the consumer's own: a small file that calls render from
// @ostendorf-cloud/govue/server with the root component, built with
// `vite build --ssr`. Plain Node cannot load a .vue file, so the bundle is
// what this script imports. Node 24+ strips TypeScript types natively, so
// relative imports carry the .ts extension.

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

import { DEFAULT_PLACEHOLDER, injectIntoTemplate } from '../src/entry-server.ts'

export const USAGE = `Usage: prerender --entry <bundle.js> --template <index.html> --out <page.html>

Renders one page at build time and writes it into a template.

Options:
  --entry <path>        SSR bundle that exports an async render() returning HTML (required)
  --template <path>     HTML file holding the placeholder (required)
  --out <path>          File to write, may be the template itself (required)
  --placeholder <text>  Marker to replace, default ${DEFAULT_PLACEHOLDER}
  --help                Show this message
`

interface Args {
  entry?: string
  template?: string
  out?: string
  placeholder?: string
  help: boolean
}

export function parseArgs(argv: string[]): Args {
  const args: Args = { help: false }
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i]
    if (flag === '--') {
      continue
    } else if (flag === '--help' || flag === '-h') {
      args.help = true
    } else if (flag === '--entry') {
      args.entry = argv[++i]
    } else if (flag === '--template') {
      args.template = argv[++i]
    } else if (flag === '--out') {
      args.out = argv[++i]
    } else if (flag === '--placeholder') {
      args.placeholder = argv[++i]
    } else {
      throw new Error(`unknown argument: ${flag}`)
    }
  }
  return args
}

export interface PrerenderOptions {
  entry: string
  template: string
  out: string
  placeholder?: string
}

/** prerender renders the entry's page into the template and writes the file. */
export async function prerender(options: PrerenderOptions): Promise<void> {
  const bundle = (await import(pathToFileURL(resolve(options.entry)).href)) as {
    render?: unknown
  }
  if (typeof bundle.render !== 'function') {
    throw new Error(`${options.entry} must export a render function`)
  }
  const html = await (bundle.render as () => unknown)()
  if (typeof html !== 'string') {
    throw new Error(`the render function of ${options.entry} must resolve to a string`)
  }
  const template = readFileSync(options.template, 'utf8')
  const page = injectIntoTemplate(template, html, options.placeholder)
  mkdirSync(dirname(resolve(options.out)), { recursive: true })
  writeFileSync(options.out, page)
}

export async function main(argv: string[]): Promise<number> {
  let args: Args
  try {
    args = parseArgs(argv)
  } catch (error) {
    process.stderr.write(`${(error as Error).message}\n\n${USAGE}`)
    return 1
  }

  if (args.help) {
    process.stdout.write(USAGE)
    return 0
  }

  if (!args.entry || !args.template || !args.out) {
    process.stderr.write(`--entry, --template and --out are all required\n\n${USAGE}`)
    return 1
  }

  try {
    await prerender({
      entry: args.entry,
      template: args.template,
      out: args.out,
      placeholder: args.placeholder,
    })
  } catch (error) {
    process.stderr.write(`${(error as Error).message}\n`)
    return 1
  }
  return 0
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exitCode = await main(process.argv.slice(2))
}
