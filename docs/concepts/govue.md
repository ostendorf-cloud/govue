# govue

The design of the library and the reasoning behind it.

## Problem

- A Go plus Vue web application needs the same layer every time, and that layer is not what the
  application is about: timeouts, graceful shutdown, security response headers, a content
  security policy, liveness and readiness endpoints, environment configuration, structured
  logging, sessions, OIDC login, CSRF protection, rate limiting, the client IP behind a proxy,
  Prometheus metrics.
- Nothing in that list is hard. All of it is easy to get subtly wrong, and the subtle part is
  the part that matters: an unbounded `X-Forwarded-For` walk, a session that logout cannot end,
  a CSP that is present but permissive, a shutdown that drops in-flight requests.
- Written once per application, it drifts. The same file in three repositories ends up with
  three different timeout values, and only one of them bounds the proxy header.
- The frontend has the same shape of problem one level up. Without a shared token contract every
  new page is designed from nothing, dark mode is added per component and rots, and contrast is
  reviewed by eye instead of tested.
- The usual answers are a full framework, which takes over routing and wiring, or nothing at
  all. The gap in between is a set of small, boring, correct pieces.

## Goals

- A Go module whose packages cover that server layer, each usable on its own, with the secure
  choice as the default rather than as an option.
- A Vue package with a published design token contract, so an application themes itself by
  filling a contract instead of inventing a palette.
- Both halves in one repository under one version, so a change that spans them is one pull
  request and one tag.
- Adoptable in pieces: the Go packages without the components, one package without the others.
- Usable by someone who is not the author: no assumption about a deployment, a cluster, an
  identity provider or a brand.

## Non goals

- A framework. Nothing here owns routing, wiring or `main`. An application assembles its own
  middleware chain and can see the order it runs in.
- An application scaffold or a template repository. This is a dependency, not a generator.
- Deployment. No chart, no image, no manifests.
- Replacing the standard library, or wrapping it for the sake of a nicer name.
- A component for every need. Where a widely used, well maintained primitive exists, adopting
  it beats writing a worse one.

## Approach

### One repository, two halves

- The Go packages sit at the module root, the Vue package in `web/`. One release-me version and
  one tag cover both.
- Two repositories would give each half its own cadence, at the price of a token rename or an
  error-shape change needing two pull requests and two versions to stay consistent. The coupling
  between the halves is real, so it lives in one repository.

### Go packages at the module root

- Everything at the root is public API, so an import reads
  `github.com/ostendorf-cloud/govue/ratelimit` with no segment that says nothing.
- The library packages of the standard library and `golang.org/x/...` are laid out this way, and
  this repository is nothing but library packages: no binary, so no `cmd/`.
- The cost is that there is no compiler-enforced boundary. A helper added at the root is public
  from that moment, and a rename is a breaking change for every consumer. `internal/` exists for
  anything that must not be imported.
- Moving to `pkg/` later would break every consumer, so the layout is decided once, now.

### Small, independent, standard library

- The Go side is the standard library, with two exceptions. `prometheus/client_golang` is
  confined to the `metrics` package, because the exposition format is not worth reimplementing.
  `golang.org/x/crypto` is confined to the `account` package, for Argon2id, which the standard
  library lacks. Writing a memory hard hash by hand is the worse risk.
- A package stays usable alone: taking `clientip` must not pull in `session`.
- Handlers return an error and one `api` helper writes the single error shape, so no handler
  writes error JSON and no cause reaches a client.
- The middleware chain is assembled by the application, not hidden behind a constructor. The
  places a constructor would hide, which checks are readiness and which limits apply to which
  route, are exactly the places applications differ.

### CORS is an explicit allow-list, never a wildcard

- `middleware.CORS` only answers for an API whose frontend is served from another origin. A
  same-origin deployment needs none of it.
- Every allowed origin is listed explicitly in `CORSConfig.Origins`. A wildcard is refused at
  construction, unconditionally, matching the Fetch spec and OWASP's CORS guidance: a reflected
  allow-everyone policy is exactly the mistake this package exists to make structurally
  impossible, with credentials or without.
- A matched origin is echoed back byte for byte, never the raw `Origin` header taken unchecked
  and never a wildcard, so a response built for one origin can never silently become valid for
  another. `Vary: Origin` is always set, even on a non-match, so a cache cannot hand one origin's
  answer to a different one.
- An origin that does not parse as a bare `scheme://host`, carrying a path, a query, userinfo, or
  the literal `null` a sandboxed context sends, is refused at construction rather than silently
  never matching: a config entry that could never fire is a mistake worth catching at startup.
- The origins of a native app are not web origins and never go into this list. They have their
  own type and handler, see "App origins" under "Token handlers".

### Security headers default to the strict choice, every time

- `middleware.DefaultSecurity` is this package's product: a strict, default-deny
  Content-Security-Policy with a fresh nonce per response for inline script and style, a year of
  HSTS with subdomains and preload, the minimum a browser preload list submission requires, MIME
  sniffing off, framing denied, a same-origin-only referrer, both cross-origin isolation headers
  set to same-origin, and every browser feature this library has no general use for denied
  through `Permissions-Policy`.
- The nonce, not a hash or an allow-listed `'unsafe-inline'`, is what lets this library's own
  inline script and style run under the default policy: a tag an attacker injects carries no
  nonce, so it does not run even once it is in the page.
- Weakening one header is the consuming application's decision, made through a field on
  `Security`, never a change to the default here. A zero-value `Security`, unlike
  `session.Config` or `oidc.Config`, applies no defaults of its own: an application has to call
  `DefaultSecurity` and override deliberately, not build a bare struct and get a watered-down
  policy by omission.

### CSRF tokens are HMAC-derived, not random nonces

- Two independent checks run, because neither covers the whole range alone. The origin check,
  `net/http`'s `CrossOriginProtection`, reads `Sec-Fetch-Site` and falls back to comparing
  `Origin` with `Host`; it allows a request that carries neither header, which is every non
  browser client and a handful of old browsers. The token check covers that gap, and also catches
  a same site attacker on a sibling subdomain the origin check cannot tell apart from the
  application itself.
- The token is derived from the session id with HMAC-SHA-256 over a domain separator and the
  length-prefixed session id, rather than generated at random and stored. A session needs
  nothing kept alongside it, and a token minted for one session is worthless in another.
- OWASP's HMAC-based double-submit pattern mixes a random value into the derivation as well,
  which makes the token single use. This package deliberately does not: `Expected` hands back one
  expected value for a request, with nothing to carry a per-request random value in. The token
  stays stable for the session's lifetime instead, rotating only when the session id itself
  rotates, and the origin check is what stands in front of a token that leaked.
- `csrf` never imports a session package. It only ever sees a session id as a plain string,
  handed to `Token`, which is what lets the two packages compose without either depending on the
  other.

### clientip walks X-Forwarded-For a bounded number of hops

- `clientip.Resolver` sits between two mistakes: trusting `X-Forwarded-For` blindly lets any
  client impersonate another address for an access log or a rate limit, and trusting the TCP peer
  alone attributes every request to the proxy in front of it, not the client.
- The header counts only once the TCP peer itself is a trusted proxy, from the prefixes
  `NewResolver` was built with. With no trusted prefixes, `Resolve` always returns the TCP peer:
  the secure default is to trust nothing, not to trust everything.
- The walk reads `X-Forwarded-For` from the right, where each trusted proxy on the path appended
  the address it received the request from, and stops at the first entry that is not itself a
  trusted proxy. That entry is the client. Entries to its left came from whoever made the
  original request and may be forged, so they never decide the result.
- The walk never looks past 32 entries. A forged header carrying thousands of entries would
  otherwise make resolution itself expensive, a cost an attacker should not be able to impose
  just by padding a header.

### ratelimit bounds its own memory, not only the request rate

- `ratelimit.Limiter` is a token bucket per key, and a caller that keys by something attacker
  controlled, a client address most commonly, must not let that key grow a map forever: a flood
  of distinct, one-shot keys would exhaust memory even though no single key ever came close to
  its own limit.
- Two mechanisms bound that. A bucket refilled all the way back to its burst size carries no
  information worth keeping, so it is swept away periodically, on an idle timer or as soon as the
  key count forces the question; dropping it only means that key starts over from a full bucket
  next time, exactly as if it had never been seen.
- The number of tracked keys is also capped at construction. If the map is still full of active
  buckets after a sweep, one arbitrary bucket is evicted to make room. That can only reset an
  unlucky key's limit early, granting it a fresh burst sooner than it would otherwise get one,
  never deny a request that should have been allowed.

### One example application, and the canonical chain order

- `example/` is a `package main` wiring every Go package in this module into one running server.
  It is never released as a binary and nothing imports it; this repository still ships none. It
  is the source of truth for the order a middleware chain runs in, checked by `go build` and by
  its own tests rather than kept consistent with the packages by hand in a document.
- The outer chain wraps the mux, outermost first: `Recover`, so a panic from everything below is
  still caught; `RequestID`, which calls `WithContext` and so has to run before anything that
  needs the real `*http.Request` value the mux matched against; the security headers, which call
  `WithContext` for the same reason when the response carries a CSP nonce; the metrics
  middleware and the access log, both of which read `r.Pattern` and so have to sit directly
  outside the mux with nothing in between that replaced the request.
- The route chain is applied to every handler registered on the mux, outermost first: CORS, the
  rate limiter, the CSRF protector, the session middleware, then the application handler. The
  session middleware sits after the CSRF protector, so a cookie request is checked before it is
  read. The rate limiter's 429, the CSRF protector's 403 and the session middleware's 401 for a
  failed bearer request answer on the exact route they refuse: each wraps one specific
  registered handler, so its answer carries that handler's real route label once the mux has
  already set `r.Pattern`. A CORS preflight is different: no `OPTIONS` pattern is registered for
  any route, so ServeMux matches the catch-all `"/"` instead, and the preflight's 204 is logged
  under that label, not the route it preflights for. In the outer chain, ahead of the mux, any of
  these answers would be logged and counted against the bounded `"unmatched"` label instead.
- A catch-all `mux.Handle("/", ...)` wraps `api.NotFoundHandler()` in the same route chain, so an
  unregistered canonical path still matches the pattern `"/"` and still goes through CORS and the
  rate limiter: without it, a request nothing recognises would skip the limiter entirely, which
  defeats the point of having one.
- A non-canonical path, one with a double slash or a `.` or `..` segment, never reaches that
  catch-all or any other registered handler. `net/http.ServeMux` answers it itself, with its own
  307 redirect to the cleaned path, before the route chain runs: CORS, the rate limiter and the
  CSRF protector are all skipped for that one response. This is accepted: the skipped response
  carries no body and reaches no application handler, and the client's follow-up request to the
  canonical path does go through the full chain. The logged route label stays correct regardless:
  ServeMux sets `r.Pattern` before it hands the request to its own redirect handler.
- The audit finding that no single position gets both the access log and a self-answering
  middleware right is true only for one flat chain. Two chains resolve it with no change to any
  package's behaviour: the packages already read `r.Pattern` correctly, and `example/` only had
  to put each one on the side of the mux where that is true.

### A second listener for what the Ingress must never route

- `metrics.Handler` returns a plain `http.Handler`, so mounting it on the same mux as the
  application put it behind the same listener, the same port, and therefore the same Ingress. It
  is not secret data, but it hands any visitor the request rates, latencies and error counts of
  the application, and that is reachable only because nothing stopped it from being mounted
  there, not because anything required it.
- The ordinary shape in a Kubernetes deployment is two ports on one process: the application
  serves its public port, metrics serve a second, in-cluster one, the Service exposes both, the
  Ingress routes only the first, and a ServiceMonitor or PodMonitor scrapes the second directly.
  Nothing on that second port is published anywhere, so nothing on it needs a credential. A
  bearer token or mTLS on the metrics path is defence in depth on top of a port that already is
  not routed, not a substitute for routing it correctly in the first place.
- `httpserver.RunAdmin` and `httpserver.ServeAdmin` give an application that shape with one call
  instead of a second, hand-rolled `Run` in its own goroutine: one cancelled context stops both
  listeners, a failure on either surfaces rather than one silently dying next to a goroutine that
  leaked, and shutdown is coordinated once rather than twice. `AdminConfig` carries no
  `ShutdownTimeout`, `DrainDelay` or `BeforeShutdown` of its own on purpose: those three are read
  from the primary `Config` alone, so the two listeners cannot end up draining on two different
  schedules just because an application set one of them on only one side.
- This is additive, not a change to `Run` or `Config`: every exported name at this module's root
  is a contract, and a changed signature breaks both of this library's current consumers. An
  application that does not want a second listener keeps calling `Run` exactly as before, and
  `RunAdmin` itself falls back to calling `Run` unchanged whenever `AdminConfig.Addr` is left
  empty. Adopting the admin listener is something an application opts into, never something it
  has to opt out of.
- The admin listener's middleware is deliberately short, and deliberately not the outer chain
  with entries removed. `example/`'s `(*app).adminHandler` keeps `Recover` and `RequestID`,
  because a panic on this listener is still worth catching and still worth a correlatable log
  line, and drops everything else: the security headers and CORS have nothing to protect, since
  neither a probe nor a scraper is a browser; the CSRF protector has nothing to check, since
  neither can be forged through one; and the access log and the rate limiter are actively wrong
  here, because a scraper polling every few seconds would either fill the log with nothing worth
  reading or get throttled into failing the one thing it is there to do. The metrics
  instrumentation middleware is absent too, deliberately rather than by omission: a scrape
  endpoint counting its own hits would feed Prometheus a metric about scraping Prometheus, and a
  probe gains nothing from being counted either. None of this needed a change to any existing
  middleware: the admin handler simply never passes through `middleware.AccessLog`,
  `middleware.RateLimit` or the metrics instrumentation middleware at all, the same way a package
  that never imports another cannot be coupled to it.
- The liveness and readiness probes moved to the admin listener too, in `example/` and in the
  recommendation here, on the same reasoning: a kubelet reaches the pod directly, so `/healthz`
  and `/readyz` need no public route either. This is a wiring decision, not a library one. The
  `health` package already takes any `*http.ServeMux` in `Handle`, so nothing in the package
  changed, and an application already running the previous, single-listener shape keeps its
  probes exactly where they are until it chooses to move them, because moving them is an edit to
  its own `main`, never something `govue` does to it.

### Sessions keep their state in a store

- The cookie carries an opaque random id. The state lives behind a `Store` interface, with an
  in-memory implementation shipped.
- A signed cookie holding the whole session is the tempting alternative: no store, every replica
  stateless. It cannot end a session, which makes logout, a disabled account and a leaked cookie
  all unenforceable, so it is not the default.
- The cost is a dependency on the request path and a store that has to exist. One replica is
  served by the in-memory store; more than one needs a shared store, which is open below.
- A short-lived signed cookie stays for the one thing a store is wrong for: the OIDC state,
  nonce and PKCE verifier, which have to survive the redirect before any session exists.
