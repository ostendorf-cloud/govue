// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The search of the proof of work, kept free of every import so the Web Worker
 * pulls in nothing else: the worker bundle holds this file and its own
 * message handler, no client, no Vue and no network code.
 *
 * The hash input is the salt string followed by the number in decimal, nothing
 * in between. The salt ends with "&", which is the boundary (pow package of the
 * Go module, pow/testdata/vectors.json).
 */

/** The largest number the Go pow package accepts as a difficulty, pow.MaxNumberLimit. */
export const MAX_NUMBER_LIMIT = 1_000_000_000

/** How many candidates one batch hashes at once. */
const BATCH = 512

const encoder = new TextEncoder()

function hexToBytes(hex: string): Uint8Array {
  const bytes = new Uint8Array(hex.length / 2)
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = Number.parseInt(hex.slice(i * 2, i * 2 + 2), 16)
  }
  return bytes
}

function equal(digest: ArrayBuffer, expected: Uint8Array): boolean {
  const view = new Uint8Array(digest)
  for (let i = 0; i < expected.length; i++) {
    if (view[i] !== expected[i]) {
      return false
    }
  }
  return true
}

/**
 * solve searches 0 to maxNumber for the smallest number whose sha256(salt +
 * decimal(number)) equals challenge. It returns null when none matches, or
 * when the challenge is not 64 lower case hex digits or maxNumber is not a
 * whole number in range.
 */
export async function solve(
  challenge: string,
  salt: string,
  maxNumber: number,
): Promise<number | null> {
  if (!/^[0-9a-f]{64}$/.test(challenge)) {
    return null
  }
  if (!Number.isSafeInteger(maxNumber) || maxNumber < 0 || maxNumber > MAX_NUMBER_LIMIT) {
    return null
  }
  const expected = hexToBytes(challenge)
  for (let start = 0; start <= maxNumber; start += BATCH) {
    const end = Math.min(start + BATCH - 1, maxNumber)
    const pending: Promise<ArrayBuffer>[] = []
    for (let n = start; n <= end; n++) {
      pending.push(crypto.subtle.digest('SHA-256', encoder.encode(salt + String(n))))
    }
    const digests = await Promise.all(pending)
    for (let i = 0; i < digests.length; i++) {
      if (equal(digests[i], expected)) {
        return start + i
      }
    }
  }
  return null
}
