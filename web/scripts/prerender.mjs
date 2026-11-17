#!/usr/bin/env node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Build time pre-rendering. A thin wrapper with two modes. With --out it
// loads a built SSR bundle, calls its render function, puts the markup into a
// template file and writes the result. With --out-dir it calls the bundle's
// files function with the template and writes every file it returns.
//
// This file is plain ESM JavaScript on purpose. It ships inside a consumer's
// node_modules, where Node does not strip TypeScript types, and it imports
// nothing from src/. The template injection is therefore a small copy of
// injectIntoTemplate in ../src/template.ts, and a test runs the same cases
// through both so they cannot drift apart. The --out-dir mode knows no
// template and no language: all of that is compiled into the bundle.

import {
  closeSync,
  constants,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  realpathSync,
  writeFileSync,
} from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

/** The marker the template holds where the page goes. */
export const DEFAULT_PLACEHOLDER = '<!--app-html-->'

export const USAGE = `Usage: govue-prerender --entry <bundle.js> --template <index.html> --out <page.html>
       govue-prerender --entry <bundle.js> --template <index.html> --out-dir <dist>

Renders pages at build time. With --out it renders one page into a template.
With --out-dir it writes every file the bundle's files(template) returns.

Options:
  --entry <path>        SSR bundle (required). With --out it exports an async render()
                        returning HTML, with --out-dir an async files(template) returning
                        a list of { path, content }.
  --template <path>     HTML file holding the placeholder (required)
  --out <path>          File to write. With the template itself, the marker is
                        consumed, so run it again only after a fresh build of the template.
  --out-dir <path>      Directory to write the files into. A file of an earlier run is
                        overwritten, every other file is kept. A path must stay inside the
                        directory: no absolute path, "..", backslash, NUL, duplicate or
                        symbolic link.
  --placeholder <text>  Marker to replace with --out, default ${DEFAULT_PLACEHOLDER}
  --help                Show this message

Exactly one of --out and --out-dir is required.
`

/**
 * @typedef {object} Args
 * @property {string} [entry]
 * @property {string} [template]
 * @property {string} [out]
 * @property {string} [outDir]
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
    } else if (flag === '--out-dir') {
      args.outDir = argv[++i]
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
 * Calls the bundle's files function with the template and writes every file
 * it returns into the output directory. Every path, and every part of it that
 * already exists in the directory, is checked before the first file is
 * written, so a bad bundle leaves the directory as it was.
 *
 * @param {{ entry: string, template: string, outDir: string }} options
 * @returns {Promise<void>}
 */
export async function prerenderFiles(options) {
  /** @type {{ files?: unknown }} */
  const bundle = await import(pathToFileURL(resolve(options.entry)).href)
  if (typeof bundle.files !== 'function') {
    throw new Error(`${options.entry} must export a files function`)
  }
  const template = readFileSync(options.template, 'utf8')
  const files = checkFiles(await bundle.files(template), options.entry)
  const outDir = resolve(options.outDir)
  mkdirSync(outDir, { recursive: true })
  for (const file of files) {
    checkTarget(outDir, file.path, false)
  }
  for (const file of files) {
    checkTarget(outDir, file.path, true)
    writeNoFollow(join(outDir, ...file.path.split('/')), file.content)
  }
}

/**
 * Checks the list a files function returned: entries of string path and
 * content, safe relative paths, no duplicate and no file where another file
 * needs a directory. Paths compare case and normalisation insensitively,
 * because a macOS or Windows file system does.
 *
 * @param {unknown} list
 * @param {string} entry
 * @returns {{ path: string, content: string }[]}
 */
