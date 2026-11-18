// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package devicehttp holds the JSON handlers of the device token sessions of
// native apps: issue a token after a verified sign in, rotate it, list the
// caller's devices, revoke one, and sign the calling device out. ADR 0001
// records the design, and the session package holds the logic these handlers
// call. They add the HTTP: method, origin and body checks, the answers and the
// rules about what never leaves the server.
//
// The package is separate from session so that an application that uses
// session alone does not pull in the api package, and so that this package
// reaches the session layer only through its exported methods. It imports
// session, never the account package. The credential check is a callback, so
// the product decides what a sign in is. The example application wires
// account.Service.Login into it.
//
// # Routes
//
// The handlers do not register themselves. The application mounts each on the
// route of its choice, with the method in the pattern. The example uses
// these, under /api/v1/auth:
//
//	POST   /token          Handlers.Issue     credentials in the body, no other credential
//	POST   /token/rotate   Handlers.Rotate    bearer only, never a cookie
//	DELETE /token          Handlers.SignOut   bearer only, ends the calling device
//	GET    /devices        Handlers.List      the caller's devices, cookie or bearer
//	DELETE /devices/{id}   Handlers.Revoke    one device of the caller, cookie or bearer
//
// Revoke reads the id with [net/http.Request.PathValue] under the wildcard
// name "id", so the route has to be a [net/http.ServeMux] pattern with {id}.
//
// # Chain
//
// Mount every route behind the same chain as the rest of the API: the CSRF
// protector with ExemptBearer, then session.Manager.Middleware. List and
// Revoke read the session from the request context that the middleware fills,
// so without it they answer 401 to everybody. Rotate and SignOut read the
// token from the header and check it themselves.
//
// A request with an Authorization header authenticates by the token only. It
// never falls back to the cookie: a token that is invalid, expired, revoked or
// reused is the middleware's one 401, and a request with a cookie and no
// Authorization header is never a bearer request. Rotate and SignOut refuse
// that cookie request with 401. Issue authenticates by nothing but the
// credentials in its body: the handler reads neither a bearer token nor a
// cookie, and it never sets one. The chain in front still acts on the headers
// before Issue runs. A junk, expired, revoked or doubled Authorization header
// gets the middleware's 401 even with valid credentials. A cookie request
// without a CSRF token gets the protector's 403. A valid pending token is
// confirmed by the middleware, as on any request.
//
// # What a device token can do
//
// Any device token of an account can list every device of the account and
// revoke any of them, itself included. That is the current behaviour and it
// is not narrowed to the calling device. A holder of the old token of a
// rotation, inside the grace window, cannot rotate: Rotate answers 409
// rotation_refused and issues no token. It can still list, revoke other
// devices and sign the device out. Signing out through the old token ends the
// device, so the new token gets 401, and no reuse is logged, because the old
// token was inside its grace. Within the grace the old token is a full
// credential of the device.
//
// # Rate limit
//
// Issue checks credentials and costs whatever the verification callback costs,
// for the example an Argon2id derivation. It needs a per client rate limit in
// front of it, like the four account routes that check a password. Without
// one, anyone can guess credentials as fast as the verifier answers, and keep
// its hashing slot busy for everybody else. Rotate costs a store lookup and a
// token generation, and the general rate limit of the API is enough.
//
// # Answers
//
// Every answer carries Cache-Control: no-store and is JSON in the error shape
// of the api package. A token shows up in the body of an Issue or Rotate
// success, once, and nowhere else: not in a list, not in an error, not in a log
// line. The Authorization header is never logged either. A failure has one
// body per cause class:
//
//   - 401 invalid_credentials: the callback refused. One body, whatever the
//     cause.
//   - 401 unauthenticated: no valid token or session. A bearer route also
//     sends WWW-Authenticate: Bearer.
//   - 404 not_found: no such device. An id of another account answers exactly
//     like an unknown id.
//   - 409 rotation_refused: the rotation issued no token and the presented
//     token stays valid. The app keeps the token it has.
//   - 409 stale: the account ended its sessions between the credential check
//     and the issue. The app signs in again.
//   - 503: the verifier is busy (with Retry-After), the device store is full
//     on an issue, or the middleware could not read the session because the
//     store failed, which List and Revoke report. A store error inside
//     List, Revoke, Rotate or SignOut is an opaque 500, and so is a failed
//     issue. Neither answer carries the cause.
//
// A device name is a label the app chose and is never trusted. It may contain
// U+2028, U+3164, trailing spaces or markup such as "<script>". The JSON is
// safe. A UI renders a name as text, never as HTML.
//
// # What belongs to the application
//
// The headers and CORS needs of an app origin are not set here, and belong to
// the application, which uses the middleware and csrf packages for them:
//
//   - The app's origin is cross site, so the CSRF protector and these
//     handlers refuse an Issue request from it. The origin check of Issue,
//     and of Issue alone, accepts the origins in
//     [HandlerConfig.IssueTrustedOrigins]. Pass middleware.AppOrigins.List
//     there and nothing else. An app origin must never go into
//     csrf.Protector.TrustOrigin or into the web CORS list, which sends
//     credentials: either would let a page of that origin ride a cookie.
//     The application calls middleware.AppOrigins.RejectOverlap at startup
//     with the web list and the trusted origins, and refuses to start on an
//     error. For the protector it calls csrf.Protector.ExemptOrigin for the
//     one issue route, which skips the origin half there and keeps the token
//     half.
//   - A browser engine sends a preflight before a request with Authorization.
//     middleware.AppCORS answers it for the app origins with Authorization
//     and Content-Type allowed and credentials off, in front of the web CORS
//     policy and before authentication.
//
// # Bodies
//
// Bodies are decoded by encoding/json with unknown fields refused, one value
// only, under the application/json content type and up to a size limit. Field
// names match case insensitively, of two equal keys the last wins, and invalid
// UTF-8 is replaced by U+FFFD. A proxy or a WAF that parses JSON differently
// is out of scope.
package devicehttp
