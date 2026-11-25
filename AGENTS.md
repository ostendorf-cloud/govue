# govue

A library for Go plus Vue web applications: a Go module with the HTTP server layer and a Vue
package with the components and the design tokens. Public, and consumed by applications outside
this organisation, so every exported name is a contract. The baseline rules apply and are not
repeated here.

## Layout

- Every package at the module root is public API. A rename or a changed signature is a breaking
  change for every consumer and needs a reason in the pull request.
- There is no `cmd/`. This repository ships no binary.
- `example/` is a `package main` example wiring every Go package together, compiled by
  `task example:build`, and the source of truth for the middleware chain order. It is not a
  shipped binary.
- `internal/` is for anything a consumer must not import.
- `web/` is the Vue package, consumed as a pnpm git dependency pinned to a release tag.
- `docs/concepts/govue.md` holds the design and the reasoning behind it. Read it before adding
  a package.
- `web/` holds the token contract, the example themes, the `useTheme` composable, the API
  client (`createClient`, exported from `web/src/index.ts`), the overlay and feedback components
  (`GovueDialog`, `GovueConfirmDialog`, `GovueToastHost`, `GovueEmptyState`, `GovueErrorState`)
  with `useToasts`, the form input components (`GovueField`, `GovueInput`, `GovueSelect`,
  `GovueCheckbox`, `GovueSwitch`, `GovueTextarea`, `GovueButton`) with `useForm`, and the layout
  components (`GovuePage`, `GovueHeader`, `GovueFooter`, `GovueNav`, `GovueSkipLink`,
  `GovueCard`, `GovueBadge`, `GovueSkeleton`), `GovueThemeSwitch` (with a `compact` icon
  variant of about 142px, named by mode, same radio group, and a `cycle` button of 44px that
  steps through `useTheme`'s `cycleMode`, named by the current and the next mode; the markup of
  the default and the compact variant is pinned by a test), and the account forms
  (`GovueLoginForm`, `GovueSignupForm`, `GovueTotpEnrolment`), `GovueContactForm` with its proof
  of work solver (`lib/pow.ts`, `lib/pow.worker.ts`), which take a `client` from
  `createClient`, and `GovuePasskeyButton` with the `usePasskey` composable and `isBearerClient`,
  which take it too. `GovuePasskeyButton` and `GovueContactForm` refuse a bearer client: the
  button is not offered, the form renders nothing, so a device token never reaches the contact
  routes. `ApiError.retryAfter` carries a valid `Retry-After` in whole seconds (at most one day,
  digits only), and the contact form names the wait in its 429, `mail_limit` and `pow_busy`
  texts. `GovueWaitlistForm` is the form of the `optin` request route on the same solver: the
  consent box is always required and its text always the site's, the consent version stays on
  the server and is never sent, and the submit button stays disabled until mount, with a
  `noscript` text. "Waitlist form" in the concept doc is the spec.
  `createClient({ auth: { mode: 'bearer' } })` is
  the client of a native app with a device token: `TokenStorage` (async `get`, `set`, `clear`,
  one opaque string) with the in memory default and no persistent adapter, rotation in
  `web/src/api/bearer.ts`, `signIn` and `signOut`.
  A token never goes into a URL, an error, a cause or a log, and the storage is written before
  the new token is used. All storage access runs through one lock, and a rotation writes only
  while the storage still holds the token it rotated. Exactly one context per storage rotates
  (`auth.rotation: 'owner'`), every other one is `'none'`. A client with its own `auth.storage`
  must say which, with no default, and `'none'` needs a storage. The types and `createClient`
  refuse anything else.
- `GovueHeader` takes its ground from the tokens `--color-header-bg`, `--color-header-border` and
  `--header-backdrop`, set in the theme file and filled by every theme and the generator. The prop
  `overlay` takes it out of the flow, transparent over a dark hero until `useScrolled` reports a
  scroll, with `data-state` and `data-overlay` as the hooks, and `navPlacement` is `start`
  (default), `center` or `end`. An open `GovueNav` menu keeps it solid. `NavItem.variant: 'cta'`
  is the call to action. The contrast test covers the header ground and a translucent one: keep
  the alpha at 0.95 or more. `GovueLanguageSwitch` renders links with `linkFor` and hands a
  click to the router through `navigate`, and `othersOnly` leaves the current language out.
  `web/e2e/header-width.spec.ts` measures the narrow header at 360, 480 and 640px in Chromium,
  on its own harness page with the real Tailwind classes, and the open menu at 360, 480, 560
  and 639px. Between 480 and 639px the open list hangs from the header element below the bar,
  so nothing between the header and the list may be positioned. "The header and the
  navigation" is the spec.
