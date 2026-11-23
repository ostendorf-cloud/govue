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

- The Go side is the standard library, with four exceptions. `prometheus/client_golang` is
  confined to the `metrics` package, because the exposition format is not worth reimplementing.
  `golang.org/x/crypto` is confined to the `account` package, for Argon2id, which the standard
  library lacks. Writing a memory hard hash by hand is the worse risk. `go-webauthn/webauthn`
  is imported by the `passkey` package only, because a WebAuthn ceremony is not worth writing
  by hand either, see "Passkeys". `jackc/pgx/v5` is confined to the `database` package,
  because the standard library has no PostgreSQL driver, see "Database".
- A package stays usable alone: taking `clientip` must not pull in `session`.
- Handlers return an error and one `api` helper writes the single error shape, so no handler
  writes error JSON and no cause reaches a client.
- A refused configuration value is never quoted in an error, and neither is a library error
  that quotes it, such as the `*url.Error` of `url.Parse` or the `*net.OpError` of a listen.
  Error text reaches CI and startup logs, and a value pasted into the wrong variable can be a
  secret. The error names the variable or the field, the position in a list and the expected
  form. An accepted value may be quoted in a conflict, a duplicate or an overlap, and a value
  from a closed set may be quoted when the error lists the set. The path of a secret file is
  never shown: `envconf.Secret(key)` reads the path from the value of key itself, and
  `SecretOrFile` from key_FILE, so a secret pasted there in place of a path would reach the
  log. A read error names the variable and the kind, a missing file, a refused read, a file
  that is not readable or an empty one, and keeps `fs.ErrNotExist` and `fs.ErrPermission`
  matchable. `SITE_DIR` in the example follows the same rule. The configured OIDC issuer is
  not quoted either, although it is accepted: it can hold a credential, and its errors are
  logged on every failed login. A failed request to the provider names its kind, without the
  URL, the host name or the names of a certificate.
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
- An origin that does not parse as a bare `scheme://host`, carrying a path, a query, a fragment,
  userinfo, or the literal `null` a sandboxed context sends, is refused at construction rather
  than silently never matching: a config entry that could never fire is a mistake worth catching
  at startup.
- `CORSConfig.ExposeHeaders` lists response headers a page of an allowed origin may read, such as
  `Retry-After`, which a browser hides from a cross origin page unless it is exposed. The default
  is empty and sends nothing. The real response of an allowed origin carries
  `Access-Control-Expose-Headers` with the names in canonical form, deduplicated, in the given
  order. A refused origin and a preflight answer get none. An empty name, a name that is not an
  HTTP token and `*` are refused at construction with a fixed error text that never carries the
  value. `*` is not accepted because the browser ignores it on a credentialed request.
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
- `DefaultPolicy` sets `worker-src 'self'`, because the proof of work solver of
  `GovueContactForm` runs in a module worker loaded from a script file of the page's own origin.
  A missing `worker-src` falls back to `script-src` in CSP 3, but to `child-src` and then
  `default-src 'none'` in CSP 2, so the directive is explicit and does not depend on which level
  a browser follows. It allows no `blob:` or `data:` worker and carries no nonce. Every
  `DefaultSecurity` response carries it, so do the pages of `frontend`, which clones the policy.
  A site that builds its own policy with `NewPolicy` and runs the contact form adds the
  directive itself.
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

#### Known devices and the per login ceiling

- A per login ceiling, all attempts at one login from any client, bounds a distributed guessing
  attack on the second factor. It also lets that attacker hold it dry, so the owner is refused
  with correct credentials. Raising it only raises the number of addresses needed. Each consumer
  wired such a ceiling itself, in a middleware that reads the login from the body.
- `HandlerConfig.Ceiling` moves it into the handlers: `func(r, login) error`, called by signup,
  confirmation and login after the body is read. `HandlerConfig.KnownDevices` is optional and
  off by default. It needs `Ceiling` and lets a request from a known device skip it. With
  neither set nothing changes: no cookie is read or set. The strict per client limits stay in
  the consumer's middleware.
- The cookie is signed, not stored. It holds `v1`, a 128 bit `crypto/rand` device id, an expiry
  and an HMAC-SHA-256. The HMAC covers the id, the expiry, the account id and the account's
  password hash, and the last two are not in the cookie. The handler looks the account up by the
  login of the body, with `FindByLogin`, only when the request carries the cookie, and checks the
  signature against that record. A cookie of another account, a changed byte, an expired one, a
  disabled account and an unknown login are all unknown, and the ceiling applies.
- No store method was added. The password hash is the account side secret that is already
  there: a password change invalidates every cookie of the account, and so does the rehash after
  a login with outdated parameters, which sets a fresh cookie in the same answer. The cost: no
  revocation of one device and no `Revoke(accountID)` without changing the password. Rotating
  the key revokes everything. A server side list or an epoch needs a store method and is left
  to a consumer that needs it.
- The cookie is `__Host-govue_device` (`govue_device` with `AllowPlainHTTP`), HttpOnly, Secure,
  SameSite=Lax, `Path=/`, no `Domain`, default lifetime 90 days from the last successful sign
  in, at most 400 days. SameSite=Lax blocks cross-site requests, not cross-origin ones. A
  frontend on a sibling origin of the same site, listed in `TrustedOrigins`, sends the host-only
  cookie when its requests include credentials. A frontend on another site does not send it, and
  gets no skip.
- Logout sends a clearing cookie. A person who signs out is unknown on the next login, which is
  the conservative choice. One browser holds one cookie, so a second account on it replaces the
  first one's.
- Owner decision: after a successful password change the response sets a fresh cookie bound to
  the new hash. The device that changed the password stays known, and every other copy stays
  invalid, since the HMAC covers the hash. When the sessions could not be ended, the response
  clears the cookie instead, the conservative choice for a change whose scope is unclear.
- The threat it leaves: a stolen cookie skips the ceiling of that one account, nothing else.
  The password, the TOTP code and the client limits still hold. The HMAC key is a secret the
  consumer supplies, 32 bytes at least, the same on every replica.
- Text for a privacy page: after a successful sign in a cookie `__Host-govue_device` is stored
  on the device. It holds a random device number, an expiry and a signature, and no personal
  data. It lets the device sign in while the account is under guessing attack, is HttpOnly and
  sent to this site only, lasts 90 days from the last sign in, and is deleted on sign out and
  replaced on a password change. It is not used for tracking. A consumer adds the cookie to its
  privacy text in the same change that turns the feature on.

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
- `AppCORS` sends `Access-Control-Expose-Headers` only when `AppCORSConfig.ExposeHeaders` names a
  header, so by default a page of an app origin cannot read `Retry-After`. No
  `Access-Control-Allow-Private-Network`, so Chrome can fail the request on a private network.
  That one is undecided, see "App origins".
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
  one type would only add an import. `AGENTS.md` keeps its list of packages.
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
  Every error names the refused entry by its position and the rule, never by its value, see
  "Small, independent, standard library". A duplicate names the origin.
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
  `Vary: Origin` and no `Access-Control-` header, and is never echoed. `NewAppCORS` sends no
  `Expose-Headers`, so a page reads the CORS safelisted response headers only.
  `CORSConfig.ExposeHeaders` belongs to the web policy and does not reach an app origin. An app
  that needs `Retry-After` of a 429 or the request id builds the handler with
  `NewAppCORSWithConfig(origins, web, AppCORSConfig{ExposeHeaders: ...})`. The names follow the
  rules of `CORSConfig.ExposeHeaders`: canonical form, deduplicated, in the given order, and an
  empty name, a name that is not an HTTP token and `*` are refused at construction with a fixed
  error text that never carries the value. The real response of a listed app origin carries
  `Access-Control-Expose-Headers`. A preflight and an origin that is not listed get none, and
  `Vary` is unchanged. The default is empty and sends nothing.
  `Access-Control-Allow-Private-Network` is not sent: a preflight with
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
  of the protector and to `NewAppCORSWithConfig`, and to nothing else. `ALLOWED_ORIGINS` stays
  the web list. `newApp` passes it to `RejectOverlap` as the trusted origins, because the example
  feeds the same list to `TrustOrigin`, and `NewAppCORSWithConfig` checks it against the web CORS
  policy. The example exposes `Retry-After` through `AppCORSConfig.ExposeHeaders`, so a page of an
  app origin reads the wait of a 429.
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

#### Passkeys in the web app

- `GovuePasskeyButton` and the `usePasskey` composable run the WebAuthn ceremony against the
  passkey routes under `/account/passkeys`, the contract of the server half. The button takes
  the `client` of `createClient` like the forms, `mode` (`login` or `enrol`), an optional `name`
  for the new passkey and an optional `path` prefix. It emits `success` with the login answer or
  the new passkey.
- There is no npm WebAuthn dependency. The browser side uses the native
  `PublicKeyCredential.parseCreationOptionsFromJSON`, `parseRequestOptionsFromJSON` and
  `credential.toJSON()`, and the binary values travel as base64url as that serialisation defines
  them.
- Support is detected, never assumed. `isPasskeySupported()` is true when both parse helpers and
  `navigator.credentials` exist. The button renders nothing until it is mounted and nothing at
  all where the check fails, so a prerendered page and the first client render agree. A product
  that wants a fallback uses `usePasskey().supported`. `enrol` and `login` reject with
  `unsupported` without a request.
- A dismissed prompt (`NotAllowedError` or `AbortError`) is the quiet `cancelled` status. The
  call resolves with `undefined`, the button emits `cancelled` and shows a short muted note in
  its live region, and no alert. Every other failure is a `PasskeyError` with a `code` of the
  contract (`reauthentication_required`, `invalid_passkey`, `invalid_credentials`,
  `last_factor`, `not_found`), `unsupported`, or one of the client's own: `unauthenticated`,
  `invalid_argument`, `permission_denied`, `rate_limited`, `unavailable`, `network`,
  `already_registered`, `busy`, `bearer_unsupported`, `failed`. The error has a fixed English
  message per code and no cause, so no challenge, credential or server text ends up in it. The
  button shows the text of its code from both catalogues under `govue.passkey`.
- A browser failure that is not a dismissal is `failed`, with `reason` set to the name of the
  browser error from a fixed set (`SecurityError` for a wrong RP id or domain,
  `NotSupportedError`, `ConstraintError`, `InvalidStateError`, and a few more) or `other`. It is
  never the browser's message, so an operator can tell a misconfigured RP id from a broken answer
  without a secret in the error. The visitor sees the generic text. `InvalidStateError` is
  `already_registered` on enrolment, where it means an excluded authenticator, and `failed` on a
  login. The CSRF middleware answers 403 as plain text, which the client turns into the code
  `internal`. `usePasskey` reads the status and maps a 403 to `permission_denied`, which asks
  for a reload. The account forms do not look at the status and show their generic text.
- The failure text is an alert, as in the account forms. The busy text and the note of a
  dismissed prompt are in a polite live region, and the busy text is announced once: the
  button's own loading text is emptied. A button that turns disabled drops the focus, which is
  the pattern of `GovueButton` in the forms too. The passkey button gives the focus back when
  the ceremony is over and nothing else has it.
- One ceremony runs at a time per page, because the challenge cookie is one per browser and a
  new begin replaces it. The lock is module level, so it holds across every `usePasskey` instance
  and every button on the page. A second `enrol` of the same instance while its own runs gets the
  same promise. Any other call while a ceremony runs rejects with `busy`, and the button is
  disabled while it waits.
- A conditional login (`login({ conditional: true })`, for the autofill UI) leaves the status
  alone. It yields to a modal ceremony in both directions. Started while one runs, it resolves
  with `undefined`, as a cancel, and sends no request. A modal `login()` or `enrol()` that starts
  while it waits aborts it, its begin request included, and it resolves with `undefined` too. The
  product restarts it when it wants it again. A conditional login that has already returned from
  the browser and waits for its finish answer is not stopped by a modal ceremony that starts
  then: the abort reaches the browser step and the begin request, and the finish request has no
  signal. That window is short. The server takes a challenge and clears the cookie at the first
  finish and a new begin replaces the cookie, so one of the two finishes can fail. A failing
  modal ceremony rejects with `invalid_passkey` (enrolment) or `invalid_credentials` (login). A
  failing conditional login resolves with `undefined`, because its signal is aborted by then, and
  one that succeeds resolves with the login. The product restarts it when it wants it again.
- A conditional begin made at page load expires with the challenge lifetime of the server, 300
  seconds by default. A person who picks a passkey from the autofill UI later gets
  `invalid_credentials` from the finish. The product restarts the conditional login, for
  example on that error or on a timer.
- The `signal` option aborts the begin request and the browser step, and not the finish request,
  which runs to its answer once the browser step has returned.
- A client with a device token cannot run a ceremony. The challenge cookie of a begin answer
  never comes back, because that client sends `credentials: 'omit'`, and a passkey login starts a
  cookie session through `StartSession`, not a device token. `usePasskey` recognises such a
  client (`isBearerClient`: only the bearer client has `signIn` and `signOut`) and rejects both
  ceremonies with `bearer_unsupported` without a request. The button does not render for it.
  This is the same host only cookie limit as below: a native app with a device token signs in
  through `signIn`, not through a passkey.
