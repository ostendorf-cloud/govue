// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/ostendorf-cloud/govue/account"
)

// TestStoreMethodSet pins the whole method set of account.Store. A method
// added here widens what every consumer has to implement and what this package
// can reach, which is a decision for review, not a side effect of a change.
func TestStoreMethodSet(t *testing.T) {
	t.Parallel()

	want := []string{
		"AdvanceTOTPCounter",
		"ConfirmTOTP",
		"Create",
		"FindByLogin",
		"SetPasswordHash",
	}

	storeType := reflect.TypeFor[account.Store]()

	got := make([]string, 0, storeType.NumMethod())
	for i := range storeType.NumMethod() {
		got = append(got, storeType.Method(i).Name)
	}

	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Fatalf("account.Store methods = %v, want %v", got, want)
	}
}

// TestIdentityFields pins Identity to an opaque id and a login. A tenant, a
// role or a login method belongs to the consumer's session, not here.
func TestIdentityFields(t *testing.T) {
	t.Parallel()

	want := []string{"ID", "Login"}

	identityType := reflect.TypeFor[account.Identity]()

	got := make([]string, 0, identityType.NumField())
	for i := range identityType.NumField() {
		got = append(got, identityType.Field(i).Name)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("account.Identity fields = %v, want %v", got, want)
	}
}
