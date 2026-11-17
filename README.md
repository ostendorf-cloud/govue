# govue

A library for web applications with a Go backend and a Vue frontend.

## What it does

Two halves, one repository, one version:

- A Go module with the HTTP server layer an application needs and should not write again:
  timeouts, graceful shutdown, security response headers and a content security policy, liveness
  and readiness endpoints, environment configuration, structured logging, sessions, OIDC login,
  CSRF protection, rate limiting, the client IP behind a proxy, Prometheus metrics. Every package
  is importable on its own.
- A Vue package with components and a design token contract, so an application themes itself by
  filling that contract instead of inventing a palette.

It is a library, not a framework, and the difference is the direction of the calls. A framework
owns `main` and calls the application at the lifecycle points it defines. The application
registers its handlers into the framework's router and its middleware into the framework's
pipeline. Every call here runs the other way. The application's `main` calls `httpserver.Run`.
The application builds the `http.ServeMux`, registers its own routes, and fixes the order the
middleware wraps in. Sessions, OIDC, CSRF, rate limiting, security headers and metrics are
framework-shaped features, but each one ships as a piece the application calls, not as a slot
the application registers into. Nothing in this module runs that the application did not call
itself.

The design and the reasoning behind it are in [docs/concepts/govue.md](docs/concepts/govue.md).

## Install

The Go packages:

```sh
go get github.com/ostendorf-cloud/govue
```

The Vue package is consumed as a pnpm git dependency pinned to a release tag, not from npm:

```sh
pnpm add "ostendorf-cloud/govue#v0.1.0&path:/web"
```

The ref before the `&` is the tag, `path:` is the subdirectory the package lives in. Quote the
whole argument, the shell would eat the `&`.

Prerequisites: Go as pinned in [go.mod](go.mod), Node and pnpm as pinned in
[mise.toml](mise.toml).

## Use

Every Go package is importable on its own. `httpserver` is the entry point most applications
reach for first, since it is what actually listens:

```go
package main

import (
    "context"
    "log/slog"
    "net/http"
    "os"

    "github.com/ostendorf-cloud/govue/httpserver"
)

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    mux := http.NewServeMux()
    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })

    err := httpserver.Run(context.Background(), httpserver.Config{
        Addr:    ":8080",
        Handler: mux,
        Logger:  logger,
    })
    if err != nil {
        logger.Error("server stopped", "error", err)
        os.Exit(1)
    }
}
```

`httpserver.Run` applies the documented timeout, header size and shutdown defaults to any field
left zero, and shuts down gracefully on a cancelled context: it waits `DrainDelay`, stops
accepting new connections, and lets requests already in flight finish within `ShutdownTimeout`.

The middleware chain this snippet leaves out lives in [example/](example/), the compiled and
tested reference for the order every package expects.

The Vue half carries the design token contract and the theming. An app imports the base
stylesheet and at most one theme, and points Tailwind at this package's sources, because Tailwind
does not scan dependencies on its own:

```css
@import '@ostendorf-cloud/govue/styles.css';
@import '@ostendorf-cloud/govue/themes/sample.css';

@source "../node_modules/@ostendorf-cloud/govue/src";
```

`base.css` registers every role token with a neutral default, so an app that imports no theme
still gets a readable page. A theme overrides those tokens under `:root[data-theme='<name>']`,
with light and dark in one `light-dark()` declaration per token. `sample` and `sample-contrast`
are generic examples; a real brand's theme is authored in that brand's own repository against the
same contract and is never committed here.

`useTheme` writes the two attributes the stylesheets switch on: `data-theme` picks the palette,
`data-mode` pins light or dark. The mode has three states, and the third is the absence of the
attribute, which hands the decision back to the operating system:

```ts
import { useTheme } from '@ostendorf-cloud/govue'

const { theme, mode, resolvedMode, setTheme, setMode, cycleMode } = useTheme({ theme: 'sample' })
```

The choice is persisted, so it survives a reload, and it wins over the system setting in both
directions.

`GovueThemeSwitch` is the ready made control for it: a radio group with system, light and dark,
operable with the arrow keys, Home and End, with the active choice announced. The default is the
system setting. Its strings are props, `label`, `systemLabel`, `lightLabel` and `darkLabel`. Left
out, they come from the language layer (see Language), English without it. A prop overrides the
catalogue:

