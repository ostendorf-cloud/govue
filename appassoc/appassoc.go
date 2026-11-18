// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package appassoc serves the two files that tie a native app to a web
// domain: the Apple App Site Association file and the Android Digital Asset
// Links file.
//
// Both files are built once, at construction, from a typed Config with
// encoding/json. A value is never spliced into a string, so a hostile value
// cannot break the document. Config is validated at the same time: the shape
// of a team id, a bundle id, a package name and a certificate fingerprint, the
// path list and the size of the result. A bad configuration fails New with
// every problem at once.
//
// A file with no entries is not mounted and answers 404 through whatever the
// application serves for an unknown path. It is never served as an empty
// document, because an empty document would tell a phone that the domain
// trusts no app, which is a different statement from having nothing to say.
// New refuses a Config that configures neither file.
//
// The files are public. They carry identifiers an app already ships with and
// a certificate fingerprint, which is a public value. They must never carry a
// secret, a token, an account name or any other value taken from a request.
// This package takes nothing from a request except the method.
//
// The handlers answer GET and HEAD only and answer 405 with an Allow header
// to every other method. They set application/json, a fixed Cache-Control, no
// cookie and no redirect. iOS and Android both refuse a redirect, so the
// paths are exact patterns. A path with a trailing slash is not the file and
// is not redirected to it. The handlers read no session and no credential, so
// a request with a bad Authorization header still gets the file, and they
// belong outside any middleware that authenticates, redirects or sets a
// cookie. Handle takes the middleware to wrap them with, nil for none.
//
// The webcredentials entry of the Apple file is what a passkey in a native app
// needs. Whether the app origin also belongs in the relying party's allowed
// origins is decided with the passkey step, not here.
package appassoc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// The exact paths of the two files. Both live under /.well-known and neither
// redirects.
const (
	ApplePath   = "/.well-known/apple-app-site-association"
	AndroidPath = "/.well-known/assetlinks.json"
)

// CacheControl is the Cache-Control value of both files. The files change
// only with a release of the app, and both platforms keep their own copy, so
// an hour in a shared cache costs nothing and spares the origin a poll.
const CacheControl = "public, max-age=3600"

// Limits on a configuration. They keep the document far below the 128 KB
// Apple allows for the Apple file.
const (
	maxApps         = 20
	maxPaths        = 100
	maxPathLength   = 256
	maxFingerprints = 10
	maxDocument     = 64 << 10
)

// Sentinel errors, to be matched with errors.Is.
var (
	// ErrEmpty means the Config configures neither file.
	ErrEmpty = errors.New("no file is configured")
	// ErrInvalid means a value in the Config does not have the required
	// shape or exceeds a limit.
	ErrInvalid = errors.New("invalid configuration")
)

// Relation is a Digital Asset Links relation an Android app is granted.
type Relation string

const (
	// RelationHandleAllURLs lets the app open the domain's links.
	RelationHandleAllURLs Relation = "delegate_permission/common.handle_all_urls"
	// RelationGetLoginCreds lets the app use the domain's saved credentials,
	// which a passkey needs.
	RelationGetLoginCreds Relation = "delegate_permission/common.get_login_creds"
)

// Config is the content of both files. A zero part is not served.
type Config struct {
	Apple   AppleConfig
	Android AndroidConfig
}

// IsEmpty reports whether the Config configures no file at all.
func (c Config) IsEmpty() bool {
	return c.Apple.isEmpty() && c.Android.isEmpty()
}

// AppleConfig is the content of the Apple App Site Association file.
type AppleConfig struct {
	// AppLinks lists the apps that open the domain's links.
	AppLinks []AppleApp
	// WebCredentials lists the apps that share the domain's credentials,
	// which a passkey in a native app needs.
	WebCredentials []AppleID
}

func (c AppleConfig) isEmpty() bool {
	return len(c.AppLinks) == 0 && len(c.WebCredentials) == 0
}

// AppleID is the id of one app, written TeamID.BundleID in the file.
type AppleID struct {
	// TeamID is the ten character Apple developer team id, uppercase letters
	// and digits.
	TeamID string
	// BundleID is the reverse domain bundle identifier of the app.
	BundleID string
}

