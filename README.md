# web-kit

A framework for web applications with a Go backend and a Vue frontend.

## What it does

Two halves, one repository, one version:

- A Go module with the HTTP server layer an application needs and should not write again:
  timeouts, graceful shutdown, security response headers and a content security policy, liveness
  and readiness endpoints, environment configuration, structured logging, sessions, OIDC login,
  CSRF protection, rate limiting, the client IP behind a proxy, Prometheus metrics. Every package
  is importable on its own.
- A Vue package with components and a design token contract, so an application themes itself by
  filling that contract instead of inventing a palette.

It is a dependency, not a framework that owns `main`: an application assembles its own
middleware chain and keeps its own routing. The design and the reasoning behind it are in
[docs/concepts/web-kit.md](docs/concepts/web-kit.md).

This is the repository baseline. No package is implemented yet and no version is released yet.
Each package and the Vue half is its own issue, and the sections below fill in as those land.

## Install

The Go packages, once the first version is released:

```sh
go get github.com/ostendorf-cloud/web-kit
```

The Vue package is consumed as a pnpm git dependency pinned to a release tag, not from npm:

```sh
pnpm add "ostendorf-cloud/web-kit#v0.1.0&path:/web"
```

The ref before the `&` is the tag, `path:` is the subdirectory the package lives in. Quote the
whole argument, the shell would eat the `&`.

Prerequisites: Go as pinned in [go.mod](go.mod), Node and pnpm as pinned in
[mise.toml](mise.toml).

## Use

No Go package is importable yet. What that half will look like in use is sketched in the
[concept doc](docs/concepts/web-kit.md); this section gets real examples when the first packages
land.

The Vue half carries the design token contract and the theming. An app imports the base
stylesheet and at most one theme, and points Tailwind at this package's sources, because Tailwind
does not scan dependencies on its own:

```css
@import '@ostendorf-cloud/web-kit/styles.css';
@import '@ostendorf-cloud/web-kit/themes/sample.css';

@source "../node_modules/@ostendorf-cloud/web-kit/src";
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
import { useTheme } from '@ostendorf-cloud/web-kit'

const { theme, mode, resolvedMode, setTheme, setMode, cycleMode } = useTheme({ theme: 'sample' })
```

The choice is persisted, so it survives a reload, and it wins over the system setting in both
directions.

`WkField`, `WkInput`, `WkSelect`, `WkCheckbox`, `WkSwitch`, `WkTextarea` and `WkButton` are the
form input components, all styled on the same tokens. `useForm` ties a set of values to
validation and a submit function, and hands back the state a form needs to render itself:

```ts
const form = useForm({
  initial: { title: '' },
  validate: (values) => (values.title.trim() === '' ? { title: 'Please enter a title.' } : {}),
  submit: (values) => client.post('/things', values),
})
```

```vue
<WkField label="Title" :error="form.errorOf('title')">
  <WkInput v-model="form.values.title" @blur="form.blur('title')" />
</WkField>
<WkButton variant="primary" type="submit" :loading="form.submitting.value" @click="form.handleSubmit">
  Save
</WkButton>
```

A failed submit that rejects with the shape `useForm` describes as `ApiError` lands its field
errors on the matching inputs; anything without a matching field shows once as `form.problem`.
The overlay components (dialogs, dropdowns) are their own issues.

An app also gets a typed client for the Go `api` package's JSON error shape. It sends the session
cookie, attaches the CSRF token to every unsafe method and turns a refusal, a timeout or a lost
connection into one `ApiError`:

```ts
import { createClient } from '@ostendorf-cloud/web-kit'

const client = createClient({ csrfToken: () => csrf.value })
const things = await client.get<Thing[]>('/things')
```

## Generating a theme

A consumer with no design background can still get a full, accessible theme: the `web/` package
ships a generator that turns a handful of seed colours into a complete `base.css`-shaped theme
file, validated against the same WCAG 2.2 AA check `contrast.test.ts` runs on every theme in this
repository.

The generator's code is public and lives here. A consumer's own seed colours and the CSS file the
generator produces for them are not: they are never committed to web-kit. They belong in the
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

Running it means checking out web-kit at the version the consumer depends on, installing `web/`'s
dependencies, and running the generator against that file:

```sh
git clone https://github.com/ostendorf-cloud/web-kit
cd web-kit
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
@import '@ostendorf-cloud/web-kit/styles.css';
@import './theme.css';
```

## Configuration

Every package takes its settings as a struct and applies a documented default when a field is
left zero. No package reads the environment by itself.

There is no settings table yet, because no package exists yet. It fills in here as the packages
land, one row per setting with its default.

## Development

```sh
task setup
task check
```

`task setup` installs the pinned toolchain through `mise`. `task check` runs every check one
after another; `task --list` shows one target per check, and CI runs one job per target.

`task check` also runs `go vet`, `golangci-lint run` and `go test -race` now that the first
package exists.

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
