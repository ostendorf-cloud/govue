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
  enrolment, and `SetPasswordHash` serves a password change. It has no method for a provider
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
- The conformance suite in `account/accounttest`, planned with the login logic, checks every
  guard an adapter has to hold:
  - the conditional counter write, and that `AdvanceTOTPCounter` refuses a counter of zero
  - that `ConfirmTOTP` refuses an account without a secret, a counter at or below zero and a
    secret that is not the stored one
  - the unconfirmed guard of `ReplaceTOTPSecret`
  - an ignored caller supplied id and counter on `Create`
  - that a disabled or pending account cannot be found for login
- Password and TOTP are checked in one login call, as before. A two step login needs somewhere
  to keep a half authenticated state, which is a second session to design and expire.
- Every refused login answers `ErrInvalidCredentials`, and a login against an unknown account
  pays the same hashing cost as a wrong password, so neither the answer nor the time tells which
  part of a guess was right.
- Passkeys are meant to follow in a separate `passkey` package with its own store, beside TOTP
  rather than replacing it. Losing one device must not lock somebody out of their records.
- Recovery codes are not part of the first version. Losing both the TOTP device and every
  passkey then locks the account, and the documentation has to say so.

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
  canonical URL would redirect. govue ships no directory handler. A site, and the example, serve
  the exact path of every route (`GET /de/impressum` and `GET /de/{$}`) and answer the slash
  form with 404 or a redirect. The example step adds a test that requests every `loc` of the
  sitemap through the real mux and expects 200 with no `Location` header. The prerender step
  writes files only and has no mux to test. A review run of the Go `ServeMux` answered `/de`
  against a `GET /de/{$}` pattern with a 307, which that test pins too.
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
- Whether `account` folds the case of a login or leaves it to the store's uniqueness rule. Next
  step: decide with the login logic.
- The passkey user handle: the opaque account id or a random handle per account. Next step:
  decide in the passkey work with its security review.
- What the first version supports for configuration beyond the environment. Nothing reads the
  environment by itself today, and whether that stays absolute is open.

## Related ADRs

- [0001 Device token sessions for native apps](../adr/0001-device-token-sessions.md): the
  bearer token a native app signs in with, and the CSRF boundary it draws.
- The other decisions above are recorded here on purpose. One gets its own ADR when it turns out
  to be contested and expensive to reverse, not before.
