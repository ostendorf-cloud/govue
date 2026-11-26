// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * A fake of the Go contact handlers for tests, written from contact/handler.go
 * and pow/pow.go of the Go module, not from memory of what a server might do:
 *
 * - the body is decoded with unknown fields refused, and a refused body is
 *   400 invalid_argument with no details (so a client that sends a key the
 *   server does not know, such as `received`, fails here as it does against
 *   the real server),
 * - `language` is an optional string, a hint for the labels of the mail, and
 *   the fake records it like any other field of the request,
 * - `consent` is an optional boolean, and with `requireConsent` a missing or
 *   false one is the field error `consent: required` next to the others,
 * - the content type has to be application/json (415),
 * - the honeypot answers 200 {"ok":true} before any limit or check,
 * - the order is honeypot, client limit, fields, proof of work, mail ceiling,
 *   send,
 * - the proof of work is verified for real: the hash of salt + number, the
 *   signature, the expiry, the minimum age and the single use,
 * - every refusal is {"error":{"code","message","details"?}} with the status
 *   and top level code of handler.go.
 *
 * It is a fake and proves only itself. The e2e spec runs the same flows
 * against the real example server.
 */

import { createHash, createHmac, randomBytes } from 'node:crypto'

export interface FakeContactOptions {
  /** The clock in milliseconds. Default Date.now. */
  now?: () => number
  /** pow.Config.MaxNumber. Default 50, small so a test solves it at once. */
  maxNumber?: number
  /** The number every challenge hides. Default random. */
  secret?: number
  /** pow.Config.TTL in seconds. Default 600. */
  ttlSeconds?: number
  /** pow.Config.MinAge in seconds. Default 3. */
  minAgeSeconds?: number
  /** The submissions one client may make. Default 5, as DefaultClientBurst. */
  clientLimit?: number
  /** contact.Config.RequireConsent. Default false. */
  requireConsent?: boolean
}

export interface FakeRequest {
  url: string
  method: string
  headers: Record<string, string>
  /** The parsed JSON body, undefined when there is none. */
  body: unknown
  credentials: RequestCredentials | undefined
}

const TEST_KEY = Buffer.from('0a1b'.repeat(16), 'hex')
const AUDIENCE = 'example.test'
const BODY_KEYS = ['name', 'email', 'message', 'consent', 'language', 'website', 'pow']
const POW_KEYS = ['algorithm', 'challenge', 'number', 'salt', 'signature']

/** The limits of contact.Max*Runes. */
const MAX = { name: 200, email: 254, message: 5000 } as const

function sign(challenge: string): string {
  return createHmac('sha256', TEST_KEY)
    .update(`govue/pow\x00v1\x00${AUDIENCE}\x00${challenge}`)
    .digest('hex')
}

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

