# Changelog

## [v0.2.0](https://github.com/ostendorf-cloud/govue/compare/v0.1.0...v0.2.0) (2026-11-12)

- Version bump without user facing changes.

## v0.1.0 (2026-11-12)

### Features

- **session,oidc:** add a login cookie helper, session in context and ready to mount OIDC handlers ([#61](https://github.com/ostendorf-cloud/web-kit/pull/61))
- **example:** ship an example app wiring every package and document the canonical chain order ([#60](https://github.com/ostendorf-cloud/web-kit/pull/60))
- **ci:** enforce the Apache-2.0 license header with addlicense ([#45](https://github.com/ostendorf-cloud/web-kit/pull/45))
- **ci:** enforce DCO sign-off via a dedicated GitHub App check ([#43](https://github.com/ostendorf-cloud/web-kit/pull/43))
- **web:** generate an accessible theme from seed colors ([#33](https://github.com/ostendorf-cloud/web-kit/pull/33))
- **web:** add the layout component set ([#32](https://github.com/ostendorf-cloud/web-kit/pull/32))
- **web:** add the form input components and useForm ([#34](https://github.com/ostendorf-cloud/web-kit/pull/34))
- **middleware:** add the app-assembled middleware chain ([#37](https://github.com/ostendorf-cloud/web-kit/pull/37))
- **oidc:** add the login flow with pkce and jwks verification ([#38](https://github.com/ostendorf-cloud/web-kit/pull/38))
- **web:** overlay components (WkDialog, WkConfirmDialog, WkToastHost, WkEmptyState, WkErrorState) ([#36](https://github.com/ostendorf-cloud/web-kit/pull/36))
- **web:** typed API client parsing the api package's error shape ([#31](https://github.com/ostendorf-cloud/web-kit/pull/31))
- **metrics:** add Prometheus HTTP instrumentation by route pattern ([#30](https://github.com/ostendorf-cloud/web-kit/pull/30))
- **session:** keep session state server side behind a Store ([#29](https://github.com/ostendorf-cloud/web-kit/pull/29))
- **csrf:** refuse a cross site state changing request ([#28](https://github.com/ostendorf-cloud/web-kit/pull/28))
- **ratelimit:** add bounded-memory per-key rate limiter ([#27](https://github.com/ostendorf-cloud/web-kit/pull/27))
- **health:** add liveness and readiness probes ([#26](https://github.com/ostendorf-cloud/web-kit/pull/26))
- **httpserver:** http.Server with correct timeouts, graceful shutdown ([#25](https://github.com/ostendorf-cloud/web-kit/pull/25))
- **web:** Vite, Vue and Tailwind v4 scaffold with the token contract and theming ([#23](https://github.com/ostendorf-cloud/web-kit/pull/23))
- **envconf:** typed environment loading with accumulated errors ([#21](https://github.com/ostendorf-cloud/web-kit/pull/21))
- **api:** one error shape and handler wrapper for every endpoint ([#24](https://github.com/ostendorf-cloud/web-kit/pull/24))
- **clientip:** resolve the real client IP behind trusted proxies ([#20](https://github.com/ostendorf-cloud/web-kit/pull/20))
- **logging:** JSON slog logger with request-id context carriers ([#19](https://github.com/ostendorf-cloud/web-kit/pull/19))

### Bug Fixes

- resolve the polish-pass findings from the holistic audit ([#62](https://github.com/ostendorf-cloud/web-kit/pull/62))
- drop the duplicate request_id log attribute ([#59](https://github.com/ostendorf-cloud/web-kit/pull/59))
- **repo:** ignore .claude/worktrees/ instead of the stale .worktrees/ ([#57](https://github.com/ostendorf-cloud/web-kit/pull/57))
- validate RateLimit and csrf.New against nil dependencies at construction ([#56](https://github.com/ostendorf-cloud/web-kit/pull/56))