- `web/src/i18n/` holds the language layer: the `createGovueI18n` plugin, the German and
  English catalogues and `catalogues.ts`, the one list of languages. `GovueLanguageSwitch`
  lives in `web/src/components/` with the other components.
  A visitor-facing default text goes into both catalogues under `govue.`, never into a
  component as a string. A text prop stays as an override.
  The legal pages (`language.HandleLegal`, `GovueLegalPage`, `govue-check-legal`) are structure
  only: govue never ships an imprint, privacy or terms text. The same holds for the consent text
  of `GovueContactForm`: there is no catalogue default. With `requireConsent` the site sets
  `consentLabel` or fills the `consent` slot, and without one the form renders nothing and warns
  in development. With `requireConsent` the request carries `consent`, the state of the box, and
  without it no `consent` key. German has a formal catalogue, `deFormal.ts`, with the keys of
  `de.ts`. `createGovueI18n({ formality: 'formal' })` loads it under the locale `de`, so it is
  never a second language in `catalogues.ts` or the switch. A test fails on a missing key in
  either and on "du" in the formal one.
- `web/example/` is the example site: the placeholder legal pages, the contact page, and the
  start and hero pages that show the solid and the overlay header. Its shared header has the
  cycle theme button and the other language link below 1024px, the compact theme switch and
  every language link from there, and the nav at the end with a call to action. It is
  prerendered and checked by its own tests, the consumer build test of
  the worker bundle and the fake of the contact handlers that the component tests use, which
  knows `consent` and `requireConsent`, with the fake of the optin request route built on it.
  It is not shipped. The Go example requires the consent
  box like its contact page shows it, and its mail body adds a placeholder consent line with
  the receive time.
- `web/` also holds a theme generator (`web/src/theme/`, run through
  `web/scripts/generate-theme.ts`) that derives an accessible theme from a consumer's own seed
  colours, never committed here.
- Twenty-five Go packages live at the module root: `account`, `analytics`, `api`, `appassoc`,
  `clientip`, `contact`, `csrf`, `database`, `envconf`, `favicon`, `frontend`, `health`,
  `httpserver`, `language`, `logging`, `mail`, `metrics`, `middleware`, `oidc`, `optin`,
  `passkey`, `pow`, `ratelimit`, `session`, `themeboot`.
- `frontend` is the frontend image of a site that splits frontend and API: `New` reads the
  Vite build with the prerender output into memory and refuses a build without a 404 page or a
  legal page per language, `Run` serves it with the admin listener. Every path that answers 200
  is a key of that table. A non canonical path (encoded slash, backslash, dot, percent or NUL,
  `//`, a dot segment or dotfile) answers 404 before routing. Pages carry the theme boot script
  through `themeboot.InjectStatic`, and `script-src` carries `themeboot.Hash`, both with
  `Config.ThemeBoot`. `themeboot.WithDefaultMode` must equal the `mode` of `useTheme`, and
  `themeboot/testdata/boot-<mode>.js` is shared with the Vitest test. It has no
  session, no cookie and no API route. `Config.Redirects` answers exact old paths with a 301 to
  a page of the sitemap, and refuses a source that is already a route or fails the path rules.
  `Config.ThemeColorDark` writes one `theme-color` tag per mode through
  `favicon.Icons.WithDarkThemeColor`, ahead of the boot script, which pins both tags to a
  stored mode, so the hash never depends on a colour. `Config.Analytics` counts page views:
  `Middleware` around the mux and `NotModified` on each page handler, so a 200 or a 304 of a
  page counts by its pattern and nothing else counts. The frontend registers
  `analytics_views_dropped_total` on its own metrics, and `Run` closes the client.
  `Config.DevAPIProxy` forwards `/api/` to a loopback or single label host for a compose stack,
  only with `Config.Development`, which the package never reads from the environment. Without
  both `/api/` stays a 404. `X-Forwarded-For` comes from `Config.ClientIP` alone.
  `Config.App` is app mode for a single page app: `index.html` with `no-cache` and an ETag for
  every `GET` and `HEAD` that is no file, `/api/` and `AppConfig.ReservedPrefixes` answer the
  JSON 404 of `api.NotFoundHandler`, an unknown asset and a failed path rule a plain 404, and
  no sitemap, 404 page or legal page. The favicon set and the boot script are optional there,
  and `Language`, `Redirects` and `Analytics` are refused. `internal/htmlhead` holds the head
  tag scan that `themeboot` and app mode share.
  "Frontend server" in the concept doc is the spec.
