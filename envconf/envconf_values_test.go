// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package envconf

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// secretLooking is built at run time so the constant is not a secret-looking
// literal in the source.
func secretLooking() string {
	return strings.Repeat("s", 2) + "ecret" + strings.Repeat("k", 6)
}

// padded surrounds s with whitespace, which the loader trims.
func padded(s string) string { return " " + s + "\t" }

// assertNoValue fails when the error text, its %+v form or its %#v form
// carries secret, or when it does not name the variable.
func assertNoValue(t *testing.T, err error, secret, key string) {
	t.Helper()

	if err == nil {
		t.Fatal("a refused value gave no error")
	}
	for _, form := range []struct{ name, text string }{
		{"Error()", err.Error()},
		{"%+v", fmt.Sprintf("%+v", err)},
		{"%#v", fmt.Sprintf("%#v", err)},
	} {
		if strings.Contains(form.text, secret) {
			t.Errorf("%s carries the raw value: %s", form.name, form.text)
		}
	}
	if key != "" && !strings.Contains(err.Error(), key) {
		t.Errorf("error text does not name the variable %s: %v", key, err)
	}
}

func TestParseErrorsKeepTheRawValueOut(t *testing.T) {
	t.Parallel()

	secret := secretLooking()

	t.Run("Bool", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"FLAG": secret}))
		l.Bool("FLAG", false)
		assertNoValue(t, l.Err(), secret, "FLAG")
	})

	t.Run("Int", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{secret, secret + "1", "1" + secret, "99999999999999999999" + secret} {
			l := New(env(map[string]string{"LIMIT": raw}))
			l.Int("LIMIT", 4, 1, 100)
			assertNoValue(t, l.Err(), secret, "LIMIT")
		}
	})

	t.Run("Duration", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{secret, secret + "d", secret + "s", "1h" + secret} {
			l := New(env(map[string]string{"TTL": raw}))
			l.Duration("TTL", time.Minute, time.Second, time.Hour)
			assertNoValue(t, l.Err(), secret, "TTL")
		}
	})

	t.Run("ParseDuration", func(t *testing.T) {
		t.Parallel()

		for _, raw := range []string{secret, secret + "d", secret + "s", "1h" + secret, "1.5d" + secret} {
			_, err := ParseDuration(raw)
			assertNoValue(t, err, secret, "")
		}
	})

	t.Run("a range error prints a parsed number, never the text", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"LIMIT": "500", "TTL": "1ms"}))
		l.Int("LIMIT", 4, 1, 100)
		l.Duration("TTL", time.Minute, time.Second, time.Hour)
		err := l.Err()
		if err == nil {
			t.Fatal("out of range values gave no error")
		}
		for _, want := range []string{"LIMIT", "TTL", "500", "1ms"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Err() = %v, does not mention %s", err, want)
			}
		}
	})
}

// TestParseDurationDecisions pins what the exported ParseDuration itself
// accepts and refuses, without the range of Loader.Duration in between. The
// values are those of the version before the error text changed.
func TestParseDurationDecisions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{input: "-1d", want: -24 * time.Hour},
		{input: "+1d", want: 24 * time.Hour},
		{input: "0d", want: 0},
		{input: "-0d", want: 0},
		{input: "-5s", want: -5 * time.Second},
		{input: ".5s", want: 500 * time.Millisecond},
		{input: "5s", want: 5 * time.Second},
		{input: "106751d", want: 106751 * 24 * time.Hour},
		{input: "+106751d", want: 106751 * 24 * time.Hour},
		{input: "-106751d", want: -106751 * 24 * time.Hour},
		{input: "106752d", wantErr: true},
		{input: "+106752d", wantErr: true},
		{input: "-106752d", wantErr: true},
		{input: " 1d", wantErr: true},
		{input: "1D", wantErr: true},
		{input: "1dd", wantErr: true},
		{input: "1d1h", wantErr: true},
		{input: "1h1d", wantErr: true},
		{input: "1.5d", wantErr: true},
		{input: "1_0d", wantErr: true},
		{input: "d", wantErr: true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDuration(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) = %s, want an error", tc.input, got)
				}
				if got != 0 {
					t.Errorf("ParseDuration(%q) = %s with an error, want 0", tc.input, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("ParseDuration(%q) = %s, %v; want %s, nil", tc.input, got, err, tc.want)
			}
		})
	}
}

// TestDayCountOverflow pins that a day count whose duration does not fit is
// refused with the fixed error text, never wrapped, and that neither the value
// nor the range text of strconv reaches the error.
func TestDayCountOverflow(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("9", 30)
	for _, raw := range []string{"106752d", "+106752d", "-106752d", huge + "d", "-" + huge + "d"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDuration(raw)
			if !errors.Is(err, errBadDuration) || got != 0 {
				t.Fatalf("ParseDuration(%q) = %s, %v; want 0 and the fixed error", raw, got, err)
			}
			for _, bad := range []string{"106752", huge, "range", "strconv"} {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("error %q contains %q", err, bad)
				}
			}

			l := New(env(map[string]string{"TTL": raw}))
			if got := l.Duration("TTL", time.Hour, -1<<63, 1<<63-1); got != time.Hour || l.Err() == nil {
				t.Fatalf("Duration(%q) = %s, %v; want the fallback and an error", raw, got, l.Err())
			}
			assertNoValue(t, l.Err(), strings.TrimSuffix(strings.TrimLeft(raw, "+-"), "d"), "TTL")
		})
	}
}

