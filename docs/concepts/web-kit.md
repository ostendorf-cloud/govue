# web-kit

The design of the framework and the reasoning behind it. Written before the packages exist, so
it states intent, not a description of what is there.

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
  `github.com/ostendorf-cloud/web-kit/ratelimit` with no segment that says nothing.
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

- A shared session store. The in-memory store is correct for one replica only. Which backend,
  and whether it ships here or in the first application that needs it, is open until a second
  consumer exists. Next step: decide in the `session` issue.
- A theme generator. A small public tool that derives a full accessible light and dark role
  palette from a handful of seed colours, OKLCH being the obvious basis, validated by the same
  contrast test. The generator would be public here; a consumer's seed configuration and the CSS
  it produces stay in the consumer's own repository. Next step: its own issue, not part of the
  first theming work.
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
