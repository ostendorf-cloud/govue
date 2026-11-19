// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Types for check-legal.mjs, which is plain JavaScript so it runs from a
// consumer's node_modules. The test imports it through this file.

export const REQUIRED_PAGES: string[]
export const OPTIONAL_PAGE: string
export const DEFAULT_LANGUAGES: string[]
export const USAGE: string

export interface Args {
  dir?: string
  languages: string[]
  requireTerms: boolean
  help: boolean
}

export function anchorHrefs(html: string): string[]
export function linkTarget(href: string): string | undefined
export function parseArgs(argv: string[]): Args
export function checkLegal(options: {
  dir: string
  languages?: string[]
  requireTerms?: boolean
}): { errors: string[]; notes: string[] }
export function main(argv: string[]): number
