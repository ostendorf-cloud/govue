// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package envconf

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ostendorf-cloud/govue/internal/configtext/configtexttest"
)

// env makes a lookup out of a map, as the process environment would answer.
func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestString(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		values   map[string]string
		key      string
		fallback string
		want     string
	}{
		{name: "set value is trimmed", values: map[string]string{"SET": " value "}, key: "SET", fallback: "fallback", want: "value"},
		{name: "missing key returns the fallback", values: nil, key: "MISSING", fallback: "fallback", want: "fallback"},
		{name: "blank value returns the fallback", values: map[string]string{"BLANK": "   "}, key: "BLANK", fallback: "fallback", want: "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := New(env(tc.values))
			if got := l.String(tc.key, tc.fallback); got != tc.want {
				t.Errorf("String(%q, %q) = %q, want %q", tc.key, tc.fallback, got, tc.want)
			}
			if err := l.Err(); err != nil {
				t.Fatalf("Err() = %v, want nil", err)
			}
		})
	}
}

func TestRequired(t *testing.T) {
	t.Parallel()

	t.Run("set value is returned without a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"SET": " value "}))
		if got := l.Required("SET"); got != "value" {
			t.Errorf("Required(SET) = %q", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("missing value is a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(nil))
		if got := l.Required("MISSING"); got != "" {
			t.Errorf("Required(MISSING) = %q, want empty", got)
		}
		if err := l.Err(); err == nil || !strings.Contains(err.Error(), "MISSING: is required") {
			t.Errorf("Err() = %v", err)
		}
	})
}

func TestBool(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		values   map[string]string
		key      string
		fallback bool
		want     bool
		wantErr  bool
	}{
		{name: "true", values: map[string]string{"KEY": "true"}, key: "KEY", fallback: false, want: true},
		{name: "false", values: map[string]string{"KEY": "false"}, key: "KEY", fallback: true, want: false},
		{name: "one is true", values: map[string]string{"KEY": "1"}, key: "KEY", fallback: false, want: true},
		{name: "missing key returns the fallback", values: nil, key: "MISSING", fallback: true, want: true},
		{name: "junk is a problem", values: map[string]string{"KEY": "yesplease"}, key: "KEY", fallback: false, want: false, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := New(env(tc.values))
			got := l.Bool(tc.key, tc.fallback)
			if got != tc.want {
				t.Errorf("Bool(%q, %v) = %v, want %v", tc.key, tc.fallback, got, tc.want)
			}
			err := l.Err()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), tc.key) {
					t.Errorf("Err() = %v, want a problem mentioning %s", err, tc.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("Err() = %v, want nil", err)
			}
		})
	}
}

func TestInt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		values  map[string]string
		key     string
		want    int
		wantErr bool
	}{
		{name: "in range", values: map[string]string{"KEY": "8"}, key: "KEY", want: 8},
		{name: "missing key returns the fallback", values: nil, key: "MISSING", want: 4},
		{name: "below the minimum is a problem", values: map[string]string{"KEY": "0"}, key: "KEY", want: 4, wantErr: true},
		{name: "above the maximum is a problem", values: map[string]string{"KEY": "500"}, key: "KEY", want: 4, wantErr: true},
		{name: "not a number is a problem", values: map[string]string{"KEY": "eight"}, key: "KEY", want: 4, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := New(env(tc.values))
			got := l.Int(tc.key, 4, 1, 100)
			if got != tc.want {
				t.Errorf("Int(%q, 4, 1, 100) = %d, want %d", tc.key, got, tc.want)
			}
			err := l.Err()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), tc.key) {
					t.Errorf("Err() = %v, want a problem mentioning %s", err, tc.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("Err() = %v, want nil", err)
			}
		})
	}
}

func TestInt_accumulatesEveryProblem(t *testing.T) {
	t.Parallel()

	l := New(env(map[string]string{"LOW": "0", "HIGH": "500", "JUNK": "eight"}))
	l.Int("LOW", 4, 1, 100)
	l.Int("HIGH", 4, 1, 100)
	l.Int("JUNK", 4, 1, 100)

	err := l.Err()
	if err == nil {
		t.Fatal("three bad values were accepted")
	}
	for _, want := range []string{"LOW", "HIGH", "JUNK"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Err() = %v, does not mention %s", err, want)
		}
	}
}

