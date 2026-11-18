// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// A full passkey ceremony in a real Chromium with a CDP virtual authenticator.
//
// The server half (govue #175) is not part of this suite. page.route answers
// the contract's routes with a small in-test mock that checks every request
// against the contract: method, content type, body fields, and the
// challenge cookie. The mock checks the registration only for shape and the
// client data (type, challenge, origin). It does NOT verify an attestation and
// accepts any. The login it verifies for real with Node's crypto: the RP id
// hash, the user presence and verification flags, the user handle and the
// signature over authenticatorData plus the hash of clientDataJSON, with the
// public key the registration sent. The mock is no substitute for the Go
// handlers: an e2e against the real server follows when #175 is merged.
import { createHash, randomBytes, verify } from 'node:crypto'

import { expect, test, type CDPSession, type Page, type Route } from '@playwright/test'

const ORIGIN = 'http://localhost:4173'
const RP_ID = 'localhost'
const COOKIE = 'govue-passkey'
const API = '**/api/v1/account/passkeys/**'

const b64u = (buffer: Buffer): string => buffer.toString('base64url')
const fromB64u = (text: string): Buffer => Buffer.from(text, 'base64url')

interface Mock {
  /** Every request the mock answered, as "METHOD path". */
  seen: string[]
  /** Bodies of the finish requests, as received. */
  finished: Record<string, unknown>[]
  /** Make the next login finish answer the contract's refusal. */
  refuseLogin: boolean
}

function problem(message: string): never {
  throw new Error(`contract violation: ${message}`)
}

/** json answers like the api package: status, JSON body, and the cookie. */
async function reply(route: Route, status: number, body: unknown, cookie?: string) {
  await route.fulfill({
    status,
    contentType: 'application/json',
    headers: cookie ? { 'Set-Cookie': cookie } : {},
    body: JSON.stringify(body),
  })
}

async function installMock(page: Page): Promise<Mock> {
  const mock: Mock = { seen: [], finished: [], refuseLogin: false }
  let challenge: Buffer | undefined
  let userId: Buffer | undefined
  let publicKey: Buffer | undefined
  let credentialId: string | undefined
  let name = ''

  await page.route(API, async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace('/api/v1/account/passkeys', '')
    mock.seen.push(`${request.method()} ${path}`)
    const headers = await request.allHeaders()
    if (request.method() !== 'POST') {
      problem(`method ${request.method()}`)
    }
    if (headers['content-type'] !== 'application/json') {
      problem('content type')
    }
    const body = request.postDataJSON() as Record<string, unknown>
    const cookie = `${COOKIE}=${randomBytes(8).toString('hex')}; Path=/; HttpOnly; SameSite=Strict`

    if (path === '/register/begin' || path === '/login/begin') {
      if (Object.keys(body).length !== 0) {
        problem('begin takes an empty object')
      }
      challenge = randomBytes(32)
      if (path === '/register/begin') {
        userId = randomBytes(16)
        return reply(
          route,
          200,
          {
            public_key: {
              rp: { id: RP_ID, name: 'govue e2e' },
              user: { id: b64u(userId), name: 'person@example.com', displayName: 'Person' },
              challenge: b64u(challenge),
              pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
              authenticatorSelection: { residentKey: 'required', userVerification: 'required' },
              attestation: 'none',
              excludeCredentials: [],
              timeout: 60000,
            },
          },
          cookie,
        )
      }
      return reply(
        route,
        200,
        {
          public_key: {
            rpId: RP_ID,
            challenge: b64u(challenge),
            userVerification: 'required',
            allowCredentials: [],
            timeout: 60000,
          },
        },
        cookie,
      )
    }

    if (path === '/register/finish' || path === '/login/finish') {
      // The challenge cookie of the begin answer has to travel back.
      if (!headers.cookie?.includes(`${COOKIE}=`)) {
        problem('the challenge cookie did not travel')
      }
      const allowed = path === '/register/finish' ? ['credential', 'name'] : ['credential']
      for (const key of Object.keys(body)) {
        if (!allowed.includes(key)) {
          problem(`unknown field ${key}`)
        }
      }
      const credential = body.credential as {
        id: string
        type: string
        response: Record<string, string>
      }
      if (typeof credential?.id !== 'string' || credential.type !== 'public-key') {
        problem('credential shape')
      }
      mock.finished.push(body)
      const clientData = JSON.parse(fromB64u(credential.response.clientDataJSON).toString()) as {
        type: string
        challenge: string
        origin: string
      }
      const wanted = path === '/register/finish' ? 'webauthn.create' : 'webauthn.get'
      const used = challenge
      challenge = undefined // used once, like the server's challenge
      if (!used || clientData.type !== wanted || clientData.challenge !== b64u(used)) {
        return reply(
          route,
          path === '/register/finish' ? 400 : 401,
          {
            error: {
              code: path === '/register/finish' ? 'invalid_passkey' : 'invalid_credentials',
              message: 'refused',
            },
          },
          `${COOKIE}=; Path=/; Max-Age=0`,
        )
      }
      if (clientData.origin !== ORIGIN) {
        problem('origin')
      }

      if (path === '/register/finish') {
        // The attestation is accepted as it is: this mock does not verify it.
        publicKey = fromB64u(credential.response.publicKey)
        credentialId = credential.id
        name = typeof body.name === 'string' ? body.name : ''
        return reply(
          route,
          201,
          {
            passkey: {
              id: credential.id,
              name,
              created_at: '2026-11-18T10:00:00Z',
              last_used_at: null,
            },
          },
          `${COOKIE}=; Path=/; Max-Age=0`,
        )
      }

      if (mock.refuseLogin) {
        mock.refuseLogin = false
        return reply(
          route,
          401,
          { error: { code: 'invalid_credentials', message: 'the passkey could not be verified' } },
          `${COOKIE}=; Path=/; Max-Age=0`,
        )
      }
      // A real check of the assertion with the key the registration sent.
      const authData = fromB64u(credential.response.authenticatorData)
      const rpIdHash = createHash('sha256').update(RP_ID).digest()
      const flags = authData[32]
      const ok =
        credential.id === credentialId &&
        publicKey !== undefined &&
        authData.subarray(0, 32).equals(rpIdHash) &&
        (flags & 0x01) !== 0 &&
        (flags & 0x04) !== 0 &&
        userId !== undefined &&
        fromB64u(credential.response.userHandle).equals(userId) &&
        verify(
          'sha256',
          Buffer.concat([
            authData,
            createHash('sha256').update(fromB64u(credential.response.clientDataJSON)).digest(),
          ]),
          { key: publicKey, format: 'der', type: 'spki' },
          fromB64u(credential.response.signature),
        )
      if (!ok) {
        return reply(
          route,
          401,
          { error: { code: 'invalid_credentials', message: 'the passkey could not be verified' } },
          `${COOKIE}=; Path=/; Max-Age=0`,
        )
      }
      return reply(route, 200, { login: 'person@example.com' }, `${COOKIE}=; Path=/; Max-Age=0`)
    }
    return problem(`unknown route ${path}`)
  })
  return mock
}