- That login cookie is the session cookie's attributes with two deliberate changes, built by the
  same unexported function so the two cannot drift apart. `SameSite` is forced to `Lax` and is not
  configurable: the provider redirects the browser back to the callback, which is a cross-site
  top-level navigation, so a `Strict` login cookie would break every login rather than harden one.
  It never gets a `Max-Age`, because the lifetime that counts is the short one signed into the
  value and a login in flight surviving a closed browser buys nothing.
- Reading the login cookie always clears it, on success and on every failure alike. A login is
  good for exactly one callback, so single use is the default rather than something an application
  has to remember, and a replayed callback URL then finds no login state to reuse.
- Every failure of that read is one error. A missing cookie and a value that does not verify are
  indistinguishable to the caller, which leaves one code path to get the refusal right.

### Enabling app mode

- App mode lets a native app, such as a Capacitor app, sign in with a device token instead of a
  cookie. It is off until a product wires every step below, and each step links to the section
  that explains it. This list repeats no design.
- Step 1, an account sign in. The token is issued after the same check as the cookie login.
  `example/` wraps `account.Service.Login` in the `devicehttp.HandlerConfig.VerifyCredentials`
  callback. The callback returns the account id and maps the errors to
  `devicehttp.ErrInvalidCredentials` and `devicehttp.ErrBusy`. So the token needs login,
  password and a TOTP code. Any other check works, because the handler takes a callback. See
  "Local accounts". The account routes refuse a cross site browser request from an app origin.
  A request without `Origin` and `Sec-Fetch-Site` passes, as for any non browser client. Whether
  the native HTTP plugin of Capacitor sends those headers is not verified.
- Step 2, a device store. Set `session.Config.Devices` to `session.DeviceConfig{Store: ...}`.
  App mode is off while `Store` is nil. A product writes a shared `session.DeviceStore` and runs
  `sessiontest.DeviceStore` against it. `session.NewMemoryDeviceStore` serves one replica and a
  restart signs every app out. Set `DeviceConfig.TokenPrefix` to the product's own name. Set
  `Config.OnBearerRefusal` for the error shape. Call `csrf.Protector.ExemptBearer` and keep
  `session.Manager.Middleware` behind the protector. See "Device sessions for native apps".
- Step 3, the app origin list and its overlap check. It comes before the token handlers, which
  take the list. Build it with `middleware.NewAppOrigins(origins, customSchemes...)` into
  `appOrigins`. Call `appOrigins.RejectOverlap(nil, trusted)` at startup, where `trusted` holds
  every origin passed to `csrf.Protector.TrustOrigin` and to
  `account.HandlerConfig.TrustedOrigins`. Refuse to start on an error. Call
  `csrf.Protector.TrustOriginOnRoute` with `http.MethodPost`, the path of the issue route and
  `appOrigins.List()`. The issue route is `POST /api/v1/auth/token` in `example/`. Once the web
  `CORS` exists, build `middleware.NewAppCORS(appOrigins, webCORS)`, where `webCORS` is the
  `*CORS`. It refuses an overlap with that policy. Use its `Middleware` in place of
  `CORS.Middleware`, at the same place in the chain. In `example/` the list comes from
  `APP_ORIGINS` and `APP_SCHEMES`, for example `APP_ORIGINS=capacitor://localhost` with
  `APP_SCHEMES=capacitor`, and `https://localhost` for Android. See "App origins".
- Step 4, the token handlers and their rate limit. `devicehttp.NewHandlers` takes
  `HandlerConfig{Sessions, VerifyCredentials, Now, IssueTrustedOrigins}`. Pass `AppOrigins.List()`
  from step 3 as `IssueTrustedOrigins`. `Now` is the clock of `session.Config.Now`. Mount
  `Issue`, `Rotate`, `SignOut`, `List` and `Revoke` as in "Token handlers". Put `Issue` behind a
  per client limit stricter than the general one, as `example/` does with
  `AUTH_RATE_LIMIT_BURST` and `AUTH_RATE_LIMIT_PER`.
- Step 5, the association files. Fill `appassoc.Config`, call `appassoc.New` and
  `Handlers.Handle(mux, nil)`, outside the route chain. They serve `appassoc.ApplePath` and
  `appassoc.AndroidPath`. `example/` reads `APPLE_APP_ID`, `APPLE_APP_PATHS`,
  `APPLE_APP_EXCLUDE_PATHS`, `APPLE_WEBCREDENTIALS`, `ANDROID_PACKAGE`, `ANDROID_CERT_SHA256` and
  `ANDROID_LOGIN_CREDS`. See "App association files".
- Step 6, the Vue client. Call
  `createClient({ baseUrl, auth: { mode: 'bearer', storage, rotation: 'owner' } })`, with a
  `TokenStorage` adapter that the product writes. A client with its own storage must state its
  `rotation`. Exactly one context per storage is the `'owner'`. Every other context sets
  `rotation: 'none'`. Sign in with
  `signIn` and out with `signOut`. `baseUrl` defaults to `/api/v1`, which is relative to the
  bundled origin of the app, so an app names its API there. Assumed, not tested. See "The bearer
  client of a native app".

#### What the server serves in app mode

- New routes: the five token routes of step 4 and the two association files of step 5.
- New answers: the CORS answers of `AppCORS` for the listed app origins, preflight included.
- Cookie based and unchanged: the web session cookie, the CSRF check of every cookie request, the
  web CORS list with its credentials and the account forms of the web app. `List` and `Revoke`
  take a cookie session too, so the web app can end a lost phone.
- An app origin never goes into `CORSConfig.Origins`, `csrf.Protector.TrustOrigin` or
  `account.HandlerConfig.TrustedOrigins`. The first two are in "Token handlers". The third is
  not named there, and follows from its doc comment: every account handler accepts those
  origins, and the login handler starts a cookie session.
- An origin is no app identity. The bearer token is the only authentication.

#### What stays the product's job

- The persistent token storage, backed by the platform keychain. govue ships none and has no
  Capacitor dependency.
- The rate limits, here `middleware.RateLimit` with a `ratelimit.Limiter`, and the shared
  `DeviceStore` with its conformance run.
- Authorisation of a device session from the product's own account record, by `Subject`. A
  device session carries no groups.
- The values in the association files. They are public identifiers: the Apple team id, the
  bundle id, the Android package name and the certificate fingerprints. No secret belongs there.
- The origin an app really sends. Check the header of the real build and list what it shows.
- Passkeys in an app. The `webcredentials` entry exists. The relying party origins come with the
  passkey step of #104, and nothing is promised here.

#### Known limits and decisions

- The ADR fallback is not built. A 401 clears the token. See "The bearer client of a native
  app", the bullet "Differences from ADR 0001, all of them".
- Two contexts that both rotate one storage revoke the device. See "The bearer client of a
  native app", the bullet "One context owns the storage".
- A custom storage must state its `rotation`, `'owner'` or `'none'`, with no default. Only the
  default in memory storage is an `'owner'` without saying so. The owner decided this on
  2026-11-18. See "The bearer client of a native app".
- The Capacitor defaults were verified on 2026-11-18 against the Capacitor and Ionic pages. The
  exact `Origin` header, `Sec-Fetch-Site` and the native HTTP plugin were not. See "App origins".
- No `Access-Control-Expose-Headers`, so a page cannot read `Retry-After`. No
  `Access-Control-Allow-Private-Network`, so Chrome can fail the request on a private network.
  Both are undecided, see "App origins".
- Any device token lists and revokes every device of its account. See "Token handlers".
- The memory stores sign every app out on a restart. See "Device sessions for native apps".
- The association files were not tried on an iOS or Android device. See "App association files".

### Device sessions for native apps

- ADR 0001 holds the design. This section says what exists.
- `session` has the device session layer without HTTP: `IssueDevice`, `AuthenticateDevice`,
  `RotateDevice`, `ListDevices` and `RevokeDevice` on the `Manager`, configured through
  `Config.Devices`.
- It is off while `DeviceConfig.Store` is nil. With a store, `EndAll` also ends the account's
  device sessions.
- The routes that issue, rotate, list and revoke tokens are in `session/devicehttp`, see "Token
  handlers" below. The origins of a native app and their CORS policy are in "App origins" below.
- `internal/bearer` holds the one predicate both `session` and `csrf` import. It reads every
  `Authorization` value and answers none, bearer or malformed. Bearer is one value with the scheme
  `Bearer` in any ASCII case. One or more spaces separate it from the token, as RFC 9110 allows.
  Malformed is more than one value. Everything else is none.
- The real guard against drift is behavioural: the contract tests in `example/` send every
  header shape through both packages and pin that they agree. A source test that parses both
  packages is a tripwire on top. It fails when either stops calling `Classify` or reads the
  header itself, but a determined rewrite can get past it.
- `csrf.Protector.ExemptBearer` skips both halves for bearer and malformed, and does not call
  `Expected` then. It is off by default, so a consumer of `csrf` alone keeps the full check on
  every request. It is safe only in front of `session.Middleware`, or an equivalent that never
  reads a cookie on such a request. Behind a handler that reads a cookie of its own, a proxy that
  injects a bearer header would open that handler to cross site requests.
- With a device store, `Manager.Read` authenticates a bearer request by the device token only. A
  request with both authenticates as the token's subject. `Session.Credential` says which
  credential it was, and `Session.DeviceID` names the device.
- Without a device store, a bearer or malformed request is anonymous: no session, no 401. A
  product's own gateway or JWT keeps working. The cookie is never read on such a request, in any
  configuration.
- A device session carries no groups and no account attributes. `Session.In` is false for every
  group, so an allow list fails closed and a deny list fails open. A product authorises a device
  session from its own account record, looked up by `Subject`.
- Every response produced by or behind `Manager.Middleware` for a bearer request carries exactly
  one `Cache-Control: no-store`, whatever a handler sets and in whatever key spelling. A response
  produced earlier in the chain, such as the rate limiter's 429, a CORS refusal or a mux redirect,
  is outside it and carries no credential data.
- The handler keeps `http.Flusher`, `http.Hijacker` and `io.ReaderFrom` of the writer underneath,
  so SSE and WebSocket code that asserts them works, and `http.ResponseController` reaches it
  through `Unwrap`. Two ways leave the guarantee, for deliberate code only: a hijacked connection
  carries whatever its handler writes, and a write through `Unwrap` bypasses the guard.
- `CDN-Cache-Control` and `Surrogate-Control` are not touched. A product behind a CDN that reads
  them must not set them on a response to a bearer request.
- With a device store, a token that does not authenticate and two headers get 401 on every route,
  through `Config.OnBearerRefusal`. The challenge is `Bearer error="invalid_token"`, and a bare
  `Bearer` for a request without a token (RFC 6750 section 3.1). A refusal callback that writes
  nothing or another status still ends as that 401, and a cookie it sets is removed. A device
  store outage passes on like a cookie store outage.
- On a bearer request `End` signs the device out and leaves the cookie alone. `Start` and `Rotate`
  refuse a request the middleware classified as bearer with `ErrBearerRequest`, and `Rotate`
  refuses a device session, so a token never mints a cookie session. `Codec.ReadLoginCookie`
  neither reads nor clears the login cookie. `Codec.SetLoginCookie` cannot see the request, so
  keeping it off a bearer request is the caller's job.
- No `Vary: Authorization` is added. A bearer response is never stored, so no cache can serve it
  to another request.
- `example/` turns `ExemptBearer` on, configures a memory device store and runs the session
  middleware after the CSRF protector on every route. Its OIDC callback reads the login cookie
  through the codec. A contract test sends the cross product of credentials, header shapes,
  methods, paths, origins and the CSRF token through that chain.
- The token is a prefix, 32 random bytes in base64url and a CRC-32 checksum. The store keeps only
  its SHA-256. A malformed token never reaches the store.
- The default prefix `device` is neutral. A product sets its own, so secret scanning finds a
  leaked token of exactly that product.
- `DeviceStore` is a separate interface, so no `Store` implementation breaks. Every method is
  atomic on one record, and rotation is a conditional swap, never a read followed by a write.
- Retired hashes are two lists: four retired current hashes and two replaced pending ones.
  Rotations by a thief can fill only the second, so the app's old token still reveals the theft.
- `IssueDevice` takes the time of the credential check. An issue whose check is five minutes old,
  or not later than the account's last `EndAll`, is refused. A password change that lands between
  the check and the issue leaves no device behind. A shared store needs synchronised clocks for
  this.
- That holds only in one order. The caller takes the check time before it reads the stored
  credential, not after a slow hash returns. A password change calls `EndAll` after the new
  credential is committed, not before.
- The cap of ten devices per account ends the least recently seen one and logs it. Whoever has
  the password can use that to sign the account's apps out. A password change is the remedy.
- `MemoryDeviceStore` is for one replica. A full one refuses to issue for everyone and never
  evicts another account's device. A restart signs every app out, so a product with apps writes a
  shared store.
- Known limits of the memory store, which a shared store decides for itself:
  - Its sweep drops only records past their absolute expiry. An idle record stays until a read
    finds it or the absolute expiry passes, and it counts toward the bound until then.
  - It remembers delete times for at most `maxDevices` accounts. Once about that many accounts
    end their sessions within the issue delay, one floor covers every account. Every issue whose
    check lies before the floor is then refused, which fails closed.
- `session/sessiontest` is the conformance suite for any `DeviceStore`: the atomic operations,
  the rotation and reuse rules through a `Manager`, and their races.

#### Token handlers

- `session/devicehttp.NewHandlers` returns one `http.Handler` per route: `Issue`, `Rotate`,
  `SignOut`, `List` and `Revoke`. The application mounts them with the method in the pattern.
  `example/` uses these under `/api/v1/auth`:
  - `POST /token` issues a token after a verified sign in.
  - `POST /token/rotate` replaces the token of the calling device.
  - `DELETE /token` ends the calling device.
  - `GET /devices` lists the caller's devices.
  - `DELETE /devices/{id}` ends one device of the caller.
- The handlers are a sub package of `session`, not a file in it. `session` imports no `api` today,
  and an application that wants the session layer alone must not pull the error shape in with it,
  the same rule that keeps `clientip` apart from `session`. The handlers need nothing that is not
  exported, so the split costs no API. `devicehttp` imports `session`, `api`, `logging` and
  `internal/bearer`, and never `account`. `account` and `session` stay independent: the
  credential check is a callback, as `StartSession` is in `account`. The ADR says the handlers
  live in `session`. The package path is the one change to that sentence, the design is not.
- `HandlerConfig.VerifyCredentials` gets login, password and code and returns the subject. It
  answers `ErrInvalidCredentials` for every refusal and `ErrBusy` for a verifier without capacity.
  In `example/` it is `account.Service.Login`, so the token needs the same factors as the cookie
  login, and the TOTP code is consumed. An optional `DeviceName` policy maps the requested name,
  for example to a default for an empty one.
- The handler takes the time of the check before it calls the callback, as the ADR requires, from
  `HandlerConfig.Now`, which has to be the clock of `session.Config.Now`. A password change that
  lands during a slow verification answers 409 `stale` and leaves no device behind. A test moves
  the order and fails.