// A number or a duration outside its range is a refused value and is never
// shown, neither as written nor as Go prints it: a numeric secret pasted into
// the wrong variable would otherwise reach the log. The bounds are shown.
func TestRangeErrorsKeepTheValueOut(t *testing.T) {
	t.Parallel()

	t.Run("Int", func(t *testing.T) {
		t.Parallel()
		l := New(env(map[string]string{"PORT": "987654321"}))
		l.Int("PORT", 4, 1, 65535)
		configtexttest.AssertHidden(t, l.Err(), "987654321", "PORT: must be between 1 and 65535")
	})
	t.Run("Duration", func(t *testing.T) {
		t.Parallel()
		l := New(env(map[string]string{"TIMEOUT": "98765h"}))
		l.Duration("TIMEOUT", time.Minute, time.Second, time.Hour)
		err := l.Err()
		configtexttest.AssertHidden(t, err, "98765h", "TIMEOUT: must be between 1s and 1h0m0s")
		configtexttest.AssertHidden(t, err, "98765h0m0s", "TIMEOUT")
	})
}

func TestDuration(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		values  map[string]string
		key     string
		minimum time.Duration
		maximum time.Duration
		want    time.Duration
		wantErr bool
	}{
		{name: "seconds", values: map[string]string{"KEY": "30s"}, key: "KEY", minimum: time.Second, maximum: time.Hour, want: 30 * time.Second},
		{name: "days", values: map[string]string{"KEY": "7d"}, key: "KEY", minimum: time.Hour, maximum: 30 * 24 * time.Hour, want: 7 * 24 * time.Hour},
		{name: "missing key returns the fallback", values: nil, key: "MISSING", minimum: time.Second, maximum: time.Hour, want: time.Minute},
		{name: "below the minimum is a problem", values: map[string]string{"KEY": "1ms"}, key: "KEY", minimum: time.Second, maximum: time.Hour, want: time.Minute, wantErr: true},
		{name: "not a duration is a problem", values: map[string]string{"KEY": "soon"}, key: "KEY", minimum: time.Second, maximum: time.Hour, want: time.Minute, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := New(env(tc.values))
			got := l.Duration(tc.key, time.Minute, tc.minimum, tc.maximum)
			if got != tc.want {
				t.Errorf("Duration(%q, ...) = %s, want %s", tc.key, got, tc.want)
			}
			err := l.Err()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), tc.key) {
					t.Errorf("Err() = %v, want a problem mentioning %s", err, tc.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("Err() = %v, want nil", err)
			}
		})
	}
}