// TestParseAcceptedAndRefusedSets pins which inputs each typed reader takes
// and which it refuses, so a change to the error text cannot change a
// decision.
func TestParseAcceptedAndRefusedSets(t *testing.T) {
	t.Parallel()

	t.Run("Bool", func(t *testing.T) {
		t.Parallel()

		accepted := map[string]bool{
			"1": true, "t": true, "T": true, "TRUE": true, "true": true, "True": true,
			"0": false, "f": false, "F": false, "FALSE": false, "false": false, "False": false,
			padded("true"): true, padded("false"): false,
		}
		refused := []string{
			"yes", "no", "on", "off", "2", "tru", "y", "n", "truee", "-1", "0x1",
			"t r", "nil", "null", "none", "enabled", "TrUe", "fALSE", "01", "1.0",
			"ja", "+1", "truefalse", "ttrue",
		}
		for raw, want := range accepted {
			l := New(env(map[string]string{"K": raw}))
			if got := l.Bool("K", !want); got != want || l.Err() != nil {
				t.Errorf("Bool(%q) = %v, %v; want %v, nil", raw, got, l.Err(), want)
			}
		}
		for _, raw := range refused {
			l := New(env(map[string]string{"K": raw}))
			if got := l.Bool("K", true); !got || l.Err() == nil {
				t.Errorf("Bool(%q) = %v, %v; want the fallback and an error", raw, got, l.Err())
			}
		}
	})

	t.Run("Int", func(t *testing.T) {
		t.Parallel()

		accepted := map[string]int{
			"1": 1, "100": 100, "50": 50, "+5": 5, "007": 7, padded("8"): 8, "99": 99,
			"2": 2, "10": 10, "+100": 100, "0001": 1,
		}
		refused := []string{
			"0", "101", "-1", "eight", "1.5", "1e2", "0x10", "5 5", "1_0",
			"99999999999999999999", "--1", "+", "-", "1,5", "five", "1a", "a1",
			"100.0", "NaN", "Inf", "0b1", "++1", "+-1", "1-", "9223372036854775808",
			"-0", "-100",
		}
		for raw, want := range accepted {
			l := New(env(map[string]string{"K": raw}))
			if got := l.Int("K", 4, 1, 100); got != want || l.Err() != nil {
				t.Errorf("Int(%q) = %d, %v; want %d, nil", raw, got, l.Err(), want)
			}
		}
		for _, raw := range refused {
			l := New(env(map[string]string{"K": raw}))
			if got := l.Int("K", 4, 1, 100); got != 4 || l.Err() == nil {
				t.Errorf("Int(%q) = %d, %v; want the fallback and an error", raw, got, l.Err())
			}
		}
	})

	t.Run("Duration", func(t *testing.T) {
		t.Parallel()

		accepted := map[string]time.Duration{
			"1s": time.Second, "30s": 30 * time.Second, "5m": 5 * time.Minute,
			"7d": 7 * 24 * time.Hour, "1h30m": 90 * time.Minute, "720h": 720 * time.Hour,
			"30d": 30 * 24 * time.Hour, "1.5h": 90 * time.Minute, "1d": 24 * time.Hour,
			padded("2m"): 2 * time.Minute, "1000ms": time.Second, "+5s": 5 * time.Second,
			"1m1s": 61 * time.Second, "0001d": 24 * time.Hour, "2.5m": 150 * time.Second,
		}
		refused := []string{
			"1ms", "0d", "0s", "31d", "721h", "soon", "1.5d", "d", "5", "5x",
			"1 h", "-5s", "-1d", "1h-5m", "NaN", "forever", "1dd", "d1", "1e3s",
			"10000000d", "99999999999999999999d", "s", "1h 30m", "5M", "5D",
		}
		for raw, want := range accepted {
			l := New(env(map[string]string{"K": raw}))
			if got := l.Duration("K", time.Hour, time.Second, 30*24*time.Hour); got != want || l.Err() != nil {
				t.Errorf("Duration(%q) = %s, %v; want %s, nil", raw, got, l.Err(), want)
			}
		}
		for _, raw := range refused {
			l := New(env(map[string]string{"K": raw}))
			if got := l.Duration("K", time.Hour, time.Second, 30*24*time.Hour); got != time.Hour || l.Err() == nil {
				t.Errorf("Duration(%q) = %s, %v; want the fallback and an error", raw, got, l.Err())
			}
		}
	})
}
