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
		"ReplaceTOTPSecret",
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

	if got := fieldNames(reflect.TypeFor[account.Identity]()); !slices.Equal(got, want) {
		t.Fatalf("account.Identity fields = %v, want %v", got, want)
	}
}

// TestRecordFields pins Record to what the logic reads. A provider subject, a
// tenant or a status field would let this package reach what the Store
// interface keeps out, so adding one is a decision for review.
func TestRecordFields(t *testing.T) {
	t.Parallel()

	want := []string{"ID", "Login", "PasswordHash", "TOTPSecret", "TOTPCounter"}

	if got := fieldNames(reflect.TypeFor[account.Record]()); !slices.Equal(got, want) {
		t.Fatalf("account.Record fields = %v, want %v", got, want)
	}
}

// fieldNames lists the field names of a struct type in declaration order.
func fieldNames(structType reflect.Type) []string {
	names := make([]string, 0, structType.NumField())
	for i := range structType.NumField() {
		names = append(names, structType.Field(i).Name)
	}

	return names
}