func TestURL(t *testing.T) {
	t.Parallel()

	t.Run("valid URLs", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name       string
			values     map[string]string
			key        string
			fallback   string
			wantString string
			wantHost   string
		}{
			{name: "trailing slash is trimmed", values: map[string]string{"KEY": "https://www.example.test/"}, key: "KEY", wantString: "https://www.example.test"},
			{name: "http with a port", values: map[string]string{"KEY": "http://localhost:8080"}, key: "KEY", wantHost: "localhost:8080"},
			{name: "missing key returns the fallback", values: nil, key: "MISSING", fallback: "https://fallback.example.test", wantHost: "fallback.example.test"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				l := New(env(tc.values))
				got := l.URL(tc.key, tc.fallback)
				if got == nil {
					t.Fatalf("URL(%q, %q) = nil", tc.key, tc.fallback)
				}
				if tc.wantString != "" && got.String() != tc.wantString {
					t.Errorf("URL(%q, %q).String() = %q, want %q", tc.key, tc.fallback, got.String(), tc.wantString)
				}
				if tc.wantHost != "" && got.Host != tc.wantHost {
					t.Errorf("URL(%q, %q).Host = %q, want %q", tc.key, tc.fallback, got.Host, tc.wantHost)
				}
				if err := l.Err(); err != nil {
					t.Fatalf("Err() = %v, want nil", err)
				}
			})
		}
	})

	t.Run("invalid URLs are a problem", func(t *testing.T) {
		t.Parallel()

		for key, raw := range map[string]string{
			"NO_SCHEME":   "www.example.test",
			"FTP":         "ftp://files.example.test",
			"NO_HOST":     "https:///path",
			"CREDENTIALS": "https://user:pw@www.example.test",
			"QUERY":       "https://www.example.test/?a=b",
		} {
			t.Run(key, func(t *testing.T) {
				t.Parallel()

				l := New(env(map[string]string{key: raw}))
				if got := l.URL(key, ""); got != nil {
					t.Errorf("URL(%s=%q) = %v, want nil", key, raw, got)
				}
				if l.Err() == nil {
					t.Errorf("URL(%s=%q) was accepted", key, raw)
				}
			})
		}
	})

	t.Run("a refused value never reaches the error text", func(t *testing.T) {
		t.Parallel()

		// Built at run time so the constant is not a secret-looking literal.
		secret := strings.Repeat("s", 2) + "ecret"
		for _, tc := range []struct{ name, raw string }{
			{"scheme that is not http or https", "s3://user:" + secret + "@host"},
			{"bad escape in the host", "http://user:" + secret + "@ho%zzst/"},
			{"bad escape in the path", "http://user:" + secret + "@host/%zz"},
			{"bad escape in the user info", "http://user:" + secret + "%zz@host"},
			{"user info is refused", "https://user:" + secret + "@www.example.test"},
			{"no host", "https://user:" + secret + "@/path"},
			{"query", "https://www.example.test/?token=" + secret},
			{"fragment", "https://www.example.test/#" + secret},
			{"no scheme", secret + "@www.example.test"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				l := New(env(map[string]string{"S3_ENDPOINT": tc.raw}))
				if got := l.URL("S3_ENDPOINT", ""); got != nil {
					t.Fatalf("URL = %v, want nil", got)
				}
				err := l.Err()
				if err == nil {
					t.Fatal("a refused value gave no error")
				}
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error text carries the raw value: %v", err)
				}
				if !strings.Contains(err.Error(), "S3_ENDPOINT") {
					t.Errorf("error text does not name the variable: %v", err)
				}
			})
		}
	})

	t.Run("a fallback is checked the same way", func(t *testing.T) {
		t.Parallel()

		secret := strings.Repeat("s", 2) + "ecret"
		l := New(env(nil))
		if got := l.URL("S3_ENDPOINT", "http://user:"+secret+"%zz@host"); got != nil {
			t.Fatalf("URL = %v, want nil", got)
		}
		err := l.Err()
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("Err() = %v, want an error without the raw value", err)
		}
		if !strings.Contains(err.Error(), "S3_ENDPOINT") {
			t.Errorf("error text does not name the variable: %v", err)
		}
	})

	t.Run("a valid URL without user info is accepted", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"S3_ENDPOINT": "https://s3.example.test/bucket/"}))
		got := l.URL("S3_ENDPOINT", "")
		if got == nil || got.String() != "https://s3.example.test/bucket" {
			t.Fatalf("URL = %v", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v", err)
		}
	})

	t.Run("an empty value is a problem that names the variable", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"S3_ENDPOINT": "  "}))
		if got := l.URL("S3_ENDPOINT", ""); got != nil {
			t.Fatalf("URL = %v, want nil", got)
		}
		if err := l.Err(); err == nil || !strings.Contains(err.Error(), "S3_ENDPOINT") {
			t.Errorf("Err() = %v, want the variable name", err)
		}
	})

	t.Run("missing without a fallback is a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(nil))
		if got := l.URL("REQUIRED", ""); got != nil {
			t.Errorf("URL(REQUIRED) = %v, want nil", got)
		}
		if l.Err() == nil {
			t.Error("a URL without a value and without a fallback was accepted")
		}
	})
}