- The login routes set `skipUnauthenticatedHook`, since a 401 there is the answer to the
  ceremony. Enrolment does not, so a lost session still reaches `onUnauthenticated`. A person
  whose login is older than the server's window gets `reauthentication_required` and logs in
  again, then retries. The composable also lists and removes passkeys (`list`, `remove(id)`).
  There is no list component: a product renders the list the way its page needs.
- The challenge cookie of the begin answer is `SameSite=Strict` and host only, and the client
  sends it with the credentials mode of the client. The ceremony therefore works for the web app
  on the host of the API. It does not work from a cross origin page, and a native app webview
  on its own origin is such a page. Not verified: WebAuthn inside a Capacitor webview was not
  tested, and a native app is expected to need its own flow.

### Passkeys

- The `passkey` package adds WebAuthn passkeys next to the password and TOTP login, never
  instead of it. A person registers a passkey while logged in and then logs in with it. It is
  the one importer of `github.com/go-webauthn/webauthn` v0.18.2, so an application without
  passkeys does not pull the library in. No exported name of `passkey` carries a type of the
  library: credentials and challenges travel as opaque bytes, so a library upgrade does not
  change the public API.
- The library brings its own dependencies into every build that imports `passkey`: `passkey`
  itself, `passkey/passkeytest` and `example/`. Among them are a CBOR codec, msgpack, a JWT parser,
  `go-tpm` for the TPM attestation format with `golang.org/x/sys/unix` through its transport, and
  `golang.org/x/crypto/ocsp` through the revocation checks. `passkey` asks for attestation `none`
  and never uses the TPM or the metadata service, but the library's `protocol` package imports
  them, so they are compiled in. `passkey` also pulls `golang.org/x/crypto/argon2` through its
  `account` import. A package that does not import `passkey` gets none of the library's modules.
- `passkey` imports `account` for `account.Identity` only. `StartSession` has the signature of
  `account.HandlerConfig.StartSession`, so an application passes the same function to both and a
  passkey login starts the session a password and TOTP login starts. `account` does not import
  `passkey`, and neither imports `session`.
- A passkey login requires user verification, a PIN or a biometric on the authenticator, so it
  proves possession and knowledge or inherence in one step and counts as both factors.

#### Setup

- `passkey.Config` takes the relying party id `RPID`, a display name and the exact `Origins`. All
  three are required, and none is derived from a request's `Host` header, which the client sets.
- Startup refuses an empty RP id, one that is not a lower case host name of at least two labels
  (`localhost` excepted), an IP address, an empty origin list, an origin that is not https
  (http on `localhost` excepted), and an origin whose host is neither the RP id nor below it.
- An origin is accepted only in the form a browser serialises it: a lower case scheme, a lower
  case host of DNS labels without a wildcard, and an optional port of 1 to 65535 without leading
  zeros that is not the scheme's default. A path, a query, user info or an empty port after a
  colon is refused. `passkey` refuses rather than normalises. The library's own comparison folds
  the case of the scheme and the host and normalises the port, so a sloppy entry might still
  match, but the configured list then differs from what browsers send, and that list is what a
  reviewer reads and what the RP id rule is checked against.
- Use the application's own host as the RP id, `app.example.com`, not a marketing parent such as
  `example.com` that the application does not control. Any host below the RP id can then ask for
  the same passkeys.
- The RP id cannot change later. Every passkey is bound to it, and a change loses all of them.
- The public suffix list is not checked. `co.uk` passes startup and fails in the browser.

#### Challenges

- A challenge is single use. The begin handler stores the ceremony state in a `ChallengeStore`
  under 32 random bytes and sends that id in an HttpOnly, `SameSite=Strict` cookie,
  `__Host-govue-passkey`. Without TLS, `AllowPlainHTTP` drops the prefix and the Secure attribute,
  as for the session cookie. The finish handler clears the cookie and takes the challenge, which
  deletes it in the same step, before it checks anything else. A success, every refusal, a missing
  session and a stale login all use it up. A second finish with the same cookie finds nothing.
- Only the method and the origin check run before that. A cross site request is refused without
  reading the cookie, so it cannot burn a person's pending challenge. A test pins it.
- A new begin replaces the cookie, so a browser runs one ceremony at a time. Two overlapping
  ceremonies fail in a fixed way: the finish of the first one carries the second cookie, takes the
  second challenge, fails the signature and gets the single refusal. The cookie is cleared with it,
  so the finish of the second one gets the same refusal, and the person starts again. A test pins
  that order.
- The id travels in a cookie and not in the response body. A script cannot read it, the client
  carries no state between the two calls, and the challenge stays bound to the browser that began
  the login.
- A registration challenge holds the account id and finishes only for the same account. A login
  challenge has no account, and the cookie binds it. Each holds its ceremony, so one never
  finishes the other.
- A challenge lives five minutes by default, 30 seconds to ten minutes by configuration.
- `MemoryChallengeStore` is the default for one process, bounded at 10000 entries. When full it
  drops expired entries, then the oldest live one. A flood can then push out a real challenge and
  the person starts again. Refusing new entries instead would let the same flood stop every login
  for a whole challenge lifetime. Several replicas without sticky sessions need a shared store
  whose `Take` is atomic.

#### The store

- `passkey.Store` has five methods: `AddCredential`, `Credentials`, `FindByUserHandle`,
  `UpdateCredential` and `DeleteCredential`. It has no provider identity, no connection, no tenant
  and no list of accounts, for the reason `account.Store` gives: a passkey login reaches its
  account through the user handle and nothing else.
- An account has exactly one user handle. `AddCredential` refuses a credential whose handle
  differs from the account's stored one, in the same step as the insert, and the registration
  finish checks it as well. Two first registrations from two browsers each bring a new handle.
  Exactly one succeeds, the other gets the one refusal and begins again with the stored handle.
  Without that, an adapter that keeps the handle in an account column would never find the second
  passkey, while a delete still counted it as live and could remove the working one. The
  conformance suite checks the guard and racing first registrations.
- A handle also belongs to one account: `AddCredential` refuses a handle another account holds.
  Handlers never send one, since every handle is 32 random bytes made on the server, but the
  rule makes `FindByUserHandle` answer one account by contract, not by chance.
- The first registration of an account has no credential row to lock. `SELECT ... FOR UPDATE`
  over that empty set locks nothing at READ COMMITTED, and both racing inserts go through. A SQL
  adapter locks the account row, runs the check and the insert SERIALIZABLE, or keeps the handle
  in an account column set with a conditional `UPDATE`.
- An account without credentials has no handle. After its last passkey is deleted, the next
  registration brings a new random handle, and it is accepted. An adapter with a handle column
  writes it only while the account has no credential, or clears it when the last credential is
  deleted. A column written once and never cleared would refuse every later registration. The
  suite and a ceremony test cover registering again after the last delete.
- The user handle is 32 random bytes per account, made with the first passkey and copied onto
  every further one. It is not the account id. The authenticator stores it and a synced passkey
  copies it to every device of the person, so an id built from an email address or a sequence
  would leak it. A consumer id may also exceed the 64 bytes WebAuthn allows.
- `UpdateCredential` is a compare and set on the sign count the login read. Of two logins racing
  with one counter value, one wins.
- `DeleteCredential` with `keepOne` deletes only while the account holds another passkey, decided
  in the same step. A SQL adapter locks the account's credential rows for it, since a `DELETE`
  with an `EXISTS` alone lets two concurrent deletes each see the other row.
- `passkeytest.Run` is the conformance suite and `passkeytest.MemoryStore` the in memory fake.
  The suite checks the unique credential id, the exact handle match, a disabled account, the
  compare and set, the account scope of a write and the racing deletes.

#### Handlers

- Registration begin and finish, login begin and finish, list and delete, JSON like the account
  handlers. Each checks the method, then the origin with `net/http.CrossOriginProtection`, then a
  strict body, for the reason the account handlers give: a passkey login has no session to bind a
  CSRF token to, and a forgotten middleware must not leave it open.
- Registration, at begin and at finish, and delete need a session whose login is at most
  `ReauthWindow` old, ten minutes by default, through `CurrentAccount`, which returns the
  identity and the time of the login. A stolen, long lived session cannot add its own passkey.
  Listing needs a session only.
- Registration asks for a discoverable credential, user verification and attestation `none`, and
  lists the account's passkeys in `excludeCredentials`. Login is discoverable: no login name, an
  empty `allowCredentials` and user verification required.
- Every refused login answers 401 `invalid_credentials` with one answer, status, headers and body:
  a missing, unknown, expired or used challenge, a registration challenge, an overlapping
  ceremony, an unknown credential or handle, a credential under another account's handle, a
  disabled account, a wrong origin or RP id hash, a bad signature, no user verification or
  presence and a sign count that did not move forward, a cloned authenticator. A table test
  compares the whole answer across all of them. The code is the account login's, so a client that
  handles a refused password login handles this one.
- The refusals are equal in content, not in time. Measured in the security review, the median
  refusal took 341 microseconds for an unknown handle, 518 for an unknown credential, 525 for a
  wrong origin and 651 for a bad signature. The time can tell an unknown handle from a known
  one, but asking needs a valid 32 byte random handle first, and the credential id second.
- A sign count of zero on both sides is allowed, since most synced passkeys keep none. Any other
  count at or below the stored one is refused. Two logins racing with one counter value both read
  the stored count, and the compare and set of `UpdateCredential` lets exactly one through. A test
  holds both at a barrier after the read.
- Every refused registration answers 400 `invalid_passkey` with one body: the same challenge
  failures, a challenge of another account, a wrong origin or RP id hash, no user verification,
  a credential id already stored and a user handle the account no longer has. A name over 64
  characters or with a character that is not printable is 400 `invalid_argument` with `name` in
  the details.
- A store or session failure is a 500, not a refusal: it says nothing about the passkey, and
  answering 401 would tell the person a right passkey is wrong.
- Deleting the last passkey is refused with 409 `last_factor` when `OtherFactors` reports no other
  way to log in, such as a confirmed TOTP secret.
- Rate limiting is the product's job. The begin and finish routes need a per client limit: a begin
  stores a challenge in a bounded store and a finish verifies a signature. Any of them may then
  answer 429 before the handler runs, which leaves the challenge and the cookie as they were.
  The product picks the limit. A frontend that begins a conditional autofill login on every page
  view calls login begin far more often than anyone types a password, so the limit belongs in a
  bucket apart from the password login.
- Passkeys work with the cookie client only. The challenge cookie is host only and
  `SameSite=Strict`, and a login finish starts a cookie session through `StartSession`. A
  frontend on another site does not get the cookie back. In `example/`, `CurrentAccount` answers
  no session for a device session, so a native app cannot register a passkey with its token.
- `passkeytest.Authenticator` is a software passkey for tests: one ES256 key, attestation `none`
  and fields for the origin, the RP id hash, the counter and the flags. Its CBOR is written by
  hand, so `passkeytest` does not import the library. The tests of `passkey` and `example/` run
  every ceremony through it.

#### In `example/`

- `PASSKEY_RP_ID` turns passkeys on, `PASSKEY_RP_NAME` names the relying party. The origins are
  `ALLOWED_ORIGINS`, and `PASSKEY_RP_ID` without them refuses to start, as does an origin that is
  not https or not below the RP id. Without `PASSKEY_RP_ID` no passkey route exists.
- The routes live under `/api/v1/account/passkeys`. The two begin and the two finish routes sit
  behind a limiter of their own, `PASSKEY_RATE_LIMIT_BURST` and `PASSKEY_RATE_LIMIT_PER`, a burst
  of ten refilled over a minute by default, in the place the account limiter has for the account
  routes. A test shows that using up one bucket leaves the other alone. Listing and delete have
  the general limit.
- A device session gets 401 from both registration routes: `CurrentAccount` answers no session
  for it. A test registers through the browser of the same account and calls begin and finish
  with the app's token.
- The store is `passkeytest.MemoryStore`, and the challenge store is the default
  `MemoryChallengeStore`, so every passkey and challenge is gone on a restart. The session's
  `IssuedAt` is the login time the reauth window counts from. `OtherFactors` is 1 for an account
  with a password and a confirmed TOTP secret.
- The account login and the passkey login share one `startSession` function. A test signs up,
  logs in with TOTP, registers a passkey and logs in with it, and compares the two sessions.

#### Native apps and recovery

- A native iOS app uses the web origin through its associated domain. It needs the
  `webcredentials` entry in the Apple App Site Association file that `appassoc` serves. An Android
  app needs the `get_login_creds` relation in `assetlinks.json`, and the origin it sends is
  `android:apk-key-hash:...`, which `Config` refuses today. Neither path is verified on a device.
- Today a native app needs a cookie client for passkeys, as a browser has: the routes set a
  challenge cookie and start a cookie session. An app that signs in with a device token does not
  use them in this version.
- Recovery codes are a follow up. Until then, an account that loses its TOTP device and every
  passkey is locked out, and only an operator can let the person back in.

### Database

- Every product with state runs PostgreSQL, and the first one wrote its connection, its
  migration runner and its readiness check itself. The next ones need the same. `database` holds
  those parts so no product writes them again. The product keeps its schema, its queries and its
  store types, and any boundary rule between them.
- It is the one package that imports `github.com/jackc/pgx/v5`, a dependency exception the owner
  accepted. pgx is the maintained PostgreSQL driver for Go, with a pool, and its TLS settings
  follow libpq's `sslmode`. `database/sql` with a driver would still need a driver, and the pool
  and the PostgreSQL types of pgx are what a product queries with anyway. No other package
  imports it, so `session` or `account` alone never pull it in.