- `pow` issues and verifies stateless proof of work challenges, the ALTCHA shape, signed with
  HMAC-SHA-256 over a domain separator and the site name. Keys are at least 32 bytes, the first
  signs and all verify. Replay is limited by the expiry plus a single use `Seen`, by default a
  bounded in memory set that is per process and refuses with `ErrBusy` rather than forget. Every
  `Use` of it first drops the expired entries through a min-heap by expiry, amortized O(log n),
  with no goroutine, so an entry outlives its expiry only until the next checked solution. A
  single `Use` can drop every expired entry while it holds the lock.
  Proof of work is a speed bump, the rate limits are the control. It sets no cookie and stores
  nothing about a visitor. `pow/testdata/vectors.json` is shared with the Vue solver. It imports
  no other govue package.
- `mail` sends plain text mail over SMTP submission and builds the message. `Message` is refused,
  never repaired: CR, LF, NUL and any control character in a header value or address is an
  error, an address is one plain ASCII mailbox (no group, local part up to 64 octets), Unicode
  format characters (Cf) except ZWNJ and ZWJ are refused in names, subject and headers, non ASCII
  subjects and names and a literal `=?` become folded RFC 2047 words with no line over 998, sizes
  are limited. `SMTP` requires STARTTLS by default (`ErrNoStartTLS` before any credential
  leaves), `ImplicitTLS` is an option, `Plain` is for a local test sink and refuses
  credentials, AUTH PLAIN or LOGIN runs inside TLS only, TLS is 1.2 or newer, there are no
  retries, `Config.Timeout` and the context are one deadline over dial and conversation, and
  `Sender` is the interface consumers fake. `LoadConfig` reads `SMTP_HOST`, `SMTP_PORT`,
  `SMTP_USERNAME` or `SMTP_USERNAME_FILE` and `SMTP_PASSWORD` or `SMTP_PASSWORD_FILE`, a
  variable and its twin together are a startup error, the password is a `mail.Secret`, and the
  environment never selects `Plain`. `*mail.Error` carries the reply for a
  log, its `Error()` text never holds an address or the password. It imports `envconf` only.
  Its tests run the SMTP dialogue over `net.Pipe`, through `Config.DialContext`, with no socket.
- `contact` is the stateless contact form: `Normalize` and `Validate` with the stable field codes
  `required`, `too_long`, `invalid_chars`, `non_ascii_email` and `invalid` (runes, not bytes), and
  `Handler` with `Challenge` and `Submit`. It builds on `pow`, `mail`, `csrf` (origin half only),
  `ratelimit`, `middleware.RateLimit`, `api` and `clientip`, and never imports `session`: no
  cookie, no session, no CSRF token. `Submit` runs the origin check, a strict
  `Content-Type: application/json`, a 64 KiB body cap with unknown fields refused, the honeypot
  (the same 200 as a success), the per client limit, validation, proof of work, the global mail
  ceiling (never refunded) and the send, in that order. From and To are fixed by the site, the
  visitor's address is only in Reply-To, and the mail holds no client address and no user agent.
  `Request.Consent` is the consent box as the client sent it, a claim and never a proof, and
  `Request.Received` the server time at decode, which a body cannot set. `RequireConsent` refuses
  a missing or false box as the field error `consent: required`, off by default. govue writes no
  consent text into the mail: a site's `Body` adds a line, starting from `DefaultBody`.
  `DefaultBody` labels its lines from `Request.Language`, a hint the form sends from the page
  locale: `LabelsFor` has `en` and `de`, anything else is English, `Config.Labels` overrides.
  Missing keys or a missing sender answer 503 `not_configured` and the process still starts. A
  log line holds codes, and for a send failure the allowed stage and code of a `mail.Error` or
  the Go type name of another error, never a name, an address or message text. A panic of a
  site function or of the `Sender` is recovered and logged by type. Tests search the log of the
  main paths, not of every refusal. The limits and the replay set are per process, so
  several replicas multiply the budgets, and a few valid clients can use up the mail ceiling.