func TestList(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		values map[string]string
		key    string
		want   []string
	}{
		{name: "entries are trimmed and empty entries dropped", values: map[string]string{"KEY": " a , b ,, "}, key: "KEY", want: []string{"a", "b"}},
		{name: "missing key is nil", values: nil, key: "MISSING", want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := New(env(tc.values))
			got := l.List(tc.key)
			if len(got) != len(tc.want) {
				t.Fatalf("List(%q) = %q, want %q", tc.key, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("List(%q)[%d] = %q, want %q", tc.key, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSecret(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "session.key")
	if err := os.WriteFile(good, []byte("  s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	blank := filepath.Join(dir, "blank.key")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("file content is trimmed", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY_FILE": good}))
		if got := l.Secret("KEY_FILE"); got != "s3cret" {
			t.Errorf("Secret(KEY_FILE) = %q", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("missing file is a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY_FILE": filepath.Join(dir, "nope.key")}))
		if got := l.Secret("KEY_FILE"); got != "" {
			t.Errorf("Secret(KEY_FILE) = %q, want empty", got)
		}
		if l.Err() == nil {
			t.Error("a missing secret file was accepted")
		}
	})

	t.Run("empty file is a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY_FILE": blank}))
		if got := l.Secret("KEY_FILE"); got != "" {
			t.Errorf("Secret(KEY_FILE) = %q, want empty", got)
		}
		if l.Err() == nil {
			t.Error("an empty secret file was accepted")
		}
	})

	t.Run("missing variable is a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(nil))
		l.Secret("KEY_FILE")
		if l.Err() == nil {
			t.Error("a missing secret variable was accepted")
		}
	})
}

// The path of a secret file is never shown: an operator may paste the secret
// itself into the variable. Each failure names the variable and its kind, and
// a missing file and a refused read stay matchable with errors.Is.
func TestSecretFileErrorsKeepThePathOut(t *testing.T) {
	t.Parallel()

	const token = "s3cr3tTOKEN"
	dir := filepath.Join(t.TempDir(), token)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.key")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked.key")
	if err := os.WriteFile(locked, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, lockedErr := os.ReadFile(locked)
	lockedRefused := errors.Is(lockedErr, fs.ErrPermission)

	for _, tc := range []struct {
		name, path, want string
		is               error
		skip             bool
	}{
		{"missing, a pasted secret", token, "cannot read the file", fs.ErrNotExist, false},
		{"a directory", dir, "cannot read the file: it is not a readable file", nil, false},
		{"permission denied", locked, "cannot read the file: permission denied", fs.ErrPermission, !lockedRefused},
		{"empty", empty, "the file is empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.skip {
				t.Skip("the file is readable here, such as for root")
			}
			for _, read := range []struct {
				name string
				run  func(*Loader)
				env  map[string]string
				want string
			}{
				{"Secret", func(l *Loader) { l.Secret("SESSION_KEY") }, map[string]string{"SESSION_KEY": tc.path}, "SESSION_KEY: " + tc.want},
				{"SecretOrFile", func(l *Loader) { l.SecretOrFile("CONTACT_POW_KEY") }, map[string]string{"CONTACT_POW_KEY_FILE": tc.path}, "CONTACT_POW_KEY_FILE: " + tc.want},
				{"RequiredSecretOrFile", func(l *Loader) { l.RequiredSecretOrFile("DB_PASSWORD") }, map[string]string{"DB_PASSWORD_FILE": tc.path}, "DB_PASSWORD_FILE: " + tc.want},
			} {
				t.Run(read.name, func(t *testing.T) {
					t.Parallel()
					l := New(env(read.env))
					read.run(l)
					err := l.Err()
					configtexttest.AssertHidden(t, err, token, read.want)
					if tc.is != nil && !errors.Is(err, tc.is) {
						t.Errorf("Err() = %v, want it to wrap %v", err, tc.is)
					}
				})
			}
		})
	}
}

// joinedErrors counts the errors an errors.Join result unwraps to, or 0 for
// nil.
func joinedErrors(err error) int {
	if err == nil {
		return 0
	}
	unwrapper, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return 1
	}
	return len(unwrapper.Unwrap())
}

func TestSecretOrFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "session.key")
	if err := os.WriteFile(good, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	blank := filepath.Join(dir, "blank.key")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("value set via the plain variable", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY": "s3cret"}))
		if got := l.SecretOrFile("KEY"); got != "s3cret" {
			t.Errorf("SecretOrFile(KEY) = %q", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("value set via the _FILE variable", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY_FILE": good}))
		if got := l.SecretOrFile("KEY"); got != "s3cret" {
			t.Errorf("SecretOrFile(KEY) = %q", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("both set is a problem naming both variables", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY": "s3cret", "KEY_FILE": good}))
		if got := l.SecretOrFile("KEY"); got != "" {
			t.Errorf("SecretOrFile(KEY) = %q, want empty", got)
		}
		err := l.Err()
		if err == nil || !strings.Contains(err.Error(), "KEY") || !strings.Contains(err.Error(), "KEY_FILE") {
			t.Errorf("Err() = %v, want a problem naming KEY and KEY_FILE", err)
		}
	})

	t.Run("neither set is not a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(nil))
		if got := l.SecretOrFile("KEY"); got != "" {
			t.Errorf("SecretOrFile(KEY) = %q, want empty", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("a _FILE that reads back empty is a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY_FILE": blank}))
		if got := l.SecretOrFile("KEY"); got != "" {
			t.Errorf("SecretOrFile(KEY) = %q, want empty", got)
		}
		if l.Err() == nil {
			t.Error("an empty secret file was accepted")
		}
	})

	t.Run("a missing _FILE path is a read error that names the variable, not the path", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(dir, "nope.key")
		l := New(env(map[string]string{"KEY_FILE": path}))
		if got := l.SecretOrFile("KEY"); got != "" {
			t.Errorf("SecretOrFile(KEY) = %q, want empty", got)
		}
		err := l.Err()
		if err == nil || !strings.Contains(err.Error(), "KEY_FILE: cannot read the file") || strings.Contains(err.Error(), path) {
			t.Errorf("Err() = %v, want a read error naming KEY_FILE and not %s", err, path)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Err() = %v, want it to wrap fs.ErrNotExist", err)
		}
		if errors.As(err, new(*fs.PathError)) {
			t.Errorf("Err() = %v wraps the *fs.PathError, which quotes the path", err)
		}
	})

	t.Run("a secret with a trailing tab resolves the same from the environment and from a file", func(t *testing.T) {
		t.Parallel()

		const raw = "s3cret\t"
		withTab := filepath.Join(dir, "with-tab.key")
		if err := os.WriteFile(withTab, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}

		direct := New(env(map[string]string{"KEY": raw}))
		fromFile := New(env(map[string]string{"KEY_FILE": withTab}))

		gotDirect := direct.SecretOrFile("KEY")
		gotFile := fromFile.SecretOrFile("KEY")

		if gotDirect != "s3cret" {
			t.Errorf("SecretOrFile(KEY) = %q, want %q", gotDirect, "s3cret")
		}
		if gotDirect != gotFile {
			t.Errorf("SecretOrFile(KEY) = %q from the environment, %q from a file, want the same value", gotDirect, gotFile)
		}
		if err := direct.Err(); err != nil {
			t.Fatalf("Err() (environment) = %v, want nil", err)
		}
		if err := fromFile.Err(); err != nil {
			t.Fatalf("Err() (file) = %v, want nil", err)
		}
	})

	t.Run("a _FILE containing only whitespace is a problem, not a secret", func(t *testing.T) {
		t.Parallel()

		onlyWhitespace := filepath.Join(dir, "only-whitespace.key")
		if err := os.WriteFile(onlyWhitespace, []byte(" \t"), 0o600); err != nil {
			t.Fatal(err)
		}

		l := New(env(map[string]string{"KEY_FILE": onlyWhitespace}))
		if got := l.SecretOrFile("KEY"); got != "" {
			t.Errorf("SecretOrFile(KEY) = %q, want empty", got)
		}
		err := l.Err()
		if err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Errorf("Err() = %v, want a problem saying the file is empty", err)
		}
	})

	t.Run("a _FILE with leading whitespace around the secret resolves to the trimmed secret", func(t *testing.T) {
		t.Parallel()

		leadingSpace := filepath.Join(dir, "leading-space.key")
		if err := os.WriteFile(leadingSpace, []byte("  s3cret\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		l := New(env(map[string]string{"KEY_FILE": leadingSpace}))
		if got := l.SecretOrFile("KEY"); got != "s3cret" {
			t.Errorf("SecretOrFile(KEY) = %q, want %q", got, "s3cret")
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("a _FILE with a multi-line secret keeps the interior newline", func(t *testing.T) {
		t.Parallel()

		const want = "line one\nline two"
		multiLine := filepath.Join(dir, "multi-line.key")
		if err := os.WriteFile(multiLine, []byte("\n"+want+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		l := New(env(map[string]string{"KEY_FILE": multiLine}))
		if got := l.SecretOrFile("KEY"); got != want {
			t.Errorf("SecretOrFile(KEY) = %q, want %q", got, want)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})
}

func TestRequiredSecretOrFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blank := filepath.Join(dir, "blank.key")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("neither set is a problem saying it is required", func(t *testing.T) {
		t.Parallel()

		l := New(env(nil))
		if got := l.RequiredSecretOrFile("KEY"); got != "" {
			t.Errorf("RequiredSecretOrFile(KEY) = %q, want empty", got)
		}
		err := l.Err()
		if err == nil || !strings.Contains(err.Error(), "is required") {
			t.Errorf("Err() = %v, want a problem saying it is required", err)
		}
	})

	t.Run("an empty _FILE is a problem, not a duplicate required problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY_FILE": blank}))
		if got := l.RequiredSecretOrFile("KEY"); got != "" {
			t.Errorf("RequiredSecretOrFile(KEY) = %q, want empty", got)
		}
		err := l.Err()
		if err == nil {
			t.Fatal("an empty secret file was accepted")
		}
		if strings.Contains(err.Error(), "is required") {
			t.Errorf("Err() = %v, want no duplicate is-required problem", err)
		}
		if got := joinedErrors(err); got != 1 {
			t.Errorf("Err() joins %d problems, want exactly 1", got)
		}
	})

	t.Run("a value set via the plain variable is not a problem", func(t *testing.T) {
		t.Parallel()

		l := New(env(map[string]string{"KEY": "s3cret"}))
		if got := l.RequiredSecretOrFile("KEY"); got != "s3cret" {
			t.Errorf("RequiredSecretOrFile(KEY) = %q", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("a value set via _FILE is not a problem", func(t *testing.T) {
		t.Parallel()

		good := filepath.Join(dir, "good.key")
		if err := os.WriteFile(good, []byte("s3cret\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		l := New(env(map[string]string{"KEY_FILE": good}))
		if got := l.RequiredSecretOrFile("KEY"); got != "s3cret" {
			t.Errorf("RequiredSecretOrFile(KEY) = %q", got)
		}
		if err := l.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})

	t.Run("both set is a problem, not a duplicate required problem", func(t *testing.T) {
		t.Parallel()

		good := filepath.Join(dir, "good2.key")
		if err := os.WriteFile(good, []byte("s3cret\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		l := New(env(map[string]string{"KEY": "s3cret", "KEY_FILE": good}))
		if got := l.RequiredSecretOrFile("KEY"); got != "" {
			t.Errorf("RequiredSecretOrFile(KEY) = %q, want empty", got)
		}
		err := l.Err()
		if err == nil {
			t.Fatal("setting both was accepted")
		}
		if strings.Contains(err.Error(), "is required") {
			t.Errorf("Err() = %v, want no duplicate is-required problem", err)
		}
		if got := joinedErrors(err); got != 1 {
			t.Errorf("Err() joins %d problems, want exactly 1", got)
		}
	})
}

func TestParseDuration(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "seconds", input: "30s", want: 30 * time.Second},
		{name: "minutes", input: "5m", want: 5 * time.Minute},
		{name: "mixed units", input: "1h30m", want: 90 * time.Minute},
		{name: "one day", input: "1d", want: 24 * time.Hour},
		{name: "many days", input: "60d", want: 60 * 24 * time.Hour},
		{name: "zero days", input: "0d", want: 0},
		{name: "not a duration", input: "soon", wantErr: true},
		{name: "fractional days are not whole numbers", input: "1.5d", wantErr: true},
		{name: "empty string", input: "", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDuration(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) = %s, want an error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ParseDuration(%q) = %s, want %s", tc.input, got, tc.want)
			}
		})
	}
}

func TestNewWithoutALookupReadsTheProcessEnvironment(t *testing.T) {
	t.Setenv("GOVUE_TEST_VALUE", "from the process")

	if got := New(nil).String("GOVUE_TEST_VALUE", ""); got != "from the process" {
		t.Errorf("String(GOVUE_TEST_VALUE) = %q, want the value t.Setenv set", got)
	}
}