- `database/dbtest` is the test helper. It imports `database` and pgx only through it.
- `Config` has separate fields, never one connection string, so the password stays a secret of
  its own with a `_FILE` twin and never travels inside a URL that gets logged. `LoadConfig`
  reads `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD` or `DB_PASSWORD_FILE`,
  `DB_SSLMODE`, `DB_SSLROOTCERT`, `DB_MAX_CONNS` and `DB_CONNECT_TIMEOUT` through `envconf` and
  returns every problem in one error. The names carry no prefix: a product has one database, and
  the same names everywhere keep charts and compose files alike. A product with a second database
  builds the second `Config` in code.
- A zero field of a `Config` written in code takes the same default as an unset variable, so the
  secure choice does not depend on how the value was built. `Validate` checks it, and `Open` calls
  `Validate`.
- The password never prints. `Config` implements `fmt.Formatter`, `fmt.GoStringer`,
  `json.Marshaler`, `encoding.TextMarshaler` and `slog.LogValuer`. `LogValue` alone was not
  enough in the first product: a whole product config logged through a JSON handler reaches
  `encoding/json`, which printed every field of the nested value. A missing password prints as
  empty, a set one as `[redacted]`. `MarshalJSON` keeps the JSON an object of the fields, which
  `MarshalText` alone would turn into one string.
- Those methods do not run everywhere. `fmt` and slog's text handler cannot call a method of a
  value they reach through an unexported struct field, such as a product's
  `struct{ db database.Config }`, so they print its fields one by one. A dumper that follows
  pointers and ignores methods, such as the diff of a test assertion library, walks further. A
  string, or a pointer to one, is printed by both. So the password is a `database.Secret`, which
  holds it in a closure: `reflect` cannot read what a closure captured, so a walker sees a func
  and prints at most its address. Where the methods run, the `Secret` prints `[redacted]`. The
  redaction test covers the fmt verbs, JSON, xml, both slog handlers and a reflection walk that
  follows pointers, in exported and unexported fields, interfaces, maps and slices.
- The price of the closure: `==` does not compile for a `Config` or a `Secret`, and
  `reflect.DeepEqual` of two set `Secret`s, or of two `Config`s holding one, is false. A test
  compares `Reveal` and the other fields. `NewSecret` builds one, `Reveal` reads it, and copies of
  a `Config` share the unchanged value.
- The DSN carries no password. The user goes through `url.User`, the host through
  `net.JoinHostPort`, and the password is set on the parsed pgx config afterwards. A password with
  `@`, `:` or `%` needs no escaping, and an IPv6 host stays one host.
- The host is a name or an address, an IPv6 address without brackets. A path starting with `/` is
  refused, because pgx would take it as a unix socket, where TLS is off even under `verify-full`.
  Brackets are refused, because they pass the config and fail only when the pool is built. A
  comma is refused, because pgx would read a list of fallback hosts. A colon outside an IPv6
  address (`host:port`), whitespace and an `@` are refused, because each fails only on the first
  connection. The port goes into `DB_PORT`, the user into `DB_USER`.

#### TLS

- `sslmode` accepts `disable`, `require`, `verify-ca` and `verify-full`. The default is
  `verify-full`. libpq's `allow` and `prefer` are refused, because both fall back to plain text
  without telling anyone. A product that wants plain text, a local compose file or a test server,
  says `disable`.
- `verify-ca` and `verify-full` need `DB_SSLROOTCERT`. It is either the PEM file of the CA that
  signed the server's certificate, for example the CA of the cluster's database operator, or
  `system` for the system's root certificates. A deployment that sets nothing about TLS does not
  start, and the error names both options. It never falls back to a weaker mode on its own.
- `system` is libpq's `sslrootcert=system`, which pgx v5.13.0 supports natively: it loads
  `x509.SystemCertPool` and switches the mode to `verify-full` whatever the mode was, `disable`
  and `require` included (read in `pgconn/config.go`). It is for a managed database whose
  certificate a public CA signed. It is accepted with `verify-full` only, as libpq 16 does,
  because with any other mode the setting would not mean what it says. Anything else is a
  startup error.
- `disable` and `require` refuse a root certificate. libpq and pgx turn `require` with a root
  certificate into `verify-ca` silently, so the setting would not mean what it says.
- With `require`, pgx also turns the mode into `verify-ca` when `PGSSLROOTCERT` is set in the
  environment or `~/.postgresql/root.crt` exists, because `Config` names no root certificate for
  `require`. The result is stronger than asked, and can refuse a server that `require` would
  accept. A product that sees this sets `verify-ca` or `verify-full` itself.
- The root certificate file is read while the config is checked, up to 1 MiB, so a wrong path, a
  file without a certificate or a path to something large is one more problem in the startup
  error, not a failure on the first connection.
- pgx builds the TLS configuration from `sslmode` and `sslrootcert` while it parses, the way
  libpq does. `verify-full` sets `RootCAs` and `ServerName`. `verify-ca` sets
  `InsecureSkipVerify` together with a `VerifyPeerCertificate` that checks the chain against the
  root without the host name, which is libpq's meaning. `require` sets `InsecureSkipVerify` and
  checks nothing. The tests inspect the built configuration and run a handshake against a
  throwaway CA over `net.Pipe`, in memory: a wrong CA fails both verifying modes, a wrong host
  name fails `verify-full`.
- pgx also reads the `PG*` environment variables and the files under `~/.postgresql` for
  settings `Config` does not name, such as a client certificate. Every setting `Config` names
  wins over them, which a test pins for `PGSSLMODE`, `PGHOST` and `PGPASSWORD`. Client
  certificates are not a field: no product authenticates that way yet.

#### Pool and readiness

- `Open` validates and builds the pool. It does not connect, so a database that starts after the
  pod does not stop it. `Pool` hands the pgx pool to the product's queries.
- Decision: `Pool` returns `*pgxpool.Pool`, pgx' own type. That makes pgx v5 part of govue's
  public contract, and a move to a pgx v6 would need a new major version of govue. It is
  accepted on purpose. A wrapper with its own query methods would be a second, thinner API that
  every product learns and that lags behind pgx, and the products query with pgx' types anyway.
  The owner decides in the pull request.
- Connections are recycled after thirty minutes and after five idle minutes, and the pool checks
  them every minute. A connection kept for hours survives a failover of the cluster only as a
  broken socket.
- `Check` is a `health.Check`: `registry.Add("database", 0, db.Check)`. It fails while the
  database does not answer, and with `ErrNotMigrated` until a `Migrate` in this process has
  succeeded. `health` names the check on `/readyz` and never the cause. Liveness runs no check,
  so a database outage never restarts a pod that would come back to the same outage.
- A failure to connect is reported by its kind and never by pgx' text, which quotes the host and
  the address: `database: ping: connection refused`, and the same kinds (connection failed,
  timeout, canceled, host lookup failed, tls failed, authentication failed, server refused,
  failed) for the connection
  phase of `Migrate`, `MigrateUntilReady` and the check of the applied files. The retry log line
  of `MigrateUntilReady` carries the same text. A context error and an errno stay matchable with
  `errors.Is`. The error names the component and the kind, not `DB_HOST`: the host is what must
  not appear.
- `DB_HOST` is a host name or an IP address. `?`, `#`, `%`, `/` and the control characters are
  refused, with a rule text that names the variable and never the value, because a URL or a path
  pasted into it would reach `String` and a dump of the `Config`. `String`
  still prints the host for an operator, and so does `%v`. `LogValue` leaves the host out.
- Known trade off: the ping needs a connection from the pool. When every connection is busy, it
  waits until `health`'s timeout for the check ends, and readiness fails. An overload that
  saturates every pod's pool takes every pod out of the load balancer together. That is kept,
  not worked around: a pod that cannot get a connection cannot serve a request that needs one
  either, and skipping the ping while the pool is busy would report ready for a database that
  hangs. A product sizes `MaxConns` for the concurrency a pod serves.
- The package imports neither `health` nor `logging`: the method has the `health.Check` shape,
  and `MigrateUntilReady` takes the logger it writes to. A nil logger is an error, not a
  default.

#### Migrations

- Migrations are plain SQL files a product embeds and hands over as an `fs.FS` and a directory.
  They are named `NNNN_name.sql`. The versions start at 1 and have no gap and no repeat, which the
  loader checks before the database is asked. A file ending in `.sql` in any spelling must follow
  that name, `.SQL` included, and an empty or blank file is refused, so a typo never drops a
  migration silently. Other files in the directory are ignored.
- Each file runs in one transaction together with its row in `schema_migrations`, under the
  advisory lock `pg_advisory_xact_lock`. A file that fails leaves nothing of itself behind, and
  two pods starting together apply each file once. The table is created under the same lock,
  because `create table if not exists` races on the catalogue otherwise.
- Because of that transaction, `create index concurrently` and anything else that cannot run in
  a transaction cannot be a migration. Such a change needs the runner extended first.
- The table and the lock key are the ones of the first product's own runner, so its swap to this
  package needs no data migration, and an old pod and a new one exclude each other during a
  rollout.
- `MigrateUntilReady` runs next to the server. A broken set of files returns at once, because
  waiting does not fix a gap. Everything else is retried with a pause that starts at one second
  and doubles up to thirty, and the loop ends with its context, during the pause too. It never
  gives up on its own: a late database is the normal case in a cluster, and readiness reports
  the gap meanwhile.
- `schema_migrations` stores a checksum per file: the SHA-256 of the exact file bytes, in hex,
  written in the same transaction as the file's own apply. The rule is raw bytes, so a checkout
  that turns LF into CRLF changes it and is refused. A product keeps its migrations LF with
  `*.sql text eol=lf` in `.gitattributes`, as this repository does.
- Threat model: the checksum guards against an accidental edit. It is no tamper protection
  against anyone with write access to the repository or the database.
- Before the runner applies anything newer, it compares every recorded row with the embedded
  files, under the advisory lock. A file whose checksum differs makes it refuse with
  `ErrChangedMigration`. A null checksum is a legacy row and is filled, an empty string is a
  checksum that matches no file and is a change. The error names the file and its version and
  carries neither content nor checksum. `MigrateUntilReady` returns it at once instead of
  retrying, because waiting does not fix it. A change goes into a new file, never into an
  applied one. `Check` repeats the comparison on each call, one small query, and reports not
  ready on the same finding.
- A recorded version above the last file is not an error. The numbering has no gap, so it means
  the database is ahead of the code, which is the normal state of a rolling deploy: the first
  new pod commits version 3 while old pods still run. There are no down migrations, so a
  rollback runs older code on a newer schema, and the only way that works is expand and
  contract: a migration keeps the schema usable for the previous release. The runner therefore
  ignores such rows, logs their versions as a warning in `MigrateUntilReady`, and `Check` stays
  ready. The checksums guard only the versions the running code knows. Refusing here would take
  every old pod out of the Service at once, and a rollback would stay unready. There is no
  `ErrMissingMigration` for that reason.
- A table made before checksums existed gets the column with `alter table ... add column`. The
  runner looks in `information_schema.columns` first and alters only when the column is
  missing, because the alter takes an `ACCESS EXCLUSIVE` lock and needs table ownership on every
  start otherwise. A row without a checksum gets it from the file in the embedded set on the
  first run, logged through the logger of `MigrateUntilReady`. That run cannot know whether the
  file was edited before, so the fill trusts the file as it is then. It never fails.
- `Config.AllowChangedMigrations` overrides the refusal. Its risk: the schema no longer matches
  the file and nothing checks it afterwards. It replaces the stored checksum with the file's, so
  one run accepts the edit and the option can go. Because the stored checksum changes, every
  running pod that still holds the other version of the file turns unready with
  `ErrChangedMigration` until it is rolled over: a deliberate act with that cost. `Check` keeps
  comparing under the option. Only `MigrateUntilReady` logs the accepted names, after the
  commit: `Migrate` has no logger and its signature stays. The option is code only: there is no
  environment variable, because the secure default must not be weakened by a typo or a stray
  value in a manifest, and `LoadConfig` never sets it. The decision belongs to the product's
  startup code.
- A migration library, `golang-migrate` or `goose`, would bring down migrations, a CLI and more
  dependencies for a schema that is a handful of files applied forward only.

#### Tests against a real database

- The rules of the package that need no server, the config, the redaction, the DSN, the TLS
  configuration, the loader, the retry loop and the readiness semantics with a fake pinger, run
  everywhere with no socket.
- What only a server can prove runs against a real PostgreSQL: connect, migrate, idempotence,
  the rollback of a failed file, the advisory lock blocking a second migrator, both around the
  bookkeeping table and around a single file once the table exists, concurrent migrators,
  `Check` before and after the schema, a wrong password, and the database per test. The
  checksum rules run there too: a clean run, a changed file, a line ending change, a row without
  a checksum and a table without the column, an empty checksum, no exclusive lock when the column
  exists, older code on a newer schema, the override, and readiness after an
  edit. The comparison itself, `compare`, and `Check` over a fake ledger, newer rows included,
  run with no socket.
