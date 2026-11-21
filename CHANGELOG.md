# Changelog

## [v0.6.0](https://github.com/ostendorf-cloud/govue/compare/v0.5.0...v0.6.0) (2026-11-21)

### Features

- **middleware:** expose response headers to app origins ([#225](https://github.com/ostendorf-cloud/govue/pull/225))
- **middleware:** expose headers through the CORS config ([#221](https://github.com/ostendorf-cloud/govue/pull/221))
- **web:** GovueContactForm with a proof of work Web Worker ([#208](https://github.com/ostendorf-cloud/govue/pull/208))
- **contact:** contact form validation and stateless handlers ([#207](https://github.com/ostendorf-cloud/govue/pull/207))
- **mail:** SMTP submission and header safe message building ([#204](https://github.com/ostendorf-cloud/govue/pull/204))
- **pow:** proof of work challenge issue and verify ([#203](https://github.com/ostendorf-cloud/govue/pull/203))
- **api:** add head to the Vue API client ([#202](https://github.com/ostendorf-cloud/govue/pull/202))
- **account:** let a known device skip the per-login ceiling ([#200](https://github.com/ostendorf-cloud/govue/pull/200))

### Bug Fixes

- keep config values out of connect errors and refuse four more inputs ([#226](https://github.com/ostendorf-cloud/govue/pull/226))
- keep environment values out of startup errors ([#222](https://github.com/ostendorf-cloud/govue/pull/222))
- **envconf:** refuse a day count that overflows ParseDuration ([#220](https://github.com/ostendorf-cloud/govue/pull/220))
- **envconf:** keep raw values out of the other parse errors ([#215](https://github.com/ostendorf-cloud/govue/pull/215))
- **envconf:** keep the raw value of a URL variable out of errors ([#213](https://github.com/ostendorf-cloud/govue/pull/213))

## [v0.5.0](https://github.com/ostendorf-cloud/govue/compare/v0.4.0...v0.5.0) (2026-11-19)

### Features

- **web:** check legal links on every prerendered page ([#189](https://github.com/ostendorf-cloud/govue/pull/189))
- **database:** refuse an applied migration that was edited ([#190](https://github.com/ostendorf-cloud/govue/pull/190))
- **database:** add the PostgreSQL connection, migrations and readiness ([#183](https://github.com/ostendorf-cloud/govue/pull/183))
- **analytics:** add server side page view counting with Umami ([#188](https://github.com/ostendorf-cloud/govue/pull/188))
- **web:** add the passkey button and composable ([#179](https://github.com/ostendorf-cloud/govue/pull/179))
- **passkey:** add passkey registration and login ([#181](https://github.com/ostendorf-cloud/govue/pull/181))
- **language:** add legal page routes with locale redirects ([#180](https://github.com/ostendorf-cloud/govue/pull/180))
- **web:** require rotation when the bearer client has its own storage ([#174](https://github.com/ostendorf-cloud/govue/pull/174))
- **web:** add a bearer mode to the API client for native apps ([#169](https://github.com/ostendorf-cloud/govue/pull/169))
- **middleware:** add app origin list and CORS handler for native apps ([#167](https://github.com/ostendorf-cloud/govue/pull/167))
- **session:** add device token handlers and wire them in example ([#162](https://github.com/ostendorf-cloud/govue/pull/162))
- **appassoc:** serve the app association files ([#157](https://github.com/ostendorf-cloud/govue/pull/157))
- **account:** add http handlers and vue forms ([#153](https://github.com/ostendorf-cloud/govue/pull/153))
- **account:** add the local account service and conformance suite ([#147](https://github.com/ostendorf-cloud/govue/pull/147))
- **session:** authenticate bearer requests and exempt them from CSRF ([#146](https://github.com/ostendorf-cloud/govue/pull/146))
- **session:** add device sessions with rotation and reuse detection ([#142](https://github.com/ostendorf-cloud/govue/pull/142))
- **account:** add argon2id password hashing and totp verification ([#143](https://github.com/ostendorf-cloud/govue/pull/143))
- **example:** serve the language pages and the root redirect ([#138](https://github.com/ostendorf-cloud/govue/pull/138))
- **web:** prerender one page per language with localizedFiles ([#135](https://github.com/ostendorf-cloud/govue/pull/135))
- **language:** redirect the site root to a language variant ([#131](https://github.com/ostendorf-cloud/govue/pull/131))
- **web:** add the language route helper and head generators ([#130](https://github.com/ostendorf-cloud/govue/pull/130))
- **account:** add the local account contract ([#126](https://github.com/ostendorf-cloud/govue/pull/126))
- **web:** pre-render one page at build time ([#124](https://github.com/ostendorf-cloud/govue/pull/124))
- **web:** add a German and English language layer with a switch ([#118](https://github.com/ostendorf-cloud/govue/pull/118))
- **favicon:** require a favicon and generate its PNG files ([#113](https://github.com/ostendorf-cloud/govue/pull/113))
- **web:** add a theme switch and a CSP safe boot script ([#111](https://github.com/ostendorf-cloud/govue/pull/111))
- **ci:** wire Renovate config check and per-repo run into CI ([#98](https://github.com/ostendorf-cloud/govue/pull/98))

### Bug Fixes

- **deps:** update module golang.org/x/crypto to v0.59.0 ([#155](https://github.com/ostendorf-cloud/govue/pull/155))
- **ci:** check license headers on .mjs and .d.mts files ([#129](https://github.com/ostendorf-cloud/govue/pull/129))
- **web:** let the empty and error states take a heading level ([#123](https://github.com/ostendorf-cloud/govue/pull/123))
- **web:** let GovueCard take the heading level of its title ([#114](https://github.com/ostendorf-cloud/govue/pull/114))

## [v0.4.0](https://github.com/ostendorf-cloud/govue/compare/v0.3.1...v0.4.0) (2026-11-13)

### Breaking Changes

- **envconf:** trim env and file secrets alike ([#91](https://github.com/ostendorf-cloud/govue/pull/91))

### Features

- **httpserver:** run an admin listener alongside the primary one ([#89](https://github.com/ostendorf-cloud/govue/pull/89))

### Bug Fixes

- **renovate:** pinDigests is a config key, not a preset ([#87](https://github.com/ostendorf-cloud/govue/pull/87))

## [v0.3.1](https://github.com/ostendorf-cloud/govue/compare/v0.3.0...v0.3.1) (2026-11-13)

### Bug Fixes

- **web:** trim baseUrl without an unanchored regex ([#82](https://github.com/ostendorf-cloud/govue/pull/82))
- **oidc:** express localPath's guard in CodeQL's recognized shape ([#83](https://github.com/ostendorf-cloud/govue/pull/83))

## [v0.3.0](https://github.com/ostendorf-cloud/govue/compare/v0.2.0...v0.3.0) (2026-11-13)

### Features

- **envconf:** add the value-or-file secret contract ([#78](https://github.com/ostendorf-cloud/govue/pull/78))
- **api:** let a handler mark a 5xx message as intended for the caller ([#76](https://github.com/ostendorf-cloud/govue/pull/76))

### Bug Fixes

- **session:** bound MemoryStore's tracked session count ([#77](https://github.com/ostendorf-cloud/govue/pull/77))

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