```vue
<GovueThemeSwitch label="Design" system-label="Auto" light-label="Hell" dark-label="Dunkel" />
```

The switch uses the shared `useTheme` state, and the first `useTheme` call decides its defaults
and storage key. An app with its own default theme or `storageKey` calls `useTheme({ ... })` in
its setup before any switch renders.

`GovueHeader` shows it without markup from the site: set `theme-switch`. The `theme` slot of the
header replaces the switch with the site's own control, and a header without either is unchanged.

```vue
<GovueHeader theme-switch>
  <template #brand>My app</template>
</GovueHeader>
```

Without help, a page paints in the system theme first and switches once the bundle runs. The Go
package `themeboot` removes that flash: it ships a small inline script that reads the same
storage key as `useTheme` and sets `data-theme` and `data-mode` on the root element before the
first paint. Under the default Content-Security-Policy an inline script runs only with the
response's nonce, so `themeboot` takes it from `middleware.Nonce`. `themeboot.Shell` serves the
built `index.html` with the script injected right after the opening head tag, and must sit inside
`middleware.Security`, which makes the nonce:

```go
mux.Handle("GET /{path...}", themeboot.Shell(indexHTML))
```

A server that renders its own page puts `themeboot.Tag(middleware.Nonce(r.Context()))` first in
the head instead. Without a nonce `Tag`, `Inject` and `Shell` panic: a script without one is
blocked silently under the policy, so a missing security middleware must not go unnoticed.
`Shell` then panics per request, which the recover middleware answers with a 500. An app that
gives `useTheme` its own `storageKey` passes the same key with
`themeboot.WithStorageKey("my-key")`, which accepts letters, digits and `. _ : -` and panics on
anything else. The response is sent `Cache-Control: no-store`, because a response with a nonce
must never be cached. A page with no stored choice keeps a `data-theme` the server rendered on the
root element, otherwise `sample`, and leaves `data-mode` off so the system decides. The key and
the attribute names are exported on both sides, `themeboot.StorageKey` and `THEME_STORAGE_KEY`,
and a test runs the shipped script against the composable.

A landing page that needs real markup for crawlers and a fast first paint can be pre-rendered once
at build time, with no Node process at runtime. The Go server serves the result like any other
static file. Write a small SSR entry that renders the page through `@ostendorf-cloud/govue/server`:

```ts
// src/entry-prerender.ts
import { createGovueI18n } from '@ostendorf-cloud/govue'
import { render as renderPage } from '@ostendorf-cloud/govue/server'

import Landing from './Landing.vue'

export const render = () =>
  renderPage(Landing, {
    setup: (app) => {
      app.use(createGovueI18n({ locale: 'en' }))
    },
  })
```

Put `<!--app-html-->` inside the mount element of `index.html`, then build the entry and run the
script. It writes the markup into the template and fails when the placeholder is missing:

```sh
pnpm exec vite build
pnpm exec vite build --ssr src/entry-prerender.ts --outDir dist-ssr
pnpm exec govue-prerender \
  --entry dist-ssr/entry-prerender.js --template dist/index.html --out dist/index.html
```

The script is plain JavaScript and needs Node 24 or later. With `--out` equal to `--template`, the
marker is consumed, so a second run fails with no placeholder: run it after a fresh `vite build`.

The client starts over the markup with `createApp`, which replaces it, and never with
`createSSRApp`. The markup is rendered with no visitor, so it knows neither the stored theme nor
the browser language. Hydrating it would keep a wrong `aria-checked` or language for a visitor with
a stored dark mode or a German browser, because Vue does not patch attribute mismatches in
production. The prerendered markup serves the first paint and crawlers.

A site with language routes prerenders one page per public route and language instead. The SSR
entry exports `files`, built by `localizedFiles` from the route table:

```ts
// src/entry-prerender.ts
import { createGovueI18n } from '@ostendorf-cloud/govue'
import { localizedFiles, render } from '@ostendorf-cloud/govue/server'

import Page from './Page.vue'
// [{ path: '/', public: true }, { path: '/impressum', public: true }]
import { routes } from './routes'

export const files = (template: string) =>
  localizedFiles(routes, {
    origin: 'https://example.com',
    template,
    render: (variant) =>
      render(Page, {
        props: { source: variant.source },
        setup: (app) => {
          app.use(createGovueI18n({ locale: variant.locale }))
        },
      }),
  })
```

