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

Prerequisites: Go as pinned in [go.mod](go.mod). The Node and pnpm versions land in
[mise.toml](mise.toml) with the Vue half.

## Use

Nothing is importable yet. What the two halves will look like in use is sketched in the
[concept doc](docs/concepts/web-kit.md); this section gets real examples when the first packages
land.

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

The Go checks that need at least one package to analyse, `go vet`, `golangci-lint run` and
`go test`, are not wired yet: all three fail on a module without a single `.go` file. The pull
request adding the first package adds them.

## Releasing

[release-me](https://github.com/ostendorf-cloud/release-me) owns the version, `CHANGELOG.md` and
the tag. Conventional Commits decide the bump, and merging the release pull request it maintains
cuts the release. Never tag by hand and never edit the changelog.

One tag covers both halves: the Go module path resolves it, and a frontend pins it in its git
dependency.

## License

MIT, see [LICENSE](LICENSE).
