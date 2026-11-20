// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The Web Worker that solves the proof of work off the main thread. It is a
 * same origin module file, so a `script-src 'self'` policy allows it, and it
 * never touches the network, a cookie or any storage: it answers the one
 * message it gets with a number, or with null.
 *
 * Nothing imports this file. lib/pow.ts starts it through
 * `new Worker(new URL('./pow.worker.ts', import.meta.url), { type: 'module' })`,
 * which is the form a bundler follows.
 */

import { solve } from './powSolve'

/** What lib/pow.ts posts to the worker. */
export interface PowWorkerRequest {
  challenge: string
  salt: string
  maxnumber: number
}

/** What the worker answers. number is null when no number solves the challenge. */
export interface PowWorkerResponse {
  number: number | null
}

self.onmessage = (event: MessageEvent<PowWorkerRequest>) => {
  const { challenge, salt, maxnumber } = event.data
  solve(challenge, salt, maxnumber).then(
    (number) => {
      self.postMessage({ number } satisfies PowWorkerResponse)
    },
    () => {
      self.postMessage({ number: null } satisfies PowWorkerResponse)
    },
  )
}