`index.html` holds one `<html>` start tag, `<!--app-head-->` in the head and `<!--app-html-->` in
the mount element. Run the script with `--out-dir`:

```sh
pnpm exec vite build
pnpm exec vite build --ssr src/entry-prerender.ts --outDir dist-ssr
pnpm exec govue-prerender \
  --entry dist-ssr/entry-prerender.js --template dist/index.html --out-dir dist
```

It writes `dist/de/index.html`, `dist/en/index.html`, `dist/de/impressum/index.html`,
`dist/en/impressum/index.html` and `dist/sitemap.xml`. Each page carries its `lang`, a canonical
link and the `hreflang` alternates with `x-default` on the German page. App routes are left out.
The script refuses a path from the bundle that leaves the directory, a duplicate and a symbolic
link, before it writes anything. It overwrites the files of an earlier run and keeps every other
file. On a language page the client passes the language of the URL:

```ts
const locale = localeFromPath(decodeURIComponent(location.pathname))
createApp(Page).use(createGovueI18n({ locale })).mount('#app')
```

The Go server must serve `/de/impressum` itself and not through a directory file server, which
redirects it to `/de/impressum/`. govue ships no directory handler.

Neither mode adds routing, per-request data or hydration. A dialog or other teleported content is
not part of the markup. `useTheme` and `useToasts` keep module level state, so a prerendered page
raises no toast and all pages of one build share one theme. See `docs/concepts/govue.md` for the
boundary.

### Favicon

Every site has a favicon, and a site without one fails instead of shipping the browser's default
icon. The package `favicon` is the contract.

What a site provides:

- One source `favicon.svg` in `web/public/`, its own artwork. A site without a brand mark uses a
  placeholder: a monogram on the site's accent colour.
- The generated PNG files next to it, made with `task favicon:generate` and committed. The target
  needs `imagemagick`, pinned in `mise.toml`. A site copies the target and the pin, and sets
  `FAVICON_DIR` to `web/public`. Nobody edits a PNG by hand.
- A theme colour, a hex value such as `#0f766e`, usually the accent colour.

What govue does:

- `favicon.LoadDir("web/public", "#0f766e")`, or `favicon.Load` on an `fs.FS`, checks that
  `favicon.svg`, `favicon-32x32.png` and `apple-touch-icon.png` exist, are not empty and hold
  what their type says: an `svg` element, the PNG signature. It fails at startup and names every
  bad file with the path it expected, so a site cannot start without them. A symbolic link is
  accepted, the file system decides what it resolves to.
- `themeboot.WithFavicon(icons)` makes `Shell` and `Inject` write the icon links and the
  `theme-color` meta tag right behind the boot script, on every page and so in every language.
  The links are root absolute, so the frontend build must copy `web/public` to the root of the
  served files. `Shell` panics when it is built, at startup, for a page without a head element
  or one that already carries icon links or a `theme-color` tag, because either would serve no
  icons or serve them twice. `Inject` stays total and returns such a page unchanged, so
  `CheckPages` is the guarantee. A server that renders its own page calls `icons.Links()`.
- `favicon.CheckPages` is the check a site's own test suite calls on its prerendered pages, and
  `favicon.CheckPage` checks one page. Each reads only the head, the way a browser does:
  comments, script and textarea content do not count, and an unterminated comment runs to the
  end of the page. A link with a `media` query other than `all` does not count, because it is not
  the icon of every screen. Attribute values are entity decoded. Each fails when a page lacks an
  icon link, a link is not a root absolute path, a link points at another file than
  `favicon.svg`, `favicon-32x32.png` or `apple-touch-icon.png` for its role, or the linked file
  is missing, empty or not what its type says. Match the cause with `errors.Is` against
  `favicon.ErrNoLink`, `ErrBadHref`, `ErrWrongFile`, `ErrMissing`, `ErrEmpty` and `ErrInvalid`.

```go
public := os.DirFS("web/public")
if err := favicon.CheckPages(os.DirFS("dist"), public); err != nil {
    t.Fatal(err)
}
```

The example application carries a favicon in `example/web/public/` and passes the check.

`GovueField`, `GovueInput`, `GovueSelect`, `GovueCheckbox`, `GovueSwitch`, `GovueTextarea` and
`GovueButton` are the form input components, all styled on the same tokens. `useForm` ties a set
of values to validation and a submit function, and hands back the state a form needs to render
itself:

