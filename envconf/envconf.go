// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package envconf reads configuration from the environment and collects every
// problem into one error, so a misconfigured deployment reports all of its
// mistakes on the first start instead of one per restart.
//
// A loader is used once, in main, and the result is validated before anything
// listens. Nothing reads the environment later.
package envconf

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Loader reads values and remembers what was wrong.
type Loader struct {
	getenv   func(string) string
	problems []error
}

// New takes the environment lookup, which tests replace. A nil getenv reads
// the real process environment through os.Getenv.
func New(getenv func(string) string) *Loader {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Loader{getenv: getenv}
}

// Err is every problem found, joined, or nil. It is checked once after the
// whole config has been read.
func (l *Loader) Err() error {
	return errors.Join(l.problems...)
}

func (l *Loader) fail(key, format string, args ...any) {
	l.problems = append(l.problems, fmt.Errorf("envconf: %s: "+format, append([]any{key}, args...)...))
}

// raw is the trimmed value, empty when unset.
func (l *Loader) raw(key string) string {
	return strings.TrimSpace(l.getenv(key))
}

// String is the value or the fallback.
func (l *Loader) String(key, fallback string) string {
	if value := l.raw(key); value != "" {
		return value
	}
	return fallback
}

// Required is the value, and a problem when it is unset.
func (l *Loader) Required(key string) string {
	value := l.raw(key)
	if value == "" {
		l.fail(key, "is required")
	}
	return value
}

// Bool reads true and false. Anything else is a problem rather than a silent
// false, because a typo in a feature flag is otherwise invisible.
func (l *Loader) Bool(key string, fallback bool) bool {
	raw := l.raw(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		l.fail(key, "must be true or false, got %q", raw)
		return fallback
	}
	return value
}

// Int reads a whole number and checks the range, which is what keeps a limit
// from being set to zero or to a million.
func (l *Loader) Int(key string, fallback, minimum, maximum int) int {
	raw := l.raw(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		l.fail(key, "must be a whole number, got %q", raw)
		return fallback
	}
	if value < minimum || value > maximum {
		l.fail(key, "must be between %d and %d, got %d", minimum, maximum, value)
		return fallback
	}
	return value
}

// Duration reads a Go duration and also whole days, so a token lifetime can be
// written as 7d.
func (l *Loader) Duration(key string, fallback, minimum, maximum time.Duration) time.Duration {
	raw := l.raw(key)
	if raw == "" {
		return fallback
	}
	value, err := ParseDuration(raw)
	if err != nil {
		l.fail(key, "must be a duration such as 30s, 5m or 7d, got %q", raw)
		return fallback
	}
	if value < minimum || value > maximum {
		l.fail(key, "must be between %s and %s, got %s", minimum, maximum, value)
		return fallback
	}
	return value
}

// URL reads an absolute http or https URL without query, fragment or
// credentials, and drops a trailing slash so joining a path is predictable.
func (l *Loader) URL(key, fallback string) *url.URL {
	raw := l.String(key, fallback)
	if raw == "" {
		l.fail(key, "is required")
		return nil
	}
	parsed, err := url.Parse(raw)
	switch {
	case err != nil:
		l.fail(key, "must be a URL, got %q", raw)
		return nil
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		l.fail(key, "must start with http:// or https://, got %q", raw)
		return nil
	case parsed.Host == "":
		l.fail(key, "must contain a host, got %q", raw)
		return nil
	case parsed.User != nil:
		l.fail(key, "must not contain credentials")
		return nil
	case parsed.RawQuery != "" || parsed.Fragment != "":
		l.fail(key, "must not contain a query or a fragment, got %q", raw)
		return nil
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed
}

// List reads a comma separated list, dropping empty entries.
func (l *Loader) List(key string) []string {
	var out []string
	for _, part := range strings.Split(l.getenv(key), ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Secret reads the file the variable points at, which is how a secret reaches
// the process: an injected file, never an environment value that shows up in
// every "kubectl describe". An empty file is a problem, because running with
// an empty signing key is worse than not starting.
func (l *Loader) Secret(key string) string {
	path := l.Required(key)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		l.fail(key, "cannot read %s: %w", path, err)
		return ""
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		l.fail(key, "%s is empty", path)
	}
	return secret
}

// secretOrFile is the twin contract shared by SecretOrFile and
// RequiredSecretOrFile: a direct value, or key_FILE naming a path, never
// both.
func (l *Loader) secretOrFile(key string) (string, error) {
	direct := l.raw(key)
	filePath := l.raw(key + "_FILE")
	if direct != "" && filePath != "" {
		return "", fmt.Errorf("%s and %s are both set, set only one", key, key+"_FILE")
	}
	if filePath != "" {
		raw, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", filePath, err)
		}
		secret := strings.TrimRight(string(raw), "\r\n")
		if secret == "" {
			return "", fmt.Errorf("%s is empty", filePath)
		}
		return secret, nil
	}
	return direct, nil
}

// SecretOrFile reads key directly or from the path named by key_FILE, never
// both: a secret manager, an injected file, a compose .env and a developer's
// shell all work without the application knowing which it is. A file that
// resolves to an empty value is a problem, because running with an empty
// secret is worse than not starting. Neither set is not a problem here; it
// returns "".
func (l *Loader) SecretOrFile(key string) string {
	value, err := l.secretOrFile(key)
	if err != nil {
		l.problems = append(l.problems, fmt.Errorf("envconf: %s: %w", key, err))
		return ""
	}
	return value
}

// RequiredSecretOrFile is SecretOrFile, and a problem when neither key nor
// key_FILE is set.
func (l *Loader) RequiredSecretOrFile(key string) string {
	value := l.SecretOrFile(key)
	if l.raw(key) == "" && l.raw(key+"_FILE") == "" {
		l.fail(key, "or %s_FILE: is required", key)
	}
	return value
}

// ParseDuration reads a Go duration, plus a whole number of days with a d
// suffix, which time.ParseDuration does not support.
func ParseDuration(raw string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("envconf: parse days %q: %w", raw, err)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("envconf: parse duration %q: %w", raw, err)
	}
	return value, nil
}