- `optin` is the waitlist with double opt-in: `Challenge`, `Request`, `Confirm` and `Unsubscribe`
  handlers, `Purge` and `RunPurge`, `DeleteConfirmedBefore`, `MailConfirmed` and `Wait`. It
  builds on the same packages as `contact` and never imports `session` or `database`. `Request`
  answers the same 200 for every address, takes the global ceiling before the state is known,
  runs the same two store updates for every address and sends the confirmation mail after the
  answer. The send limit is a counter per mailbox (lower case, `+tag` removed, dots not folded),
  the entry stays per full address. Every mail leaves through one function that takes a
  `recipient`, which only a pending entry (confirmation) or a confirmed entry (mailing) yields,
  and carries the unsubscribe link and `List-Unsubscribe` with `List-Unsubscribe-Post`.
  Confirmation tokens are random and stored as a hash, unsubscribe tokens are an HMAC of the
  entry key and not stored. An unsubscribe deletes the entry at once and keeps nothing, the
  owner's decision. A panic in the mail goroutine is recovered, `http.ErrAbortHandler`
  included. The `Store` interface has six methods and `MemoryStore`, and govue ships no SQL
  store: the package doc has the schema and the transaction. No IP address is stored. The site
  writes every mail text and sets the retention of confirmed entries. "Waitlist with double
  opt-in" in the concept doc is the spec.
- `database` holds the PostgreSQL connection of a product: `Config` and `LoadConfig` with the
  `DB_*` variables, the pgx pool, the migration runner over an `fs.FS` and `Check` for
  readiness. The password is a `database.Secret`, held in a closure, so no fmt verb, encoder,
  slog handler or reflection walk prints it, also through an unexported field or a pointer.
  `==` and `reflect.DeepEqual` therefore do not compare a `Config` with a password. The default
  `sslmode` is `verify-full` with `DB_SSLROOTCERT`, a PEM file or `system`. The product keeps its
  schema and its queries. The runner stores a SHA-256 of each applied file's bytes and refuses a
  changed file, `Check` reports it, a schema newer than the code is left alone, and only the code
  option `AllowChangedMigrations` overrides the refusal, never an environment variable.
  `database/dbtest` gives a test a fresh database on the server named by
  `GOVUE_TEST_DATABASE_URL`, start one with `docker compose -f example/compose.yaml up -d postgres`.
- `analytics` counts page views on the server and posts them to Umami. It is off unless
  `UMAMI_URL` and `UMAMI_WEBSITE_ID` are both set, and one of them alone is a startup error. It
  sets no cookie, honours Do Not Track and Global Privacy Control, and never sends a query
  string or an id: the URL is the route pattern, and a view with no pattern is not counted. The
  client address is masked to /24 or /48 unless `Options.FullIP` is set. `NotModified` counts
  the 304 of a page revalidated with an ETag and wraps page handlers only. It imports `clientip`
  only. A change to what
  the payload holds is a change to every product's privacy text and needs a reason in the pull
  request and an update of the checklist in the concept doc.
- `appassoc` serves the Apple App Site Association and Android `assetlinks.json` files from a
  typed `Config`. The files hold public identifiers only, never a secret, a token or a value
  from a request. They are exact patterns, never redirect, never set a cookie and never need a
  session, so a product mounts them outside any middleware that does. A file with no entries is
  not mounted, never served empty.
- `account` holds local accounts against the consumer's own tables: the contract, Argon2id
  password hashing, TOTP and `Service` with signup, TOTP confirmation, login and password
  change, and `Handlers`, the JSON handlers over it. The handlers take callbacks, check the
  method, the origin and a strict body themselves, and answer every failed login the same way.
  `account/accounttest` holds the conformance suite every adapter runs and an in memory
  store. `Store.SetPasswordHash` is a compare and set on the old hash. Its `Store` interface
  carries only the calls its logic makes for login, TOTP enrolment and password change. A new
  method needs a reason in the pull request, because what the interface lacks is what the
  package cannot reach. `KnownDevices` is optional and off by default: a signed device cookie
  that lets a known browser skip the consumer's per login `Ceiling`. It needs no store method,
  the HMAC covers the password hash. It is a cookie, so a change to what it holds is a change to
  every consumer's privacy text.
