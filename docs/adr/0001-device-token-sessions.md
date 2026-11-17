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

- The token is a prefix, 32 random bytes base64url encoded, and a short checksum.
- The product configures the prefix for secret scanning. The default is neutral and does not
  name the library, like the default cookie name.
- The checksum rejects a malformed token before any store lookup.
- Fake example: `Authorization: Bearer example_AAAAexampleexampleexampleexampleexampleAAAA_x1y2`.
- The token travels only in the `Authorization` header. Never in a cookie, a query parameter, a
  form field or a URL.
- The store holds plain `SHA-256(token)`. 256 random bits need no salt, no slow KDF and no HMAC.
- Record: `DeviceSession{ID, Subject, Name, CreatedAt, LastSeen, ExpiresAt, CurrentHash,
  PendingHash, PendingNonce, Retired}`.
- `Retired` holds at most the last four retired hashes, each with its grace end. The oldest is
  dropped first.
- `ID` is a random handle for listing and revoking, never a credential. `Name` is a bounded
  display label, never trusted.

### DeviceStore contract

- Device sessions live behind a new `DeviceStore` interface, separate from `Store`.
- Every operation is atomic on one record. No operation writes a whole record back.
- Lookup matches a hash against the current, pending and retired hashes and says which matched.
  A store needs an index over up to six hashes per record.
- Rotate and confirm are conditional swaps on the record state the caller read: current hash,
  pending hash and nonce. Only a truly concurrent second caller loses. A later caller reads the
  new state and follows the table below.
- Touch updates only `LastSeen`. No update recreates a missing record.
- Revoke by id takes the subject in the same call and removes nothing of another subject.
- Delete by subject always wins over a concurrent touch, rotate or confirm.
- Each subject has a device cap. Issuing past it evicts that subject's least recently seen
  device, never another subject's.
- A full memory store refuses to issue for everyone. That global lever is acceptable for a
  single replica memory store. Open sign up of #104 needs its own bound before it meets it.
- A conformance suite, like `account/accounttest` of #104, runs these rules and their races
  against `MemoryDeviceStore` and any product store. It covers lookup through every slot.

### Issue, lifetime and rotation

- `POST /api/v1/auth/token` takes the same credentials as the normal sign in. It does not
  exchange a cookie session, and it neither reads nor sets a cookie.
- The credential check is a callback. The handlers live in `session`.
- `auth` of #104 does not import `session`. It uses the `StartSession` and `EndAllSessions`
  callbacks.
- `Identity.ID` goes into `Session.Subject` and `DeviceSession.Subject`. One `EndAll` reaches
  every sign in path only if a consumer maps an OIDC `sub` to `Identity.ID` before
  `Manager.Start`. The `Subject` doc comment will say so.
- An account that only signs in through OIDC cannot use the token endpoint. That is out of scope.
- Idle lifetime 30 days, absolute 180 days, both exclusive bounds, checked server side.
- The client rotates at `POST /api/v1/auth/token/rotate` at least every 24 hours and on app
  start. Each rotate attempt carries a random client nonce.
- Rotation keeps `ID` and never moves `ExpiresAt`. A new token is pending until its first use.
- States: Stable has no pending token, Pending has one. Retiring a hash adds it to `Retired`.
  Retired hashes survive every confirm and rotate.
- Three configurable durations, all server side:
  - Grace, default five minutes: how long a retired current hash still authenticates. It only
    covers a token its holder already had.
  - Minimum interval, default one hour: the least time between two confirmed rotations of one
    device.
  - Rotate timeout, default 30 seconds: how long after issue a same nonce retry may overwrite
    the pending token.

| State | Token presented | Request | Rotate |
| --- | --- | --- | --- |
| Stable | Current | Authenticated, touch | Minimum interval passed: new pending with nonce, state Pending. Else: refused, no revocation |
| Pending | Current | Authenticated, pending kept | Same nonce within the rotate timeout: pending overwritten, not retired. Else: pending retired without grace, new pending |
| Pending | Pending | Confirm: current retired with grace, pending becomes current, state Stable | Confirm as for a request, no new pending before the minimum interval |
| Any | Retired, inside grace | Authenticated | Refused, no revocation |
| Any | Retired, after grace | Device session revoked | Device session revoked |
| Any | Unknown, expired or malformed | 401 | 401 |