async function virtualAuthenticator(
  page: Page,
  isUserVerified: boolean,
): Promise<{ cdp: CDPSession; authenticatorId: string }> {
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified,
      automaticPresenceSimulation: true,
    },
  })
  return { cdp, authenticatorId }
}

test.describe('a passkey ceremony with a virtual authenticator', () => {
  test('enrols a passkey and then logs in with it', async ({ page }) => {
    const mock = await installMock(page)
    const { cdp, authenticatorId } = await virtualAuthenticator(page, true)
    await page.goto('/passkey.html')

    await page.getByRole('button', { name: 'Add a passkey' }).click()
    await expect(page.locator('#result')).toHaveText('enrolled:Laptop')

    const stored = await cdp.send('WebAuthn.getCredentials', { authenticatorId })
    // One resident credential, held by the authenticator and not by the page.
    expect(stored.credentials).toHaveLength(1)
    expect(stored.credentials[0].isResidentCredential).toBe(true)

    await page.getByRole('button', { name: 'Log in with a passkey' }).click()
    await expect(page.locator('#result')).toHaveText('login:person@example.com')

    expect(mock.seen).toEqual([
      'POST /register/begin',
      'POST /register/finish',
      'POST /login/begin',
      'POST /login/finish',
    ])
    expect(mock.finished[0]).toMatchObject({ name: 'Laptop' })
    // A successful ceremony leaves no error text.
    await expect(page.getByText('could not')).toHaveCount(0)
  })

  test('treats a dismissed prompt as a quiet state', async ({ page }) => {
    const mock = await installMock(page)
    // An authenticator that cannot verify the user: the browser rejects the
    // ceremony with NotAllowedError, as it does when a person dismisses it.
    await virtualAuthenticator(page, false)
    await page.goto('/passkey.html')

    await page.getByRole('button', { name: 'Add a passkey' }).click()

    await expect(page.locator('#result')).toHaveText('cancelled')
    await expect(page.getByText('The passkey prompt was closed. Nothing changed.')).toBeVisible()
    await expect(page.getByRole('alert')).toHaveCount(0)
    expect(mock.seen).toEqual(['POST /register/begin'])
  })

  test('shows the refusal of a login', async ({ page }) => {
    const mock = await installMock(page)
    await virtualAuthenticator(page, true)
    await page.goto('/passkey.html')
    await page.getByRole('button', { name: 'Add a passkey' }).click()
    await expect(page.locator('#result')).toHaveText('enrolled:Laptop')

    mock.refuseLogin = true
    await page.getByRole('button', { name: 'Log in with a passkey' }).click()

    await expect(page.getByText('The passkey could not be verified.')).toBeVisible()
    await expect(page.locator('#result')).toHaveText('enrolled:Laptop')
  })
})
