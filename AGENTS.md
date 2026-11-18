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
  `GovueCard`, `GovueBadge`, `GovueSkeleton`), `GovueThemeSwitch`, and the account forms
  (`GovueLoginForm`, `GovueSignupForm`, `GovueTotpEnrolment`), which take a `client` from
  `createClient`. `createClient({ auth: { mode: 'bearer' } })` is the client of a native app with a
  device token: `TokenStorage` (async `get`, `set`, `clear`, one opaque string) with the in memory
  default and no persistent adapter, rotation in `web/src/api/bearer.ts`, `signIn` and `signOut`.
  A token never goes into a URL, an error, a cause or a log, and the storage is written before
  the new token is used. All storage access runs through one lock, and a rotation writes only
  while the storage still holds the token it rotated. Exactly one context per storage rotates
  (`auth.rotation: 'owner'`), every other one is `'none'`.
- `web/src/i18n/` holds the language layer: the `createGovueI18n` plugin, the German and
  English catalogues and `catalogues.ts`, the one list of languages. `GovueLanguageSwitch`
  lives in `web/src/components/` with the other components.
  A visitor-facing default text goes into both catalogues under `govue.`, never into a
  component as a string. A text prop stays as an override.
- `web/` also holds a theme generator (`web/src/theme/`, run through
  `web/scripts/generate-theme.ts`) that derives an accessible theme from a consumer's own seed
  colours, never committed here.
- Eighteen Go packages live at the module root: `account`, `api`, `appassoc`, `clientip`,
  `csrf`, `database`, `envconf`, `favicon`, `health`, `httpserver`, `language`, `logging`,
  `metrics`, `middleware`, `oidc`, `ratelimit`, `session`, `themeboot`.
- `database` holds the PostgreSQL connection of a product: `Config` and `LoadConfig` with the
  `DB_*` variables, the pgx pool, the migration runner over an `fs.FS` and `Check` for
  readiness. The password never prints, and the default `sslmode` is `verify-full` with
  `DB_SSLROOTCERT`. The product keeps its schema and its queries. `database/dbtest` gives a test
  a fresh database on the server named by `GOVUE_TEST_DATABASE_URL`, start one with
  `docker compose -f example/compose.yaml up -d postgres`.
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
  package cannot reach.
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
  `.ts`/`.vue`/`.mjs`/`.mts` file (`.d.mts` included) under `web/src/` and `web/scripts/`, with
  `addlicense`.
  `addlicense` skips `.mts` files, so the check runs on a `.ts` copy of each.

## Constraints

### Go

- The standard library only, with three exceptions. `prometheus/client_golang` stays confined to
  the `metrics` package. `golang.org/x/crypto` stays confined to the `account` package, for
  Argon2id, which the standard library lacks. The owner accepted it for local accounts.
  `github.com/jackc/pgx/v5` stays confined to the `database` package, for the PostgreSQL
  driver and pool the standard library lacks. `database/dbtest` reaches it only through
  `database`. The owner accepted it
  for the database package. Another dependency needs a reason in the pull request.
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
- Errors wrap with `%w` and name the component.
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
