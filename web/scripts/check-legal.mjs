#!/usr/bin/env node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The build check of the legal pages. It reads the output directory of
// govue-prerender --out-dir and fails when the imprint or the privacy page is
// missing in a language, carries another language on its html element, or is
// not listed in sitemap.xml. The terms page is optional: when it exists in one
// language it must exist in all, and --require-terms makes it required.
//
// Plain ESM JavaScript like prerender.mjs, so it runs from a consumer's
// node_modules. It reads files only and writes nothing.

import { lstatSync, readFileSync, realpathSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

/** The pages every site has. */
export const REQUIRED_PAGES = ['imprint', 'privacy']

/** The page a site may leave out. */
export const OPTIONAL_PAGE = 'terms'

/** The languages of the Vue package, catalogues.ts. */
export const DEFAULT_LANGUAGES = ['de', 'en']

export const USAGE = `Usage: govue-check-legal --dir <out-dir> [--languages de,en] [--require-terms]

Checks the legal pages in the output of govue-prerender --out-dir. Fails when
the imprint or the privacy page is missing in a language, has another lang on
its html element, or is not in sitemap.xml. The terms page is optional, but
when it exists in one language it must exist in all.

Options:
  --dir <path>          The prerender output directory (required).
  --languages <codes>   Comma separated language codes. Default: de,en.
  --require-terms       Fail when the terms page is missing.
  -h, --help            Show this help.`

/**
 * @typedef {object} Args
 * @property {string} [dir]
 * @property {string[]} languages
 * @property {boolean} requireTerms
 * @property {boolean} help
 */

/**
 * @param {string[]} argv
 * @returns {Args}
 */
export function parseArgs(argv) {
  /** @type {Args} */
  const args = { languages: DEFAULT_LANGUAGES, requireTerms: false, help: false }
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i]
    if (flag === '--') {
      continue
    } else if (flag === '--help' || flag === '-h') {
      args.help = true
    } else if (flag === '--dir') {
      args.dir = argv[++i]
    } else if (flag === '--languages') {
      args.languages = (argv[++i] ?? '').split(',').map((code) => code.trim())
    } else if (flag === '--require-terms') {
      args.requireTerms = true
    } else {
      throw new Error(`unknown argument: ${flag}`)
    }
  }
  return args
}

/**
 * The decoded paths of every loc in a sitemap.
 *
 * @param {string} xml
 * @returns {Set<string>}
 */
function sitemapPaths(xml) {
  const paths = new Set()
  for (const match of xml.matchAll(/<loc>\s*([^<]*?)\s*<\/loc>/g)) {
    const text = match[1]
      .replaceAll('&lt;', '<')
      .replaceAll('&gt;', '>')
      .replaceAll('&quot;', '"')
      .replaceAll('&apos;', "'")
      .replaceAll('&amp;', '&')
    try {
      paths.add(decodeURIComponent(new URL(text).pathname))
    } catch {
      // A loc that is no URL lists no legal page.
    }
  }
  return paths
}

/**
 * The lang attribute of the html start tag, or undefined.
 *
 * @param {string} html
 * @returns {string | undefined}
 */
function htmlLang(html) {
  const tag = /<html\b[^>]*>/i.exec(html)
  if (!tag) return undefined
  const lang = /\slang\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i.exec(tag[0])
  return lang ? (lang[1] ?? lang[2] ?? lang[3]) : undefined
}

/**
 * The content of a regular file, or undefined when there is none. A symbolic
 * link counts as missing: the prerender never writes one.
 *
 * @param {string} path
 * @returns {string | undefined}
 */
function readPage(path) {
  try {
    if (!lstatSync(path).isFile()) return undefined
    return readFileSync(path, 'utf8')
  } catch {
    return undefined
  }
}

/**
 * Checks one page in every language and returns the problems.
 *
 * @param {string} dir
 * @param {string} page
 * @param {string[]} languages
 * @param {Set<string>} listed
 * @returns {{ problems: string[], present: number }}
 */
function checkPage(dir, page, languages, listed) {
  const problems = []
  let present = 0
  for (const code of languages) {
    const file = `${code}/${page}/index.html`
    const html = readPage(join(dir, code, page, 'index.html'))
    if (html === undefined) {
      problems.push(`${page} page missing in ${code}: no ${file}`)
      continue
    }
    present++
    const lang = htmlLang(html)
    if (lang !== code) {
      problems.push(`${page} page ${file} has lang ${JSON.stringify(lang ?? '')}, want "${code}"`)
    }
    if (!listed.has(`/${code}/${page}`)) {
      problems.push(`${page} page /${code}/${page} is not listed in sitemap.xml`)
    }
  }
  return { problems, present }
}

/**
 * Checks the legal pages of a prerender output directory.
 *
 * @param {{ dir: string, languages?: string[], requireTerms?: boolean }} options
 * @returns {{ errors: string[], notes: string[] }}
 */
export function checkLegal({ dir, languages = DEFAULT_LANGUAGES, requireTerms = false }) {
  if (languages.length === 0 || new Set(languages).size !== languages.length) {
    throw new Error('--languages must list each code once')
  }
  for (const code of languages) {
    if (!/^[a-z]{2,3}$/.test(code)) {
      throw new Error(
        `language code ${JSON.stringify(code)} must be two or three lowercase letters`,
      )
    }
  }

  const errors = []
  const notes = []
  const sitemap = readPage(join(dir, 'sitemap.xml'))
  if (sitemap === undefined) {
    errors.push('sitemap.xml missing: run govue-prerender --out-dir first')
  }
  const listed = sitemapPaths(sitemap ?? '')

  for (const page of REQUIRED_PAGES) {
    errors.push(...checkPage(dir, page, languages, listed).problems)
  }

  const terms = checkPage(dir, OPTIONAL_PAGE, languages, listed)
  const termsListed = languages.some((code) => listed.has(`/${code}/${OPTIONAL_PAGE}`))
  if (terms.present > 0 || termsListed || requireTerms) {
    errors.push(...terms.problems)
    if (terms.problems.length === 0) notes.push('terms page: present in every language')
  } else {
    notes.push('terms page: none, which is allowed. Pass --require-terms to require one.')
  }
  return { errors, notes }
}

/**
 * The CLI. Returns the exit code: 0 when every check passes, 1 otherwise.
 *
 * @param {string[]} argv
 * @returns {number}
 */
export function main(argv) {
  let args
  try {
    args = parseArgs(argv)
  } catch (error) {
    process.stderr.write(`govue-check-legal: ${errorMessage(error)}\n${USAGE}\n`)
    return 1
  }
  if (args.help) {
    process.stdout.write(`${USAGE}\n`)
    return 0
  }
  if (!args.dir) {
    process.stderr.write(`govue-check-legal: --dir is required\n${USAGE}\n`)
    return 1
  }
  let result
  try {
    result = checkLegal({
      dir: args.dir,
      languages: args.languages,
      requireTerms: args.requireTerms,
    })
  } catch (error) {
    process.stderr.write(`govue-check-legal: ${errorMessage(error)}\n`)
    return 1
  }
  for (const note of result.notes) process.stdout.write(`govue-check-legal: ${note}\n`)
  for (const error of result.errors) process.stderr.write(`govue-check-legal: ${error}\n`)
  if (result.errors.length > 0) return 1
  process.stdout.write(
    `govue-check-legal: imprint and privacy present in ${args.languages.join(', ')}\n`,
  )
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
  process.exitCode = main(process.argv.slice(2))
}