- `Issue` authenticates by the credentials in its body and by nothing else. The handler reads no
  bearer token and no cookie and sets none. A valid token with an empty body is a 401, and with
  the credentials of another account it issues for that account. In the real chain the
  middleware in front still acts first: a junk, expired, revoked or doubled `Authorization`
  header gets the middleware's 401 even with valid credentials, a cookie request without a CSRF
  token gets the protector's 403, and a valid pending token is confirmed by the session
  middleware as on any request. The contract test pins these.
- The token is in the body of an `Issue` or `Rotate` success once, as `token`, next to `token_id`,
  which is the device id that the list shows, and `expires_at`. A rotation never moves
  `expires_at`. No other answer carries a token. A list, an error body and every log line are
  scanned for the token and for its hash in nine spellings by a test.
- `Rotate` and `SignOut` need a bearer token. A request with a cookie and no token is a 401, and
  the cookie is never read. `Rotate` answers 409 `rotation_refused` when it issued no token and
  the presented token stays valid: inside the minimum interval, a retired token inside its
  grace, the first use of a pending token, a concurrent rotation that won. An app takes it
  quietly and keeps the token it has.
- `List` and `Revoke` take a cookie session or a device, so the web app can end a lost phone. They
  read the session from the request context that `session.Manager.Middleware` fills, and answer
  401 without it. `Revoke` answers an id of another account exactly like an unknown id: the same
  404 with the same body and headers, and nothing of the other account ends.
- The list carries id, name, created, last seen, expires and `current`, which is true for the
  device that makes the request. It carries neither token nor hash.
- Every handler checks the method first, then the origin with `net/http.CrossOriginProtection`.
  Only `Issue` trusts `IssueTrustedOrigins`. `Rotate` and `SignOut` then check the bearer token
  before they read the body. The body is `application/json`, one object, at most 16 KiB by
  default, unknown fields refused. A bearer request skips the origin check on the routes that
  take one, because a browser never attaches that header by itself. `Issue` checks the origin for
  every request, since it carries no token and its answer carries the new one. The device name is
  checked before the credentials. The name rules are those of `IssueDevice`, written twice so that
  a bad name costs no verification, and a test pins that the two agree.
- A device name is a label the app chose. It can carry U+2028, U+3164, trailing spaces or markup
  such as `<script>`. The JSON is safe. A UI renders a name as text, never as HTML.
- A `DeviceName` policy that returns a name the session package refuses is a bug of the policy.
  It answers an opaque 500 before the credentials are checked, so no code is consumed.
- `NewHandlers` refuses a session manager without a device store, so the first issue does not
  check credentials and consume a code before it fails.
- Every answer is JSON in the error shape of `api`, with `Cache-Control: no-store`. A failure has
  one body per cause class: 401 `invalid_credentials` for a refused sign in, 401 `unauthenticated`
  with `WWW-Authenticate: Bearer` for a bearer route without a valid token, 404 `not_found` for a
  device that is not the caller's, 409 `rotation_refused` and `stale`. 503 is a busy verifier
  (with `Retry-After`), a full device store on an issue, and a session read that failed because
  the store did, which `List` and `Revoke` report. A store error inside `List`, `Revoke`,
  `Rotate`, `SignOut` or the issue itself is an opaque 500. A 5xx carries no cause.
- Any device token of an account can list every device of the account and revoke any of them,
  itself included. This is the current behaviour, not narrowed to the calling device. A holder of
  the old token inside the grace of a rotation cannot rotate: `Rotate` answers 409
  `rotation_refused` and issues no token. It can still list, revoke other devices and sign the
  device out. The sign out ends the device, so the new token gets 401, and no reuse is logged,
  because the old token was inside its grace. A test pins this. Whether it should be narrowed is
  the owner's decision.
- The issue route needs a per client rate limit, like the four account routes that check a
  password. It costs whatever the callback costs, here an Argon2id derivation. `example/` mounts
  it behind the stricter limit and a test pins the 429.
- The origin of a native app is cross site. The CSRF protector and `Issue` both refuse an issue
  request from it unless the application sets the app mode up, see "App origins" below.
  `HandlerConfig.IssueTrustedOrigins` names origins for `Issue` and for no other route. An app
  origin must never go into `csrf.Protector.TrustOrigin` or into the web CORS list, which sends
  credentials: either lets a page of that origin ride a cookie. A test pins that a web origin is
  refused on `Issue`.
- `example/` pins the handlers three ways. A contract test sends credential, site and CSRF token
  through the real chain for each handler: cookie only, bearer only, both, a junk bearer, two
  headers and neither. An HTTP test runs sign up, confirm, issue, an authenticated call, rotation,
  the old token inside and after the grace, list, revoke and sign out. Another runs a password
  change that ends the device sessions. The handler package has its own tests, and the mutations
  of the pull request show each rule fails a test.

#### App origins

- `middleware.AppOrigins` is the list of origins a native app sends its requests from. It is a
  type of its own, which helps to keep it apart from the web lists. That is a convention helped
  by the type and not a compile error: `List()` returns a plain `[]string`, which the compiler
  accepts in `CORSConfig.Origins` or `TrustOrigin`. What is structural is the web CORS list,
  see `AppCORS` below. The decision is made in `middleware` and not in a new
  package for one reason: it sits next to `CORS` and shares its origin rules, and a package of
  one type would only add an import. `AGENTS.md` keeps its list of seventeen packages.
- `NewAppOrigins(origins, customSchemes...)` validates exactly. Each entry is `scheme://host` or
  `scheme://host:port`, written the way a browser sends it: lower case, no path, no trailing
  slash, no query, no fragment, no userinfo. The host is ASCII, because a browser sends an
  internationalised host as punycode and a Unicode lookalike would never match. A port is
  numeric from 1 to 65535 without a leading zero, an empty port is an error, and so is `:443` on
  `https`, which a browser leaves out. The scheme is `https` or one of the custom schemes
  the caller names. A wildcard, `null`, `http`, an empty list and a duplicate are errors, and so
  is a custom scheme that is not a lower case URL scheme or that a browser handles itself (`file`,
  `data`, `javascript`, `blob`, `about`, `ws`, `chrome`, `chrome-extension`, `moz-extension`,
  `safari-web-extension`, `view-source`, `vbscript`, `intent`, `content` and a few more). Nothing
  is normalised: a spelling that differs
  from what a browser sends is an error, so the list an operator reads is the list that matches.
  Every error names the offending value.
- The web CORS list is checked structurally. `NewAppCORS(origins, web)` takes the web `CORS` and
  fails construction when an app origin is on its list, naming the origin. The comparison is
  trimmed, without a trailing slash, lower case, as the web policy reads its own list.
- `AppOrigins.RejectOverlap(webCORS, csrfTrusted)` is the same comparison as a function, for the
  list this package cannot see. A consumer calls it at startup with every origin it passes to
  `csrf.Protector.TrustOrigin`, and refuses to start on an error. The error names the origin and
  the list. This stays a call of the application, and it is the one part of ADR 0001's check
  that is not structural. The function cannot see a list it is not given.
- `AppCORS` is the one CORS handler of an app mode deployment and replaces `CORS.Middleware` in
  the chain at the same place, before the rate limit, the protector and the session. It holds the
  web `CORS` and routes by the `Origin` header. A request whose `Origin` is on the app list is
  answered by the app policy and goes to the next handler directly, never through the web policy.
  Any other request goes through the web policy, which answers its own origins and refuses
  the rest as before. No origin gets the answer of both, so a real request from an app origin
  never carries the credentials of the web policy and has one `Vary: Origin`, even if the
  overlap check were bypassed. A test builds the struct past the check to pin that.
- The preflight of an app origin is answered inside the handler with 204, as the web handler
  does. It sends `Access-Control-Allow-Origin` with the matched origin, `Allow-Methods: GET,
  POST, DELETE` (the methods of the token routes), `Allow-Headers: Authorization, Content-Type`,
  `Max-Age: 600` and `Vary: Origin, Access-Control-Request-Method,
  Access-Control-Request-Headers`. A response to a real request from an app origin carries the
  matched origin and `Vary: Origin`. No response carries `Access-Control-Allow-Credentials`: a
  bearer token is not a cookie, so credentials mode stays off. An origin that is not listed gets
  `Vary: Origin` and no `Access-Control-` header, and is never echoed. No `Expose-Headers` is
  sent, so a page reads the CORS safelisted response headers only. An app that needs
  `Retry-After` of a 429 or the request id has to ask for it, and that is not decided here.
  `Access-Control-Allow-Private-Network` is not sent either: a preflight with
  `Access-Control-Request-Private-Network` gets the answer without it, and a browser that
  enforces Private Network Access, such as Chrome, fails the request when the API is on a private
  network. That is not decided here either.
- The `Authorization` header is allowed for app origins only. The web policy does not allow it.
- The issue route needs the origin half of the CSRF protector out of the way for the app origins,
  because the app origin is cross site and carries no bearer token on that route.
  `csrf.Protector.TrustOriginOnRoute(method, path, origins)` does that for one method, one path
  and the listed origins, as ADR 0001 plans. It takes the origins and not a switch for every
  origin, for one reason: a switch on a route that sets a cookie without a session, such as a
  login, would open it to login CSRF from any site. Every other origin keeps the origin half on
  that route, and the origins of `TrustOrigin` are not added there. The method is POST, PUT,
  PATCH or DELETE: a safe method needs no origin check, so it is an error and not a silent no-op,
  and so is `CONNECT`. The path is absolute, exact and clean: no wildcard, variable, query,
  trailing slash, `..`, `//`, `/.` or `%`. It is matched case sensitively against the escaped
  request path, so another case, `%2F` for a slash or `%74` for a letter does not match. The mux
  may route such a spelling to the same handler, and the protector then refuses the app request
  with 403, which fails closed. Each origin follows the rules of `TrustOrigin`, at least one is
  required, and a second call for the route is an error. The token half stays. A request with a
  session cookie still needs the CSRF token, and an app has no session. The route checks the
  origin itself as a second layer: `Issue` accepts `IssueTrustedOrigins` and no other cross
  origin caller. A consumer that sets `IssueTrustedOrigins` calls `TrustOriginOnRoute` for the
  issue route with the same list, and calls neither without app origins.
- `example/` turns the mode on with `APP_ORIGINS` and `APP_SCHEMES`, both empty by default. A
  custom scheme is an opt in, one scheme at a time: `APP_ORIGINS=capacitor://localhost` needs
  `APP_SCHEMES=capacitor`. The list goes to `Issue` as `IssueTrustedOrigins`, to the issue route
  of the protector and to `NewAppCORS`, and to nothing else. `ALLOWED_ORIGINS` stays the web
  list. `newApp` passes it to `RejectOverlap` as the trusted origins, because the example feeds
  the same list to `TrustOrigin`, and `NewAppCORS` checks it against the web CORS policy.
  Startup fails for an overlap, a wildcard, `null`, a path, a duplicate, `http`, a bad port, a
  Unicode host, a custom scheme that is not opted in, and `APP_SCHEMES` without `APP_ORIGINS`.
- A cookie request from an app origin stays a cross site request. The protector refuses it on
  every route that changes state, with a valid CSRF token as well, and a safe request from it
  gets an answer without `Access-Control-Allow-Credentials`, so a browser does not hand it to the
  page. The issue route refuses a request with a session cookie and no CSRF token.
- What a Capacitor webview sends. Verified on 2026-11-18 against
  <https://capacitorjs.com/docs/config>: `server.hostname` defaults to `localhost`,
  `server.iosScheme` defaults to `capacitor` and cannot be `http` or `https`, `server.androidScheme`
  defaults to `https`, and the page warns against a value other than `http` or `https` on
  Android. Verified against <https://ionicframework.com/docs/troubleshooting/cors> on the same
  day: for Capacitor on iOS the origin is `capacitor://localhost`, and for the Ionic WebView 3.x
  plugin it is `ionic://localhost`. The page says to replace `localhost` with the own hostname when
  the config changed it. That page lists `http://localhost` for Android, which is the older
  Capacitor default and disagrees with the config page. The config page decides. So the origins
  to list are `capacitor://localhost` for iOS and `https://localhost` for Android, both with
  the hostname of the app's own config. `http://localhost` is refused here by design: an app
  on the `http` Android scheme has to change `androidScheme`.
- Assumed and not verified: the exact `Origin` header a webview puts on a request, which is
  derived from scheme and hostname here and not quoted from a page. The `Sec-Fetch-Site` value of
  a request from the bundled origin. Whether the native HTTP plugin of Capacitor bypasses CORS
  when a product enables it, in which case no CORS header matters for it. A product checks the
  real header of its own build and lists what it sees.
- An origin is no app identity. Every Capacitor app and any local dev server share these origins.
  The bearer token is the only authentication.

### Session in context lives in `session`, not in `middleware`

- `Manager.Middleware` reads the session once per request and puts the outcome in the request
  context, where `FromContext` and `ErrorFromContext` find it. Reading per handler means a store
  round trip per read, and a read can write the last use back.
- It is in `session` because `middleware` is the package every application imports for the request
  id and the access log. Putting it there would make all of them pull a session store in with it,
  which is the coupling the `clientip` rule exists to prevent. `session` already imports
  `net/http`, and nothing in the module imports `session`, so there is no cycle either way.
- `csrf` still imports nothing from `session`: it is the application's `csrf.Expected` closure
  that reads the session. Both import `internal/bearer`, and `csrf.Protector.ExemptBearer` relies
  on the middleware never reading the cookie on a bearer request.
- For a cookie request the middleware refuses nothing and writes no response. It is not an
  authorisation gate: a public route behind the same chain must not start answering 503 because a
  cookie could not be read, and nothing is lost by that, because `FromContext` reports false for a
  store that could not answer. A bearer request whose token fails is the one exception, see
  "Device sessions for native apps".
- `FromContext` is false for a request the middleware never ran on, so a handler mounted without it
  looks logged out rather than logged in as somebody.
- `ErrorFromContext` keeps the distinction `Read` draws between a logged-out request and a store
  that could not answer. A middleware that dropped it would send every user to the identity
  provider at the moment the infrastructure is already failing.

### The OIDC handlers take an interface and a callback

- `oidc.NewHandlers` ships the flow assembled: one handler starts a login, one finishes it, in the
  order the steps have to happen in. It is the third of these packages whose correct use was
  previously a page of application code to copy.
- `oidc` still imports nothing from `session`. The handlers take what they need through a
  three-method `LoginStore` interface and a `StartSession` callback, and a `*session.Codec`
  satisfies that interface as it is, with no adapter in between, which is what makes the wiring
  short enough to read.
- `StartSession` is required rather than defaulted. This package has no session of its own and must
  not guess what belongs in one. An error it returns is answered as `ErrProvider`, which is the
  honest answer for the store write that usually fails there, so an account that authenticated but
  may not use the application is refused by the application's own authorisation gate instead: that
  refusal is not something a browser should be told to retry.
- The callback reads and clears the login before it exchanges the code, so a replay cannot reuse
  the state and a failure anywhere leaves the browser with no login cookie.
- A post-login redirect target is checked as a local path twice: once out of the query string and
  once out of the cookie, because the cookie was signed by this application but the value inside it
  came from a query string. A target that fails the check is dropped silently rather than failing
  the login, since a stray parameter must not be the reason somebody cannot log in.

