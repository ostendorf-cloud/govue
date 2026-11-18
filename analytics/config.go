// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ostendorf-cloud/govue/envconf"
)

// The environment variables LoadConfig reads.
const (
	// EnvURL is the base URL of the Umami instance.
	EnvURL = "UMAMI_URL"
	// EnvWebsiteID is the lowercase UUID Umami names the website by.
	EnvWebsiteID = "UMAMI_WEBSITE_ID"
	// EnvTimeout bounds one post to Umami.
	EnvTimeout = "UMAMI_TIMEOUT"
	// EnvQueueSize bounds the views waiting to be sent.
	EnvQueueSize = "UMAMI_QUEUE_SIZE"
)

const (
	// DefaultTimeout is the bound of one post to Umami.
	DefaultTimeout = 5 * time.Second
	// DefaultQueueSize is the number of views that may wait for a worker.
	DefaultQueueSize = 256

	minTimeout   = time.Second
	maxTimeout   = 30 * time.Second
	maxQueueSize = 10_000
)

// websiteIDPattern is the lowercase UUID Umami names a website by. Anything
// else is a copied wrong value, and counting would silently go nowhere.
var websiteIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Config is the connection to one Umami website.
//
// The zero value is a valid configuration that counts nothing. Both URL and
// WebsiteID set turns counting on. Exactly one of them set is an error, so a
// deployment that meant to count and typed only half cannot run silently
// without statistics.
type Config struct {
	// URL is the base URL of Umami, for example
	// https://umami.example.test. It carries no credentials, no query and no
	// fragment. A path is kept, for an instance served under one.
	URL string
	// WebsiteID is the lowercase UUID of the website in Umami.
	WebsiteID string
	// Timeout bounds one post. Zero means DefaultTimeout.
	Timeout time.Duration
	// QueueSize is how many views may wait for a worker. Zero means
	// DefaultQueueSize.
	QueueSize int
}

// LoadConfig reads the configuration through getenv, so a test passes a map
// lookup. A nil getenv reads the process environment. Every problem is
// returned together. No error repeats the URL, since an operator may have put
// a secret into it.
func LoadConfig(getenv func(string) string) (Config, error) {
	l := envconf.New(getenv)
	cfg := Config{
		URL:       strings.TrimRight(l.String(EnvURL, ""), "/"),
		WebsiteID: l.String(EnvWebsiteID, ""),
		Timeout:   l.Duration(EnvTimeout, DefaultTimeout, minTimeout, maxTimeout),
		QueueSize: l.Int(EnvQueueSize, DefaultQueueSize, 1, maxQueueSize),
	}
	if err := errors.Join(l.Err(), cfg.Validate()); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Enabled reports whether views are counted: both URL and WebsiteID are set.
func (c Config) Enabled() bool {
	return c.URL != "" && c.WebsiteID != ""
}

// Validate returns every problem of c at once, or nil. A message never holds
// the configured URL.
func (c Config) Validate() error {
	var problems []error
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf("analytics: "+format, args...))
	}

	switch {
	case c.URL == "" && c.WebsiteID == "":
	case c.URL == "" || c.WebsiteID == "":
		fail("%s and %s must be set together or not at all", EnvURL, EnvWebsiteID)
	}
	if c.URL != "" {
		if reason := checkURL(c.URL); reason != "" {
			fail("%s %s", EnvURL, reason)
		}
	}
	if c.WebsiteID != "" && !websiteIDPattern.MatchString(c.WebsiteID) {
		fail("%s must be the lowercase UUID of the website in Umami", EnvWebsiteID)
	}
	if c.Timeout != 0 && (c.Timeout < minTimeout || c.Timeout > maxTimeout) {
		fail("%s must be between %s and %s", EnvTimeout, minTimeout, maxTimeout)
	}
	if c.QueueSize < 0 || c.QueueSize > maxQueueSize {
		fail("%s must be between 1 and %d", EnvQueueSize, maxQueueSize)
	}
	return errors.Join(problems...)
}

// checkURL returns why raw is not usable as the Umami base URL, or "". The
// reason never quotes raw, and the parse error is dropped because net/url
// repeats the whole input in it.
func checkURL(raw string) string {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return "must be a URL"
	case u.Scheme != "http" && u.Scheme != "https":
		return "must start with https://"
	case u.Hostname() == "":
		return "must contain a host"
	case u.User != nil:
		return "must not contain credentials"
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "":
		return "must not contain a query or a fragment"
	case u.Scheme == "http" && !trustedPlainHost(u.Hostname()):
		return "must use https unless the host is localhost, a loopback address or inside the cluster"
	}
	return ""
}

// trustedPlainHost reports whether visitor addresses may travel over plain
// HTTP to host: the machine itself, or a name only a cluster resolves, which
// is a single label such as "umami" or ends in .svc or .svc.cluster.local.
func trustedPlainHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.IsLoopback()
	}
	return !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".svc") ||
		strings.HasSuffix(host, ".svc.cluster.local")
}
