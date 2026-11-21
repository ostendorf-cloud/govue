// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package configtext quotes a configured value for a startup error. A startup
// error reaches a CI log and a startup log, so a value that may carry a
// credential is not shown.
package configtext

import (
	"strconv"
	"strings"
)

// Hidden replaces a value that may carry a credential.
const Hidden = "(the value is not shown: it holds an @, a ? or a #, where a URL carries a credential)"

// Quote returns s quoted, so an operator sees which entry of a list is wrong.
// A value that holds an @, a ? or a # is replaced by Hidden: that is where a
// URL carries userinfo or a token in the query or the fragment, and an origin,
// a host name or a scheme never needs one.
func Quote(s string) string {
	if strings.ContainsAny(s, "@?#") {
		return Hidden
	}
	return strconv.Quote(s)
}