- A lost rotate response costs nothing. A retry with the same nonce within the rotate timeout
  overwrites the pending token. A later one retires it.
- The app never presents a retired pending token, because it falls back to its current token
  first. One in use means a second holder rotated, so it revokes the device.
- The minimum interval keeps app launches from draining `Retired`. In normal use a retired token
  stays detectable for at least four intervals.

### Client contract

- One context of the app owns rotation, and its rotate calls are single flight. Other contexts
  read the token at send time and never rotate.
- The client stores a new token before it sends any further request. It keeps the current token
  until the pending one has succeeded once.
- On a 401 for a pending token that has not succeeded yet, the client falls back to its current
  token and rotates again with a new nonce. A second 401 clears the stored token.
- A rotate refused for the minimum interval is quiet. The app keeps its current token, and it
  rotates on start only when the interval has passed.
- The `Authorization` header is built at send time from the stored token, never kept in a queue.
- Token issue and rotate calls have no transport level retry and a timeout below the rotate
  timeout. A retry is a new call with the same nonce.
- Every bearer request has a client timeout well below the grace, so a delayed request never
  arrives after it.
- How a Capacitor background runner or an app extension shares the token store is not verified.

### Revocation

- The owner lists their own devices and revokes one by `DELETE /api/v1/auth/devices/{id}`. An id
  of another subject is refused and reveals nothing.
- The device signs itself out, which deletes its own record.
- Reuse detection and expiry remove the record.
- `EndAll` ends every cookie and device session of the subject. A password change calls it and
  revokes every device.

### Bearer requests and the CSRF boundary

- One predicate in `internal/` classifies a request. `session` and `csrf` both import it, so
  `csrf` still never imports `session`.
- It has three answers: none, bearer and malformed. It counts every value of `Authorization`.
- Bearer: exactly one value whose auth scheme is `Bearer`, compared case insensitively (RFC 9110
  section 11.1). An empty or malformed token is still bearer and fails.
- Malformed: more than one value. It is handled exactly like a failed bearer request.
- None: no `Authorization` header, or one with another scheme.
- It never reads a query parameter or a form field. There is no `access_token` (RFC 6750).
- Bearer and malformed skip both halves of the CSRF check. None keeps both.
- Bearer and malformed authenticate by the token only, never by the cookie. On them `session`
  does not read the cookie at all: not in `Read`, `End`, `Rotate` or the `Codec` login cookie.
  The cookie is not cleared.
- Every token failure, invalid, expired, revoked, revoked for reuse or malformed, is the same
  401. A 400 for two headers would need a second writer and tell a caller which check failed.
- A reuse event is logged with device id and subject. No log line and no error text carries a
  token or a hash.
- A cross site page cannot attach an `Authorization` header without a preflight, and a browser
  never attaches a bearer token by itself.
- A contract test in `example/` sends cookie, bearer, both, junk bearer, two headers and neither
  through the real chain and pins each outcome.

### The token endpoint and the origin check

- The token endpoint is the one unsafe request from the app without a bearer header. Its origin
  is cross site, so the origin half of the CSRF check refuses it.
- It is exempt from the origin half only. The token half stays, and it passes because the app
  sends no cookie.
- The exemption is safe because the endpoint carries no ambient credential and changes no browser
  state. Its only effect is a response that a caller must read cross origin through CORS.
- `csrf.Protector` gets a dedicated method that names exactly one method and path. It returns an
  error at setup for a missing method, a host part, a trailing slash, `{$}` or a wildcard.
- The underlying `net/http` bypass panics on an invalid or conflicting pattern. The method
  validates first and turns a conflict into an error, so refusal stays an error at setup.