- `passkey` holds WebAuthn passkeys next to the password and TOTP login: `Config`, the five
  method `Store`, the single use `ChallengeStore` with a bounded in memory default, and the JSON
  handlers. It imports `account` for `Identity` only and never `session`. Its `StartSession` has
  the signature of the account one. The RP id and the origins are required configuration and
  never come from a request. A finish clears the challenge cookie and uses the challenge up,
  whatever the outcome, and every refused login has one answer. Passkeys work with the cookie
  client only. `passkey/passkeytest` holds the conformance suite, an in memory store and
  `Authenticator`, a software passkey for tests.
- `session/devicehttp` holds the JSON handlers of the device token sessions of native apps:
  issue, rotate, list, revoke and sign out of the calling device. It is a sub package so that
  `session` alone does not pull `api` in. It imports `session` and never `account`, and the
  credential check is a callback. The issue route needs a per client rate limit, like the account
  routes that check a password. ADR 0001 is the spec.
- `middleware.AppOrigins` is the typed origin list of native apps and `middleware.AppCORS` their
  CORS handler, which takes the web `CORS`, replaces it in the chain and refuses an overlap. An
  app origin never goes into `CORSConfig`, `csrf.Protector.TrustOrigin` or any web list, and a
  product calls `AppOrigins.RejectOverlap` at startup with its trusted origins, as `example/`
  does. The issue route trusts the app origins through `csrf.Protector.TrustOriginOnRoute`.
  `NewAppCORSWithConfig` with `AppCORSConfig.ExposeHeaders` exposes response headers such as
  `Retry-After` to an app origin. `NewAppCORS` exposes none.
- `example/web/public/` holds the example's favicon. The PNG files are generated by
  `task favicon:generate` from `favicon.svg` and committed, never edited by hand.

## Commands

```sh
task setup           # mise install
task check           # every check, one after another
task --list          # one target per check
task web:test        # one half on its own, here the Vue package
task example:build   # compile the example application
```

- One task target per check and one CI job per target. `check` aggregates them and holds no
  command of its own. CI calls the small targets, never `check`, except `dco`, `codeql` and
  `license`, which call the organisation's shared reusable workflows instead.
- `lint:vet`, `lint:golangci` and `test:go` run `go vet`, `golangci-lint run` and
  `go test -race` across every package at the module root, each its own CI job.
- `lint:license` checks the Apache-2.0 header on every Go file at the module root and every
  `.ts`/`.vue`/`.mjs`/`.mts` file (`.d.mts` included) under `web/src/`, `web/scripts/` and
  `web/example/`, with `addlicense`.
  `addlicense` skips `.mts` files, so the check runs on a `.ts` copy of each.

## Constraints

### Go

- The standard library only, with four exceptions. `prometheus/client_golang` stays confined to
  the `metrics` package. `golang.org/x/crypto` stays confined to the `account` package, for
  Argon2id, which the standard library lacks. The owner accepted it for local accounts.
  `github.com/jackc/pgx/v5` stays confined to the `database` package, for the PostgreSQL
  driver and pool the standard library lacks. `database/dbtest` reaches it only through
  `database`. The owner accepted it for the database package.
  `github.com/go-webauthn/webauthn` v0.18.2 is imported by the `passkey` package only, for the
  WebAuthn ceremonies, and no exported name carries one of its types. The owner accepted it for
  passkeys. Its own modules, CBOR, msgpack, JWT and `go-tpm` among them, reach every build that
  imports `passkey`: `passkey`, `passkey/passkeytest` and `example/`, and no other package.
  Through them `golang.org/x/crypto/ocsp` and, by `go-tpm`'s transport, `golang.org/x/sys/unix`
  enter the build of `passkey` as well. `passkey` also pulls `golang.org/x/crypto/argon2`
  through its `account` import. `passkey/passkeytest` writes its
  CBOR by hand and imports no library package itself. Another dependency needs a reason in the
  pull request.
- A package stays usable on its own. An application that wants `clientip` must not pull in
  `session` with it.
- The secure choice is the default, never an option an application has to remember. Weakening
  one is the application's decision through a field, never a change to the default here.