function checkFiles(list, entry) {
  if (!Array.isArray(list)) {
    throw new Error(`the files function of ${entry} must resolve to an array`)
  }
  /** @type {{ path: string, content: string }[]} */
  const files = []
  for (const item of list) {
    if (typeof item !== 'object' || item === null) {
      throw new Error(`the files function of ${entry} must return { path, content } strings`)
    }
    // Each property is read once, so a getter cannot hand the check one
    // value and the write another.
    const path = item.path
    const content = item.content
    if (typeof path !== 'string' || typeof content !== 'string') {
      throw new Error(`the files function of ${entry} must return { path, content } strings`)
    }
    checkPath(path)
    files.push({ path, content })
  }

  /** @param {string} path */
  const fold = (path) => path.normalize('NFC').toLowerCase()
  const paths = new Set()
  const directories = new Set()
  for (const file of files) {
    const key = fold(file.path)
    if (paths.has(key)) {
      throw new Error(`the file path ${JSON.stringify(file.path)} appears more than once`)
    }
    paths.add(key)
    const segments = key.split('/')
    for (let i = 1; i < segments.length; i++) {
      directories.add(segments.slice(0, i).join('/'))
    }
  }
  for (const file of files) {
    if (directories.has(fold(file.path))) {
      throw new Error(
        `the file path ${JSON.stringify(file.path)} is also the directory of another file`,
      )
    }
  }
  return files
}

/**
 * A path from the bundle is relative with "/" as separator and stays inside
 * the output directory.
 *
 * @param {string} path
 */
function checkPath(path) {
  const name = JSON.stringify(path)
  // An empty path is one empty segment and fails the segment check below.
  if (path.includes('\0')) {
    throw new Error(`the file path ${name} has a NUL byte`)
  }
  if (path.includes('\\')) {
    throw new Error(`the file path ${name} has a backslash`)
  }
  if (path.startsWith('/') || /^[A-Za-z]:/.test(path)) {
    throw new Error(`the file path ${name} must be relative`)
  }
  for (const segment of path.split('/')) {
    if (segment === '' || segment === '.' || segment === '..') {
      throw new Error(`the file path ${name} has an empty, "." or ".." segment`)
    }
  }
}

/**
 * Walks the path inside the output directory and throws on a symbolic link,
 * on a file where a directory belongs and on a directory where the file
 * belongs. With create, it makes the missing directories on the way.
 *
 * @param {string} outDir
 * @param {string} path
 * @param {boolean} create
 */
function checkTarget(outDir, path, create) {
  const name = JSON.stringify(path)
  const segments = path.split('/')
  let current = outDir
  for (let i = 0; i < segments.length; i++) {
    current = join(current, segments[i])
    const last = i === segments.length - 1
    /** @type {import('node:fs').Stats | undefined} */
    let stat
    try {
      stat = lstatSync(current)
    } catch (error) {
      if (/** @type {NodeJS.ErrnoException} */ (error).code !== 'ENOENT') throw error
    }
    if (stat === undefined) {
      if (!create) return
      if (!last) mkdirSync(current)
      continue
    }
    if (stat.isSymbolicLink()) {
      throw new Error(`the file path ${name} runs through a symbolic link at ${current}`)
    }
    if (last && stat.isDirectory()) {
      throw new Error(`the file path ${name} is a directory at ${current}`)
    }
    if (!last && !stat.isDirectory()) {
      throw new Error(
        `the file path ${name} needs ${current} to be a directory, it is not a directory`,
      )
    }
  }
}

/**
 * Writes the file without following a symbolic link at its own name, a
 * second guard behind checkTarget for a link created in between.
 *
 * @param {string} file
 * @param {string} content
 */
function writeNoFollow(file, content) {
  const flags =
    constants.O_WRONLY | constants.O_CREAT | constants.O_TRUNC | (constants.O_NOFOLLOW ?? 0)
  const fd = openSync(file, flags, 0o644)
  try {
    writeFileSync(fd, content)
  } finally {
    closeSync(fd)
  }
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

  if (args.out && args.outDir) {
    process.stderr.write(`use either --out or --out-dir, not both\n\n${USAGE}`)
    return 1
  }

  if (args.outDir && args.placeholder !== undefined) {
    process.stderr.write(`--placeholder applies to --out only\n\n${USAGE}`)
    return 1
  }

  if (!args.entry || !args.template || !(args.out || args.outDir)) {
    process.stderr.write(`--entry, --template and --out or --out-dir are all required\n\n${USAGE}`)
    return 1
  }

  try {
    if (args.outDir) {
      await prerenderFiles({ entry: args.entry, template: args.template, outDir: args.outDir })
    } else {
      await prerender({
        entry: args.entry,
        template: args.template,
        out: /** @type {string} */ (args.out),
        placeholder: args.placeholder,
      })
    }
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