### The OIDC key cache expires, and a failure is opaque

- The ID token is verified in process against the provider's JWKS. The alternative, asking the
  provider to introspect every token, puts a network call on the login path and still needs the
  same claim checks afterwards.
- A key id is looked up, never guessed: the token's `kid` selects one key and the signature is
  checked against that key alone. Trying every key in the set until one verifies is what makes
  algorithm confusion reachable, so the allowed algorithms are a fixed table and `none` and the
  HMAC family are not in it.
- The cached set expires. A cache with no expiry trusts a signing key the provider has withdrawn
  for as long as the process runs, which is the one thing rotation is supposed to end. The TTL
  comes from the JWKS response's own `Cache-Control` where there is one, clamped between a minute
  and a day.
- An unknown key id refetches, at most once a minute. Rotation introduces a key id nothing has
  seen before, so a miss has to be able to refetch, and the limit is what stops a made-up key id
  from being one request to the provider per login attempt.
- A set past its TTL is still used for an hour when the provider cannot be reached to replace it.
  Refusing every login over a blip is worse than briefly accepting a key the provider published
  minutes ago, and an outage longer than that stops the logins rather than trusting keys nobody
  has confirmed since.
- Every failure of a login reaches the caller as one of two errors, one meaning start over and
  one meaning the provider is unreachable. The cause goes to the log with the request id. A
  caller is an HTTP handler, and a bad client secret, a drifted clock and a forged token must not
  be distinguishable from outside.

### Local accounts

- Two applications already carry their own local accounts with a password and TOTP, and more
  are about to need them. The `account` package is meant to replace those copies before they
  drift further.
- govue owns no schema. The existing applications store accounts differently, so a consumer
  satisfies `account.Store` with an adapter over its own tables. For the schema, adoption is an
  adapter, not a data migration: the password hash format and the TOTP secret format already
  match, and the record needs no column the existing tables lack. For behaviour it is an
  adapter only with the counter mapping below.
- `Store` has six methods, taken from the calls the logic makes. `FindByLogin`, `Create` and
  `AdvanceTOTPCounter` serve signup and login. `ConfirmTOTP` and `ReplaceTOTPSecret` serve TOTP
  enrolment, and `SetPasswordHash`, a compare and set on the old hash, serves a password change
  and the rehash after a login. It has no method for a provider
  identity, a connected account, a tenant or a list of accounts.
- That absence is the guarantee. Code in `account` cannot reach what the interface does not
  offer, so a login can never be matched against a provider account somebody connected for
  another purpose. Tests pin the method set and the fields of `Record`, so widening either is a
  reviewed decision.
- Scoping stays in the adapter. A consumer with row level security or tenants reads its scope
  from `ctx` there, and fails closed when the scope is missing. That a call never acts outside
  the account it was asked about is structural only where the adapter enforces such a scope.
- Where the logic looks an account up by login while it already holds an `Identity`, it checks
  that the record's id equals the identity's id before it writes.
- `account` has no status field. An account the consumer disables or holds back answers
  `ErrNotFound` from `FindByLogin` or comes back without a password hash. Both existing
  applications refuse such accounts today, and the adapter is where that stays true.
- `Identity` carries an opaque `ID` and a `Login`, nothing else. The id is the consumer's
  account id, never a provider subject, so a session keys on one identifier however the login
  happened. Tenant, role and groups are added by the consumer when it builds its session. An
  OIDC login converges by the consumer mapping the subject to its own account id, and `oidc`
  stays unchanged.
- A TOTP code is good once, and the store enforces it. `AdvanceTOTPCounter` compares and
  writes in one statement and answers `ErrNotFound` when the counter did not move. Reading the
  counter and writing the whole record, the other shape found in the existing code, lets two
  racing logins with one captured code both pass.
- A TOTP secret counts only after a code from it was confirmed. An account usable at once with
  a secret shown once locks out anyone who loses that response before scanning it.
- Confirmation is derived, not stored. `ConfirmTOTP` writes the counter of the confirming code,
  so a secret is confirmed exactly when the counter is above zero. No confirmed column is
  needed. Zero means unconfirmed only for an account created through this contract.
- An adopted account needs the counter mapping. The existing applications keep a nullable last
  counter, and an account that never logged in has NULL there. Mapped to zero it would be
  unconfirmed, its login would answer `ErrInvalidCredentials` with the right password and code,
  and nothing could route it to a confirm without telling a refused login apart. So the adapter
  maps a NULL counter on a row that existed before adoption to 1, with
  `COALESCE(totp_last_counter, 1)` in its reads and conditional writes. Counter 1 is 1970, so
  no live code is refused. `Create` writes an explicit 0 and never NULL, so the mapping cannot
  confirm a new account. The alternative is a one statement backfill:
  `SET totp_last_counter = 1 WHERE totp_secret IS NOT NULL AND totp_last_counter IS NULL`.
  Without either, every adopted account that never logged in is locked out of password login.
- `ReplaceTOTPSecret` swaps a secret only while it is unconfirmed. Without it, a person who lost
  the enrolment response could neither confirm nor log in, and signing up again would find the
  login taken.
- An unconfirmed account is password only. No session exists before the first login, so the
  login logic gates `ReplaceTOTPSecret` on the password, and the password alone can then swap
  the secret, confirm it and own the account. For a fresh signup that is the accepted window
  between signup and confirm. For an adopted account it would downgrade a working second
  factor to the password, which the counter mapping above prevents.
- `ConfirmTOTP` takes the secret the code was verified against and writes only while that exact
  secret is stored and the counter is zero, in one statement. A `ReplaceTOTPSecret` landing
  between verify and confirm then makes the confirm fail, instead of confirming a secret the
  person never saw.
- `AdvanceTOTPCounter` refuses an unconfirmed account, so a login can never confirm a secret.
- The conformance suite `accounttest.Run` checks every guard an adapter has to hold. Each
  product runs it against its own storage:
  - the conditional counter write, and that `AdvanceTOTPCounter` refuses a counter of zero
  - that racing calls of `AdvanceTOTPCounter` with one counter succeed exactly once, and that
    racing calls with rising counters never move it back
  - that `ConfirmTOTP` refuses an account without a secret, a counter at or below zero and a
    secret that is not the stored one, and that racing confirms succeed exactly once
  - the unconfirmed guard of `ReplaceTOTPSecret`
  - an ignored caller supplied id and counter on `Create`
  - that racing signups of one login create exactly one account
  - that `FindByLogin` and `Create` agree on letter case. The adapter declares whether it folds
    case. Folding, a case variant finds the account and conflicts with it. Not folding, it is a
    separate account
  - that `SetPasswordHash` changes only the hash, and that no write reaches another account
  - that `SetPasswordHash` is a compare and set: it refuses an old hash that is not the stored
    one and changes nothing then, refuses an account without a password, and of racing writes
    from one old hash exactly one succeeds
  - that a disabled or pending account cannot be found for login, through a `Disable` function
    the adapter supplies
  - that `FindByLogin` matches exactly: `%` and `_` are no wildcards, and a prefix or an
    extension of an existing login finds nothing
- Each race check starts 32 calls at once behind a start barrier and repeats that 1000 times,
  `accounttest.DefaultRacers` and `DefaultRounds`. Both are `Harness` fields as well. A store
  that reads, compares in Go and then writes has a window of a few instructions and passes a
  single round most of the time.
- The defaults were measured, not argued. Five such stores without any sleep, one per race
  check, ran through their check 200 times each, with and without the race detector, on one
  15 core machine. With the earlier 16 racers and 10 rounds they escaped up to 178 of 200 runs
  without the race detector and up to 59 with it. With 32 racers and 200 rounds one escaped
  once. With the defaults every run caught every store. Without the start barrier, 200 rounds
  let one escape in 63 of 200 runs. The whole suite takes about half a second against
  `MemoryStore`, a few seconds under the race detector.
- That is still a measurement and not a proof. A test shows only the races it hits, and another
  machine or store latency hits them less often. Load lowers it as well: next to the CPU heavy
  Argon2 tests in the same `go test` run, such a store escaped the defaults in one of ten runs.
  An adapter that keeps its state in Go memory raises `Harness.Rounds`. A SQL adapter that reads
  and then writes has a window of a round trip and is far easier to catch. The meta test runs
  its broken stores with ten times the default rounds for that reason, and a separate test pins
  the defaults. The suite is a safety net. What proves a SQL
  adapter atomic is its statement, one conditional `UPDATE` with the comparison in its `WHERE`
  clause, checked by reading it. A product runs the suite with the race detector as well.
- With `GOMAXPROCS` 1 the racers take turns on one processor and a narrow race is never hit: at
  `go test -cpu=1` a read then write store passed every one of 200 runs. The race checks raise
  `GOMAXPROCS` to at least four while they run and restore it afterwards. The checks of one
  `Run` are not parallel tests. `GOMAXPROCS` is process wide, so other tests running at that
  time get more processors, which changes nothing they can observe. A test that changes
  `GOMAXPROCS` itself in that time is overwritten: the value the first race check saw comes back
  when the last one ends. Such a test does not run in parallel with a suite run.
- More processors than CPUs do not help on a machine with one CPU, where the threads take turns
  as well. There a race check fails with a message instead of passing, since a green suite that
  cannot see a race proves nothing. A product runs the suite on at least two CPUs.
- The check reads `runtime.NumCPU()`, which does not see a container CPU quota. The default
  `GOMAXPROCS` of the Go runtime does honour a quota, rounded up with a minimum of two, so a pod
  limited to one CPU starts at 2 and the suite raises it to 4. The quota caps the total CPU time
  of the threads, so in the same wall time fewer racing attempts overlap, and a green run there
  is weaker evidence. That is not measured. A pod pinned to a cpuset changes the affinity mask,
  and `runtime.NumCPU()` does see that. The job gets a quota of two CPUs or more, or the product
  raises `Harness.Rounds`.
- A test runs the suite in a child process against 39 stores that each break one guard, and
  expects the matching check to fail. Four of them run with `-test.cpu=1`. The read then write
  stores in it have no sleep, so the races they show are as narrow as in a real adapter.
- `accounttest.MemoryStore` is an in memory store that passes the suite, for tests of code
  built on `account`.
- Password and TOTP are checked in one login call, as before. A two step login needs somewhere
  to keep a half authenticated state, which is a second session to design and expire.
- Every refused login answers `ErrInvalidCredentials`, and a login against an unknown account
  pays one derivation, as a wrong password does, so neither the answer nor the work tells which
  part of a guess was right. The service section below says where the cost of that derivation
  differs.
- Passkeys are meant to follow in a separate `passkey` package with its own store, beside TOTP
  rather than replacing it. Losing one device must not lock somebody out of their records.
- Recovery codes are not part of the first version. Losing both the TOTP device and every
  passkey then locks the account, and the documentation has to say so.

#### Password hashing

- `Hasher` writes Argon2id in the PHC string both existing applications already write:
  `$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>`, with a 16 byte random salt and a 32 byte key.
  Their stored hashes verify unchanged.
- The parameters are fixed: three passes over 64 MiB with four lanes, the lower memory
  recommendation of RFC 9106. A deployment does not tune its own hashing cost by accident.
- `VerifyPassword` reads the parameters, the salt and the key length from the stored hash, so a
  later parameter change keeps every older hash verifiable. It reports `NeedsRehash` when a hash
  matched but would be written differently today. It never reports it without a match, so a
  failed login cannot cause a write.
- A stored hash is read from the consumer's database, and its parameters set the cost of a
  login. So the parser is strict and bounded: version 19 only, the parameters in the order
  `m`, `t`, `p` as canonical decimals, at most 64 MiB, 16 passes and 16 lanes, salt of 8 to 64
  bytes and key of 16 to 64 bytes in canonical unpadded base64. A corrupted or planted value
  answers `ErrPasswordHashInvalid` rather than a gigabyte allocation.
- Every `VerifyPassword` call runs exactly one derivation, whatever the outcome. The login logic
  passes an empty hash for an unknown account. No hash, an invalid hash and an over long
  password run it at today's parameters, so they cost what a wrong password for an account
  hashed today costs. A valid stored hash runs at its own parameters. A weaker one costs less,
  and the strongest the parser accepts, 16 passes, costs about five times today's three. The
  hashes of the existing applications use today's parameters.
- The comparison is constant time. A test reads the source as a tripwire: it refuses variable
  time helpers and a raw `==` of two values inside the comparers. A compare moved into another
  helper or a `switch` on a converted string passes it, so review stays responsible.
- The cap on concurrent derivations is `HasherConfig.MaxConcurrentHashes`, default 1. One
  derivation holds 64 MiB, and a finished block stays resident until the next collection.
  Measured in calbridge, not here, the heap peaked near twice the cap times 64 MiB, so one fits
  a 256 MiB container. A caller waits at most five seconds for a slot, then gets
  `ErrHashingBusy`, also when its context ends first. A handler answers that with 503, never
  with a failed login.
- The cap is one global slot per `Hasher`, not one per client. Sustained login traffic from
  anyone answers `ErrHashingBusy` for everyone, including people with the right password. The
  login route needs a per client rate limit in front of it: `ratelimit` with
  `middleware.NewRateLimit`, keyed by `clientip`, as `example/app.go` wires it. That does not
  stop traffic spread over many addresses. The login throughput one slot allows was not
  measured.
- A new password has at least 12 characters, counted as code points, and at most 1024 bytes.
  NIST SP 800-63B-4 section 3.1.1.2 counts code points, asks for at least eight characters
  where a second factor stands behind the password, and forbids composition rules. The
  existing applications count 12 bytes, so the code point minimum is stricter for non ASCII
  input. `VerifyPassword` does not apply the minimum, so an adopted account with a shorter
  password still logs in. The upper bound bounds the work on an unauthenticated route.
- No Unicode normalisation is applied, as in the existing applications, so their hashes keep
  verifying. The same NIST section says a verifier SHOULD normalise before hashing. Without it,
  a password typed once with composed and once with decomposed characters does not match.
  `CheckPassword` counts a NUL byte and each byte of invalid UTF-8 as one code point and refuses
  neither.

#### TOTP

- TOTP follows RFC 6238 on the standard library: HMAC-SHA-1, six digits, a 30 second step and
  one step either side of now. A secret is 160 random bits as 32 base32 characters without
  padding. A stored secret of 128 to 512 bits is accepted.
- `VerifyTOTPCode` returns the counter of the matched step. When one code is valid for two
  steps of the window, it returns the higher one. The lower one would let the same code pass
  `AdvanceTOTPCounter` again one step later. The code is consumed only when the
  caller hands that counter to `AdvanceTOTPCounter`, or to `ConfirmTOTP` during enrolment, and
  the conditional write succeeds. Comparing it with a counter read earlier is not replay
  protection.
- A step whose counter is below one never matches. Counter zero means an unconfirmed secret in
  the store, so a code of that step must never reach it. The step counter rounds down for a time
  before 1970 as well.
- An invalid secret runs the same three HMACs against a dummy key and is refused, so an empty
  secret for an unknown account costs what a real one does. A test counts the HMACs through an
  injected function. A secret longer than 103 characters, the base32 length of 512 bits, is
  refused before anything is allocated for it.