func (id AppleID) String() string { return id.TeamID + "." + id.BundleID }

// AppleApp is one app and the paths it opens.
type AppleApp struct {
	AppleID
	// Paths are the path patterns the app opens, such as "/app/*". At least
	// one is required. A pattern starts with a slash, holds letters, digits
	// and the characters / _ . ~ - and may use the wildcards * and ?.
	Paths []string
	// ExcludePaths are patterns the app does not open. They are written
	// before Paths, because the first matching entry decides.
	ExcludePaths []string
}

// AndroidConfig is the content of the Digital Asset Links file.
type AndroidConfig struct {
	Apps []AndroidApp
}

func (c AndroidConfig) isEmpty() bool { return len(c.Apps) == 0 }

// AndroidApp is one app and the certificates it is signed with.
type AndroidApp struct {
	// PackageName is the application id, such as com.example.app.
	PackageName string
	// Fingerprints are SHA-256 fingerprints of the signing certificates, 32
	// hex bytes separated by colons. Case is normalised to uppercase. A
	// product with Play App Signing lists the Play certificate, and the
	// upload certificate only where it installs builds itself.
	Fingerprints []string
	// Relations are the permissions granted. Empty means
	// RelationHandleAllURLs alone.
	Relations []Relation
}

// Handlers serves the configured files. The zero value is not usable, get one
// from New.
type Handlers struct {
	apple   *file
	android *file
}

// New validates cfg and builds the documents. Every problem is returned at
// once, joined, and each wraps ErrInvalid. A cfg that configures neither file
// fails with ErrEmpty.
func New(cfg Config) (*Handlers, error) {
	if cfg.IsEmpty() {
		return nil, fmt.Errorf("appassoc: %w", ErrEmpty)
	}
	var problems []error
	h := &Handlers{}

	if !cfg.Apple.isEmpty() {
		body, errs := buildApple(cfg.Apple)
		problems = append(problems, errs...)
		if len(errs) == 0 {
			h.apple = newFile(body)
		}
	}
	if !cfg.Android.isEmpty() {
		body, errs := buildAndroid(cfg.Android)
		problems = append(problems, errs...)
		if len(errs) == 0 {
			h.android = newFile(body)
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return h, nil
}

// Handle registers the configured files on mux. A file that is not configured
// is not registered. Each path gets two exact patterns: "GET path", which
// also matches HEAD, and "path" for every other method, which answers 405. The
// second one matters behind a catch-all "/", which would otherwise answer a
// POST with its own 404.
//
// wrap is applied to each handler, nil for none. Wrap with care: the files
// must not sit behind a middleware that requires a session, redirects or
// sets a cookie.
func (h *Handlers) Handle(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if wrap == nil {
		wrap = func(next http.Handler) http.Handler { return next }
	}
	for _, e := range []struct {
		path string
		f    *file
	}{{ApplePath, h.apple}, {AndroidPath, h.android}} {
		if e.f == nil {
			continue
		}
		handler := wrap(e.f)
		mux.Handle("GET "+e.path, handler)
		mux.Handle(e.path, handler)
	}
}

// file is one finished document.
type file struct {
	body   []byte
	length string
}

func newFile(body []byte) *file {
	return &file{body: body, length: strconv.Itoa(len(body))}
}

func (f *file) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", CacheControl)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", f.length)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(f.body)
	}
}

// marshal renders v as indented JSON with a final newline and checks the
// size.
func marshal(name string, v any) ([]byte, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("appassoc: %s: encode: %w", name, err)
	}
	body = append(body, '\n')
	if len(body) > maxDocument {
		return nil, invalid("%s: document is %d bytes, at most %d are allowed", name, len(body), maxDocument)
	}
	return body, nil
}

// invalid builds an error that wraps ErrInvalid.
func invalid(format string, args ...any) error {
	return fmt.Errorf("appassoc: %w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// show quotes a value for an error message and cuts it, so a hostile value
// cannot flood a log line.
func show(s string) string {
	const limit = 40
	if len(s) > limit {
		return strconv.Quote(s[:limit]) + "..."
	}
	return strconv.Quote(s)
}