/** refusal is api.WriteError: one object under "error". */
export function refusal(
  status: number,
  code: string,
  message = 'An English server text.',
  details?: Record<string, string>,
  headers?: Record<string, string>,
): Response {
  return json(status, { error: { code, message, ...(details ? { details } : {}) } }, headers)
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function runes(value: string): number {
  return [...value].length
}

export class FakeContactServer {
  readonly requests: FakeRequest[] = []
  private readonly used = new Set<string>()
  private submissions = 0
  private forced: Array<() => Response> = []
  private readonly now: () => number
  private readonly maxNumber: number
  private readonly secret: number | undefined
  private readonly ttl: number
  private readonly minAge: number
  private readonly clientLimit: number
  protected readonly requireConsent: boolean

  constructor(options: FakeContactOptions = {}) {
    this.requireConsent = options.requireConsent ?? false
    this.now = options.now ?? Date.now
    this.maxNumber = options.maxNumber ?? 50
    this.secret = options.secret
    this.ttl = options.ttlSeconds ?? 600
    this.minAge = options.minAgeSeconds ?? 3
    this.clientLimit = options.clientLimit ?? 5
  }

  /** The POST requests, which are the submissions. */
  get submits(): FakeRequest[] {
    return this.requests.filter((r) => r.method === 'POST')
  }

  /** The GET requests, which are the challenges. */
  get challenges(): FakeRequest[] {
    return this.requests.filter((r) => r.method === 'GET')
  }

  /** Answers the next submission with this response instead of handling it. */
  refuseNext(make: () => Response): void {
    this.forced.push(make)
  }

  /** The fetch of createClient. */
  readonly fetch = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const headers = (init?.headers ?? {}) as Record<string, string>
    const method = init?.method ?? 'GET'
    const raw = typeof init?.body === 'string' ? init.body : undefined
    let body: unknown
    try {
      body = raw === undefined ? undefined : JSON.parse(raw)
    } catch {
      body = raw
    }
    this.requests.push({ url, method, headers, body, credentials: init?.credentials })
    return Promise.resolve(
      url.endsWith('/challenge') ? this.challenge(method) : this.submit(method, headers, raw),
    )
  }

  private challenge(method: string): Response {
    if (method !== 'GET') {
      return refusal(405, 'method_not_allowed', 'method not allowed')
    }
    const issued = Math.floor(this.now() / 1000)
    const salt = `${randomBytes(12).toString('hex')}?expires=${issued + this.ttl}&issued=${issued}&`
    const number = this.secret ?? Math.floor(Math.random() * (this.maxNumber + 1))
    const challenge = createHash('sha256').update(`${salt}${number}`).digest('hex')
    return json(200, {
      algorithm: 'SHA-256',
      challenge,
      maxnumber: this.maxNumber,
      salt,
      signature: sign(challenge),
    })
  }

  private submit(
    method: string,
    headers: Record<string, string>,
    raw: string | undefined,
  ): Response {
    if (method !== 'POST') {
      return refusal(405, 'method_not_allowed', 'method not allowed')
    }
    const forced = this.forced.shift()
    if (forced) {
      return forced()
    }
    const contentType = Object.entries(headers).find(
      ([k]) => k.toLowerCase() === 'content-type',
    )?.[1]
    if (contentType?.split(';')[0]?.trim() !== 'application/json') {
      return refusal(415, 'unsupported_media_type', 'content type must be application/json')
    }
    const notValid = () => refusal(400, 'invalid_argument', 'body is not a valid submission')
    let sub: unknown
    try {
      sub = JSON.parse(raw ?? '')
    } catch {
      return notValid()
    }
    if (!isObject(sub) || Object.keys(sub).some((key) => !this.bodyKeys.includes(key))) {
      return notValid()
    }
    for (const key of ['name', 'email', 'message', 'language', 'website']) {
      if (key in sub && typeof sub[key] !== 'string') {
        return notValid()
      }
    }
    if ('consent' in sub && typeof sub.consent !== 'boolean') {
      return notValid()
    }
    const pow = sub.pow
    if (pow !== undefined && pow !== null) {
      if (
        !isObject(pow) ||
        Object.keys(pow).some((key) => !POW_KEYS.includes(key)) ||
        typeof pow.number !== 'number'
      ) {
        return notValid()
      }
    }
    if (typeof sub.website === 'string' && sub.website !== '') {
      // The honeypot answers like a success and does nothing.
      return json(200, { ok: true })
    }
    this.submissions++
    if (this.submissions > this.clientLimit) {
      return refusal(429, 'too_many_requests', 'too many requests', undefined)
    }
    const fields = this.validate(sub)
    if (fields) {
      return refusal(400, 'invalid_argument', 'some fields are invalid', fields)
    }
    if (pow === undefined || pow === null) {
      return refusal(400, 'pow_required', 'proof of work is required')
    }
    return this.verify(pow)
  }

  /** The keys the decoder knows. Any other key is refused as unknown. */
  protected readonly bodyKeys: readonly string[] = BODY_KEYS

  /** The field check of the handler, `{field: code}` or undefined. */
  protected validate(sub: Record<string, unknown>): Record<string, string> | undefined {
    const out: Record<string, string> = {}
    for (const field of ['name', 'email', 'message'] as const) {
      const value = typeof sub[field] === 'string' ? sub[field].trim() : ''
      if (value === '') {
        out[field] = 'required'
      } else if (runes(value) > MAX[field]) {
        out[field] = 'too_long'
      } else if (field === 'email') {
        if ([...value].some((c) => c.charCodeAt(0) > 127)) {
          out[field] = 'non_ascii_email'
        } else if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(value)) {
          out[field] = 'invalid'
        }
      }
    }
    if (this.requireConsent && sub.consent !== true) {
      out.consent = 'required'
    }
    return Object.keys(out).length > 0 ? out : undefined
  }

  private verify(pow: Record<string, unknown>): Response {
    const invalid = () => refusal(400, 'pow_invalid', 'the proof of work is invalid')
    const { algorithm, challenge, number, salt, signature } = pow as {
      algorithm: string
      challenge: string
      number: number
      salt: string
      signature: string
    }
    if (
      algorithm !== 'SHA-256' ||
      !/^[0-9a-f]{64}$/.test(String(challenge)) ||
      !/^[0-9a-f]{64}$/.test(String(signature)) ||
      typeof salt !== 'string' ||
      salt === '' ||
      salt.length > 128 ||
      !Number.isInteger(number) ||
      number < 0 ||
      number > this.maxNumber ||
      signature !== sign(challenge) ||
      createHash('sha256').update(`${salt}${number}`).digest('hex') !== challenge
    ) {
      return invalid()
    }
    const match = /^[0-9a-f]{24}\?expires=(0|[1-9]\d*)&issued=(0|[1-9]\d*)&$/.exec(salt)
    if (!match) {
      return invalid()
    }
    const now = Math.floor(this.now() / 1000)
    if (now >= Number(match[1])) {
      return refusal(400, 'pow_expired', 'the challenge expired')
    }
    if (now - Number(match[2]) < this.minAge) {
      return refusal(400, 'pow_too_fast', 'the challenge was solved too fast')
    }
    if (this.used.has(challenge)) {
      return refusal(400, 'pow_replay', 'the challenge was used before')
    }
    this.used.add(challenge)
    return json(200, { ok: true })
  }
}
