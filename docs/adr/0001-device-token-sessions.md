---
status: proposed
date: 2026-11-17
---

# Device token sessions for native apps

## Context and Problem Statement

- A product built on govue ships a native app: its Vue frontend, bundled and wrapped with
  Capacitor, next to the web app.
- The web signs in with an opaque `__Host-session` cookie, HttpOnly and `SameSite=Lax`, plus a
  CSRF token derived from the session id.
- The bundled app runs on its own origin, not on the product domain. Cookies across that
  boundary are third party cookies, and webviews do not carry them reliably.
- The app therefore needs a credential it sends itself. How that credential looks, lives, rotates
  and ends is a security boundary and expensive to change once apps are in the stores.
- The web cookie session and its CSRF protection must stay exactly as they are.

## Decision Drivers

- Revocable before expiry: sign out, a lost phone and a password change take effect at once.
- Theft is bounded in time and, where possible, detected.
- No weakening of CSRF protection for any request that a browser authenticates by cookie.
- Standard library only, and `csrf` keeps not importing `session`.
- Additive: `session.Store` keeps its four methods, so no implementer breaks.
- Works with the bundled origins Capacitor uses by default.

## Considered Options

- Opaque device session, stored server side by hash, sent as a bearer token.
- Self contained signed token (JWT).
- Short lived access token plus long lived refresh token.
- The existing cookie session inside the webview.

## Decision Outcome

Chosen option: an opaque device session stored server side by hash and sent as a bearer token.
It is the only option that is revocable on the next request without a second store, and it
reuses the reasoning that already rejected a signed cookie for the web session.

### Token and record

- The token is a fixed recognisable prefix plus 32 random bytes, base64url encoded. The entropy
  equals `session.NewID`.
- Fake example: `Authorization: Bearer example_AAAAexampleexampleexampleexampleexampleAAAA`.
- The token travels only in the `Authorization` header. Never in a cookie, a query string or a
  URL.
- The store holds `SHA-256(token)`, never the token. 256 random bits need no salt and no slow KDF.
- Lookup is by hash, so no secret comparison happens on the request path.
- Record: `DeviceSession{ID, Subject, Name, CreatedAt, LastSeen, ExpiresAt, TokenHash,
  PreviousHash, PreviousValidUntil}`.
- `ID` is a separate random handle for listing and revoking. It is never a credential.
- `Name` is a bounded display label from the client. It is never trusted for anything.
- Device sessions live behind a new `DeviceStore` interface, separate from `Store`.

### Issue, lifetime and rotation

- `POST /api/v1/auth/token` takes the same credentials as the normal sign in. It does not
  exchange a cookie session.
- The credential check is a callback. The handlers live in `session`.
- Idle lifetime 30 days, absolute lifetime 180 days. Both are checked server side and both bounds
  are exclusive, as for the cookie session.
- The client rotates at `POST /api/v1/auth/token/rotate` on every app start and at least every 24
  hours.
- Rotation issues a new token, keeps `ID`, and never moves `ExpiresAt`.
- The previous hash stays valid for a grace window of 60 seconds, so concurrent requests do not
  sign the app out.
- Reuse detection: the previous token presented after its grace window revokes the whole device
  session. Both holders are signed out.

### Revocation

- The owner lists their own devices and revokes one by `DELETE /api/v1/auth/devices/{id}`.
- A device id of another subject is refused and reveals nothing about its existence.
- The device signs itself out, which deletes its own record.
- Reuse detection revokes the device session, see above.
- `EndAll` ends every cookie session and every device session of the subject when a device store
  is configured. A password change calls it and revokes every device.
- Expiry removes a device session like any other revocation.

### Interface to local accounts (#104)

- `auth` does not import `session`. Its handlers take `StartSession` and `EndAllSessions`
  callbacks that the application wires to `session`.
- `Identity.ID` is the value in `Session.Subject` and `DeviceSession.Subject`. It is opaque and
  stable, so one `EndAll` reaches every sign in path.

### CSRF boundary

- Predicate: a request is a bearer request when its `Authorization` header has the auth scheme
  `Bearer`, compared case insensitively as RFC 9110 section 11.1 defines.
- The predicate looks at the header only, never at the token's validity.
- A bearer request skips both halves of the CSRF check, origin and token. Every other request
  keeps both.
- A bearer request authenticates by the token only. An invalid, expired or revoked token is 401.
- There is no fallback from an invalid bearer token to the cookie. Otherwise a junk header would
  skip CSRF and ride the cookie.
- With a cookie and a bearer token on one request, the bearer token wins. The cookie is ignored
  for authentication and not cleared.
- A cross site page cannot attach an `Authorization` header without a CORS preflight, and
  browsers never attach a bearer token by themselves.