- `dbtest.Config` and `dbtest.Open` read the server from `GOVUE_TEST_DATABASE_URL`, create a
  fresh database per test and drop it afterwards with `DROP DATABASE ... WITH (FORCE)`, from a
  connection to the database the URL names, never to the one it drops. `WITH (FORCE)` needs
  PostgreSQL 13 or later, and CI and `example/compose.yaml` run 17. A test closes the pools it
  opens before it ends, and `dbtest.Open` does that for its own. The role needs `CREATEDB`. Without
  the variable the test is skipped with a message naming it, and `task test:go` prints a
  warning first, because `go test` hides a skip. With `CI` set to `true` or `1` and the variable
  missing the test fails, so the suite cannot go green by skipping. The `test` job runs a
  PostgreSQL service container with a `pg_isready` health check and sets the variable. The
  workflow test allows that one service only, from the official `postgres` image, without
  registry credentials and without a secret. A product uses the same helper and the same
  variable in its own CI.
- These tests open a socket, in CI only. It is the one deliberate exception to "tests use no
  socket" and is recorded under Deviations in `AGENTS.md`. A fake would prove the fake: the
  transaction, the lock and the rollback are the server's behaviour.

### HEAD on the API client

- `client.head(path, options)` sends a HEAD request and resolves with `{ status, headers }`, never
  a body. Both the cookie and the bearer client have it. The call still goes through
  `createClient`, the one place an app calls `fetch`.
- A HEAD answer has no body, so a refusal is mapped from the status alone: 400 to
  `invalid_argument`, 401 to `unauthenticated`, 403 to `permission_denied`, 404 to `not_found`,
  409 to `conflict`, 413 to `request_too_large`, 415 to `unsupported_media_type`, 429 to
  `too_many_requests`, 503 to `unavailable` and every other status to `internal`. The `ApiError`
  carries the status. A 401 still calls `onUnauthenticated`.
- A network failure, a timeout and an abort map as for `get`. HEAD is safe, so no CSRF header
  is sent. `RequestOptions.cache` is new and applies to every method: it is passed to `fetch`
  as given, and left out when unset.
- The server decides what HEAD means. The Go side is unchanged.

```ts
// Is a one time secret waiting? The route answers 200 or 404 and never consumes it.
try {
  await client.head(`/secrets/${id}`, { cache: 'no-store' })
  waiting.value = true
} catch (error) {
  if (!isApiError(error) || error.code !== ErrorCode.notFound) throw error
  waiting.value = false
}
```

### The bearer client of a native app

- `createClient({ auth: { mode: 'bearer', storage, rotateAfterMs, rotation, deviceName } })` is
  the client of a native app. It returns a `BearerClient`: the same `get`, `post`, `put`, `patch`,
  `del` and `head`, plus `signIn` and `signOut`. The default is `auth: { mode: 'cookie' }`, and its
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
- The `compact` prop turns the three text buttons into three icons: a device for the system
  setting, a sun and a moon. They are inline SVG with `currentColor` and `aria-hidden`, so the
  role tokens colour them, no icon library is involved and no hex value appears. The group, the
  roving tabindex and the keys are the same as in the default layout, and each target stays at
  least 44px. An icon says nothing to a screen reader, so each option is named by its mode
  ("Light mode"). The group name stays static ("Theme"), because the checked option already
  announces the current mode and a group name that changes while focus is inside is not
  re-announced by every screen reader. The texts are in both catalogues under `govue.theme` and
  the `systemLabel`, `lightLabel` and `darkLabel` props override them. The three targets take
  about 142px (3 x 44px, two 4px gaps, the border, computed from the classes, not measured),
  where the text layout needs about 200 to 220px (an estimate), and a header on a 360px phone
  has about 328px for the brand and every action. The default layout is unchanged. The
  transition is dropped by the global reduced motion rule, and focus is the global ring.
- The header recipe for a phone: `<GovueHeader>` with `<GovueThemeSwitch compact />` in its
  `#theme` slot. The slot is the supported way and needs no header prop. A compatible
  `themeSwitch?: boolean | 'compact'` stays possible later.
- A choice kept in `localStorage` is only readable once script runs, so a page that waits for
  the bundle flashes the wrong theme. The Go package `themeboot` injects a small inline script
  into the head that sets `data-theme` and `data-mode` before the first paint.
- Under the default policy an inline script runs only with the response's nonce, so injection
  happens at serve time and takes the nonce from `middleware.Nonce`. A static `index.html`
  cannot carry one. The response is `no-store`, since a cached nonce is a reused nonce.
- A page served as the same bytes on every response, with an ETag, takes the script without a
  nonce from `themeboot.InjectStatic`, and `script-src` carries `themeboot.Hash` with the same
  options. The hash allows that exact text and nothing else. The `frontend` package does both,
  see "Frontend server".
- The script is a plain file embedded in the Go package, not generated from the TypeScript. The
  key and the attribute names live in `themeStorage.ts` and in `themeboot` as constants, and a
  Vitest test runs the shipped file in a DOM and checks it against both, so a drift fails a
  test instead of showing up as a flash.
- With nothing stored the script leaves `data-mode` off, because the stylesheet already follows
  the system setting through `light-dark()` and an explicit value would only pin it.
- The browser bar follows the mode too. `favicon.Icons.WithDarkThemeColor` writes one
  `theme-color` tag per mode, with the media query `(prefers-color-scheme: light)` or `dark`, so
  the browser picks the system's one with no script at all. Media queries alone cannot follow a
  stored mode that differs from the system, so for a stored light or dark mode the script copies
  the colour of that mode's tag into every `theme-color` tag above it. The tags therefore come
  ahead of the script in the head, both still ahead of every stylesheet. The colours live in the
  tags and not in the script, so `themeboot.Hash` does not change with them and one hash covers
  every site. A site with one colour keeps one tag without a media query, which the script
  leaves alone.
- A change of the mode after load re-pins the tags too. Before it pins anything, the boot script
  stores each tag's own colour in `data-govue-color`. `useTheme` calls `pinThemeColor` from
  `web/src/composables/themeColor.ts` whenever the mode changes: light and dark give every tag
  that mode's colour, system gives each tag its own colour back, so the browser follows the
  system again. The boot script is inline and imports nothing, so it holds a small copy of the
  same step, and a Vitest test runs both on the same page and compares the tags.

### The header and the navigation

- `GovueHeader` is the one banner landmark. Its ground is three role tokens, set in the theme
  file only and never with a selector on govue's markup. A site that sets none keeps the look
  the header always had.

  | Token                   | Default                | What it styles                        |
  | ----------------------- | ---------------------- | ------------------------------------- |
  | `--color-header-bg`     | `var(--color-surface)` | the ground of the solid header        |
  | `--color-header-border` | `var(--color-border)`  | the bottom border of the solid header |
  | `--blur-header`         | `0px`                  | the backdrop blur of the solid header |

  Every theme declares all three, and the generator writes them, so a theme file shows the whole
  contract. `--color-header-bg` and `--color-header-border` follow `surface` and `border` through
  `var()` until a theme gives them a value of their own. The blur is a Tailwind `--blur-*` token,
  used as `backdrop-blur-header`.
- `GovueHeader` props and slots:

  | Name           | Type                           | Default   | Meaning                           |
  | -------------- | ------------------------------ | --------- | --------------------------------- |
  | `themeSwitch`  | `boolean`                      | `false`   | shows `GovueThemeSwitch`          |
  | `overlay`      | `boolean`                      | `false`   | header out of the flow, see below |
  | `navPlacement` | `'start' \| 'center' \| 'end'` | `'start'` | where the navigation sits         |
  | slot `brand`   |                                |           | the brand, first in the row       |
  | slot default   |                                |           | the navigation                    |
  | slot `theme`   |                                |           | replaces the theme switch         |
  | slot `actions` |                                |           | the actions, last in the row      |

  `navPlacement` acts from 480px up. `start` keeps the navigation next to the brand, `end` puts
  it next to the actions, `center` in the middle of the space between. Below 480px the
  navigation is its own row, and the markup order never changes.
- `overlay` takes the header out of the flow (`position: fixed`), so the page content starts
  under it and nothing shifts when it turns solid: the row has the same height and border width
  in both states. It is transparent, without border and blur, until the page has scrolled past a
  small threshold (16 px), then takes the solid ground above. The state is `data-state` on the
  header, `transparent` or `solid`, and `data-overlay="true"` marks the mode, so a site can hook
  its own rules on them. Without `overlay` the header is `sticky` and always `solid`.
- An open mobile menu always has the solid ground, as the menu lists links over whatever is
  behind the header. `GovueNav` reports its open state to the header it sits in, which is the
  only change the menu got. Reduced motion switches the ground without a transition
  (`motion-reduce:transition-none`, and the global rule in `base.css`).
- The scroll state is `useScrolled({ threshold?, active? })`, exported from the package. The
  listener is passive, is attached only while `active` is true (the header passes `overlay`),
  writes a ref that triggers nothing while the state stays the same and reads no layout. Before
  mount and on the server the state is false, so a prerendered page is the transparent one, and
  at mount the offset is read once.
- What the host guarantees over the transparent state. The transparent header shows the dark mode
  roles whatever the page mode is, through `color-scheme: dark` on the header, so text, icons,
  the focus ring and the call to action keep their AA ratios on a dark ground. The hero behind
  the header must therefore be dark: its relative luminance has to stay at or below
  `(L + 0.05) / 4.5 - 0.05`, where `L` is the luminance of the dark `--color-text`. Dark `bg`
  is inside that bound in every shipped theme. A hero on `bg-bg` with `[color-scheme:dark]` on
  the section is dark in both modes, and a photograph needs a scrim. govue cannot check the
  page behind the header, the contrast test checks the roles. A page that cannot guarantee a
  dark hero uses the default header, which has a ground of its own.
- A translucent ground, for example
  `--color-header-bg: color-mix(in srgb, var(--color-surface) 95%, transparent)` with a
  `--blur-header` of 12px, lets the page content show through, so the contrast depends on the
  content. The contrast test checks it against the lightest (`#ffffff`) and the darkest
  (`#000000`) content in every shipped theme and both modes. The alpha of the ground at which
  text keeps 4.5:1 over both:

  | Text role                                     | Minimum alpha |
  | --------------------------------------------- | ------------- |
  | `text`, the brand and nav items               | 0.85          |
  | every text role (`text-muted`, `accent-text`) | 0.95          |

  Use 0.95 or more. Below it a role can fall under 4.5:1 over the worst content, and the blur
  does not help, because it only mixes content between the two extremes. The constants are
  `TRANSLUCENT_ALPHA_TEXT` and `TRANSLUCENT_ALPHA_ALL_TEXT` in `web/src/styles/contrast.ts`.
- A `NavItem` is `{ href, label, current?, variant? }`. `variant: 'cta'` renders the item as a
  solid button from `accent-strong` and `accent-on` (hover `accent-strong-hover`), in the nav row
  and in the open menu, with a 44 px target. It stays a link, keeps `aria-current` and shows the
  current page with an underline as well as the fill. Mark one item at most. The pairs are in the
  contrast test in both modes.

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
- `GovueLanguageSwitch` has an opt-in link mode for a site whose languages have their own paths.
  `linkFor: (code) => path` maps a language to the same page in that language, usually
  `(code) => localizedPath(route.path, code)`. Each language is then a real `<a>` with `href`,
  `hreflang` and `lang`, in a `role="group"` named like the select, and the current one has
  `aria-current="true"`. A crawler and a visitor without script can follow the links, and a
  screen reader announces links, not radios. Targets are 44 px, the styling is role tokens only.
  Without `linkFor` the select is rendered and nothing changes.
- A plain click in link mode sets the locale, stores the choice and emits `change`, as the
  select does. Whether the page reloads is the router's decision. A site hands the path to its
  router with the `navigate` prop, `(href, code) => boolean`, and returns true, for example
  `(href) => { router.push(href); return true }`: govue then cancels the browser's navigation,
  the locale is already updated and nothing reloads. Without `navigate`, or when it returns
  anything else, the browser follows the link and the stored choice is there for the next page.
  A click with a modifier key or a middle click is left to the browser and changes nothing.
  The `link rel="alternate" hreflang` tags of the head stay `hreflangLinks` in "Language
  routes".
- Catalogues are TypeScript files, not JSON, so each carries the license header and no
  exception is needed. Only visitor-facing text is translated; developer errors stay English.
- The choice is kept in `localStorage` by default. A cookie or an account callback serves
  signed in areas that need no path prefix. A test compares the key sets and the named
  placeholders of all catalogues, so a missing key fails the build.
- German has two registers. `de.ts` says "du", `deFormal.ts` says "Sie" with exactly the same
  keys, and a text that addresses nobody is the same in both. `createGovueI18n({ formality:
  'formal' })` loads the formal one under the locale `de`. The register is a site's tone, not a
  language: a code such as `de-formal` would have shown a second German in the switch, set
  `lang` to a tag no browser reads, needed its own routes under `/de-formal/`, its own legal
  pages and its own stored choice. The option changes none of these. The default stays
  informal, so no existing site changes. English has one register and is the same either way.
  `formalCatalogues` lists the variants and is not part of `catalogues`. A test fails when a key
  is missing in either German catalogue, when a placeholder differs, when a formal text says
  "du" or an informal imperative, and when a text that says "du" was not changed. A site
  installs the option in the browser and in the prerender, so both say the same. Before it a
  site overrode each contact text that says "du" by hand.
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
- An unprefixed public path such as `/about` is not redirected and answers 404. The legal paths
  are the exception, see "Legal pages". The cookie storage of the language layer stays an option
  and is not the default.

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
- An unprefixed public path such as `/about` and an unknown prefix such as `/fr/` are not
  redirected. They fall to the catch-all 404, because redirecting them would invent URLs and
  need the route table in Go. The fixed legal paths of "Legal pages" are the one exception.