- `TOTPProvisioningURI` takes the issuer as a parameter and percent encodes the issuer and the
  account name, with a space as `%20`. Both are refused when empty, too long, not printable or
  holding the colon the label format reserves. The secret is validated and written in upper
  case.
- Both checks are pure functions without the store. `account.Service` combines them with the
  store.

#### The service

- `account.Service` has `SignUp`, `ConfirmTOTP`, `Login` and `ChangePassword`, built on `Store`
  and `Hasher`. The HTTP handlers and the Vue forms on top of it are the next section.
- `SignUp` validates the login, applies the password policy, then generates the secret, hashes
  and calls `Create`. The secret starts unconfirmed. The login must be printable, at most 254
  bytes, without a colon and without a leading or trailing space. Whether it must be an email
  address is the application's check.
- The service does not normalise logins. Letter case, Unicode composed and decomposed forms and
  confusable characters, a Cyrillic `а` for a Latin `a`, are the adapter's or the application's
  to handle, the same way before `SignUp` and before `Login`. Without that, `josé` typed composed
  and decomposed are two accounts.
- The service never trims a login or a password. calbridge's login trimmed the login before the
  lookup. A product adopting `account` keeps trimming in its handler where it did before, or a
  login typed with a leading space no longer finds its account.
- `Login` and `ConfirmTOTP` never send a login to the store that `SignUp` would refuse as a
  label: empty, too long, not valid UTF-8, not printable or holding a colon. Such a login is
  refused like an unknown one, after the same work. A Postgres text column answers a NUL byte
  with an error, which would otherwise turn the refusal into a store failure without the equal
  work. An adopted account whose login holds a colon cannot log in, and needs its login
  changed.
- `ConfirmTOTP` takes the login, the password and a code. No session exists before the first
  login, so the password gates it. It writes through `Store.ConfirmTOTP`, so the confirming code
  is consumed and cannot log in afterwards. It returns the `Identity`, and the application may
  start a session with it.
- `Login` checks the login, the password and the code in one call. A secret that is not
  confirmed cannot log in. The service checks that itself, and `AdvanceTOTPCounter` refuses it
  as well.
- `AdvanceTOTPCounter` is the only replay guard. The service never compares the code's counter
  with one it read. A test lets several logins read the account before any of them writes, and
  exactly one gets through. Over a store that reads and then writes, more than one does, which
  shows the race in the test is real.
- Every refusal of `Login` and `ConfirmTOTP` answers `ErrInvalidCredentials`, unwrapped, after
  one Argon2id derivation and three HMACs over the window. That holds for an unknown login, a
  disabled account, an account without a hash or a secret, a wrong password, a wrong, missing
  or replayed code and an unconfirmed secret. Tests count the work through injected functions,
  not wall time. The conditional counter write runs only after both factors matched, so its
  time tells nothing to whoever does not hold both already.
- The derivation runs at today's parameters when there is no usable hash: no account, no
  password, a corrupted hash. For an existing account with a valid hash it runs at the
  parameters stored in that hash. Today every hash uses today's parameters, so all refusals cost
  the same. After a parameter change, a wrong password for an account still on the old
  parameters costs what those say, so the time can tell such an account from an unknown login
  until a successful login rehashes it.
- A corrupted stored hash is logged at debug level with the account id only. Anyone can reach
  that path without credentials, so a warning per request would let them flood the log.
- A login that could not be decided is not a refusal. `ErrHashingBusy` and a failing store
  answer their own errors, and neither consumes the code.
- A login whose hash is outdated rewrites it at today's parameters. That is best effort: a
  failure is logged with the account id and the cause, never with a password, a hash, a secret
  or a code, and the login still succeeds. A password below today's policy, kept by an adopted
  account, keeps its old hash.
- `SetPasswordHash` is a compare and set. It writes only when the stored hash is not empty and
  equals the old hash the caller read and verified, in one statement, and answers `ErrNotFound`
  otherwise. Without it, a password change landing between a login's lookup and its rehash
  write would be overwritten with the old password, and of two racing password changes both
  would report success while one is lost.
- The rehash writes from the hash the login verified. A change that landed meanwhile makes the
  write fail, the change stands, and the rehash is dropped with a debug log line.
- Refusing an empty stored hash means `account` can never give a password to an account that
  has none, such as one created through OIDC alone.
- `ChangePassword` checks the new password against the policy first, then looks the account up
  by the identity's login and refuses when the record's id is not the identity's id. It verifies
  the current password, hashes the new one, stores it with `SetPasswordHash` from the verified
  hash and then calls the `EndSessions` callback the application supplies. `account` imports
  nothing from `session`.
- Of two password changes racing from the same password, exactly one wins. The other answers
  `ErrStale`, writes nothing and ends no session, since the winner already ended them. A
  rehash of a login with the same password landing in between yields `ErrStale` as well, since
  it rewrites the hash. The caller asks the person to try again.
- The order is the rule of the device sessions above: commit the new credential first, then end
  the sessions. Ending them first leaves a window in which a session started with the old
  password, which still verifies, survives the change. A test records the order. The
  application's callback ends the device sessions as well, for example through `EndAll` with a
  device store configured.
- `EndSessions` runs with a context detached from the request's cancellation but keeping its
  values, so a client that hangs up after the commit does not keep its old sessions and an
  adapter still finds its tenant. `ServiceConfig.EndSessionsTimeout`, 30 seconds by default,
  bounds it, so a hung callback cannot hold the call forever. A timeout below one second is
  refused, since it would end the callback before it can run. A long one is accepted.
- Once the current password is verified, `ChangePassword` has four outcomes:
  - nil: the password changed and every session ended
  - `ErrSessionsNotEnded` with the cause: the password changed, ending the sessions failed or
    timed out. The change stands and is not rolled back. The caller retries ending the sessions
    and does not tell the person the change failed
  - `ErrStale`: the compare and set was refused, nothing changed and no session ended. The
    caller asks the person to try again
  - another error, the store's: whether the write committed is not known, since a driver can
    commit and still report the context ending. The sessions are ended anyway, since ending them
    once too often is harmless and a changed password with live old sessions is not. When ending
    fails as well, its error is joined in without `ErrSessionsNotEnded`, which would claim the
    password changed. The caller tells the person the change may not have happened
- `ErrSessionsNotEnded` is only returned when the write is known to have succeeded. A handler
  that follows its doc and reports success must never do so for a write that did not happen.
- `SignUp` answers `ErrConflict` for a taken login. With this flow an account existence oracle
  exists: signing up with a login tells whether an account exists. Answering `ErrConflict`
  like a success does not hide it, since the confirm step tells again: the enrolment shown
  confirms for a fresh login and is refused for a taken one. Both existing applications accept
  the oracle. A product that must hide account existence has to defer creating the account
  until the person proved control of the address, for example with a mailed link, and
  `SignUp` does not support that. The work is the same either way, one derivation before the
  store is asked. The signup route needs the same per client rate limit as the login route.

#### HTTP handlers and Vue forms

- `account.NewHandlers` returns one `http.Handler` per step: `Signup`, `ConfirmTOTP`, `Login`,
  `ChangePassword` and `Logout`. A product mounts the ones it offers on POST routes. Registration
  policy is the product's: it mounts the signup handler or not, and an optional
  `AllowSignup func(*http.Request) error` decides per request. A returned `*api.Error` is
  answered as it is, any other error as 403 without its text.
- The handlers take callbacks like `oidc.HandlerConfig`, so `account` imports nothing from
  `session`. `StartSession` turns a verified `Identity` into a session, `EndSession` ends the one
  session of a logout, and `CurrentIdentity` reads the account of a request for the password
  change. The service's `EndSessions` ends every session after a password change. All four are
  required. In `example/` they are `Manager.Start`, `Manager.End`, the session in the request
  context and `Manager.EndAll`, with the account id as the session's subject.
- `account` imports `api` and `logging`, for the error shape and the strict decoder. A second error
  shape for the account routes would be worth more than the import only if a product wanted one,
  and `api` imports nothing but `logging`, so it pulls no session code in.
- One login call checks the password and the code, as the service does. There is no half logged
  in state between two requests. The confirm handler starts a session as well: it proved both
  factors, the service says the caller may, and without it the person would have to wait for the
  next code, since the confirming code is consumed. A product that wants no session there has no
  switch for it yet. Whether it should is open.
- Bodies and answers are JSON objects with named fields, never a bare value. The request types
  are `SignupRequest`, `CredentialsRequest` and `PasswordChangeRequest`, the answers
  `SignupResult`, `LoginResult` and `PasswordChangeResult`. Fields may be added to an answer in a
  later release, so a client ignores names it does not know. The token of a native app is not one
  of them: `LoginResult` stays `login` only, and the token comes from `devicehttp`'s `Issue`
  route. A request refuses a field the type does not name. The Vue types carry an index
  signature for the same reason.
- A body has to be `application/json`, one object, at most 16 KiB by default, with no unknown
  field. `api.DecodeLimit` accepts a missing content type, because a form post is caught by the
  decoder anyway. The handlers refuse it with 415, since a cross site form can post a body that
  parses as JSON under `text/plain`, and this check is one more thing it has to get past.
- Every handler checks the method, then the origin with `net/http.CrossOriginProtection`, then the
  body. The origin check lives in the handler because a login is the one request that has no
  session to bind a token to, and mounting it behind a middleware somebody forgot must not leave it
  open to a cross site form. `Sec-Fetch-Site` of `cross-site` or `same-site`, and an `Origin` that
  is not this host or one of `TrustedOrigins`, are refused with 403, with or without a session. A
  request with neither header passes, as it does for `csrf`: that is every client that is not a
  browser, which is also why a login still needs the rate limit. The session bound token of
  `csrf` stays in the application's chain, so password change and logout carry both.
- A refused login and a refused confirmation answer 401 with code `invalid_credentials` and one
  message, for an unknown login, a wrong password, a wrong, missing or replayed code, an
  unconfirmed secret and a disabled account. A table test compares the whole answer, status,
  headers and body, across every cause. Only `request_id`, added by the request id middleware,
  differs per request. A request that is not a request of this shape, such as a missing field,
  is not special cased: it runs through the service and gets the same answer.
- `ErrHashingBusy` answers 503 with `Retry-After: 2` and the message of `api.Unavailable`, marked
  `Exposed`, since the server chose it. It is no failed login, and a 401 would tell a person with
  the right password that it is wrong.
- A client that treats every 401 as a lost session, as `createClient`'s `onUnauthenticated`
  does, sees a failed login as one. The forms opt out per request, see below.
- A password change answers the four outcomes of the service. 200 with
  `{"password_changed": true, "sessions_ended": true}` is the clean one. 200 with
  `sessions_ended` false is a change that stands with `EndSessions` failed, logged, and the
  application retries ending them. 409 `stale` says nothing changed and the person tries again.
  500 `password_change_unknown` carries a message on purpose, `Exposed`, since the person must
  hear that the change may not have happened and that logging in with the new password tells.
  A wrong current password is 403 `invalid_credentials`, not 401, so a client that logs out on a
  401 does not log a person out for a typo. A new password below the policy is 400 with
  `new_password` in the details as `too_short` or `too_long`.
- The change ends every session of the account, the caller's own included, and the handler
  starts none. The person logs in again. A product that wants to keep the caller in starts a
  session in its own handler after the change.
- No password, code, secret, hash or enrolment URI is logged or put into an error. A test runs
  every flow with failures injected into the store, the session callbacks and the context, at
  debug level, and searches the log for each value.
- `example/` mounts the handlers under `/api/v1/account`, over the in memory store of
  `accounttest`, behind the same chain as every other route. Login, TOTP confirmation, password
  change and signup sit behind a second rate limiter, `AUTH_RATE_LIMIT_BURST` and
  `AUTH_RATE_LIMIT_PER`, a burst of five refilled over a minute by default, because a guess
  costs an Argon2id derivation and the hasher has one slot by default. TOTP confirmation and password
  change are included since both check a password. Signup is mounted only with
  `ALLOW_SIGNUP=true`. The in memory store is the conformance fake, so every account is gone on a
  restart.
- Three Vue components take a `client` from `createClient` and never assume a cookie.
  `GovueLoginForm` and `GovueSignupForm` post to `/account/login` and `/account/signup` under the
  client's base URL, `path` overrides it. `GovueTotpEnrolment` shows the secret and the
  provisioning URI in read only inputs, a link that opens an authenticator app when the URI is an
  `otpauth` one, and asks for the password and a code to confirm. No QR code: that is a library,
  and the secret and the link work without one.
- The forms show their own texts from both catalogues under `govue.account`. The server's message
  is never shown: it is English, and the code and the field details decide the text. Every
  field has a label, a failure is announced, a request in flight disables the fields and the
  button, and a `disabled` prop does the same. A password and a used code are cleared once a
  request went through, and kept after a failure, so a retry costs one field.
- `GovueTotpEnrolment` asks for the password again instead of receiving it from the signup form,
  so no secret travels through the parent, and a reload between the two steps costs only one
  field. It also holds a visually hidden read only input with `autocomplete="username"` and the
  login, so a password manager saves the password under the right name. The parent drops the
  enrolment secret after `confirmed`.
- Four routes need a per client rate limit, not two: signup, login, TOTP confirmation and
  password change. Each costs an Argon2id derivation and the default is one slot, so without a
  limit anyone keeps the hasher busy and everybody gets 503. Confirmation needs no session.
  Password change answers 403 for a wrong current password and 200 for the right one, so a
  stolen session can test passwords as fast as the hasher allows. The package doc and the doc
  comment of each handler say so, and tests in `example/` pin the 429 of all four.
- A login does not end a session the request already carries, because `account` does not know
  sessions. In `example/`, `StartSession` ends the old session first, unless the session layer
  classifies the request as bearer (`bearer.Classify`, the rule `Manager.End` and `Start` use):
  a bearer request gets no cookie session, and ending its device session would sign the app
  out. Any other `Authorization` header, such as a Basic one from a proxy, is an ordinary cookie
  request. If ending the old session fails, no new session is started and the login answers an
  opaque 500, so a failure never leaves two live sessions. The code is consumed by then. The
  clearing cookie of `End` is written before the new one, since a browser keeps the last.
- The ids of local accounts and the subjects of an OIDC login share the namespace of the
  session's subject, which `EndAll` keys on. A product that runs both prefixes one of them, for
  example `local:` before the account id, or an OIDC subject could equal a local id and share
  its sessions. The memory store of `example/` keeps its plain `memory-1` ids, and the example says
  so in a comment, since it runs no OIDC account next to them.
- Bodies are decoded by Go's `encoding/json` with unknown fields refused. Field names match case
  insensitively, of two equal keys the last wins and invalid UTF-8 becomes U+FFFD. A proxy or a
  WAF that parses JSON differently is out of scope. `AllowSignup` runs before the body is read
  and cannot take an invite code from it, so an invite check uses a header, a cookie or a query
  parameter.
- `createClient` calls `onUnauthenticated` on every 401, a failed login included. The forms set
  `skipUnauthenticatedHook` on their requests, so a wrong password does not look like a lost
  session. The option is per request, additive and false by default.