- `csrf` and `session` apply the same predicate. A contract test in `example/` sends cookie,
  bearer, both, junk bearer and neither through the real chain and pins each outcome.

### Responses and CORS

- Every response to a bearer request carries `Cache-Control: no-store`.
- The access log records no request header, so `Authorization` is never logged.
- App mode adds `Authorization` to the allowed CORS headers.
- The preflight is answered inside the CORS middleware, before rate limit, CSRF and any session
  read. That order stays.
- Custom scheme origins are an explicit opt in: a list of exact origins such as
  `capacitor://localhost`. No wildcard, no scheme wide rule and never `null`.
- Capacitor's defaults are hostname `localhost`, scheme `capacitor` on iOS and `https` on
  Android. Checked against <https://capacitorjs.com/docs/config> on 2026-11-17.
- The resulting origins are `capacitor://localhost` on iOS and `https://localhost` on Android.
  Only the iOS one needs the custom scheme opt in.

### Consequences

- Good: a device ends on the next request after any revocation path, including a password change.
- Good: a store dump yields only hashes, and a leaked token is found by its prefix.
- Good: the web session, its cookie and its CSRF check do not change.
- Good: every change is additive and off until a product configures a device store.
- Bad: every bearer request is a store lookup, like every cookie request today.
- Bad: the in memory store signs every app out on each deploy. App mode needs a shared
  `DeviceStore` that the product writes.
- Bad: a script running in the webview can read the token, unlike an HttpOnly cookie. Native secure
  storage protects it at rest only.
- Bad: the CSRF boundary depends on two packages agreeing on one predicate. Drift reopens CSRF,
  and the contract test is the only guard.
- Bad: only one previous hash is kept, so a token two rotations old is a plain 401 without a
  theft signal.
- Bad: a custom scheme in CORS widens a validator that is strict on purpose.

### Confirmation

- A reviewer for security boundaries signs this ADR off before any code pull request of #117
  opens. Status moves to `accepted` then.
- Tests pin issue, rotation, grace, reuse detection, both expiry bounds, revocation, a stolen
  token after revocation, and the cookie and bearer cross product.

## Pros and Cons of the Options

### Opaque device session by hash

- Good: revocable on the next request, same model as the cookie session.
- Good: rotation and reuse detection need nothing beyond the record.
- Bad: needs a shared store for more than one replica.

### Self contained signed token (JWT)

- Good: no store lookup per request.
- Bad: not revocable before expiry without a deny list. A deny list is a store lookup per
  request, which removes the only advantage.
- Bad: a signing key to manage and rotate, and a parser with a history of algorithm confusion.
- Rejected for the reason `session` already rejected a signed cookie.

### Access token plus refresh token

- Good: a stolen access token is short lived, and refresh reuse detection is an established
  pattern.
- Bad: with a self contained access token, revocation lags by the access lifetime.
- Bad: with an opaque access token, it is the chosen option with a second token and more client
  state.
- Rejected: the chosen option keeps the reuse detection idea on a single token.

### Cookie session in the webview

- Good: no new credential, CSRF protection as on the web.
- Bad: the bundled origin makes the cookie a third party cookie, and webviews do not carry those
  reliably.
- Bad: the `SameSite=Lax` cookie is not sent on cross site fetches, so it would have to be
  weakened.
- Rejected: it fails in practice and needs a weaker cookie for every user.

## More Information

### Theft cases

- Stolen and used before the next rotation: valid until the app rotates. Then one holder presents
  a stale token and reuse detection revokes the device.
- Stolen and used after revocation: the hash is gone, the request is 401, nothing falls back to
  a cookie.
- Stolen from a store dump: only hashes, which are not usable as tokens.
- Leaked into a log or a URL: the token never travels in a URL, and `Authorization` is never
  logged.

### Open questions for the security review

- The numbers: idle 30 days, absolute 180 days, rotation every 24 hours, grace 60 seconds.
- Plain SHA-256 or an HMAC with a server key. HMAC adds a key to manage for little gain at 256
  bits.
- The exact prefix string. It helps secret scanning and also tells an observer which library is
  behind it.
- A rotate call that presents the previous token inside the grace window: rotate again, or refuse.
- A request with more than one `Authorization` header: refuse with 400, or treat it as a bearer
  request that fails.
- Whether the app origins receive `Access-Control-Allow-Credentials`. Bearer mode sends no
  cookie, so they may not need it.
- Whether a password change keeps the device that made it. The default revokes all of them.
- Whether the web cookie session ids should be stored hashed too. Out of scope here.

### Related

- Concept doc: `docs/concepts/govue.md`, sections "Sessions keep their state in a store" and
  "CORS is an explicit allow-list, never a wildcard".
- Issue #117, and #104 for local accounts.