- `ServeMux` answers `/de` with a 307 to `/de/` and keeps the query. A test pins it.

### Legal pages

- Every site has an imprint and a privacy statement. Only a site that sells something has terms.
  Each site writes its
  own texts, and the owner reviews them. govue ships no legal text, no provider data and no shared
  sections: the rights to some existing texts are not the owner's, and nothing here is legal
  advice. govue carries the structure only, so no site writes the paths, the redirects, the shell
  or the check again.
- The canonical pages follow the language routes: `/de/imprint`, `/en/imprint`, `/de/privacy`,
  `/en/privacy`, `/de/terms`, `/en/terms`. The slug is English in both languages, so the paths
  are one fixed set and the German names are aliases.
- `language.HandleLegal(mux, LegalConfig, wrap)` registers the redirects:

  | Path | Location | Language |
  | --- | --- | --- |
  | `/imprint` | `/<code>/imprint` | the visitor's |
  | `/privacy` | `/<code>/privacy` | the visitor's |
  | `/terms` (with `Terms`) | `/<code>/terms` | the visitor's |
  | `/impressum` | `/de/imprint` | German |
  | `/datenschutz` | `/de/privacy` | German |
  | `/datenschutzerklaerung` | `/de/privacy` | German |
  | `/agb` (with `Terms`) | `/de/terms` | German |

- Terms are opt in. The zero value of `LegalConfig` leaves `/terms` and `/agb` unregistered, so
  they fall to the site's 404. A site that sells something sets `Terms: true` and builds a terms
  page. With terms on by default, a site that forgot the switch would redirect to a missing page.
- The visitor's language is picked like the redirect at the root: the cookie, then
  `Accept-Language`, then the default. `LegalConfig` embeds `Config`, and `Languages` must hold
  `de`. The server sees the visitor's choice only through the cookie. The Vue language layer
  keeps the choice in `localStorage` by default, which the server cannot read, so a site that
  wants `/imprint` to follow the stored choice passes `storage: { kind: 'cookie' }` to
  `createGovueI18n`. Without it the redirect follows `Accept-Language`. The footer links go
  straight to `/<code>/imprint` in the active language and need no redirect at all.
- Every answer is a 302 with `Cache-Control: no-store` and no body. The visitor dependent ones add
  `Vary: Accept-Language, Cookie`. A browser may cache a 301 or 308 for a long time, and a
  cached redirect cannot be withdrawn when a visitor changes the language or the scheme changes.
  307 would add nothing, since only GET and HEAD are served. 302 matches the root redirect.
- The Location is `/`, a configured code, `/` and a page name from the table. The request path,
  the query, the `Host` header, the cookie value and the header text never enter it. The query is
  dropped, not carried over as at the root, because a legal page takes no parameters.
- Each path is registered as `GET <path>` and `<path>`, and the same with one trailing slash
  (`<path>/{$}`), like `appassoc`. Other methods get 405 with `Allow: GET, HEAD`. The paths are
  case sensitive: `/Impressum` falls to the catch-all. A double slash is cleaned by `ServeMux`
  with its own 307 before any handler runs.
- The pattern without a method needs a catch-all without a method. A catch-all bound to a method,
  such as `GET /`, `GET /{path...}` or `GET /{page}`, makes `ServeMux` panic at startup. The
  message ends in `/imprint matches more methods than GET /, but has a more specific path
  pattern`, or in `GET / matches fewer methods than /imprint, but has a more general path
  pattern` when the catch-all comes second. The fix is `mux.Handle("/", notFound)`, which every
  current site and `example/` already use. A test pins the message.
- The redirects need no session and no CSRF check, so a site mounts them outside the route chain,
  as `example/` does. The outer chain still applies.
- The Vue package gives `legalRouteDefs(pages)` for the route table, `legalRoutes(pages)` with
  one record per page and language shaped like a vue-router record, `legalPath` and
  `legalLinks`. govue does not depend on vue-router. `GovueLegalPage` is the shell: skip link,
  back link, language switch, one h1 from the catalogue and a slot for the site's text, whose
  headings start at h2. The switch goes to the same page in the other language. `GovueFooter`
  takes `legal`, a list of pages, and links them first in the active language. The texts live
  under `govue.legal` in both catalogues.
- `govue-check-legal --dir <out-dir>` runs after the prerender. It fails when the imprint or the
  privacy page is missing in a language, carries another `lang`, or is not in `sitemap.xml`,
  which the server serves from, or when the sitemap names more than one origin. Comments in the
  sitemap and the pages are ignored. Terms are optional: a terms page in one language must exist
  in all, and `--require-terms` makes it required for a site that sells.
- The same check reads every prerendered HTML page under `<out-dir>/<code>/`, the legal pages
  included, and fails a page with no `a` link to the imprint and the privacy page of its own
  language, and to the terms page when the site has one. All failures are listed, each naming
  the page, the missing page and the language. A link counts when it is an `a` element with a
  root relative `href` that is exactly the path, `/de/imprint`, query and fragment ignored, tag
  and attribute names in any case, any quoting. A trailing slash or `/index.html` does not
  count, because the server registers exact patterns and answers those with a 404. A link in a
  comment, CDATA, a `script`, `style`, `textarea`, `title`, `noscript` or `template`, a `link`
  element, another attribute, an absolute URL or a document relative path does not count. A
  page outside the language directories, such as the build template at the root, has no
  language and is not checked. The check proves that every page carries the links. It does not
  prove the link text, that the links sit in a footer, or that the footer is visible. The HTML
  is read by a small tokenizer in the script, because the package has no parser dependency. It
  differs from a browser in three places: the escape states of a script, CDATA in HTML content
  and a self closed raw text element in svg. They hide a link and never invent one, or Vue SSR
  does not produce them.
- `web/example/` is a placeholder site with the three pages. Its text says that it is a
  placeholder. A test prerenders it and runs the check on the output. The Go example leaves
  `Terms` off, and its tests follow each redirect to a page that answers 200.

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
  and a test runs the same cases through both copies. The package requires Node 24 or later
  (`engines`). Only Node 26 is exercised, and the repository's own tooling config extends
  `@tsconfig/node26`.
- With `--out` equal to `--template`, the marker is consumed, so a second run fails with no
  placeholder. Run it after a fresh `vite build`.
- What this does not solve: data that changes per request, personalised content, client side
  routing, page discovery beyond the route table, incremental rebuilds and hydration beyond what
  a normal client mount does once the static HTML has loaded. A site that needs those is better
  served by a real meta-framework.

### Frontend server

- A site may run its frontend and its API as two images. The frontend image serves the build
  and nothing else, the API image serves the API and nothing else. Every site used to wire the
  same pieces by hand for the frontend half: the embedded build, the redirect at `/`, the
  trailing slash rules, the 404 page per language, the cache headers, the legal aliases and the
  hash of the theme boot script in the policy. The package `frontend` is that half, so a site
  builds none of it.
- One call builds the server and one call runs it:

  ```go
  s, err := frontend.New(frontend.Config{
      Build:      dist,      // fs.Sub of an embed.FS, or os.OpenRoot(dir).FS()
      ThemeColor: "#0f766e", // the favicon set at the root of the build
      Logger:     logger,
  })
  if err != nil { return err } // an incomplete build never starts
  return s.Run(ctx, ":8080", ":8081")
  ```

  `Handler` and `AdminHandler` are there for a site that runs the listeners itself and for tests.
- The build is Vite's `dist` with the output of `govue-prerender --out-dir` written into it.
  `New` reads all of it into memory at startup. Every path that answers 200 is a key of a table
  built then, so no request path is ever joined to a file name and no directory is ever listed.
  The site directory can be closed right after `New`.
- What is served, and with which cache header:

  | Path | Source | `Cache-Control` | ETag |
  | --- | --- | --- | --- |
  | each `loc` of `sitemap.xml` | `<path>/index.html` | `no-cache` | yes |
  | `/<code>` and `/<code>/<page>/` | 301 to the canonical path | none | no |
  | `/assets/<hashed name>` | `assets/` | `public, max-age=31536000, immutable` | yes |
  | `/assets/<other name>` | `assets/` | `no-cache` | yes |
  | `/assets/<name>.map` | only with `ServeSourceMaps`, else 404 | as above | yes |
  | `/favicon.svg`, the two PNG icons, `PublicFiles` | root of the build | `public, max-age=86400` | yes |
  | `/sitemap.xml` | as built | `public, max-age=3600` | yes |
  | `/robots.txt` | generated, names the sitemap | `public, max-age=3600` | yes |
  | `/` | `language.Redirect`, 302 | `no-store`, `Vary: Accept-Language, Cookie` | no |
  | legal aliases | `language.HandleLegal`, 302 | `no-store` | no |
  | each `From` of `Config.Redirects` | 301 to its page | none | no |
  | anything else | `<code>/404.html`, 404 | `no-cache` | no |

- An ETag is the quoted SHA-256 of the body. `http.ServeContent` answers a matching
  `If-None-Match` with 304. A hashed asset name ends in `-` or `.` and the eight characters of
  a Vite hash, at least one of them a digit or a capital letter, such as `index-BXa3k2_9.js`.
  A name that fails the rule is revalidated, which costs a request and never serves a stale
  file. The content type comes from a closed table of extensions, and a file with another
  extension, HTML included, fails startup.
- A source map under `assets/` is skipped by default and answers 404. It publishes the original
  sources, and Vite's `build.sourcemap: 'hidden'` writes maps a site means to keep for its error
  tracker only. `Config.ServeSourceMaps` serves them as `application/json`, for a site that
  wants its sources public. A map is never a `PublicFiles` entry. Vite's `build.sourcemap: true`
  also writes a `//# sourceMappingURL=` comment into every served bundle, whose map then answers
  404 and the browser's developer tools report a failed load. A site uses `'hidden'` or `false`
  with this package, or sets `ServeSourceMaps`.
- The security headers are those of `middleware.DefaultSecurity` on every response, the 404,
  the redirects and the 405 included. `Config.Security` replaces them for a site with a reason,
  such as `HSTS = 0` over plain HTTP. A page is the same bytes on every response, so it cannot
  carry a nonce. `New` writes the boot script with `themeboot.InjectStatic`, without a nonce,
  and adds `themeboot.Hash` to `script-src` of a copy of the policy (`middleware.Policy.Clone`).
  The hash allows that one script text and nothing else. The policy still carries a nonce per
  response, which no static page uses. A policy without `script-src` is refused, because adding
  the directive would take the `default-src` fallback away from every other script.
- Header table of a page under the default:

  | Header | Value |
  | --- | --- |
  | `Content-Security-Policy` | `default-src 'none'; script-src 'self' '<themeboot.Hash>' 'nonce-…'; …; worker-src 'self'; …` |
  | `Strict-Transport-Security` | `max-age=31536000; includeSubDomains; preload` |
  | `X-Content-Type-Options` | `nosniff` |
  | `X-Frame-Options` | `DENY` |
  | `Referrer-Policy` | `strict-origin-when-cross-origin` |
  | `Permissions-Policy` | `middleware.DefaultPermissionsPolicy` |
  | `Cross-Origin-Opener-Policy` | `same-origin` |
  | `Cross-Origin-Resource-Policy` | `same-origin` |
  | `X-Request-Id` | per request |

- The path rules, checked before routing. A path that fails answers the 404 page of the default
  language, so the mux never cleans or redirects a path itself:
  - an encoded slash, backslash, dot, percent or NUL (`%2f`, `%5c`, `%2e`, `%25`, `%00`, either
    case) in the written path.
  - a decoded backslash, percent or control character, and invalid UTF-8.
  - an empty segment other than the last one, so `//` anywhere.
  - a segment that starts with a dot. That covers `.`, `..`, dotfiles, `.vite` and
    `.well-known`.
- The table refuses most of these paths a second time. A test checks each rule against the
  check alone and against the whole handler, and a mutation of each rule fails it.
- One canonical form per page: the path of its `loc`, `/de/` for a language root and
  `/de/imprint` for a page. `/de` and `/de/imprint/` answer a 301 to it, with the query kept and
  its unsafe bytes escaped. The example's own site handler answers the slash form with 404
  instead. The frontend redirects it, because sites linked it before. `/index.html`, a page file
  path and a page the sitemap does not list answer 404. A browser's percent encoding of a page,
  such as `/de/%C3%BCber`, and the raw form reach the same page, the canonical link in the page
  names one.