```ts
const form = useForm({
  initial: { title: '' },
  validate: (values) => (values.title.trim() === '' ? { title: 'Please enter a title.' } : {}),
  submit: (values) => client.post('/things', values),
})
```

```vue
<GovueField label="Title" :error="form.errorOf('title')">
  <GovueInput v-model="form.values.title" @blur="form.blur('title')" />
</GovueField>
<GovueButton
  variant="primary"
  type="submit"
  :loading="form.submitting.value"
  @click="form.handleSubmit"
>
  Save
</GovueButton>
```

A failed submit that rejects with the shape `useForm` describes as `ApiError` lands its field
errors on the matching inputs; anything without a matching field shows once as `form.problem`.

An app also gets a typed client for the Go `api` package's JSON error shape. It sends the session
cookie, attaches the CSRF token to every unsafe method and turns a refusal, a timeout or a lost
connection into one `ApiError`:

```ts
import { createClient } from '@ostendorf-cloud/govue'

const client = createClient({ csrfToken: () => csrf.value })
const things = await client.get<Thing[]>('/things')
```

## Heading levels

A page has one `h1`, set through `GovuePage`'s `title`. A component that renders a heading takes
its level from the caller where the right level depends on the place it is used, so the outline
of a page has no skipped level.

`GovueCard` renders its `title` at level 2 by default, which fits a card directly under the page's
`h1`. A card inside a section headed by an `h2` passes `:heading-level="3"`. The prop takes 2 to 6
and the styling is the same for every level. An invalid value falls back to level 2. Write
`:heading-level="3"` with the colon: `heading-level="3"` passes the string `"3"`, which is invalid.

```vue
<GovueCard title="Recent activity">...</GovueCard>
<GovueCard title="Sessions" :heading-level="3">...</GovueCard>
```

`GovueEmptyState` and `GovueErrorState` take the same `heading-level` prop, with the same type
(`HeadingLevel`), the same default of 2 and the same fallback to 2. Pass `:heading-level="3"` when
the state sits in a card or section headed by an `h2`.

```vue
<GovueCard title="Sessions">
  <GovueEmptyState :heading-level="3" />
</GovueCard>
```

`GovueDialog` renders its title through a dialog title (an `h2`, in its own portal, outside the
page outline) and keeps its level.

## Language

Every component text is available in German and English, through `vue-i18n` in Composition API
mode. `vue-i18n` is a peer dependency, `^11.1.5`, so the app and govue share one copy. Install
the plugin once:

```ts
import { createApp } from 'vue'
import { createGovueI18n } from '@ostendorf-cloud/govue'

createApp(App).use(createGovueI18n()).mount('#app')
```

The language is chosen in this order: the stored choice, then the browser (`navigator.languages`,
first supported entry, a region code such as `de-AT` counts as `de`), then German. A stored
value that is not a known language is ignored. Without a `window` (a server render or a
prerender) nothing is read and the language is German, because Node defines `navigator` and
`localStorage` itself and they would describe the build host, not a visitor. The `locale` option
forces a language; an unknown code gives German. The plugin sets `lang` on the document element,
and keeps it in step when the language changes by any route.

`GovueLanguageSwitch` is the control. It is a native select, with each language written in its
own language (Deutsch, English), a 44px target and its accessible name in the active language.
Changing it keeps the visitor on the page and keeps focus on the select. It emits `change`.

```vue
<GovueLanguageSwitch />
```

Without the plugin, components stay English, as before, and `GovueLanguageSwitch` throws a
setup error. A text prop (`confirmLabel`, `retryLabel`, `label`, ...) always wins over the
catalogue.

### Where the choice is kept

`storage` picks the backend. It never throws: blocked storage means no stored choice.

```ts
createGovueI18n({ storage: { kind: 'local', key: 'my-site-lang' } }) // default: govue-language
createGovueI18n({ storage: { kind: 'cookie', name: 'lang' } }) // signed in app areas
createGovueI18n({ storage: { kind: 'custom', get: () => account.language, set: saveToAccount } })
createGovueI18n({ storage: { kind: 'none' } })
```

- The cookie is a functional preference cookie. It holds a language code, identifies nobody and
  is written only when the visitor picks a language.
