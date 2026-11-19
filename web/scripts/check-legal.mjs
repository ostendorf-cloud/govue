#!/usr/bin/env node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The build check of the legal pages. It reads the output directory of
// govue-prerender --out-dir and fails when the imprint or the privacy page is
// missing in a language, carries another language on its html element, or is
// not listed in sitemap.xml. The terms page is optional, since only a site
// that sells something has one: when it exists in one language it must exist
// in all, and --require-terms makes it required. Comments are ignored, and
// the sitemap must name one origin.
//
// It also reads every prerendered HTML page under <dir>/<code>/ and fails a
// page that has no link to the imprint and the privacy page of its own
// language, and to the terms page when the site has one. A link counts when
// it is an a element with an href that is exactly the root relative path,
// /de/imprint, query and fragment ignored. A trailing slash or /index.html
// does not count: the server does not redirect them. A link in a comment, a
// script, style, textarea, title, noscript or template, a link element and
// an absolute URL do not count. The link text and the look of the footer are
// not checked.
//
// Known differences from a browser: the escape states of a script
// (<!--<script>), CDATA in HTML content (a browser reads a bogus comment,
// the tokenizer reads to ]]>) and a self closed raw text element in svg are
// not modelled. They fail loudly (a link is missed, never invented) or Vue
// SSR does not produce them.
//
// Plain ESM JavaScript like prerender.mjs, so it runs from a consumer's
// node_modules. It reads files only and writes nothing. It has no
// dependency, so the HTML is read by the small tokenizer below.

import { lstatSync, readdirSync, readFileSync, realpathSync } from 'node:fs'
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
its html element, or is not in sitemap.xml, and when sitemap.xml lists more
than one origin. Comments are ignored. Terms are only for a site that sells
something, so the page is optional, but when it exists in one language it
must exist in all.

Also fails every HTML page under <out-dir>/<code>/ that has no a href to the
imprint and the privacy page of its own language, and to the terms page when
the site has one. The href must be exactly the root relative path, such as
/de/imprint, since the server redirects no trailing slash or /index.html. A
link in a comment, a script or a style block does not count.

Options:
  --dir <path>          The prerender output directory (required). Also --dir=<path>.
  --languages <codes>   Comma separated language codes. Default: de,en.
  --require-terms       Fail when the terms page is missing, for a site that sells.
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
    // --dir=d is the same as --dir d.
    const eq = argv[i].startsWith('--') ? argv[i].indexOf('=') : -1
    const flag = eq < 0 ? argv[i] : argv[i].slice(0, eq)
    const value = () => (eq < 0 ? argv[++i] : argv[i].slice(eq + 1))
    if (eq >= 0 && flag !== '--dir' && flag !== '--languages') {
      throw new Error(`unknown argument: ${argv[i]}`)
    }
    if (flag === '--') {
      continue
    } else if (flag === '--help' || flag === '-h') {
      args.help = true
    } else if (flag === '--dir') {
      args.dir = value()
    } else if (flag === '--languages') {
      args.languages = (value() ?? '').split(',').map((code) => code.trim())
    } else if (flag === '--require-terms') {
      args.requireTerms = true
    } else {
      throw new Error(`unknown argument: ${flag}`)
    }
  }
  return args
}

/**
 * The text without its comments. A loc or an html tag inside a comment is
 * read by no crawler and no browser, so it must not satisfy the check.
 *
 * @param {string} text
 * @returns {string}
 */
function stripComments(text) {
  return text.replace(/<!--[\s\S]*?(?:-->|$)/g, '')
}

/**
 * The decoded paths and the origins of every loc in a sitemap.
 *
 * @param {string} xml
 * @returns {{ paths: Set<string>, origins: Set<string> }}
 */
function sitemapLocs(xml) {
  const paths = new Set()
  const origins = new Set()
  for (const match of stripComments(xml).matchAll(/<loc>\s*([^<]*?)\s*<\/loc>/g)) {
    const text = match[1]
      .replaceAll('&lt;', '<')
      .replaceAll('&gt;', '>')
      .replaceAll('&quot;', '"')
      .replaceAll('&apos;', "'")
      .replaceAll('&amp;', '&')
    try {
      const url = new URL(text)
      origins.add(url.origin)
      paths.add(decodeURIComponent(url.pathname))
    } catch {
      // A loc that is no URL lists no legal page.
    }
  }
  return { paths, origins }
}

/**
 * The lang attribute of the html start tag outside comments, or undefined.
 *
 * @param {string} html
 * @returns {string | undefined}
 */
