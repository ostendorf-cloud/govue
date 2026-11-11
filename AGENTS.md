# web-kit

A framework for Go plus Vue web applications: a Go module with the HTTP server layer and a Vue
package with the components and the design tokens. Public, and consumed by applications outside
this organisation, so every exported name is a contract. The baseline rules apply and are not
repeated here.

## Layout

- Every package at the module root is public API. A rename or a changed signature is a breaking
  change for every consumer and needs a reason in the pull request.
- There is no `cmd/`. This repository ships no binary.
- `internal/` is for anything a consumer must not import.
- `web/` is the Vue package, consumed as a pnpm git dependency pinned to a release tag.
- `docs/concepts/web-kit.md` holds the design and the reasoning behind it. Read it before adding
  a package.
- `web/` holds the token contract, the example themes, the `useTheme` composable, the overlay
  and feedback components (`WkDialog`, `WkConfirmDialog`, `WkToastHost`, `WkEmptyState`,
  `WkErrorState`) with `useToasts`, and the form input components (`WkField`, `WkInput`,
  `WkSelect`, `WkCheckbox`, `WkSwitch`, `WkTextarea`, `WkButton`) with `useForm`. Layout
  components are a separate, not yet merged issue.
- No Go package exists yet: each one is its own issue.

## Commands

```sh
task setup           # mise install
task check           # every check, one after another
task --list          # one target per check
task web:test        # one half on its own, here the Vue package
```

- One task target per check and one CI job per target. `check` aggregates them and holds no
  command of its own. CI calls the small targets, never `check`.
- `go vet`, `golangci-lint run` and `go test` all fail on a module without a single `.go` file,
  so they are not wired yet. The pull request adding the first package adds `lint:vet`,
  `lint:golangci` and `test:go` to `Taskfile.yaml` and one CI job each.

## Constraints

### Go

- The standard library only. `prometheus/client_golang` is the one exception and stays confined
  to the `metrics` package. A second dependency needs a reason in the pull request.
- A package stays usable on its own. An application that wants `clientip` must not pull in
  `session` with it.
- The secure choice is the default, never an option an application has to remember. Weakening
  one is the application's decision through a field, never a change to the default here.
- No error reaches a client with a cause in it. A 5xx collapses to one opaque shape, and the
  detail goes to the log with the request id.
- The access log and the metric labels carry the route pattern, never the raw path: a path holds
  ids and tokens.
- Tests use no network and no socket. A test that binds a port does not run in the sandbox or in
  CI.
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
- For component behaviour nothing custom covers yet, modals, dropdowns and comboboxes, evaluate
  adopting a maintained headless primitives library before writing one. Reka UI, the Vue port of
  Radix, is wired up as a dependency for `WkDialog`, whose focus trap, inert background and
  Escape handling are not something `jsdom` can verify on a hand-rolled implementation, and for
  `WkSelect`. Both style their primitives entirely with these tokens. A checkbox and a switch
  stay hand rolled on native elements, which already give the right keyboard handling and ARIA
  for free.

## Documentation

- ADRs sparingly. The design reasoning lives in `docs/concepts/web-kit.md`, including the
  decisions a separate record would otherwise hold. `docs/adr/` does not exist and stays absent
  until a decision is genuinely contested and expensive to reverse.

## Releasing

- release-me owns the version, `CHANGELOG.md` and the tag. Never edit those by hand and never tag
  by hand.
- One tag covers both halves: the Go module path resolves it and a frontend pins it in its git
  dependency.

## Deviations

- MIT instead of the baseline's Apache-2.0: a framework meant for broad reuse, and the prior
  attempt this one replaces was MIT.
- No license header in source files. That convention travels with Apache-2.0; MIT is carried by
  `LICENSE` alone.