- Its attributes are `Path=/`, `Max-Age` of one year (`maxAge` overrides it), `SameSite=Lax`,
  `Secure` on https, and no `HttpOnly`, because the client has to read it.
- A cookie `name` with anything but letters, digits, `.`, `_` and `-`, and a `maxAge` that is not
  a non-negative integer, are ignored silently: the defaults apply.
- The `custom` backend reads synchronously, because the first render needs the language. A value
  that loads later is applied with `setLocale(code, { persist: false })` from `useLanguage()`.
- No path prefix is involved. Routing by `/de/` and `/en/` is a separate concern.

### Your own messages

Pass them to the plugin, or merge them later on the instance. Your messages load after govue's,
so a key can be overridden. Keep your own keys out of the `govue.` namespace.

```ts
const govueI18n = createGovueI18n({
  messages: { de: { site: { title: 'Start' } }, en: { site: { title: 'Home' } } },
})
govueI18n.i18n.global.mergeLocaleMessage('en', { site: { more: 'More' } })
```

Use them with `useI18n()` as usual.

Messages use vue-i18n syntax. A bare `|` is a plural separator, `@` starts a linked message, and
`{` and `}` open a placeholder. Write a literal one as a quoted literal: `{'|'}`, `{'@'}`,
`{'{'}` and `{'}'}`, for example `"mail{'@'}host"`. Without it, `A | B` renders only `A`, and
`a@b.de` fails to compile.

### An app that already uses vue-i18n

Pass the instance, created with `legacy: false`. govue merges its catalogue into it and shares
it, so there is one instance. The app installs its own instance itself. The plugin then leaves
the instance's locale alone, so the app's router stays in charge; pass `detect: true` to run the
choice order anyway, or `locale` to force one.

```ts
const i18n = createI18n({ legacy: false, locale: 'de', fallbackLocale: 'de', messages })
app.use(i18n).use(createGovueI18n({ i18n }))
```

### Adding a language

Add a catalogue file next to `web/src/i18n/de.ts`, and one line in `web/src/i18n/catalogues.ts`.
The switch, the choice order and the completeness test all read that list. The test fails the
build when a language lacks a key or uses other named placeholders than German.

## Generating a theme

A consumer with no design background can still get a full, accessible theme: the `web/` package
ships a generator that turns a handful of seed colours into a complete `base.css`-shaped theme
file, validated against the same WCAG 2.2 AA check `contrast.test.ts` runs on every theme in this
repository.

The generator's code is public and lives here. A consumer's own seed colours and the CSS file the
generator produces for them are not: they are never committed to govue. They belong in the
consumer's own repository, imported next to `base.css` exactly like a hand-authored theme.

A seed configuration is a small JSON file. `neutral` and `accent` are required; the four semantic
colours are optional and default to generic hues already documented in `base.css`:

```json
{
  "name": "acme",
  "seeds": {
    "neutral": "#51637a",
    "accent": "#d4863a"
  }
}
```

Running it means checking out govue at the version the consumer depends on, installing `web/`'s
dependencies, and running the generator against that file:

```sh
git clone https://github.com/ostendorf-cloud/govue
cd govue
git checkout v0.1.0
cd web
pnpm install
pnpm theme:generate -- --config seeds.json --out theme.css
```

The command prints the theme's name and the output path, and any warning about the seed colours
themselves, for instance an accent too close in hue to the neutral ramp to read as an accent. A
seed combination that cannot clear WCAG 2.2 AA is rejected with the failing pairs named, and no
file is written.

`theme.css` then moves into the consumer's own repository and is imported the same way `sample.css`
is above:

```css
@import '@ostendorf-cloud/govue/styles.css';
@import './theme.css';
```

## Configuration

Every package that takes a struct applies a documented default when a field is left zero, except
where noted below. No package reads the environment by itself; `envconf` is for an application
that wants to build such a struct from environment variables, but its own defaults are per call,
not a struct field.

### session.Config

| Field            | Default                                            |
| ---------------- | -------------------------------------------------- |
| `CookieName`     | `__Host-session`                                   |
| `AllowPlainHTTP` | `false`: the cookie carries the `Secure` attribute |
| `SameSiteStrict` | `false`: `SameSite=Lax`                            |
| `Persistent`     | `false`: no `Max-Age`, the cookie is session only  |
| `Idle`           | 2 hours                                            |
| `Lifetime`       | 12 hours                                           |
| `TouchEvery`     | 1 minute                                           |
| `Now`            | `time.Now`                                         |