- A form used while a person is logged in, for a second login or a confirmation, sends no
  CSRF token unless the client has `csrfToken` set, and the server answers 403, which the form
  shows as a refused request. An application with such a flow sets `csrfToken` in
  `createClient`.

### The bearer client of a native app

- `createClient({ auth: { mode: 'bearer', storage, rotateAfterMs, rotation, deviceName } })` is
  the client of a native app. It returns a `BearerClient`: the same `get`, `post`, `put`, `patch`
  and `del`, plus `signIn` and `signOut`. The default is `auth: { mode: 'cookie' }`, and its
  requests are byte for byte what they were. The option is one object, not flags, so the modes
  cannot mix.
  Bearer mode refuses the `credentials` and `csrfToken` options at construction, since both belong
  to the cookie.
- A bearer request carries `Authorization: Bearer <token>`, uses `credentials: 'omit'`, sends no
  CSRF header and never reads `document.cookie`. The client owns the `Authorization`, `Cookie` and
  `X-CSRF-Token` headers: a caller's copy is dropped. The token is read from the storage at send
  time, never kept in a queue. A request without a stored token goes out without an
  `Authorization` header and is never a reason to rotate.
- `TokenStorage` has three async methods, `get`, `set` and `clear`. It holds ONE opaque string. The
  client writes a small JSON record in it, `{"v":1,"token":"...","at":<ms>}`, where `at` is the
  time the token was issued or last rotated. The interval check needs `at`, and a second storage
  key would let the two drift apart. An adapter stores and returns the string unchanged and never
  logs it. A value the client cannot read is a failure, not an empty storage.
- govue ships the in memory adapter, `createMemoryTokenStorage`, and nothing persistent. A product
  that keeps the sign in across app starts supplies an adapter backed by the platform keychain.
  govue has no Capacitor dependency and does not ship a plugin.
- Rotation follows ADR 0001, with the differences listed below. The client rotates whenever the
  stored token is older than `rotateAfterMs`, 24 hours by default, and on the first use after the
  app start when the token is at least one hour old. A rotation is one call to
  `POST /auth/token/rotate` with a random nonce from `crypto.getRandomValues`, single flight:
  concurrent requests share the one in flight. Its timeout is 10 seconds, below the 30 seconds of
  the server's rotate timeout. The new token is written to the storage before any request uses
  it. A request that waits for a rotation sends the new token.
- One context owns the storage. Exactly one context per storage is the owner. A client on the
  default in memory storage nobody else can read is always the owner, so `auth.rotation` is
  optional there and its only value is `'owner'`. A client with its own `auth.storage` must say
  `rotation: 'owner'` or `rotation: 'none'`, with no default. A product that shares a keychain
  adapter with an extension or a background runner and forgets the option would start a second
  owner, and two owners on one storage revoke the device. The options type is a union, so a
  missing `rotation` next to a `storage` is a compile error, and `createClient` throws a
  `TypeError` for it at runtime, for a caller without types. `rotation: 'none'` without a
  storage is refused the same way, since a `'none'` client reads the storage of an owner and has
  none to read. The owner of the repository decided this on 2026-11-18.
  The owner rotates, signs in, signs out and clears the storage on a 401. Every other context
  that reads the same storage, an app extension or a background runner, sets `rotation: 'none'`.
  That client never rotates, uses the stored token as it finds it, tells the app about a 401 once
  per token and leaves the storage to the owner. Its `signIn` and `signOut` refuse. Two rotating
  contexts revoke the device: the second rotation carries another nonce and retires the pending
  token of the first, and the first one's use of it is a replay. An option, not a hidden default,
  so that a product states which context owns the token.
- All reads and writes of the storage run one after another. A read that starts while a write is
  pending waits for it. The generation counter moves inside that order, together with the write.
  A rotation writes only when the generation is the one it started with and the storage still
  holds the token it rotated, read again inside the lock. So a sign in, a sign out or another
  writer that got there first wins, and the rotation result is dropped. Two overlapping sign ins
  end with the one whose write came last.
- A network error or a 5xx on the rotation keeps the old token. The next request tries again with
  the same nonce, as the server allows within its rotate timeout. A 409 `rotation_refused` is quiet:
  the token stays, nothing is signed out, and the client waits five minutes before it asks again.
  A 401 clears the storage and calls `onUnauthenticated` once, however many requests wait. A 401
  on an ordinary request does the same, unless that request set `skipUnauthenticatedHook`.
- `signIn(credentials, { deviceName })` posts `login`, `password`, `code` and `device_name` to
  `POST /auth/token`, stores the token and answers `{ deviceId, expiresAt }`. The token is not in
  the answer. It sends no `Authorization` header, so a stale token cannot make the server refuse
  valid credentials, and it replaces a stored token without ending that device on the server. The
  device name is checked before anything is sent: at most 64 bytes, no control or format
  characters. These are the rules of the server, and a bad name costs no verification there.
- `signOut()` calls `DELETE /auth/token` and clears the storage whatever the call answered. A 401
  counts as done. Any other failure is thrown after the clear, so the app knows the server side
  may still be live and can revoke the device from another one.
- A storage that throws fails closed. The client throws an `ApiError` with code `token_storage`
  and sends nothing, so a broken keychain never turns into an unauthenticated request. The error
  carries no cause, since the adapter's own error may quote the value.
- An app answers a `token_storage` error with `signOut()` or `signIn()`. A value in the storage
  that the client cannot read fails every request with that error and calls no
  `onUnauthenticated`, because no server said the sign in is gone. `signOut()` clears it.
- A 401 that an explicit `signOut()` causes does not call `onUnauthenticated`: the app asked
  for it.
- The token appears in no URL, no header but `Authorization`, no error message and no console
  output. A transport error, an unreadable body and a body that is not JSON are thrown without
  their cause, since a failed `fetch` may quote the request and a `SyntaxError` quotes part of the
  body, which is the token for an Issue or Rotate answer. The client object has no method that
  returns the token.
- Differences from ADR 0001, all of them:
  - No fallback. A 401 for a pending token that has not succeeded yet does not send the client
    back to its current token with a new nonce. The client keeps one token and a 401 clears it.
    This is a documented limit. A lost rotate answer is harmless: the old token stays current, a
    retry with the same nonce inside 30 seconds works, and later the unused pending token is
    retired without a revocation. Only a rare case forces a new sign in: the delayed original
    rotate reaches the server between the answer of the retry and the first use of its token.
  - The start rule. The ADR says to rotate on every start and also that a refused rotation is
    quiet and that the app rotates on start only when the interval has passed. The client rotates
    on start only for a token at least one hour old, the server's default minimum interval, so a
    fresh token costs no 409. A server with a longer minimum interval answers one quiet 409 per
    start.
  - The owner option above. The ADR says one context owns rotation. The client makes that an
    option.
- Not shipped: a cleanup of the device that a failed storage write leaves behind after an
  issue, and a persistent adapter.
- Limits to know. A stored time `at` in the future, from a clock that moved back, counts as due,
  so the client asks for a rotation, gets a 409 and asks again every five minutes until the clock catches
  up. After a network error on a rotation every request first waits for the next rotation attempt,
  up to its 10 second timeout, until the network is back. The rotate timeout stays below the
  server's 30 seconds, so a shorter wait would cost the retry with the same nonce nothing, but it
  is not built.
- A native app also needs the server half: `AppOrigins`, `AppCORS` and `TrustOriginOnRoute`, see
  "App origins". The web view of the app sends its own origin, which the server must list. The
  steps in order are in "Enabling app mode".

### Theming through a published token contract

- `web/src/styles/base.css` registers every role token as a Tailwind v4 theme variable with a
  generic, neutral default. Tailwind v4 emits `var(--color-x)` in its utilities, so a token can
  be overridden under a selector at runtime.
- A theme is a file overriding those tokens under `:root[data-theme='<name>']`. Light and dark
  are one declaration per token through `light-dark()`, so the two values of a role cannot drift
  apart, which is the usual way a dark mode rots. `data-mode` on the root element pins
  `color-scheme`; without it the system decides.
- A component names a role, never a colour, and carries no hex value. Switching theme or mode is
  one attribute on `<html>`.
- What this repository publishes is the contract and one or two genuinely generic example themes,
  enough to prove the mechanism and to give a consumer with no design a starting point.
- A specific brand's theme is authored in the consumer's own repository, as a CSS file filling
  the same contract, imported next to these base styles. It is never committed here. The
  mechanism is identical either way, only the file's home differs, and a public repository then
  carries no real brand's colour values for anyone to copy.
- That rule reaches further than the identity colours. A near-white page ground has about twenty
  usable levels, so a grey one is always within a unit or two of some real theme's ground, and
  "no value anyone can copy" then rules out a grey default rather than only a branded accent.
  The committed palettes therefore carry a deliberate hue in the neutral ramp, and every value is
  a round HSL coordinate whose saturation and lightness follow from the contrast requirement, so
  the palette can be re-derived from the rules instead of being taken on trust.
- Contrast is a test, not a review step: the theme files are parseable, so a test fails any pair
  below WCAG 2.2 AA in either mode.

### The theme switch and its boot script

- `GovueThemeSwitch` is a radio group of three native buttons, system, light and dark, over
  `useTheme`. Only the active option is in the tab order and the arrow keys move the choice,
  which is the keyboard model a screen reader user expects of a radio group. Its strings are
  props that override the catalogue of the language layer. `GovueHeader` shows it through a
  `themeSwitch` prop, off by default.
- A choice kept in `localStorage` is only readable once script runs, so a page that waits for
  the bundle flashes the wrong theme. The Go package `themeboot` injects a small inline script
  into the head that sets `data-theme` and `data-mode` before the first paint.
- Under the default policy an inline script runs only with the response's nonce, so injection
  happens at serve time and takes the nonce from `middleware.Nonce`. A static `index.html`
  cannot carry one. The response is `no-store`, since a cached nonce is a reused nonce.
- The script is a plain file embedded in the Go package, not generated from the TypeScript. The
  key and the attribute names live in `themeStorage.ts` and in `themeboot` as constants, and a
  Vitest test runs the shipped file in a DOM and checks it against both, so a drift fails a
  test instead of showing up as a flash.
- With nothing stored the script leaves `data-mode` off, because the stylesheet already follows
  the system setting through `light-dark()` and an explicit value would only pin it.

### The language layer

- Every site built on govue is German and English, so the translation lives here once, not per
  site. `vue-i18n` in Composition API mode carries it. It is a peer dependency like `vue`: the
  plugin installs onto the app's instance, and two copies would be two injection keys and two
  states. A site that already depends on `vue-i18n` passes its instance and shares it.
- `createGovueI18n` merges the govue catalogue under one `govue.` namespace, picks the language
  (stored choice, browser, German), keeps `lang` on the document element in step with the
  locale, and provides the state the components and the switch read. The languages are listed in
  one place, `web/src/i18n/catalogues.ts`, so a third language is a catalogue file and one line.
- Components take their default text from the catalogue and keep their text props as overrides.
  The props no longer carry English defaults: left out, they follow the language. Without the
  plugin the components read the English catalogue directly, so an app that never installs it
  sees what it saw before.
- `GovueLanguageSwitch` is a native select, not a radio group like the theme switch. The list
  of languages is open ended, a select stays one control at any length, and the platform gives
  it keyboard handling, screen reader support and a touch picker. Focus stays on it after a
  change, and nothing navigates.
- Catalogues are TypeScript files, not JSON, so each carries the license header and no
  exception is needed. Only visitor-facing text is translated; developer errors stay English.
- The choice is kept in `localStorage` by default. A cookie or an account callback serves
  signed in areas that need no path prefix. A test compares the key sets and the named
  placeholders of all catalogues, so a missing key fails the build.
- The redirect at `/` and prerendering are routing and are not part of this layer.
  Prerendering is the render entry described below. The route helper is described in the next
  section.

### Language routes

- Public pages live under `/de/` and `/en/`. `web/src/i18n/routes.ts` is pure TypeScript with no
  Vue import and reads the languages from `catalogues.ts`, so the build, the sitemap and the
  client navigation share one implementation.
- A site writes its route table as plain data, `[{ path: '/impressum', public: true }]`.
  `public` defaults to false, so a forgotten flag hides a page from the sitemap instead of
  exposing an app route. App routes keep no prefix and are not in any output of the helper.
- Route paths are decoded text. `validateRoutes` rejects a path without a leading `/`, with `//`,
  a dot segment, a query, a fragment, a `%`, a backslash, a trailing slash (except `/`), a
  language prefix or a duplicate. It also rejects Unicode white space (`\p{White_Space}`, which
  covers the no break space, U+2028 and U+2029), control characters (`\p{Cc}`, which include
  DEL), format characters (`\p{Cf}`, such as the zero width space and the byte order mark) and a
  lone surrogate (`\p{Cs}`). Every error starts with `govue route:`.
- `localizeRoutes(routes)` returns one `{ source, locale, path }` per language for every public
  route. The root becomes `/en/`, every other path `/en/impressum`.
- The route table is the source of the canonical form: `/de/impressum` has no trailing slash and
  the root is `/de/`. The canonical link, the alternates and the sitemap all use it.
- `localizedPath(path, code)` swaps or adds the prefix, so a language switch passes the decoded
  `location.pathname` and navigates to the result. A single trailing slash on the input is
  dropped, so `/de/impressum/` gives `/en/impressum` and the root stays `/en/`. An empty
  segment inside the path, such as `//`, still throws. `localeFromPath(path)` reads the language
  from the first segment, exact match only, so a site can pass it as the `locale` of
  `createGovueI18n` on a public page. Both take decoded text, never the percent encoded
  `location.pathname`: a `%` throws.
- `hreflangLinks(variant, origin)` returns the head markup of one variant: a self referential
  absolute canonical, one alternate per language and `x-default`, which points at the German
  variant of every page. `sitemapXml(routes, origin)` lists every variant with the same
  alternates and no `lastmod`. It fails above 50,000 URLs, above 50 MiB of UTF-8 for the whole
  document and for a URL longer than 2048 characters, the limits of the sitemap protocol.
- The origin is `https://host[:port]` with a lower case scheme and host, a punycode host, no
  default port, no port 0, no trailing dot and no path, query, fragment or userinfo. `http` is
  accepted for `localhost` only. The generators validate it themselves, so the check is not
  exported. Path segments are percent encoded, then escaped for markup.
- Serving is a requirement on the Go side, because the build writes `de/impressum/index.html`
  while the canonical URL is `/de/impressum`. A directory file server such as
  `http.FileServerFS` answers `/de/impressum` with a redirect to `/de/impressum/`, so the
  canonical URL would redirect. govue ships no directory handler. A site serves the exact path
  of every route (`GET /de/impressum` and `GET /de/{$}`) and answers the slash form with 404.
