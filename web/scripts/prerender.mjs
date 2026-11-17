#!/usr/bin/env node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Build time pre-rendering of one page. A thin wrapper: load a built SSR
// bundle, call its render function, put the markup into a template file and
// write the result.
//
// This file is plain ESM JavaScript on purpose. It ships inside a consumer's
// node_modules, where Node does not strip TypeScript types, and it imports
// nothing from src/. The template injection is therefore a small copy of
// injectIntoTemplate in ../src/entry-server.ts, and a test runs the same
// cases through both so they cannot drift apart.

import { mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

/** The marker the template holds where the page goes. */
export const DEFAULT_PLACEHOLDER = '<!--app-html-->'

export const USAGE = `Usage: govue-prerender --entry <bundle.js> --template <index.html> --out <page.html>

Renders one page at build time and writes it into a template.

Options:
  --entry <path>        SSR bundle that exports an async render() returning HTML (required)
  --template <path>     HTML file holding the placeholder (required)
  --out <path>          File to write (required). With the template itself, the marker is
                        consumed, so run it again only after a fresh build of the template.
  --placeholder <text>  Marker to replace, default ${DEFAULT_PLACEHOLDER}
  --help                Show this message
`

/**
 * @typedef {object} Args
 * @property {string} [entry]
 * @property {string} [template]
 * @property {string} [out]
 * @property {string} [placeholder]
 * @property {boolean} help
 */

/**
 * @param {string[]} argv
 * @returns {Args}
 */
export function parseArgs(argv) {
  /** @type {Args} */
  const args = { help: false }
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

/**
 * Puts the markup in the place of the placeholder, which has to appear
 * exactly once. Same behaviour as injectIntoTemplate in src/entry-server.ts.
 *
 * @param {string} template
 * @param {string} html
 * @param {string} [placeholder]
 * @returns {string}
 */
export function injectIntoTemplate(template, html, placeholder = DEFAULT_PLACEHOLDER) {
  const first = template.indexOf(placeholder)
  if (placeholder === '' || first === -1) {
    throw new Error(
      `govue prerender: the template has no placeholder ${JSON.stringify(placeholder)}`,
    )
  }
  // From first + 1, so two overlapping matches count as two.
  if (template.indexOf(placeholder, first + 1) !== -1) {
    throw new Error(
      `govue prerender: the placeholder ${JSON.stringify(placeholder)} appears more than once`,
    )
  }
  // Slicing instead of String.replace: the markup may hold "$&" and friends.
  return template.slice(0, first) + html + template.slice(first + placeholder.length)
}

/**
 * Renders the entry's page into the template and writes the file.
 *
 * @param {{ entry: string, template: string, out: string, placeholder?: string }} options
 * @returns {Promise<void>}
 */
export async function prerender(options) {
  /** @type {{ render?: unknown }} */
  const bundle = await import(pathToFileURL(resolve(options.entry)).href)
  if (typeof bundle.render !== 'function') {
    throw new Error(`${options.entry} must export a render function`)
  }
  const html = await bundle.render()
  if (typeof html !== 'string') {
    throw new Error(`the render function of ${options.entry} must resolve to a string`)
  }
  const template = readFileSync(options.template, 'utf8')
  const page = injectIntoTemplate(template, html, options.placeholder)
  mkdirSync(dirname(resolve(options.out)), { recursive: true })
  writeFileSync(options.out, page)
}

/**
 * @param {string[]} argv
 * @returns {Promise<number>} the exit code
 */
export async function main(argv) {
  /** @type {Args} */
  let args
  try {
    args = parseArgs(argv)
  } catch (error) {
    process.stderr.write(`${errorMessage(error)}\n\n${USAGE}`)
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
    process.stderr.write(`${errorMessage(error)}\n`)
    return 1
  }
  return 0
}

/**
 * @param {unknown} error
 * @returns {string}
 */
function errorMessage(error) {
  return error instanceof Error ? error.message : String(error)
}

// A package manager runs a bin through a symlink, so compare real paths.
if (process.argv[1] && import.meta.url === pathToFileURL(realpathSync(process.argv[1])).href) {
  process.exitCode = await main(process.argv.slice(2))
}