`Store` has no default; it is required.

### oidc.Config

| Field         | Default                           |
| ------------- | --------------------------------- |
| `Scopes`      | `openid`, `profile`, `email`      |
| `GroupsClaim` | `groups`                          |
| `AuthParams`  | none                              |
| `HTTPClient`  | a client with a 10 second timeout |
| `Now`         | `time.Now`                        |

`Issuer`, `ClientID`, `ClientSecret` and `RedirectURL` have no default; they are required.

### middleware.CORSConfig

| Field         | Default                                         |
| ------------- | ----------------------------------------------- |
| `Methods`     | `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE` |
| `Headers`     | `Content-Type`                                  |
| `Credentials` | `false`                                         |
| `MaxAge`      | 0: no `Access-Control-Max-Age` header is sent   |

`Origins` has no default; at least one origin is required.

### middleware.Security

`Security`'s zero value sends almost nothing. Build it with `DefaultSecurity`, whose fields are:

| Field                   | Default                                                                                                                                 |
| ----------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `Policy`                | `DefaultPolicy()`: default-deny CSP with a per-response nonce for inline script and style                                               |
| `HSTS`                  | 1 year                                                                                                                                  |
| `HSTSIncludeSubDomains` | `true`                                                                                                                                  |
| `HSTSPreload`           | `true`                                                                                                                                  |
| `ReferrerPolicy`        | `strict-origin-when-cross-origin`                                                                                                       |
| `PermissionsPolicy`     | denies accelerometer, browsing-topics, camera, display-capture, geolocation, gyroscope, magnetometer, microphone, midi, payment and usb |
| `FrameOptions`          | `DENY`                                                                                                                                  |
| `COOP`                  | `same-origin`                                                                                                                           |
| `CORP`                  | `same-origin`                                                                                                                           |
| `RobotsTag`             | empty: the header is omitted                                                                                                            |

### middleware.RateLimit

| Field      | Default                                                               |
| ---------- | --------------------------------------------------------------------- |
| `ClientIP` | `nil`: every request shares one bucket                                |
| `Respond`  | `nil`: answers with the `api` package's error shape and `Retry-After` |

`Limiter` has no default; it is required.

### ratelimit.New

Not a struct, a constructor: `New(burst, per, maxKeys)`. `per` at or below zero becomes 1 second,
and `maxKeys` below 1 becomes 1. `burst` has no floor: zero refuses every request for every key.

### httpserver.Config

| Field               | Default                                    |
| ------------------- | ------------------------------------------ |
| `ReadHeaderTimeout` | 5 seconds                                  |
| `ReadTimeout`       | 15 seconds                                 |
| `WriteTimeout`      | 30 seconds                                 |
| `IdleTimeout`       | 60 seconds                                 |
| `MaxHeaderBytes`    | 64 KiB                                     |
| `ShutdownTimeout`   | 20 seconds                                 |
| `DrainDelay`        | 2 seconds                                  |
| `BeforeShutdown`    | `nil`: nothing runs before the drain delay |

`Addr`, `Handler` and `Logger` have no default; `Addr` is required only for `Run`.

`csrf` takes no struct: `New` takes the one required `Expected` function, and `TrustOrigin` and
`OnRefusal` are called during setup, not through fields.

## Development

```sh
task setup
task check
```

`task setup` installs the pinned toolchain through `mise`. `task check` runs every check one
after another, including `go vet`, `golangci-lint run` and `go test -race`; `task --list` shows
one target per check, and CI runs one job per target.

The `web:` targets cover the Vue half: `web:lint`, `web:format`, `web:types`, `web:test` and
`web:build`. Each installs `web/`'s dependencies from the lockfile first, so one of them works on
its own in a fresh checkout. `task web:fmt` writes the formatting the check only reports.

## Releasing

[release-me](https://github.com/ostendorf-cloud/release-me) owns the version, `CHANGELOG.md` and
the tag. Conventional Commits decide the bump, and merging the release pull request it maintains
cuts the release. Never tag by hand and never edit the changelog.

One tag covers both halves: the Go module path resolves it, and a frontend pins it in its git
dependency.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to propose a change. See
[SECURITY.md](SECURITY.md) for how to report a vulnerability.

## License

Apache-2.0. See `LICENSE`.