- A moved page keeps its old links through `Config.Redirects`, a list of exact
  `frontend.Redirect{From, To}` pairs, such as `/de/agb` and `/de/agb/` to `/de/terms`. Each
  answers a 301 to the escaped path of the target's `loc`, with the query kept and escaped like
  the slash redirect, so no part of the request reaches `Location` but the query. A trailing
  slash belongs to the path, so a site lists both forms. There are no patterns and no regular
  expressions. `New` refuses, by position and kind and never by value:
  - a target that is not a page of the sitemap, an alias, an asset or a file included.
  - a source that fails the path rules above, `/` itself, or holds `{`, `}` or white space, the
    syntax of a `ServeMux` pattern, or `?` or `#`, which a request carries outside its path, so
    such a source would never match.
  - a source under `/assets/`, which would shadow a file of the build.
  - a source that is a route already: a page, its slash form, a language root, an alias, the
    sitemap, `robots.txt` or a public file. `ServeMux` finds the conflict: its panic, which
    quotes the pattern, is recovered and becomes an error that does not.
  - a source listed twice.
- A redirect answers no HTML with 200, so it is never counted as a view.
- The build is refused at startup, every problem at once, naming the file:
  - no `<code>/404.html` for a configured language, or one without a head element.
  - no `/<code>/imprint` or `/<code>/privacy` in the sitemap for a configured language, or a
    `/<code>/terms` in some languages only. A terms page in every language mounts `/terms` and
    `/agb`, so a site needs no flag for it.
  - no `sitemap.xml`, one that is a symbolic link or not a regular file, or one of the shapes
    `example/site.go` refuses, see "Language routes". A favicon file that is a link is refused
    the same way.
  - a page or a 404 page without a head element, or one that carries icon links or a
    `theme-color` tag already.
  - a missing favicon file or `PublicFiles` entry, an invalid theme colour, an invalid language
    configuration. A refused configuration value is never quoted. A `PublicFiles` entry that is
    missing, a directory, a link or another non regular file is named by its position and the
    kind only, and no `*fs.PathError` under the error carries it. A missing one still matches
    `fs.ErrNotExist`. The files of the build itself, the pages, the 404 pages, the sitemap, the
    assets and the favicon files, are named in an error: they are the operator's build output,
    not configuration values. The 404 page and legal page names also hold a language code,
    which is an accepted code of `Config.Language`, never a refused value.
  - an asset that is a symbolic link, not a regular file, has a name outside letters, digits,
    `.`, `_` and `-`, or an extension outside the table. A file or directory under `assets/`
    whose name starts with a dot is skipped and never served.
- The browser bar colour: `Config.ThemeColor` alone writes one `theme-color` tag for both
  modes. With `Config.ThemeColorDark` it is the light colour, and every page carries one tag
  per mode, which the boot script pins to a stored mode, see "The theme switch and its boot
  script". An invalid dark colour is refused, by field and never by value.
- Migration of a site's prerender. `frontend` writes the head markup itself, so a page that
  already carries it is refused. Before a site moves its pages into the frontend image, its
  prerender stops writing:
  - the icon links, `rel="icon"` and `rel="apple-touch-icon"`. The favicon files stay at the root
    of the build.
  - every `theme-color` meta tag. The colours move to `Config.ThemeColor` and
    `Config.ThemeColorDark`.
  - its own theme boot script, inline or as a file. A script of its own would also need its own
    hash in the policy. A site with its own `localStorage` key passes
    `themeboot.WithStorageKey` in `Config.ThemeBoot`.
- No session, no cookie, no API route, no CORS. Every method but `GET` and `HEAD` answers 405
  with `Allow: GET, HEAD`. A bare `OPTIONS *` reaches the handler under `Run`, because
  `httpserver` sets `DisableGeneralOptionsHandler`, and answers the 404 page with every header.
  A site that serves `Handler` on its own `http.Server` without that field gets the empty 200
  `net/http` writes itself, with none of the headers above. `/api/` answers 404 from the
  frontend, which is right only when the Ingress sends it to the API image first, or in
  development when `Config.DevAPIProxy` forwards it, see below.
- Health and readiness live on the admin listener with the metrics, behind `Recover` and
  `RequestID` only, as in "A second listener for what the Ingress must never route". The public
  chain is the outer chain of `example/`: `Recover`, `RequestID`, the security headers, the
  metrics middleware and the access log with `Config.ClientIP`. The path check sits between the
  access log and the mux and passes the request on unchanged, so `r.Pattern` still reaches both.
  `Config.RateLimit` wraps every route when set. `Run` drains readiness before the drain delay
  and refuses an empty admin address.
- Splitting the images:
  - The frontend image holds the static build and a small Go binary that embeds it and calls
    `frontend.New` and `Run`. No Node process and no nginx run in it.
  - The API image holds the second binary of the same site: the API routes, sessions, the
    contact form and whatever the site has, built from `example/` as before but with no page
    handler.
  - Both run with an admin listener. The Service of each exposes both ports, the Ingress routes
    the public port only.
- Same origin, the default. One host, the Ingress routes by path:

  ```yaml
  rules:
    - host: example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend: { service: { name: site-api, port: { name: http } } }
          - path: /.well-known
            pathType: Prefix
            backend: { service: { name: site-api, port: { name: http } } }
          - path: /
            pathType: Prefix
            backend: { service: { name: site-frontend, port: { name: http } } }
  ```

  `pathType: Prefix` matches by segment, so `/api` takes `/api/v1/...` and not `/apidocs`. The
  longest path wins in the Ingress specification. The frontend refuses `/.well-known/`, so the
  app association files and a `security.txt` are served by the API image. Nothing changes for
  the browser: the policy's `connect-src 'self'` covers the API, the session cookie stays
  host-only, the CSRF origin check sees the same origin and the Vue client keeps its default
  `baseUrl`.
- Another origin, such as `api.example.com`, has consequences on both sides:
  - the API needs `middleware.CORS` with the frontend origin in `Origins`, `Credentials: true`
    for the session cookie, `Headers` with `csrf.HeaderName`, and
    `ExposeHeaders: []string{"Retry-After"}`, so the contact form reads its wait. The CSRF
    protector needs `TrustOrigin` with the frontend origin.
  - the frontend policy needs the API origin in `connect-src`, through
    `Config.Security.Policy.Add("connect-src", "https://api.example.com")`.
  - the Vue client needs `createClient({ baseUrl: 'https://api.example.com/api/v1',
    credentials: 'include' })`.
  - a sibling host is the same site, so a `SameSite=Lax` cookie still goes along. A host on
    another site is a third party cookie, which several browsers block by default. Keep the API
    on the same site.
  - every preflight is a round trip more. Same origin needs none of this, so it is the default.
- Page views. `Config.Analytics` takes the site's `*analytics.Client`, nil counts nothing. A site
  cannot wrap `Handler` itself: `RequestID` and the security headers replace the request inside
  the chain, so an outer counter would never see `r.Pattern`. So `New` places the client:
  - `Middleware` directly around the mux, inside the access log and the path check, as
    `example/` does for the one server. A page answered with 200 counts by its pattern, such as
    `/de/imprint`, never by its path, and the query is never sent.
  - `NotModified` on the handler of each page and of nothing else, so a page revalidated with
    its ETag and answered with 304 counts too, see "Analytics".
  - A redirect, an alias, the slash form, a 404, an asset, a public file, `robots.txt` and the
    sitemap answer no HTML with 200 and no page 304, so none of them counts.
  - The rules of `analytics` hold unchanged: GET only, Do Not Track and Global Privacy Control
    honoured, no bot, no prefetch, no private client, the address masked unless the site sets
    `FullIP`, the client address from the same `Config.ClientIP` as the access log.
- The drop counter. The frontend owns its metrics registry, so it registers
  `analytics_views_dropped_total` itself, read from `Stats().Dropped` through
  `metrics.NewCounterFunc`, when the client counts. The site passes no `OnDrop`, and the name is
  the one `example/` uses on the one server.
- Shutdown. `Run` closes the client once both listeners stopped, within
  `frontend.AnalyticsCloseTimeout` (five seconds), so the queued views are sent, and joins its
  error to the server's, on every way out, a listen that fails included. A test runs `Run` with
  an address without a port, which fails while it is parsed, before any socket. A site that
  serves `Handler` on its own listener calls `Server.Close` after that listener stopped.
- Development, one origin without nginx. In production the Ingress puts the frontend and the API
  on one origin. A compose stack on a developer's machine needs the same, so the browser keeps
  its same origin rules: no CORS, `connect-src 'self'`, the session cookie host-only, the CSRF
  origin check on one origin and the Vue client on its default `baseUrl`. Two ports would be two
  origins, and the image would differ from production in the API address it was built with.
  So the frontend forwards `/api/` to the API container itself, for development only:
  - `Config.Development` must be set, and the package never reads it from the environment. The
    site decides, the example reads `DEVELOPMENT=true`. Without it a `DevAPIProxy` is refused,
    and without a `DevAPIProxy` `/api/` stays the 404 of production. Tests pin both.
  - `Config.DevAPIProxy` is `http` or `https` on a loopback address, `localhost` or one label
    such as the compose service name `api`, with an optional port and path prefix, and no
    userinfo, query or fragment. A public or private network address, a dotted name and `0.0.0.0`
    are refused, by field and kind, never by value. So is a label a resolver may read as an IPv4
    address: all digits, octal with a leading `0` included, or `0x` and hex digits. The macOS
    resolver turns `167772161` and `0x0a000001` into `10.0.0.1`.
  - Only the prefix `/api/` is forwarded, after the path rules. `/api` without the slash stays a
    404, the shape of the Ingress rule. The path and the query go to the target unchanged, under
    its prefix.
  - `httputil.ReverseProxy` with `Rewrite`: the hop by hop headers and every inbound `Forwarded`,
    `X-Forwarded-For`, `-Host`, `-Proto`, `X-Real-Ip` and `Via` are dropped, and
    `X-Forwarded-For` is set from `Config.ClientIP` alone, so a client cannot claim an address.
    The browser's cookies and `Authorization` go along: they are the API's own, the frontend sets
    none.
  - A request body over one MiB (`DevAPIProxyMaxBody`) answers 413. The dial has five seconds
    and the response header thirty, and the environment's proxy settings are ignored. A failure
    answers one opaque 502 and logs the error type only.
  - The answer passes the frontend's chain: the frontend's security headers replace the API's
    copies, so none is sent twice, the access log names the pattern `/api/`, and no answer under
    `/api/` reaches the page view counter.
  - A compose stack:

    ```yaml
    services:
      api:
        image: ghcr.io/example/site-api:dev
      frontend:
        image: ghcr.io/example/site-frontend:dev
        environment:
          DEVELOPMENT: "true"
          DEV_API_PROXY: http://api:8080
          ALLOW_PLAIN_HTTP: "true"
        ports: ["8080:8080"]
    ```

  - Cookies on `localhost` are shared by every port, so another app on the same machine can see
    them. That is the development machine's concern, the reason the option exists nowhere else.
- What it does not do: the API server of a site and per request rendering.

### Favicons are a build requirement

- Every site needs a favicon, and a site that forgets one ships the browser's default icon
  without anyone noticing. So the absence is an error, not a missing nicety.
- The Go package `favicon` fixes three file names in one directory per site, `web/public`: an
  SVG, a 32 pixel PNG fallback and a 180 pixel Apple touch icon. `favicon.Load` fails at startup
  naming the file and the path it expected. A web manifest stays optional and is not generated.
- The links are written by `themeboot.Inject`, through `themeboot.WithFavicon`, in the same step
  as the boot script, right ahead of it. A second injection point would have been a second place
  to forget. The links hold no script, so they need no nonce.
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
- The Umami URL is `https`. Plain `http` is accepted for exactly `localhost`, a loopback address,
  a single label name such as `umami`, and names ending in `.svc` or `.svc.cluster.local`, which
  only a cluster resolves. Anything else over plain HTTP would carry visitor addresses in clear
  text. A single label that reads as a number (`2130706433`, `0x7f000001`) or starts with `xn--`
  is refused, and so is `foo.localhost`: a resolver or a search domain may send those to another
  host. A single label can still resolve through a search domain, so a product that is not in a
  cluster should use `https`. Credentials, a query and a fragment are refused. The website id is
  a UUID. Upper case is accepted and lowered, which is safe because a UUID is case insensitive.
  Whether Umami itself accepts upper case is not verified.
- The wiring is `analytics.New(cfg, Options{...})` and `client.Middleware`. `Middleware` has the
  shape of every other middleware here. It sits directly around the mux, innermost in the outer
  chain, because it reads `r.Pattern` like `metrics` and `AccessLog` do. A product with several
  sites in one process builds one client per site.
- A view is counted when all of these hold:
  - The method is GET. HEAD is not counted, a crawler or a link checker sends it.
  - The response is 200 with a `text/html` body. A 404, a 5xx, a redirect, a 304, JSON, an asset
    and an empty 200 are not pages. A handler that sets no content type gets the type net/http
    would sniff. A panicking handler counts nothing. `Middleware` never counts a 304: it
    carries no body and no content type, so it cannot tell a page from an asset. A server that
    revalidates its pages with an ETag wraps the handler of each page, and only of a page, with
    `NotModified`, which counts a 304 of a GET with every other rule here unchanged. Without it
    a revisit that answers 304 is missing from the numbers. The `frontend` package does this,
    so its pages count the way a server without an ETag counted them.
  - The request has no `DNT: 1` and no `Sec-GPC: 1`. Either one means the visitor is not counted
    at all. Every header line and every comma separated part is read, so `0` then `1` and
    `0, 1` are a signal. `DNT: 0`, a missing header and a value that is not `1`, such as `yes`,
    count.
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
    exclude it. Skip only adds to the built in list. The match is exact, so a route such as
    `/api/healthz` needs `Skip`.
