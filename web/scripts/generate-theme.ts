// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// A thin wrapper around ../src/theme/generate.ts. No business logic of its
// own: parse two flags, read a file, call generateTheme, write the result.
// Everything that decides whether a theme is correct lives in the module
// this imports, which already has its own tests.
//
// Run directly with node (Node 24+/26 strips TypeScript types natively, no
// build step and no extra dependency): every relative import here and
// throughout src/theme/*.ts carries an explicit .ts extension, because
// native type stripping does real ESM resolution and does not guess an
// extension the way a bundler would.

import { readFileSync, writeFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

import { generateTheme } from '../src/theme/generate.ts'

export const USAGE = `Usage: generate-theme --config <seeds.json> --out <theme.css>

Generates a complete, WCAG 2.2 AA theme CSS file from a seed colour
configuration. See README.md for the config file's shape.

Options:
  --config <path>  Path to a JSON file matching the ThemeSeedConfig shape (required)
  --out <path>      Path to write the generated CSS file to (required)
  --help             Show this message
`

interface Args {
  config?: string
  out?: string
  help: boolean
}

// A bare "--" is tolerated and skipped wherever it appears, not just once at
// the front: npm consumes the "script -- args" separator itself, but pnpm
// and yarn forward it straight into argv, so a consumer invoking this script
// through any of them, or reflexively typing it out of habit, must not hit
// "unknown argument" for a token that carries no meaning of its own here.
export function parseArgs(argv: string[]): Args {
  const args: Args = { help: false }
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i]
    if (flag === '--') {
      continue
    } else if (flag === '--help' || flag === '-h') {
      args.help = true
    } else if (flag === '--config') {
      args.config = argv[++i]
    } else if (flag === '--out') {
      args.out = argv[++i]
    } else {
      throw new Error(`unknown argument: ${flag}`)
    }
  }
  return args
}

export function main(): number {
  let args: Args
  try {
    args = parseArgs(process.argv.slice(2))
  } catch (error) {
    process.stderr.write(`${(error as Error).message}\n\n${USAGE}`)
    return 1
  }

  if (args.help) {
    process.stdout.write(USAGE)
    return 0
  }

  if (!args.config || !args.out) {
    process.stderr.write(`--config and --out are both required\n\n${USAGE}`)
    return 1
  }

  let raw: string
  try {
    raw = readFileSync(args.config, 'utf8')
  } catch (error) {
    process.stderr.write(`could not read ${args.config}: ${(error as Error).message}\n`)
    return 1
  }

  let configInput: unknown
  try {
    configInput = JSON.parse(raw)
  } catch (error) {
    process.stderr.write(`${args.config} is not valid JSON: ${(error as Error).message}\n`)
    return 1
  }

  const result = generateTheme(configInput)
  if (!result.ok) {
    process.stderr.write('could not generate a theme:\n')
    for (const message of result.errors) {
      process.stderr.write(`  - ${message}\n`)
    }
    return 1
  }

  writeFileSync(args.out, result.css)
  // generateTheme already validated configInput, so it carries a name by
  // this point; read it back only to say what was written, not to decide
  // anything.
  const name = (configInput as { name?: unknown }).name
  process.stdout.write(`generated theme "${String(name)}", wrote ${args.out}\n`)
  for (const message of result.warnings) {
    process.stderr.write(`warning: ${message}\n`)
  }
  return 0
}

// Only run as a side effect of being executed directly, not of being
// imported, so generate-theme.test.ts can exercise parseArgs and main() on
// their own without triggering a second, unwanted CLI run against this
// process's real argv.
if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  process.exitCode = main()
}