- Tests in `csrf` pin the refusals, and that a neighbouring path and a path with a trailing slash
  still get the origin check. The `example/` contract test covers the token route.

### Responses and CORS

- Every response that carries a token and every response to a bearer request sends
  `Cache-Control: no-store` (as RFC 6749 section 5.1 does for token responses).
- App origins are a separate list with their own CORS policy: `Authorization` allowed,
  credentials off. The web policy is unchanged.
- The app origin list never reaches `csrf.TrustOrigin`. Startup fails when an origin is both an
  app origin and a web or trusted origin. A test pins that refusal.
- A custom scheme is an explicit opt in of exact origins such as `capacitor://localhost`. No
  wildcard, no scheme wide rule and never `null`.
- The preflight is answered inside the CORS middleware, before rate limit, CSRF and any session
  read. That order stays.
- Capacitor's defaults give `capacitor://localhost` on iOS and `https://localhost` on Android.
  Checked against <https://capacitorjs.com/docs/config> on 2026-11-17.
- An origin is no app identity. Every Capacitor app and any local dev server share these origins.
  The bearer token is the only authentication.

### Consequences

- Good: a device ends on the next request after any revocation path, including a password change.
- Good: a store dump yields only hashes, and a leaked token is found by its prefix.
- Good: the web session, its cookie and its CSRF check do not change.
- Good: every change is additive and off until a product configures a device store.
- Bad: every bearer request is a store lookup, like every cookie request today.
- Bad: the in memory store signs every app out on each deploy. App mode needs a shared
  `DeviceStore` that the product writes and runs through the conformance suite.
- Bad: a script running in the webview can read the token, unlike an HttpOnly cookie. Native
  secure storage protects it at rest only.
- Bad: the token endpoint is a named exception to the origin check. It stays one route and must
  never set a cookie.
- Bad: reuse detection holds only while the stolen token is among the last four retired hashes.
  After that it is a plain 401 without a theft signal.
- Bad: against an informed thief who holds the current token, detection is best effort. Pending
  replacements also fill `Retired`, and the minimum interval does not bound them.
- Bad: web cookie session ids are still stored in the clear. Hashing them too is defence in depth
  and tracked in a separate issue.

### Confirmation

- A reviewer for security boundaries signs this ADR off before any code pull request of #117
  opens. Status moves to `accepted` then.
- The conformance suite, the `csrf` exemption tests and the `example/` contract test pin the
  rules above.

## Pros and Cons of the Options

### Opaque device session by hash

- Good: revocable on the next request, same model as the cookie session.
- Good: rotation and reuse detection need nothing beyond the record.
- Bad: needs a shared store for more than one replica, and a store with atomic operations.

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

- Stolen and used: valid until either holder rotates and confirms. The other holder's token is
  then retired, and its use after the grace revokes the device. This holds while the stolen
  token is among the last four retired hashes.
- Stolen and kept passive: the same, and the minimum interval keeps it detectable for at least
  four intervals in normal use.
- A thief who replaces the app's pending token: the app falls back to its current token and
  rotates again, which retires the thief's pending token. Its next use revokes the device.
- A thief who knows the nonce can overwrite silently only within the rotate timeout. The app's
  fallback then retires that token as above.
- Stolen and used after revocation: the hash is gone, the request is 401, nothing falls back to
  a cookie.
- Stolen from a store dump: only hashes, which are not usable as tokens.
- Leaked into a log or a URL: the token never travels in a URL, and no log carries it.

### Open questions

- The default prefix string and the default device cap. Both are set in the session step.
- Which `Sec-Fetch-Site` and `Origin` values the iOS and Android webviews send from the bundled
  origins. The exemption holds either way.
- Whether the native HTTP plugin of Capacitor bypasses CORS when a product enables it.
- Whether passkeys work inside a Capacitor webview.

### Related

- Concept doc: `docs/concepts/govue.md`, sections "Sessions keep their state in a store" and
  "CORS is an explicit allow-list, never a wildcard".
- Issue #117, and #104 for local accounts.