- The URL sent is the route pattern, never the path. `GET /notes/{id}` is sent as `/notes/{id}`:
  the method and a host are cut off, `{$}` is removed and the wildcard names stay. An id, a
  token or a slug therefore never leaves the process, and the statistics group by page instead
  of by record.
- A view with no route pattern is not counted, and one warning, "no route pattern, view not
  counted", is logged once per client. The path is never sent as a fallback: it may hold an id or
  a token. A request has no pattern when no mux answered it (a plain handler, a file server) or
  when a middleware between the analytics middleware and the mux replaced the request with
  `WithContext`, as `RequestID` and the security middleware do. A product registers every page,
  its SPA fallback too, through the mux (`GET /` is reported as `/`), and puts the middleware
  directly around the mux.
- A product that serves static pages outside a mux and knows what it sends sets `Options.URL`,
  which is asked only for a request with no pattern. It must return a fixed path of a page the
  product chose, such as `/de/impressum`. It must never build the answer from the request path,
  the query, a header or a cookie, because whatever it returns leaves the process. A query or
  fragment in the answer is cut off. An empty or relative answer counts nothing.
- The payload is the fields below and nothing else. Each one is in it for a reason Umami needs:
  - `website` and `hostname`: the website id and the product's own public host, from `Options`,
    never from the `Host` header, which a request controls.
  - `url`: the route pattern, as above.
  - `title`: only when `Options.Title` is set. A title can hold user content, so the default is
    none.
  - `referrer`: the origin of another site, scheme, host and port and no path. A path on another
    site can hold a token. A token in a subdomain or a port would still leave, and the package
    cannot tell it from a name. The product's own host, with or without `www`, is no referrer.
    ostendorf.tech sends the path without the query. This package sends less.
  - `language`: the first language tag of `Accept-Language`, if it is a well formed tag.
  - `ip`: the client address, masked by default to its /24 for IPv4 and its /48 for IPv6, before
    it is sent. `Options.FullIP` sends the whole address. Without the field Umami would take the
    address of the server for every visitor, which is why it is passed at all. Umami derives
    country, region and city from it (a GeoLite2 City lookup in its `src/lib/detect.ts`, read
    by the reviewer and not run here), so masking costs some city accuracy and keeps the country
    and usually the region. To our knowledge, not verified here, masking also lowers the count
    of unique visitors: Umami builds its session id from the website, the address, the user
    agent and a salt, so visitors of one /24 with the same user agent merge into one. The address
    is only checked, masked and sent. It is never stored or logged here.
  - `userAgent`: the user agent, which Umami reads the browser and the system from. The request
    to Umami also carries it as its own header, as ostendorf.tech does. The payload's `ip` and
    `userAgent` win over the request's own in Umami (`payload?.ip || getIpAddress(...)`, as
    reviewed). The payload shape was carried over from ostendorf.tech and not checked against
    the Umami documentation again.
  - Not sent: the query string, any cookie, an `Authorization` header, `X-Forwarded-For`, the
    session, an account or a request id.
- The send is off the request path. A view goes into a channel of `UMAMI_QUEUE_SIZE` that two
  worker goroutines read. A full queue drops the view. Nothing in the handler waits for Umami,
  so a slow or dead Umami costs the visitor nothing. One post is bounded by `UMAMI_TIMEOUT`. The
  default HTTP client has no cookie jar, verifies TLS, refuses redirects and takes no proxy from
  `HTTP_PROXY` or `HTTPS_PROXY`, so a visitor address never follows a redirect to another host
  and never passes a proxy nobody configured for it. A product that needs a proxy passes its own
  `Options.HTTPClient`, which must honour the request context.
- A dropped view is counted in `Stats().Dropped` and reported through `Options.OnDrop`. The
  package does not import `metrics`, which would pull Prometheus into a product that only wants
  counting. `metrics.NewCounter` returns the function to pass, as `example/` does with
  `analytics_views_dropped_total`.
- A failure is logged at warn level, once per class (`timeout`, `transport`, `status_3xx`,
  `status_4xx`, `status_5xx`, `queue_full`, `shutdown`, `canceled`, and the rare `encode` and
  `request`) and then at most once a minute with the count since the last line. Warn is a
  deliberate deviation from the low level the issue asked for: a failing statistics backend is
  an operational signal, and the once per class rule keeps it quiet. The line holds the class,
  the count and the Umami host. It never holds the URL, the path, a query, a visitor address or a
  user agent. The error of a post is reduced to its class before it is stored, because
  `net/http` repeats the whole URL in its errors.
- `Close(ctx)` stops accepting views and posts what is queued until `ctx` ends. After that the
  posts in flight are cancelled and counted as failed with class `canceled`, not as dropped, the
  queued rest is counted as dropped, and `Close` returns the context error. `Close` waits for the
  workers, up to one second after the deadline, so `Stats` and the log are final when it returns.
  A second call is safe. A product calls it after the server stopped, with a deadline of a few
  seconds.
- Known limits of the response wrapper: it has no `ReadFrom`, so a handler that copies a file with
  `io.Copy` or `http.ServeContent` loses the sendfile fast path on a counted GET, and a direct
  `w.(http.Hijacker)` assertion fails. `http.ResponseController` reaches `Hijack` and every other
  capability through `Unwrap`. A flush before any body counts only when the handler set a
  content type, because `net/http` sends no sniffed type then.
- What a product must say in its privacy text, as a checklist and not as legal text or legal
  advice. The product's own counsel decides what the law asks of it:
  - Page views are counted on the server, with no script, no cookie and nothing stored on the
    visitor's device. A revisit of a page the browser revalidates counts like the first visit.
  - What is sent to Umami: the page (the route, not the address with its query), the referring
    site (its origin only), the browser language, the user agent and the visitor's IP address,
    shortened to its first three bytes for IPv4 or six for IPv6 unless the product sets
    `FullIP`. Umami derives country, region and city from the address. A product that sets
    `FullIP` says the whole address is sent. What Umami stores of the address is Umami's and the
    product's statement, not this package's.
  - Who runs Umami and where, and that nothing goes to a third party. This depends on the
    product's own Umami and is not a property of this package.
  - A visitor who sends Do Not Track or Global Privacy Control is not counted.
  - How long Umami keeps the data and the legal basis the product relies on. Both are the
    product's statements. This package decides neither.
- Not verified here: Umami's own handling of `ip` and `userAgent`, its hashing and retention, and
  a post against a real Umami. Tests use a fake transport and no network.
- Stays in the product: which routes count, the privacy text, the Umami instance and website id,
  and the retention statement. Out of scope are a client side script, events beyond page views,
  a dashboard and a consent banner.

### Proof of work

- The `pow` package issues and verifies stateless proof of work challenges for a public form,
  first for the contact form series. It keeps the ALTCHA shape so a site that already runs it
  migrates without a break: `challenge = sha256(salt + n)`, `signature = HMAC-SHA-256(key, ...)`,
  and the salt is a 24 digit hex nonce plus `?expires=<unix>&issued=<unix>&`. The browser searches
  `n` in `0..maxnumber`. The package imports nothing outside the standard library and no other
  govue package.
- The HMAC input is `govue/pow\x00v1\x00` + audience + `\x00` + challenge. The domain separator
  is the version, and the audience is the site name. A key reused across sites cannot redeem
  another site's challenge. The expiry and the issue time are inside the salt, the salt is inside
  the challenge hash and the signature covers the hash, so neither can change without breaking
  the signature.
- The salt ends in `&`, and parsing requires it. The hash input is `salt + decimal(n)`, so without
  a boundary a client could move the last digit of `issued` into the number, keep the hash and
  the signature valid and skip the minimum age (CVE-2025-68113, GHSA-6gvq-jcmp-8959, found in
  ALTCHA). The trailing `&` is the explicit boundary: no digit of the salt can move into the
  number, and a salt without it is malformed. The hash stays `sha256(salt + n)`, the ALTCHA shape.
- `pow.New` takes a `Config`. Keys are required and at least 32 bytes each. The first key signs,
  every key verifies, so a rotation lists the new key first and keeps the old one for one TTL.
  Defaults: `maxnumber` 300000, TTL 10 minutes, minimum age 3 seconds. A `MaxNumber` above
  `MaxNumberLimit` (1e9) is refused, and a negative `MinAge` means no minimum age. Lowering
  `MaxNumber` across a restart refuses outstanding challenges with a higher number as malformed.
  `Now` and `Rand` must be safe for concurrent use.
- `Verify` runs in a fixed order and each refusal has its own error: `ErrMalformed`,
  `ErrSignature` (also for another audience), `ErrWrong`, `ErrExpired`, `ErrTooFast`,
  `ErrReplay`, `ErrBusy`. The first three wrap `ErrInvalid`. The signature is compared in
  constant time against every key. The single use record is written last, so a request that fails
  for another reason does not burn the solution.
- Replay is limited by the expiry window plus a single use record. The default record is
  `MemorySeen`, bounded at 100000 entries. A full record refuses with `ErrBusy` and never forgets
  an unexpired entry, because forgetting would allow a replay. It is per process: with several
  replicas a solution can be used once per replica until it expires. A consumer that needs one
  global answer passes its own `Seen`. Statelessness is the owner decision: nothing about an
  issued challenge is stored.
- `MemorySeen` keeps a min-heap by expiry next to its map. Every `Use` first drops each entry
  whose expiry has passed, then checks and records. A `Use` costs amortized O(log n). A single
  `Use` can drop every expired entry, up to the size of the set, while it holds the lock: 100000
  entries that expire together took one `Use` about 34 ms in a review probe, where the old full
  scan took 0.7 ms. An entry stays past its expiry only until the next solution reaches the
  replay check, or until the process ends: on an idle site that can be long. There is no
  background goroutine and nothing to close. The other choices were a sweep goroutine, which
  needs a `Close` that every consumer has to call, and a first in first out queue, which is O(1)
  but lets an entry with a late expiry hold back the ones behind it, so the documentation could
  not say when an entry goes. `ErrBusy` is a global fail closed lockout, because a full set holds
  only unexpired entries and refuses every solution until one expires. `Verify` therefore runs
  behind the consumer's per client rate limit, so one client cannot fill the set.
- Limits. Proof of work is a speed bump: native code solves 300000 hashes in tens of
  milliseconds, so it only slows a naive script. The rate limits of the route and a global
  ceiling are the real control. There is no binding to an address or a session, because mobile
  clients change networks. The challenge route is unauthenticated and costs the server one HMAC.
- Privacy facts a consumer's page can quote: the server stores no challenge and nothing about
  the visitor, the single use record holds one hash of a solved challenge in process memory and
  is not linked to a person, no cookie is set, no browser storage is used and no third party is
  called. With `MemorySeen` the hash goes with the first solution checked after the challenge
  expired, 10 minutes by default, or with the process, whichever comes first.
