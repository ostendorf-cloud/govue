// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Types for prerender.mjs, which is plain JavaScript so it runs from a
// consumer's node_modules. The test imports it through this file.

export const DEFAULT_PLACEHOLDER: string
export const USAGE: string

export interface Args {
  entry?: string
  template?: string
  out?: string
  placeholder?: string
  help: boolean
}

export function parseArgs(argv: string[]): Args
export function injectIntoTemplate(template: string, html: string, placeholder?: string): string
export function prerender(options: {
  entry: string
  template: string
  out: string
  placeholder?: string
}): Promise<void>
export function main(argv: string[]): Promise<number>
