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

- The Go side is the standard library. `prometheus/client_golang` is the one exception, confined
  to the `metrics` package, because the exposition format is not worth reimplementing.
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
  rate limiter, then the CSRF protector, then the application handler. The rate limiter's 429 and
  the CSRF protector's 403 answer on the exact route they refuse: each wraps one specific
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

### Session in context lives in `session`, not in `middleware`

- `Manager.Middleware` reads the session once per request and puts the outcome in the request
  context, where `FromContext` and `ErrorFromContext` find it. Reading per handler means a store
  round trip per read, and a read can write the last use back.
- It is in `session` because `middleware` is the package every application imports for the request
  id and the access log. Putting it there would make all of them pull a session store in with it,
  which is the coupling the `clientip` rule exists to prevent. `session` already imports
  `net/http`, and nothing in the module imports `session`, so there is no cycle either way.
- `csrf` is unaffected and still imports nothing from `session`: it is the application's
  `csrf.Expected` closure that reads the session.
- The middleware refuses nothing and writes no response. It is not an authorisation gate: a public
  route behind the same chain must not start answering 503 because a cookie could not be read, and
  nothing is lost by that, because `FromContext` reports false for a store that could not answer.
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

### Component behaviour

- Where behaviour is genuinely hard, a modal's focus trap, a dropdown, a combobox, adopting a
  maintained headless primitives library beats writing a worse one. Reka UI, the Vue port of
  Radix, is the leading candidate and is evaluated in its own issue, styled with these tokens.
- Nothing is wired up for this yet. The decision belongs to the issue that needs the first such
  component.

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
- Publishing the Vue package to npm: the obvious distribution path. Dropped for now because no
  registry is set up for this organisation, and a git dependency pinned to a tag needs no publish
  step and no token.

## Open questions

- A shared session store. The in-memory store is correct for one replica only, and the `session`
  issue settled that nothing else ships here for now: `session.Store` is four methods an
  application implements against whatever it already runs. Which backend would ship here, if any
  ever does, stays open until a second consumer exists and the two agree on one. Next step: the
  first application that needs more than one replica writes its own and reports what the
  interface made awkward.
- A theme generator. Done: `web/src/theme/` derives a full accessible light and dark role palette
  from a handful of seed colours in OKLCH, re-verified against the same `contrast.ts` the shipped
  themes are checked with, and `web/scripts/generate-theme.ts` is the CLI entry point. The
  generator's code is public here; a consumer's seed configuration and the CSS it produces stay in
  the consumer's own repository, as before.
- End-to-end tests. jsdom cannot do focus trapping, an inert background or a real Escape on a
  modal. Whether a browser runner is worth it here, or belongs to the consuming application, is
  open until the first component with real focus behaviour lands.
- Whether `api`'s error shape is the right contract for consumers that already have one of their
  own. Next step: revisit when a second consumer appears.
- What the first version supports for configuration beyond the environment. Nothing reads the
  environment by itself today, and whether that stays absolute is open.

## Related ADRs

- None. The decisions above are recorded here on purpose. One gets its own ADR when it turns out
  to be contested and expensive to reverse, not before.