- No error reaches a client with a cause in it. A 5xx collapses to one opaque shape, and the
  detail goes to the log with the request id. `Error.Exposed` is the one deliberate exception: a
  handler marks one message as intended for the caller, the default stays collapse, and it is for
  a 503 a server chooses on purpose, not a general escape hatch.
- The access log and the metric labels carry the route pattern, never the raw path: a path holds
  ids and tokens.
- Tests use no network and no socket. A test that binds a port does not run in the sandbox or in
  CI. The tests of `database` against a real PostgreSQL are the one exception, see Deviations.
- Errors wrap with `%w` and name the component. A library error that quotes a configuration
  value, such as the `*url.Error` of `url.Parse` or the `*net.OpError` of a listen, is not
  wrapped, and a refused configuration value is never quoted: the error names the variable and
  the expected form. A sentinel or an errno without the value may stay wrapped. See "Small,
  independent, standard library" in the concept doc.
- Logging through `log/slog`. A package takes a logger, it never builds one.

### Vue

- Components are `<script setup lang="ts">` with typed props and emits, Tailwind v4 utilities on
  the role tokens only. A component never names a colour and never carries a hex value.
- `base.css` holds the token contract with generic neutral defaults. A theme file overrides those
  tokens under `:root[data-theme='<name>']`, both modes in one `light-dark()` declaration per
  token.
- No committed theme file encodes a real, specific brand's colour values. Only generic example
  themes belong here; a branded theme is authored in the consumer's own repository against this
  same contract.
- A new token is added to `base.css` and to every theme in the same commit, and the contrast test
  decides whether the values are usable.
- Focus is styled once, globally. A component never adds a focus ring and never removes one.
- Touch targets are at least 44px.
- `vue-i18n` (Composition API mode, `legacy: false`) is the one translation library and a peer
  dependency like `vue`, so an app and govue share one instance. Do not add a second i18n
  library.
- For component behaviour nothing custom covers yet, modals, dropdowns and comboboxes, evaluate
  adopting a maintained headless primitives library before writing one. Reka UI, the Vue port of
  Radix, is wired up as a dependency for `GovueDialog`, whose focus trap, inert background and
  Escape handling are not something `jsdom` can verify on a hand-rolled implementation, and for
  `GovueSelect`. Both style their primitives entirely with these tokens. A checkbox and a switch
  stay hand rolled on native elements, which already give the right keyboard handling and ARIA
  for free.

## Documentation

- ADRs sparingly. The design reasoning lives in `docs/concepts/govue.md`, including the
  decisions a separate record would otherwise hold.
- `docs/adr/` starts with ADR 0001, the device token sessions for native apps. A new ADR is
  warranted only when a decision is genuinely contested and expensive to reverse, such as a
  security boundary or a public contract. Everything else goes into the concept doc.

## Releasing

- release-me owns the version, `CHANGELOG.md` and the tag. Never edit those by hand and never tag
  by hand.
- One tag covers both halves: the Go module path resolves it and a frontend pins it in its git
  dependency.

## Deviations

- `themeboot/boot.js` carries no license header. The license check covers `.go`, `.ts`,
  `.vue`, `.mjs` and `.mts` only, and the file is sent inline in every page, so a header would
  cost bytes on each response. The Go package that embeds it carries the header.

- `example/web/public/favicon.svg` and the generated PNG files carry no license header. The
  license check covers `.go`, `.ts`, `.vue`, `.mjs` and `.mts` only, and an image or a PNG
  cannot hold a comment header without changing what it is.

- The license-header file set is written twice. CI calls the organisation's shared
  `reusable-license.yaml`, which pins `addlicense` itself and takes the `find` invocations as an
  input, so the `license` job runs no task target. `task lint:license` keeps the same invocation
  for local development, and the two are kept in step by hand.

- The tests of `database` that need a server open a socket to a real PostgreSQL. They run only
  when `GOVUE_TEST_DATABASE_URL` is set, which the `test` CI job does with its PostgreSQL
  service container. Without it they skip loudly, and with `CI` set and the variable missing
  they fail. A fake would prove only the fake: the transaction per file, the advisory lock and
  the rollback are the server's behaviour. Everything else in the package is tested without a
  socket. No other package may follow this, and `.github/scripts/checks-workflow.test.sh`
  allows a service container on the `test` job alone.