- `pow/testdata/vectors.json` is the shared contract with the browser solver (#198): a fixed
  key, audience, clock and challenge, hash vectors and verify cases with the expected outcome.
  The Go test reads it, and `go test ./pow -run TestVectors -update` rewrites it.

### Mail

- `mail` is the generic half of a contact form: a message builder and an SMTP submission sender.
  It knows nothing about forms. The contact handlers build on it.
- The builder refuses, it never repairs. Stripping a CR or LF from a subject hides an attack and
  sends a changed mail. Every header value passes one check for control characters, NUL, CR, LF,
  and the Unicode line separators, and the error names the field, never the value, because a
  value is a visitor's name or address and errors reach logs.
- An address is one plain ASCII mailbox, parsed with `net/mail`. A list, a group (also one with a
  single member, which `net/mail` would accept), a quoted local part, a domain literal, a non
  ASCII address and a local part over 64 octets are refused. SMTPUTF8 is not supported. The
  header is rebuilt from the parsed name and address, so a comment in the input never survives.
- A display name is checked after `net/mail` decoded its encoded words, so a word that decodes
  to CR, LF, NUL, ESC or U+2028 is refused like the character itself.
- Unicode format characters (category Cf: bidi controls, zero width characters, the BOM, the soft
  hyphen) are refused in names, the subject and extra headers. They are invisible and let a name
  show something else than it holds. The error names the field.
- `ReplyTo` takes a visitor's address. `From` stays the site's own address on its own domain, so
  the mail passes the sender domain's SPF and DKIM.
- Non ASCII text becomes RFC 2047 `q` words that the package writes itself, because
  `mime.QEncoding` can leave a line over the 998 octet limit. Only letters and digits stay as
  they are. Words are at most 75 bytes and are folded with CRLF and a space, so no line passes 78
  bytes where the header name leaves room, and never 998. The builder refuses a message whose
  line would still pass 998. Encoded text is about three times its size, which the size limits
  allow for.
- A literal `=?` in a subject, a name or an extra header value is encoded too. The other choice,
  refusing it, would reject honest text for no gain, and encoding makes a forged encoded word
  show as the text it is.
- The body is UTF-8 in quoted-printable, which the standard library writes. Base64 is not
  offered: quoted-printable is readable in a raw view, which an operator debugging a mail wants.
- Message-ID is 128 random bits on the sender's domain, Date is UTC. Nothing about the visitor is
  added.
- The sender requires STARTTLS by default. A server that does not offer it gets `ErrNoStartTLS`
  after EHLO and nothing else. `net/smtp`'s own `PlainAuth` exempts localhost, so the package has
  its own PLAIN and LOGIN that need `ServerInfo.TLS`, and checks again before it authenticates.
  `Plain` is a field set in code and never from the environment, and `Validate` refuses it with
  credentials.
- One attempt per `Send`. After the data went out a failure is ambiguous, and a retry could send
  the mail twice. The caller decides. A message is refused before it dials.
- One context with `Config.Timeout` covers the dial and the conversation, so a custom
  `DialContext` is bound too and a `Send` never takes twice the timeout. The connection gets no
  deadline of its own: only the context ends it, by setting a deadline in the past. A failure
  after the context ended always wraps `context.Cause`, so `errors.Is` with
  `context.DeadlineExceeded` or `context.Canceled` holds every time and never depends on which of
  two timers fired first.
- `Config.Validate` takes a bare host name: a colon, a slash, white space and the URL characters
  are refused.
- `LoadConfig` reads `SMTP_HOST`, `SMTP_PORT` (587 by default, 465 means implicit TLS),
  `SMTP_USERNAME` or `SMTP_USERNAME_FILE`, and `SMTP_PASSWORD` or `SMTP_PASSWORD_FILE`. A
  `_FILE` variable names a file mounted from a secret store, and its content is trimmed of
  surrounding white space like the direct value. The user name is not a secret, but it sits next
  to the password in the same secret, so it has the same twin. A variable and its twin together,
  an empty file and an unreadable file are startup errors that name the variables, never a value
  or a path.
- `Error` splits the reply from the text. `Error()` holds the stage and the code. `Reply` holds
  the server's text for the log, with the password removed, also its base64 form and the base64
  AUTH PLAIN string a server may echo. It can echo an address, so it never goes to a client.
- Tests run the dialogue over `net.Pipe` with a fake server and a test certificate, through the
  `DialContext` field. AGENTS.md says no socket, so none is opened.
- Stays in the consumer: the recipient, the sender address, the subject and body text, the rate
  limits, and the privacy text. The package doc lists what a privacy text can quote.
- Out of scope: attachments, HTML bodies, DKIM signing, a queue and retries.

### Contact form

- `contact` joins `pow` and `mail` into two handlers: `Challenge` issues a signed challenge and
  `Submit` validates a submission and sends one plain text mail. `Normalize` and `Validate` are
  usable on their own. The package is stateless by the owner's decision. It sets no cookie, reads
  no session, uses no CSRF token and imports no `session` package.
- Protection of `Submit`, in the order it runs:
  1. Method POST and the origin check, `csrf.Protector` with no token half (Sec-Fetch-Site and
     Origin). A cross site browser POST is refused with 403 `cross_origin`. A client that sends
     neither header is a script, not a browser, and passes: the other layers are the control.
  2. 503 `not_configured` when keys, sender or envelope are missing.
  3. `Content-Type: application/json` is required. `api.DecodeLimit` alone accepts a missing
     header, so the handler checks first. The body cap is 64 KiB, unknown fields and a second
     JSON value are refused. A decode failure answers one fixed text and never echoes a field.
  4. The honeypot field `website`: a hidden field that a person never fills. A filled value is
     treated as a bot and dropped.
  5. The per client limit, 5 per 10 minutes, keyed by `clientip` with trusted proxies only.
     Every attempt counts, a refused one included.
  6. Validation with stable codes. Every failing field is reported as `{field: code}` under
     `details`, and a value is never echoed. With `RequireConsent` a missing or false consent
     box is `consent: required` here.
  7. Proof of work through `pow`. It runs after validation, so a typo does not burn the solution.
     The challenge is signed, bound to the site name, expires and is used once. The codes are
     `pow_required`, `pow_invalid`, `pow_expired`, `pow_too_fast`, `pow_replay` and `pow_busy`.
  8. The global mail ceiling, 50 per hour for the whole process. A token is taken before the
     send and never given back, so a failing mail server cannot be used to retry without limit.
     A refusal answers 503 `mail_limit` and logs one warning per minute, with no content. See
     the lockout note below.
  9. Build and send with a timeout that survives a visitor who leaves. A failure answers 503
     `send_failed`. The log holds the stage and the code of a `mail.Error`, never its `Reply`,
     because a reply can echo an address, and only the type name of any other error.
- The `Challenge` route is limited per client, 10 per 20 seconds, through
  `middleware.RateLimit`. It costs the server one HMAC and stores nothing.
- Mail: From and To come from the configuration, never from a request. The visitor's address is
  only in Reply-To, as a bare address. The name goes into the body, not into a header. The mail
  carries `Auto-Submitted: auto-generated`, no client address and no user agent. The subject and
  body are functions of the site. The defaults use a fixed subject without the name.
- Consent. `Request.Consent` is the state of the form's consent box as the client sent it, a
  missing key is false. `Request.Received` is the server's clock when `Submit` decoded the
  request, without a monotonic reading. It has no JSON name, so a body that carries it is
  refused as an unknown field. `Config.RequireConsent` refuses a missing or false box with the
  field error `consent: required`, next to the other field errors and before the proof of work,
  so the solution is not spent. It is off by default, and then a submission passes with or
  without the box. `GovueContactForm` sends `consent` only with `requireConsent`, so a site sets
  both together. A `contact` handler older than this change refuses the unknown key with 400
  `invalid_argument`, so `requireConsent` needs the Go handler of the same release: the Go
  module and the Vue package share one version.
- The consent field records a claim, never a proof. A modified client sends `true` without
  showing a box, and nothing on the server can tell. The owner wants the record for a clean
  trail of what the form sent, not as evidence against a tampering client. govue writes no
  consent text and no legal wording into the mail: the default body is unchanged, and
  `DefaultBody` is exported so a site's `Body` adds one line of its own wording from
  `Consent` and `Received`. The example does that with a placeholder line. The consent text the
  visitor sees is the site's as well, see the component.
- Header injection: a CR, LF or other control character in the name or the address is a
  validation error with `invalid_chars`, never repaired. The address is checked by the mail
  builder itself, so a value that passes cannot be refused at send time. Whatever a site puts in
  a subject passes the same refusal checks in `mail`.
- Limits count runes, not bytes: name 200, address 254, message 5000. Format characters are
  refused like in `mail`, except the zero width joiners. The message keeps LF and tab.
- Answers carry codes. The text next to a code is a fixed English sentence for a log reader, and
  a site translates by code in its own frontend. There is no message catalogue in Go.
- Logs: a line holds a code, a status and, for a send failure, a cause that is safe by
  construction: the stage and code of a `mail.Error`, the fixed text of a `mail` sentinel, and
  for any other error the Go type name only, because a custom `Sender` can put the visitor's
  address into its error. The stage is one of the stage names of `mail` and anything else logs
  as `unknown`. The package never calls `Error` on a send error and never reads text out of one
  through `errors.As`. The tests drive the paths below with distinctive values and search
  the captured output: success, the origin, media type and decode refusals, validation, no
  proof of work, replay, the honeypot, the client limit, the ceiling, a send failure with a
  `mail.Error`, with a custom error that holds the address, with an error whose `As` writes
  text, with a typed nil `*mail.Error` and with a plain error, and a panicking site function or
  `Sender`. Not every refusal code has its own log assertion. A site's own `Subject`, `Body` and
  `Sender` must not log visitor input.
- Site functions and the `Sender` run after the ceiling token. They must not panic and must not
  put visitor input unchecked into a header or into an error text. A panic of any of them is
  recovered inside the handler, logged by the type of its value only and answered 503
  `send_failed`, because the value can hold visitor content and the `Recover` middleware would
  log it. `http.ErrAbortHandler` is re-panicked unchanged, as `middleware.Recover` does.
- Blank values: a value of only white space, format characters, the zero width joiners, combining
  marks or variation selectors with no base rune, or the filler characters that render blank
  (U+3164, U+115F, U+1160, U+FFA0, U+2800, U+17B4, U+17B5, U+034F) counts as empty and answers
  `required`, in all three fields. A joiner between letters (Persian ZWNJ, Hindi ZWJ), an emoji
  sequence and a mark on a base stay accepted.
- Per process. The client limit, the mail ceiling, the challenge limit and the replay set of
  `pow` live in memory of one process. With several replicas every budget multiplies by the
  replica count, and a solution can be used once per replica until it expires. A site that needs
  one global answer passes its own limiters or its own `pow.Seen`. Every limiter is bounded at
  100000 keys. A full limiter evicts an arbitrary active bucket, which fails open for that key
  as `ratelimit` documents, and one IPv6 /48 holds 65536 /64 keys that can fill it.
- Lockout by a few clients. The client limit allows 30 submissions per hour per key and the
  ceiling is 50 per hour. Only a verified proof of work takes a ceiling token, so keeping the
  form refused costs an attacker 50 valid solves per hour. Four IPv4 addresses or one IPv6 /62
  are enough, because the key is the /64. The default is a fail closed choice for the mailbox:
  it protects the owner from a flood and gives a few clients the power to switch the form off.
  A site can lower `Limiter`, raise `MailLimiter` and watch the throttled warning log line
  `global mail ceiling reached`, which is the signal. A token burns also when a site function
  panics or the builder refuses the message, because both run after the ceiling.
- Not configured: `New` accepts a missing `PoW` or `Sender`, and both routes answer 503
  `not_configured`, so a site boots without keys or SMTP. A bad envelope or trusted origin is a
  startup error.
- Stays per site: the recipient and sender addresses, the subject and body text, the privacy
  text, the frontend and its worker (#198) and the translation of the codes.
- `example/` wires the handlers outside the session and CSRF chain with a fake sender that keeps
  the last message in memory and opens no socket. It sets `RequireConsent`, because its contact
  page shows the box, and a `Body` that adds a placeholder consent line with the receive time.
  `CONTACT_POW_KEY` or `CONTACT_POW_KEY_FILE`, `CONTACT_FROM` and `CONTACT_TO` configure it.
  The routes have a CORS handler of their own, because `TrustedOrigins` comes from
  `ALLOWED_ORIGINS`: a cross origin page that may send mail can also read the answer and does
  not retry into a duplicate mail. It lists the web origins
  only, serves GET and POST, sends no `Access-Control-Allow-Credentials`, exposes `Retry-After`
  through `ExposeHeaders` so a page reads the wait of a 429, and has explicit
  OPTIONS routes, so a preflight is answered by it and not by the catch-all. A page of another
  origin gets a 403 preflight, no CORS headers and 403 `cross_origin` on the POST.
- Timeouts: the send is not cancelled when the visitor leaves, and `SendTimeout` defaults to 10
  seconds. `httpserver` has a read timeout of 15 seconds, a write timeout of 30 seconds and a
  shutdown timeout of 20 seconds by default. A slow send can finish after the answer can no
  longer be written, and a send in flight at shutdown can be cut off. A site keeps the read
  timeout plus `SendTimeout` below the write timeout and `SendTimeout` below the shutdown
  timeout. The example sets 10 seconds as well, and a test pins the value and the sums.
- Privacy facts, for the privacy text of a site. The package persists nothing. In process memory
  it holds the limiter keys, which are the IPv4 address or the IPv6 /64 of a client, up to 100000
  keys, kept until the bucket refills, and the replay set of the proof of work, one hash per
  solved challenge, dropped by the first solution checked after it expired or with the process.
  The visitor's name, address and message, and with a site's `Body` the consent state and the
  receive time, leave the process only through the mail. A site's privacy text needs to say so.
  This is a statement of fact, not legal advice.
- Deploy: behind a reverse proxy, `TRUSTED_PROXIES` (or the site's own `clientip.Resolver` in
  `Config.ClientIP`) must name the proxy. Without it every visitor shares the proxy's address and
  one bucket, which fails closed: 5 submissions per 10 minutes for the whole site.
- Out of scope: a session or CSRF token variant and content scoring of messages.

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
- `database/sql` with `lib/pq` or pgx' `stdlib` adapter for the database: the standard interface.
  Dropped because a product still needs a driver, `lib/pq` is in maintenance mode, and the
  products query with pgx' own pool and types.
- A connection string in one variable, `DATABASE_URL`: one setting instead of eight. Dropped
  because the password would travel inside a URL that gets logged, and it could not have a
  `_FILE` twin of its own.
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
- Whether `AppCORS` sends `Access-Control-Allow-Private-Network`. It is not sent. The owner
  decides when a product needs it. `Access-Control-Expose-Headers` is a setting, see "App
  origins".
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
- Whether `database` offers a way to mark the schema applied for a product that migrates with
  its own tool, such as a Kubernetes job. Today `Check` stays unready until `Migrate` succeeded in
  the same process. The owner decides when a product needs it.
- Whether `database` refuses to start when `schema_migrations` records a version newer than the
  embedded files, a rollback to an older image. Today it applies nothing and reports ready.
  The owner decides.
- The passkey user handle. Decided: 32 random bytes per account, see "Passkeys" above.
- A native Android app's passkey origin, `android:apk-key-hash:...`. `passkey.Config` refuses
  every origin that is not https today. The library keeps such opaque origins in a field of
  their own, `RPOpaqueOrigins`, matched by exact string comparison and checked against the
  prefixes it knows, `android:apk-key-hash:` among them. Next step: decide when the first
  Android app needs it, probably as a separate `Config` field that feeds `RPOpaqueOrigins`.
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