function htmlLang(html) {
  const tag = /<html\b[^>]*>/i.exec(stripComments(html))
  if (!tag) return undefined
  const lang = /\slang\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i.exec(tag[0])
  return lang ? (lang[1] ?? lang[2] ?? lang[3]) : undefined
}

/** Elements whose content is text, never markup: no link inside counts. */
const RAW_TEXT = new Set([
  'script',
  'style',
  'textarea',
  'title',
  'xmp',
  'iframe',
  'noembed',
  'noframes',
  // A browser with scripting on reads noscript as text.
  'noscript',
])

const TAG_NAME = /[A-Za-z][^\s/>]*/y
const ATTR_NAME = /[^\s/>][^\s/>=]*/y
const UNQUOTED = /[^\s>]*/y
const SPACE = /\s*/y
const CHAR_REF = /&(?:#[xX]([0-9a-fA-F]+);?|#([0-9]+);?|(amp|lt|gt|quot|apos|sol|colon);)/g
const NAMED = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", sol: '/', colon: ':' }

/**
 * The attribute value with its character references decoded, one pass.
 *
 * @param {string} value
 * @returns {string}
 */
function decode(value) {
  return value.replace(CHAR_REF, (all, hex, dec, name) => {
    if (name) return NAMED[/** @type {keyof typeof NAMED} */ (name)]
    const code = hex ? parseInt(hex, 16) : parseInt(dec, 10)
    return code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : all
  })
}

/**
 * Reads the attributes of a tag from position p, after its name. The first
 * of a repeated attribute wins, like in a browser. Returns undefined when the
 * input ends inside the tag, which a browser drops.
 *
 * @param {string} html
 * @param {number} from
 * @returns {{ attrs: Map<string, string>, end: number } | undefined}
 */
function readAttributes(html, from) {
  const attrs = new Map()
  let p = from
  for (;;) {
    while (p < html.length && /[\s/]/.test(html[p])) p++
    if (p >= html.length) return undefined
    if (html[p] === '>') return { attrs, end: p + 1 }
    ATTR_NAME.lastIndex = p
    const name = ATTR_NAME.exec(html)?.[0]
    if (name === undefined) return undefined
    p += name.length
    SPACE.lastIndex = p
    p += SPACE.exec(html)?.[0].length ?? 0
    let value = ''
    if (html[p] === '=') {
      p++
      SPACE.lastIndex = p
      p += SPACE.exec(html)?.[0].length ?? 0
      const quote = html[p]
      if (quote === '"' || quote === "'") {
        const close = html.indexOf(quote, p + 1)
        if (close < 0) return undefined
        value = html.slice(p + 1, close)
        p = close + 1
      } else {
        UNQUOTED.lastIndex = p
        value = UNQUOTED.exec(html)?.[0] ?? ''
        p += value.length
      }
    }
    const key = name.toLowerCase()
    if (!attrs.has(key)) attrs.set(key, decode(value))
  }
}

/**
 * The href of every a element that is live markup. Comments, CDATA, bogus
 * comments, raw text elements (script, style and the like) and template
 * content are skipped, tag and attribute names are case insensitive, and an
 * attribute value is quoted with " or ', or not quoted.
 *
 * @param {string} html
 * @returns {string[]}
 */
export function anchorHrefs(html) {
  const hrefs = []
  const n = html.length
  let template = 0
  let i = 0
  while (i < n) {
    const lt = html.indexOf('<', i)
    if (lt < 0) break
    i = lt
    if (html.startsWith('<!--', i)) {
      // <!--> and <!---> are empty comments. Otherwise the comment runs to
      // the next --> or --!>, searched after the opening <!--.
      if (html.startsWith('<!-->', i)) {
        i += 5
      } else if (html.startsWith('<!--->', i)) {
        i += 6
      } else {
        const close = /--!?>/g
        close.lastIndex = i + 4
        const m = close.exec(html)
        i = m ? m.index + m[0].length : n
      }
      continue
    }
    if (html.startsWith('<![CDATA[', i)) {
      const end = html.indexOf(']]>', i)
      i = end < 0 ? n : end + 3
      continue
    }
    const closing = html[i + 1] === '/'
    TAG_NAME.lastIndex = i + (closing ? 2 : 1)
    const name = TAG_NAME.exec(html)?.[0]
    if (name === undefined) {
      if (closing || html[i + 1] === '!' || html[i + 1] === '?') {
        // A bogus comment runs to the next >.
        const end = html.indexOf('>', i)
        i = end < 0 ? n : end + 1
      } else {
        i++
      }
      continue
    }
    const tag = readAttributes(html, TAG_NAME.lastIndex)
    if (!tag) break
    i = tag.end
    const lower = name.toLowerCase()
    if (lower === 'template') template = Math.max(0, template + (closing ? -1 : 1))
    if (closing) continue
    if (lower === 'a' && template <= 0 && tag.attrs.has('href')) {
      hrefs.push(/** @type {string} */ (tag.attrs.get('href')))
    } else if (lower === 'plaintext') {
      break
    } else if (RAW_TEXT.has(lower)) {
      const end = new RegExp(`</${lower}(?=[\\s/>])`, 'gi')
      end.lastIndex = i
      const m = end.exec(html)
      i = m ? m.index : n
    }
  }
  return hrefs
}

/**
 * The canonical path an href points to, or undefined when it is not a root
 * relative path. Query and fragment drop out, nothing else is normalised:
 * the server registers exact paths and does not redirect /de/imprint/ or
 * /de/imprint/index.html, so those links are dead and do not count. Absolute
 * URLs, protocol relative URLs and document relative paths do not count: the
 * last depend on the URL the page is served under.
 *
 * @param {string} href
 * @returns {string | undefined}
 */
export function linkTarget(href) {
  // A browser strips surrounding whitespace and tab and newline anywhere.
  let path = href.replace(/^[\t\n\f\r ]+|[\t\n\f\r ]+$/g, '').replace(/[\t\n\r]/g, '')
  if (!path.startsWith('/') || path[1] === '/' || path[1] === '\\') return undefined
  return path.split(/[?#]/, 1)[0]
}

/**
 * The relative paths of every HTML file under <dir>/<code>, sorted. A
 * symbolic link is skipped: the prerender never writes one.
 *
 * @param {string} dir
 * @param {string} code
 * @returns {string[]}
 */
function htmlPages(dir, code) {
  /** @type {string[]} */
  const found = []
  /** @param {string} rel */
  const walk = (rel) => {
    let entries
    try {
      entries = readdirSync(join(dir, rel), { withFileTypes: true })
    } catch {
      return
    }
    for (const entry of entries.sort((a, b) => (a.name < b.name ? -1 : 1))) {
      const next = `${rel}/${entry.name}`
      if (entry.isDirectory()) walk(next)
      else if (entry.isFile() && entry.name.toLowerCase().endsWith('.html')) found.push(next)
    }
  }
  walk(code)
  return found
}

/**
 * Checks that every page of a language links the legal pages of that language.
 *
 * @param {string} dir
 * @param {string[]} languages
 * @param {string[]} pages
 * @returns {string[]}
 */
function checkLinks(dir, languages, pages) {
  const problems = []
  for (const code of languages) {
    for (const file of htmlPages(dir, code)) {
      const html = readPage(join(dir, file))
      if (html === undefined) continue
      const targets = new Set(anchorHrefs(html).map(linkTarget))
      for (const page of pages) {
        if (!targets.has(legalPath(code, page))) {
          problems.push(
            `${file} has no link to the ${page} page in ${code}: want an a href to ${legalPath(code, page)}`,
          )
        }
      }
    }
  }
  return problems
}

/**
 * The canonical path of a legal page, the one the sitemap lists and the
 * footer links: /<code>/<page>, as legalPath in src/i18n/legal.ts.
 *
 * @param {string} code
 * @param {string} page
 * @returns {string}
 */
function legalPath(code, page) {
  return `/${code}/${page}`
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
    if (!listed.has(legalPath(code, page))) {
      problems.push(`${page} page ${legalPath(code, page)} is not listed in sitemap.xml`)
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
  const { paths: listed, origins } = sitemapLocs(sitemap ?? '')
  // The sitemap of one site has one origin. A loc of another origin is a
  // build mistake, and its path would count for a page of this site.
  if (origins.size > 1) {
    errors.push(`sitemap.xml lists more than one origin: ${[...origins].sort().join(', ')}`)
  }

  for (const page of REQUIRED_PAGES) {
    errors.push(...checkPage(dir, page, languages, listed).problems)
  }

  const terms = checkPage(dir, OPTIONAL_PAGE, languages, listed)
  const termsListed = languages.some((code) => listed.has(legalPath(code, OPTIONAL_PAGE)))
  const hasTerms = terms.present > 0 || termsListed || requireTerms
  if (hasTerms) {
    errors.push(...terms.problems)
    if (terms.problems.length === 0) notes.push('terms page: present in every language')
  } else {
    notes.push(
      'terms page: none, which is right for a site that sells nothing. Pass --require-terms when the site sells something.',
    )
  }

  errors.push(
    ...checkLinks(dir, languages, hasTerms ? [...REQUIRED_PAGES, OPTIONAL_PAGE] : REQUIRED_PAGES),
  )
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
