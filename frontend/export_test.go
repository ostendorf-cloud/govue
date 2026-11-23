// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package frontend

// CanonicalPath exposes the path check to the external tests, so each rule
// is proven on its own and not only behind the table of served paths, which
// refuses most of the same paths a second time.
var CanonicalPath = canonicalPath

// HashedName exposes the cache rule of an asset name.
var HashedName = hashedName
