// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package configtexttest checks that a startup error keeps a value out of
// every way it can be printed.
package configtexttest

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// AssertHidden fails t when secret appears in err printed with %s, %v, %+v,
// %#v or %q, or in the Error text or the %#v of any error in its unwrap tree,
// and when want, the name of the variable or the field, does not appear in
// err.Error().
func AssertHidden(t testing.TB, err error, secret, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("err is nil, want an error")
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name %q", err.Error(), want)
	}
	for _, verb := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
		if text := fmt.Sprintf(verb, err); strings.Contains(text, secret) {
			t.Errorf("%s of the error holds the value: %s", verb, text)
		}
	}
	walk(err, func(e error) {
		if strings.Contains(e.Error(), secret) {
			t.Errorf("an error in the unwrap tree holds the value: %T %q", e, e.Error())
		}
		if text := fmt.Sprintf("%#v", e); strings.Contains(text, secret) {
			t.Errorf("%%#v of an error in the unwrap tree holds the value: %s", text)
		}
	})
}

// walk calls f for err and every error below it, at least once each.
func walk(err error, f func(error)) {
	if err == nil {
		return
	}
	f(err)
	walk(errors.Unwrap(err), f)
	// errors.As checks err first, so a joined err yields itself here. A
	// joined error n single wrappers further down is found by errors.As at
	// each of those wrappers and through Unwrap, so it is visited n+1 times.
	// A repeated visit only repeats a check.
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		for _, e := range joined.Unwrap() {
			walk(e, f)
		}
	}
}