- The example (`example/site.go`) is the model. Go keeps no copy of the route table: at startup
  it reads `sitemap.xml` from the prerender output, takes the path of every `loc` and registers
  one exact pattern per page, each served through `themeboot.Shell`. A page is read into memory
  once, so no request path is ever joined to a file name and a directory listing cannot exist.
  Startup fails, naming the `loc`, for:
  - a sitemap that is not a `urlset` in the sitemap namespace, such as a `sitemapindex`, one
    over 50 MiB, a `url` without a `loc` or with more than one, and locs of more than one origin.
  - a `loc` that is not a plain path under a configured language: a relative or non HTTP URL,
    userinfo, a query or a fragment, a bare `/de`, a dot, empty or control segment, white space,
    `\`, `{`, `}` or `%` in a decoded segment, and a `%2f` or `%5c` in the written path, because
    decoding it would change the segment count.
  - a duplicate, a page file that is missing, has no `<head>` or links out of the directory.
  The directory is opened with `os.OpenRoot`, not `os.DirFS`: a symbolic link inside it that
  leaves it is refused, and so a page that links out fails startup. A link that stays inside is
  allowed. `SITE_DIR` points at the directory, and the example closes it on shutdown. Without it
  the example mounts neither the redirect, the pages nor the assets.
- A route path of the table may not hold `{` or `}`: `validateRoutes` rejects them, because the
  Go side registers the path as a `ServeMux` pattern, where they are wildcard syntax.
- The build's `assets/` directory (Vite's default name) holds the client bundle and the
  stylesheet, so a real build renders without them otherwise. The example serves it with
  `GET /assets/{file...}`, the one prefix pattern of the site, because the files are hashed and
  not in the sitemap. The handler answers a regular file only: a directory, a trailing slash, a
  missing file and a link out of the site directory give 404, and nothing is listed. A file is
  sent with `Cache-Control: public, max-age=31536000, immutable` and `nosniff`, and its content
  type comes from the extension. The files of `public/` that sit next to `index.html`, such as
  the favicon, are not served by the example's page handlers. A site serves them itself.
- `GET /{$}` carries the `language` redirect and sits in the same route chain as every app
  route, so CORS, the rate limiter and the CSRF check see it. The middleware order does not
  change. The example's tests build a directory like the prerender output, request every `loc`
  of its sitemap through the real handler and expect 200, the HTML content type and no
  `Location` header. They also check the redirect order, the crawler case, 404 for the slash
  form, an unknown prefix and a file the sitemap does not list, and path traversal attempts.
  `ServeMux` answers a bare `/de` with a 307 to `/de/` before any handler runs, which a test
  pins. A path it has to clean is redirected the same way. `/de/../x` leads to a 404, but
  `GET /de/../en/` gets a 307 to `/en/` and is served there. Those 307s come from the mux
  before any handler runs, so they carry no `Vary` and no `Cache-Control`, and a raw client
  can reach another language this way. That is accepted: it is a path the client chose, no
  crawler follows it, and the target is a listed page that answers 200 itself. A test pins it.
  A POST to a page answers 404 from the catch-all. The 405 of `language.Redirect` is not
  reachable through this mux, because no pattern of the site is registered without its method.
- An unprefixed public path such as `/impressum` is not redirected and answers 404. The
  cookie storage of the language layer stays an option and is not the default.

### The language redirect at the site root

- Public pages live under a path prefix per language, `/de/` and `/en/`. The Go package
  `language` sends a visitor of `/` to one of them. It is its own package: `middleware` imports
  `api`, `logging` and `ratelimit`, `session` is for server side state and the cookie here is
  written by the client, and `themeboot` would pull `favicon` and `middleware` into a redirect.
  It imports no other govue package.
- The language comes from the language cookie when its value equals a configured code exactly,
  else from `Accept-Language`, else from the default, German. The cookie is the one the Vue
  package writes with its `cookie` storage kind. The default `localStorage` storage is invisible
  to the server, so a site with public pages picks the cookie kind. A test in `web/` compares
  the cookie name in both halves.
- The redirect target is built from the configured codes only: `/`, a code, `/`. The path, the
  `Host` header, the cookie value and the header text never enter it, so there is no open
  redirect and no header injection. The query string is carried over with every byte that is not
  safe in a header escaped.
- The handler is mounted for `GET /{$}` and answers 404 for any other path. A request for
  `/en/` never reaches it, so a crawler and a visitor with a stored choice keep the language of
  the URL they asked for. `HEAD` follows the `GET` pattern. Other methods get 405 when the handler is
  mounted as `GET /{$}`, because no method-less pattern matches `/`. The handler answers 405
  itself too.
- The cookie rules differ in two corners from the Vue reader. The Go side accepts a quoted value
  and takes the first value that equals a configured code among several cookies of the same name.
  The Vue `readCookie` takes the first cookie of that name. Both agree on every value the Vue
  package writes.
- The status is 302, not 301 or 308: the target depends on the visitor, and a permanent
  redirect would be cached by the browser. The answer carries `Cache-Control: no-store` and
  `Vary: Accept-Language, Cookie`, added to the header so a `Vary: Origin` from the CORS
  middleware stays.
- `Negotiate` reads the header with bounds. A value over 4 KiB counts as absent and only 32
  entries are read, the bound `clientip` uses. Tags match by primary subtag, so `de-AT` is `de`.
  A quality must be 0 to 1 with at most three decimals. An entry with another quality is
  dropped and the rest still count. A quality of 0 excludes a language. `*` gives its quality
  to every language the header does not name, and `Redirect` lists the default first so the
  wildcard means the default. No match means the default.
- An unprefixed public path such as `/impressum` and an unknown prefix such as `/fr/` are not
  redirected. They fall to the catch-all 404, because redirecting them would invent URLs and
  need the route table in Go.
- `ServeMux` answers `/de` with a 307 to `/de/` and keeps the query. A test pins it.

### Build time pre-rendering

- A landing page needs real markup for crawlers and a fast first paint. Per-request SSR would
  need a live Node process, hydration handling and per-route data fetching, which is the
  framework shape this repository avoids. So the page is rendered once, at build time, into a
  static HTML file the Go server serves like any other static asset. Nothing changes in the
  backend and no Node process runs in production.
- `@ostendorf-cloud/govue/server` (`web/src/entry-server.ts`) is the render entry. The package
  only reaches `vue/server-renderer` through that separate export, so a browser bundle never
  pulls it in. The contract is deliberately small:
  - `render(root, { props, setup })` returns the HTML of one root component as a string. `setup`
    receives the app before it renders, which is where a plugin such as `createGovueI18n` is
    installed.
  - `injectIntoTemplate(template, html, placeholder)` puts the markup into a template, and
    throws when the placeholder (default `<!--app-html-->`) is missing or appears twice.
  - `render` takes no route, no language and no template. The caller installs the language in
    its own `setup`. `localizedFiles` below adds the routes, `<html lang>`, canonical and
    `hreflang`.
  - It runs in Node with no DOM, keeps no state of its own and swallows no error: a failing
    component rejects the promise and so fails the build. The composables it renders may keep
    state, see the limits below.
  - It does not render teleported content, such as an open dialog. A prerendered page shows the
    closed state.
- `web/scripts/prerender.mjs`, installed as the `govue-prerender` bin, is the build script. It
  loads the consumer's SSR bundle, built with `vite build --ssr` since plain Node cannot load a
  `.vue` file. It has two modes:
  - `--out <file>` calls the bundle's `render`, injects the markup into the template and writes
    one file. This mode is unchanged.
  - `--out-dir <dir>` reads the template, calls the bundle's `files(template)` and writes every
    `{ path, content }` it returns inside the directory. It knows no language and no
    placeholder, so all language logic stays in TypeScript, compiled into the bundle by Vite,
    and nothing is copied into the script.
- `localizedFiles(routes, { origin, template, render })` builds that list. It renders every
  public route of `localizeRoutes` once per language, one after another, through the site's
  `render(variant)` callback. The callback installs `createGovueI18n({ locale: variant.locale })`
  in `setup`, so the render entry stays generic. Each page gets:
  - `lang` on the `<html>` start tag. The tag is rewritten with `lang` first and every other
    attribute kept. The template must hold exactly one such tag outside comments. A placeholder
    inside the attribute would leak into Vite dev mode.
  - The output of `hreflangLinks` at a required `<!--app-head-->` placeholder: the self
    referential canonical, one alternate per language and `x-default` on the German variant.
  - The markup at `<!--app-html-->`, injected last, so a placeholder or an `<html>` tag inside
    the markup is never taken for the template's own.
- `/de/` is written as `de/index.html`, `/de/impressum` as `de/impressum/index.html`, and
  `sitemapXml` of the same table as `sitemap.xml`. App routes appear in neither. A path is decoded
  text, so `/de/über` is written as `de/über/index.html`.
- `localizedFiles` checks the origin, the route table, the sitemap limits and the template before
  the first render. Both placeholders must appear exactly once, through the same
  `injectIntoTemplate`. A placeholder inside a comment or inside a tag, such as an attribute
  value, is refused too: it would be replaced as text no browser reads, and the page would lose
  its links without an error. Script and style bodies are not tracked. A render that rejects or
  returns no string rejects the whole call.
- No per language title or description, and no `robots.txt`. A site that needs them renders
  them itself.
- `--out-dir` treats a path from the bundle as untrusted. It refuses an absolute path, a drive
  letter, a `..`, `.` or empty segment, a backslash, a NUL byte, a duplicate, a duplicate that
  differs only in case or Unicode normalisation (macOS and Windows file systems would merge them)
  and a path that is also the directory of another path. It also refuses a symbolic link and a
  file in the way anywhere inside the directory, and a directory where a file belongs. All of
  this is checked before the first write, so a bundle that fails a check leaves the directory as
  it was. An IO error halfway, such as a read only subdirectory, can leave part of the files
  written, and the run exits 1. Each path and content is read once from the bundle's list, so a
  getter cannot pass the check with one value and write another. The file itself is opened with
  `O_NOFOLLOW` as a second guard against a link created after the check. A file of an earlier
  run is overwritten, every other file is kept. The template is not changed, so a run can repeat.
- Windows reserved names such as `CON` and alternate data streams such as `page.html:x` are not
  refused. The CLI is tested on macOS and Linux only.
- The exit code is 0 on success and 1 for a usage error and for any failure, in both modes.
- DOM audit of the exported components and composables: no component reads `window`, `document`
  or `navigator` itself. `useTheme` guards `document`, `window` and `matchMedia`, and skips
  storage altogether without a window, because newer Node versions define a global
  `localStorage` that warns on access. The language layer guards `document` and `navigator`
  and does no detection without a window, so the language is the one passed in, else German.
  Reka UI primitives render without a DOM. A test renders the layout components, both switches
  and a select in a Node environment, not jsdom.
- Two limits stay. `useTheme` and `useToasts` keep their state at module level, because one page
  has one theme and one toast host. Rendering several pages with different themes in one
  process shares the theme. A `notify` call during render would leak a toast into the next
  render, and its pending timer would keep the build process alive, so a prerendered page never
  raises a toast. `localizedFiles` renders every page of a site in one process, so a site renders
  all its pages with one theme, and a test renders two languages in one process to show the
  language does not leak. This is documented rather than changed.
- The client mounts with `createApp`, which replaces the prerendered markup, never with
  `createSSRApp`. The markup is rendered with no visitor: system mode and the language passed to
  `setup`. The client then reads the stored choice and the browser language. With `createSSRApp`
  Vue would hydrate instead of replace, and a visitor with a stored dark mode or a German
  browser gets mismatches, which in production Vue does not patch for attributes: a wrong
  `aria-checked` would stay on screen. `createApp` gives no warning in those cases, and a test
  mounts over the markup with a stored choice to keep it that way. On a language page the client
  passes `locale: localeFromPath(location.pathname)` to `createGovueI18n`, and a test
  mounts over the German and the English page with a browser of the other language. The
  prerendered markup is for the first paint and for crawlers, and the client takes over from it.
- The script ships as plain JavaScript, `govue-prerender`, because Node does not strip
  TypeScript types under `node_modules`. It holds its own small copy of `injectIntoTemplate`,
  and a test runs the same cases through both copies. The package requires Node 24 or later,
  the lowest the repository's `@tsconfig/node24` targets. Only Node 26 is exercised.
- With `--out` equal to `--template`, the marker is consumed, so a second run fails with no
  placeholder. Run it after a fresh `vite build`.
- What this does not solve: data that changes per request, personalised content, client side
  routing, page discovery beyond the route table, incremental rebuilds and hydration beyond what
  a normal client mount does once the static HTML has loaded. A site that needs those is better
  served by a real meta-framework.

### Favicons are a build requirement

- Every site needs a favicon, and a site that forgets one ships the browser's default icon
  without anyone noticing. So the absence is an error, not a missing nicety.
- The Go package `favicon` fixes three file names in one directory per site, `web/public`: an
  SVG, a 32 pixel PNG fallback and a 180 pixel Apple touch icon. `favicon.Load` fails at startup
  naming the file and the path it expected. A web manifest stays optional and is not generated.
- The links are written by `themeboot.Inject`, through `themeboot.WithFavicon`, in the same step
  as the boot script. A second injection point would have been a second place to forget. The
  links hold no script, so they need no nonce.
- The PNG files are generated from the one source SVG by the `favicon:generate` task target with
  ImageMagick, pinned in `mise.toml`, and committed. Output depends on the tool version, so the
  pin matters. A site brings its own artwork. A site without a brand mark gets a placeholder SVG,
  a monogram on its accent colour.
- `favicon.CheckPages` is the shared check, called from a site's own tests on its prerendered
  pages. It reads the links back from the HTML instead of trusting the shell, so it also catches
  a prerender that dropped them. It scans the head only, with a small tokenizer that skips
  comments and script content like a browser, because a regular expression over the whole page
  passes on a link inside a comment or a string. It also checks content, so a file that is
  merely non-empty, such as a `robots.txt` typed as an SVG, fails.
- `Inject` stays total and returns a page without a head unchanged, as it did before. `Shell`
  with `WithFavicon` panics at construction for such a page and for one that already carries
  icon markup, the same startup failure as the empty nonce of the theme boot script. The
  check on the built pages remains the guarantee.
- Not covered here: `og:image` (govue#69) and the Vue components, which write no head markup.

### App association files

- A native app only opens a domain's links, and only shares its credentials, when the domain
  says so. iOS reads `/.well-known/apple-app-site-association`, Android reads
  `/.well-known/assetlinks.json`. The package `appassoc` serves both from one typed `Config`.
- The name is `appassoc`, not `wellknown`. The package serves these two files and nothing else.
  A name that says `wellknown` invites `security.txt` and `change-password` into it, and each of
  those has its own rules.
- What a product sets:
  - Apple: per app the team id, the bundle id and the path patterns it opens. Excluded patterns
    come first in the file, because the first match decides. A `WebCredentials` list names the
    apps that share credentials.
  - Android: per app the package name, the SHA-256 fingerprints of the signing certificates and
    the relations. The default is `handle_all_urls`. `get_login_creds` is explicit.
- The documents are built with `encoding/json` from typed structs at construction, never by
  joining strings. `Config` is validated in the same step and every problem is returned at once:
  a ten character uppercase team id, a bundle id, an Android package name with at least two
  segments, a fingerprint of 32 hex bytes with colons, a path from a safe character set, and
  caps on the number of apps, paths and fingerprints and on the size of the document. A
  fingerprint is normalised to uppercase. The files carry public identifiers only. They never
  hold a secret, a token, an account name or any value from a request.
- A file with no entries is not mounted and answers like any unknown path. It is never served as
  an empty document, which would tell a phone that the domain trusts no app. `New` refuses a
  `Config` that configures neither file.
- Both platforms refuse a redirect, so the paths are exact patterns. `Handle` registers
  `GET <path>`, which also matches HEAD, and `<path>` without a method. The second one matters
  behind the catch-all `"/"`. Without it a POST matches the catch-all, which answers 404, and
  ServeMux's own 405 never happens. The handler answers 405 with `Allow: GET, HEAD` for it.
  A path with a trailing slash matches no pattern, so it goes to the catch-all and ServeMux
  never redirects it. What the catch-all answers is the application's choice: in `example/` a
  bad `Authorization` header gets 401 from the route chain there. A path with a double slash is
  cleaned by ServeMux with a 307 before any handler runs, like every other path, and no platform
  fetches such a path.
- The response is `application/json`, `Cache-Control: public, max-age=3600`, `nosniff`, no
  cookie and no redirect. The handlers read neither a cookie nor an `Authorization` header.
- The origin `max-age` is not the lever for a correction. Apple's CDN fetches the file within
  about 24 hours and devices check about weekly. Android 15 and later re-verify in the
  background, and a change takes up to seven days to reach every device. A wrong path list or
  certificate therefore stays live for days whatever the header says. The hour only spares the
  origin a poll.
- A passkey only product sets `APPLE_APP_ID` and `APPLE_WEBCREDENTIALS=true` in the example
  with no path. The file then holds `webcredentials` and no `applinks`. In `Config` that is an
  empty `AppLinks` with a `WebCredentials` list.
- `example/` mounts the files only when `APPLE_APP_ID` or `ANDROID_PACKAGE` is set, and outside
  the route chain. The rate limiter would count a platform fetcher that shares a few addresses.
  The session middleware answers a malformed `Authorization` header with 401. CORS and CSRF
  protect nothing on a public GET. The outer chain still runs, so the access log and the
  security headers apply. A test sends the files through the whole example chain with a session
  cookie, a bad bearer token and a foreign origin, and pins that the catch-all never answers,
  nothing redirects and no cookie is set.
- Passkeys in a native app need the `webcredentials` entry here and the app origin in the relying
  party's allowed origins. The first exists now. The second comes with the passkey step of
  #104, where the origin format is decided.
- Sources checked against Apple and Google documentation:
  - The Apple shape, `applinks.details[].appIDs` with `components` entries (`/` and `exclude`)
    and `webcredentials.apps`, is in
    <https://developer.apple.com/tutorials/data/documentation/xcode/supporting-associated-domains.json>.
    `components` needs iOS 13 or later, the first matching entry wins, and the file is served
    over HTTPS without a redirect and without a file extension. The wildcard rules (`*`, `?`,
    `?*`) are in
    <https://developer.apple.com/tutorials/data/documentation/bundleresources/applinks/details-swift.dictionary/components-swift.dictionary.json>.
    The older `paths` format and `apps: []` are not written.
  - The Android relations `handle_all_urls` and `get_login_creds`, and the statement that
    Digital Asset Links is required for passkeys, are in
    <https://developer.android.com/identity/credential-manager/prerequisites>. The file must
    answer 200 with `application/json`, and a 301 or 302 fails verification:
    <https://developer.android.com/training/app-links/configure-assetlinks>. The uppercase colon
    separated fingerprint example is on that page too. The re-verification on Android 15 and
    later and the seven day delay are in
    <https://developer.android.com/training/app-links/verify-android-applinks>. Package name
    rules are in <https://developer.android.com/build/configure-app-module>.
- Not verified from a primary source:
  - Apple's 128 KB size limit. Only an archived 2016 page and third party guides name it. The
    64 KiB cap is this package's own.
  - The 155 character bundle id cap and the ten uppercase alphanumeric team id. Both are local
    checks.
  - How Apple's CDN treats `Cache-Control`. Apple publishes nothing on it.
  - Behaviour on a real device. No iOS or Android device was used.

### Analytics

- The package `analytics` counts page views on the server and reports them to Umami. No script
  runs in the browser, no cookie is set and nothing is read from the visitor's device. It is the
  generalised form of the `internal/analytics` that ostendorf.tech runs. Umami is the one
  statistics tool shared across the products, so the code lives here once.
- Counting is off unless `UMAMI_URL` and `UMAMI_WEBSITE_ID` are both set. Exactly one of them is a
  startup error, because a deployment that meant to count and typed half the pair would otherwise
  run for months without statistics and say nothing. `UMAMI_TIMEOUT` (1s to 30s, default 5s) and
  `UMAMI_QUEUE_SIZE` (1 to 10000, default 256) are optional. `LoadConfig(getenv)` reads them with
  `envconf` and returns every problem at once. No message repeats the URL, because an operator
  may have put a secret into its path.
- The Umami URL is `https`. Plain `http` is accepted for `localhost`, a loopback address, a single
  label name such as `umami`, and names ending in `.svc` or `.svc.cluster.local`, which only a
  cluster resolves. Anything else over plain HTTP would carry visitor addresses in clear text.
  Credentials, a query and a fragment are refused. The website id is the lowercase UUID shape.
- The wiring is `analytics.New(cfg, Options{...})` and `client.Middleware`. `Middleware` has the
  shape of every other middleware here. It sits directly around the mux, innermost in the outer
  chain, because it reads `r.Pattern` like `metrics` and `AccessLog` do. A product with several
  sites in one process builds one client per site.
- A view is counted when all of these hold:
  - The method is GET. HEAD is not counted, a crawler or a link checker sends it.
  - The response is 200 with a `text/html` body. A 404, a 5xx, a redirect, a 304, JSON, an asset
    and an empty 200 are not pages. A handler that sets no content type gets the type net/http
    would sniff. A panicking handler counts nothing. A 304 is never counted: it carries no body,
    and a revalidating visitor is therefore missing from the numbers. That undercount is
    accepted over counting an asset.
  - The request has no `DNT: 1` and no `Sec-GPC: 1`. Either one means the visitor is not counted
    at all. `DNT: 0` and a missing header count.
  - The request is no prefetch (`Sec-Purpose`, `Purpose`, `X-Purpose`, `X-Moz`) and, if the
    browser sends `Sec-Fetch-Dest`, it says `document`. A `fetch` call or an iframe is no page
    view.
  - The user agent is present and names no bot, preview fetcher or script. The marker list is
    best effort and Umami filters bots again.
  - The client address, from `clientip`, is a public address. A private, loopback, link local
    or shared (CGNAT) address is a probe, the cluster or a VPN. `AllowPrivateClients` turns that
    off for a development run. Behind a proxy a product must pass a `clientip.Resolver` that
    trusts it, or every view comes from the proxy and none is counted.
  - The path is not `/healthz`, `/readyz`, `/livez` or `/metrics`, and `Options.Skip` does not
    exclude it. Skip only adds to the built in list.
- The URL sent is the route pattern, never the path. `GET /notes/{id}` is sent as `/notes/{id}`:
  the method and a host are cut off, `{$}` is removed and the wildcard names stay. An id, a
  token or a slug therefore never leaves the process, and the statistics group by page instead
  of by record. A request with no pattern, because the middleware sits outside something that
  replaced the request, is sent as the cleaned path without query. That path may hold an id.
  A product serves its pages through a `ServeMux` with one pattern per page, as `example/` does.
  A catch-all `/` pattern reports every page it answers as `/`.
- The payload is the fields below and nothing else. Each one is in it for a reason Umami needs:
  - `website` and `hostname`: the website id and the product's own public host, from `Options`,
    never from the `Host` header, which a request controls.
  - `url`: the route pattern, as above.
  - `title`: only when `Options.Title` is set. A title can hold user content, so the default is
    none.
  - `referrer`: the origin of another site, scheme and host and no path. A path on another site
    can hold a token. The product's own host, with or without `www`, is no referrer.
    ostendorf.tech sends the path without the query. This package sends less.
  - `language`: the first language tag of `Accept-Language`, if it is a well formed tag.
  - `ip` and `userAgent`: the client address and the user agent, which Umami reads the country,
    the browser and the system from, and hashes itself. They go to the operator's own Umami and
    are never stored here, never logged and never sent anywhere else. The request to Umami also
    carries the user agent as its own header, as ostendorf.tech does. The shape of the
    payload and that Umami prefers `ip` and `userAgent` of the payload over the request's own
    were carried over from ostendorf.tech and not checked against the Umami documentation again.
  - Not sent: the query string, any cookie, an `Authorization` header, `X-Forwarded-For`, the
    session, an account or a request id.
- The send is off the request path. A view goes into a channel of `UMAMI_QUEUE_SIZE` that two
  worker goroutines read. A full queue drops the view. Nothing in the handler waits for Umami,
  so a slow or dead Umami costs the visitor nothing. One post is bounded by `UMAMI_TIMEOUT`. The
  default HTTP client has no cookie jar and refuses redirects, so a visitor address never follows
  a redirect to another host. A custom client must honour the request context.
- A dropped view is counted in `Stats().Dropped` and reported through `Options.OnDrop`. The
  package does not import `metrics`, which would pull Prometheus into a product that only wants
  counting. `metrics.NewCounter` returns the function to pass, as `example/` does with
  `analytics_views_dropped_total`.
- A failure is logged at warn level, once per class (`timeout`, `transport`, `status_4xx`,
  `status_5xx`, `queue_full`, `shutdown`) and then at most once a minute with the count since the
  last line. The line holds the class, the count and the Umami host. It never holds the URL, the
  path, a query, a visitor address or a user agent. The error of a post is reduced to its class
  before it is stored, because `net/http` repeats the whole URL in its errors.
- `Close(ctx)` stops accepting views and posts what is queued until `ctx` ends. After that the
  posts in flight are cancelled, the rest are counted as dropped and `Close` returns the context
  error. A product calls it after the server stopped, with a deadline of a few seconds.
- What a product must say in its privacy text, as a checklist and not as legal text or legal
  advice. The product's own counsel decides what the law asks of it:
  - Page views are counted on the server, with no script, no cookie and nothing stored on the
    visitor's device.
  - What is sent to Umami: the page (the route, not the address with its query), the referring
    site, the browser language, the user agent and the visitor's IP address, which Umami turns
    into a hash and a country without storing the address.
  - Who runs Umami and where, and that nothing goes to a third party. This depends on the
    product's own Umami and is not a property of this package.
  - A visitor who sends Do Not Track or Global Privacy Control is not counted.
  - How long Umami keeps the data and the legal basis the product relies on. Both are the
    product's statements. This package decides neither.
- Not verified: Umami's own handling of `ip` and `userAgent`, the hashing and the retention
  inside Umami, and a post against a real Umami. Tests use a fake transport and no network.
- Stays in the product: which routes count, the privacy text, the Umami instance and website id,
  and the retention statement. Out of scope are a client side script, events beyond page views,
  a dashboard and a consent banner.

### Component behaviour

- Where behaviour is genuinely hard, a modal's focus trap, a dropdown, a combobox, adopting a
  maintained headless primitives library beats writing a worse one. Reka UI, the Vue port of
  Radix, was evaluated and is adopted: `GovueDialog` and `GovueSelect` import `reka-ui`, styled
  with these tokens. A checkbox and a switch stay hand rolled on native elements.

## Alternatives considered

- Two repositories, one per ecosystem: each half releases on its own cadence, and a Go consumer
  never sees frontend churn. Dropped because a change spanning both halves, a token rename or
  the error shape, would need two pull requests and two versions to land consistently.
- A framework with one constructor that wires a server: an application would call one function.
  Dropped because it hides the parts applications actually differ in, and a chain the application
  assembles itself stays readable.
- Everything under `pkg/`: matches the common Go house layout. Dropped because the segment
  carries no information in a repository that is only public packages.
- Tokens as plain CSS custom properties, framework neutral: usable from server-rendered
  templates and plain HTML too. Dropped for now because the target is Vue plus Tailwind v4, and
  a second consumption path would be maintained for a consumer that does not exist. The files
  stay parseable, so generating a neutral build later is possible.
- Tokens authored in TypeScript with CSS generated from them: one source of truth. Dropped
  because it adds a generator and generated files to review, and nothing needs the tokens as
  data.
- Shipping the account tables and migrations with `account`: no adapter to write. Dropped
  because the existing consumers' schemas already differ, and owning the tables would dictate a
  shape to every consumer and force a data migration on each.
- One wide account store with every repository a user table usually has: one interface for
  every use. Dropped because an interface that offers more than the logic needs turns the
  boundary into a convention the next consumer does not know about.
- Publishing the Vue package to npm: the obvious distribution path. Dropped for now because no
  registry is set up for this organisation, and a git dependency pinned to a tag needs no publish
  step and no token.

## Open questions

- A shared session store. The in-memory store is correct for one replica only, and the `session`
  issue settled that nothing else ships here for now: `session.Store` is four methods an
  application implements against whatever it already runs. The same holds for
  `session.DeviceStore`, which an app mode product writes. Which backend would ship here, if any
  ever does, stays open until a second consumer exists and the two agree on one. The owner
  decides then. Next step: the first application that needs more than one replica writes its
  own and reports what the interface made awkward.
- Whether a device token may list and revoke only its own device. Today it can list every device
  of the account and revoke any of them. The owner decides.
- Whether `AppCORS` sends `Access-Control-Expose-Headers`, for `Retry-After` and the request id,
  and `Access-Control-Allow-Private-Network`. Neither is sent. The owner decides when a product
  needs one.
- What a Capacitor webview sends: the exact `Origin`, `Sec-Fetch-Site`, and whether the native
  HTTP plugin bypasses CORS. A product checks its own build and reports, see "App origins".
- Whether the app origin belongs in the passkey relying party's allowed origins, and whether
  passkeys work inside a Capacitor webview. Decided in the passkey work of #104, with its
  security review.
- Whether `api`'s error shape is the right contract for consumers that already have one of their
  own. Next step: revisit when a second consumer appears. The owner decides.
- Whether the TOTP confirm handler may start a session without a switch to turn that off. Next
  step: the owner decides when the first product that wants a login after confirmation reports
  back.
- The passkey user handle: the opaque account id or a random handle per account. Next step:
  decide in the passkey work with its security review.
- What the first version supports for configuration beyond the environment. Nothing reads the
  environment by itself today, and whether that stays absolute is open. The owner decides.

## Related ADRs

- [0001 Device token sessions for native apps](../adr/0001-device-token-sessions.md), accepted:
  the bearer token a native app signs in with, and the CSRF boundary it draws. "Enabling app
  mode" lists the steps that apply it. Two sentences of the record are out of date. The handlers
  live in `session/devicehttp` and not in `session`, and "Token handlers" says so. The account
  package is `account` and not `auth`, and it ends sessions through `ServiceConfig.EndSessions`,
  not `EndAllSessions`.
- The other decisions above are recorded here on purpose. One gets its own ADR when it turns out
  to be contested and expensive to reverse, not before.
