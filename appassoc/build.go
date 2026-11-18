// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package appassoc

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// teamIDPattern is the ten character Apple team id.
	teamIDPattern = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	// bundleIDPattern is a bundle identifier: letters, digits, hyphen and
	// period, with no empty label. No wildcard is accepted.
	bundleIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*$`)
	// pathPattern is the safe subset of an Apple path pattern.
	pathPattern = regexp.MustCompile(`^/[A-Za-z0-9/_.~*?-]*$`)
	// packagePattern is an Android application id: at least two segments,
	// each starting with a letter.
	packagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
	// fingerprintPattern is 32 hex bytes separated by colons.
	fingerprintPattern = regexp.MustCompile(`^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){31}$`)
)

const (
	maxBundleIDLength = 155
	maxPackageLength  = 255
)

// The JSON shapes. They follow the documented files and nothing else is
// emitted.
type aasaFile struct {
	AppLinks       *aasaAppLinks `json:"applinks,omitempty"`
	WebCredentials *aasaWebCreds `json:"webcredentials,omitempty"`
}

type aasaAppLinks struct {
	Details []aasaDetail `json:"details"`
}

type aasaDetail struct {
	AppIDs     []string        `json:"appIDs"`
	Components []aasaComponent `json:"components"`
}

type aasaComponent struct {
	Path    string `json:"/"`
	Exclude bool   `json:"exclude,omitempty"`
}

type aasaWebCreds struct {
	Apps []string `json:"apps"`
}

type linkStatement struct {
	Relation []Relation `json:"relation"`
	Target   linkTarget `json:"target"`
}

type linkTarget struct {
	Namespace    string   `json:"namespace"`
	PackageName  string   `json:"package_name"`
	Fingerprints []string `json:"sha256_cert_fingerprints"`
}

func buildApple(c AppleConfig) ([]byte, []error) {
	var errs []error
	var doc aasaFile
	seen := map[string]bool{}

	if len(c.AppLinks) > maxApps {
		errs = append(errs, invalid("apple.applinks: %d apps, at most %d are allowed", len(c.AppLinks), maxApps))
	}
	if len(c.WebCredentials) > maxApps {
		errs = append(errs, invalid("apple.webcredentials: %d apps, at most %d are allowed", len(c.WebCredentials), maxApps))
	}

	if len(c.AppLinks) > 0 {
		doc.AppLinks = &aasaAppLinks{Details: []aasaDetail{}}
	}
	for i, app := range c.AppLinks {
		where := "apple.applinks[" + strconv.Itoa(i) + "]"
		problems := checkAppleID(where, app.AppleID)
		if seen[app.String()] {
			problems = append(problems, invalid("%s: app %s is listed twice", where, show(app.String())))
		}
		seen[app.String()] = true
		problems = append(problems, checkPaths(where+".paths", app.Paths, true)...)
		problems = append(problems, checkPaths(where+".excludePaths", app.ExcludePaths, false)...)
		if len(app.Paths)+len(app.ExcludePaths) > maxPaths {
			problems = append(problems, invalid("%s: %d paths, at most %d are allowed", where, len(app.Paths)+len(app.ExcludePaths), maxPaths))
		}
		errs = append(errs, problems...)
		if len(problems) > 0 {
			continue
		}
		detail := aasaDetail{AppIDs: []string{app.String()}, Components: []aasaComponent{}}
		for _, p := range app.ExcludePaths {
			detail.Components = append(detail.Components, aasaComponent{Path: p, Exclude: true})
		}
		for _, p := range app.Paths {
			detail.Components = append(detail.Components, aasaComponent{Path: p})
		}
		doc.AppLinks.Details = append(doc.AppLinks.Details, detail)
	}

	seen = map[string]bool{}
	if len(c.WebCredentials) > 0 {
		doc.WebCredentials = &aasaWebCreds{Apps: []string{}}
	}
	for i, id := range c.WebCredentials {
		where := "apple.webcredentials[" + strconv.Itoa(i) + "]"
		problems := checkAppleID(where, id)
		if seen[id.String()] {
			problems = append(problems, invalid("%s: app %s is listed twice", where, show(id.String())))
		}
		seen[id.String()] = true
		errs = append(errs, problems...)
		if len(problems) == 0 {
			doc.WebCredentials.Apps = append(doc.WebCredentials.Apps, id.String())
		}
	}

	if len(errs) > 0 {
		return nil, errs
	}
	body, err := marshal("apple", doc)
	if err != nil {
		return nil, []error{err}
	}
	return body, nil
}

func checkAppleID(where string, id AppleID) []error {
	var errs []error
	if !teamIDPattern.MatchString(id.TeamID) {
		errs = append(errs, invalid("%s: team id %s must be 10 uppercase letters or digits", where, show(id.TeamID)))
	}
	if len(id.BundleID) > maxBundleIDLength || !bundleIDPattern.MatchString(id.BundleID) {
		errs = append(errs, invalid("%s: bundle id %s must be dot separated labels of letters, digits and hyphens, at most %d characters", where, show(id.BundleID), maxBundleIDLength))
	}
	return errs
}

func checkPaths(where string, paths []string, required bool) []error {
	var errs []error
	if required && len(paths) == 0 {
		errs = append(errs, invalid("%s: at least one path is required", where))
	}
	for i, p := range paths {
		if len(p) > maxPathLength || !pathPattern.MatchString(p) {
			errs = append(errs, invalid("%s[%d]: path %s must start with a slash, hold only letters, digits and / _ . ~ * ? -, and be at most %d characters", where, i, show(p), maxPathLength))
		}
	}
	return errs
}

func buildAndroid(c AndroidConfig) ([]byte, []error) {
	var errs []error
	statements := []linkStatement{}
	seen := map[string]bool{}

	if len(c.Apps) > maxApps {
		errs = append(errs, invalid("android: %d apps, at most %d are allowed", len(c.Apps), maxApps))
	}
	for i, app := range c.Apps {
		where := "android.apps[" + strconv.Itoa(i) + "]"
		var problems []error
		if len(app.PackageName) > maxPackageLength || !packagePattern.MatchString(app.PackageName) {
			problems = append(problems, invalid("%s: package name %s must be at least two dot separated segments of letters, digits and underscores, each starting with a letter, at most %d characters", where, show(app.PackageName), maxPackageLength))
		}
		if seen[app.PackageName] {
			problems = append(problems, invalid("%s: package %s is listed twice", where, show(app.PackageName)))
		}
		seen[app.PackageName] = true

		prints, fpProblems := normalizeFingerprints(where, app.Fingerprints)
		problems = append(problems, fpProblems...)
		rels, relProblems := normalizeRelations(where, app.Relations)
		problems = append(problems, relProblems...)

		errs = append(errs, problems...)
		if len(problems) > 0 {
			continue
		}
		statements = append(statements, linkStatement{
			Relation: rels,
			Target:   linkTarget{Namespace: "android_app", PackageName: app.PackageName, Fingerprints: prints},
		})
	}
	if len(errs) > 0 {
		return nil, errs
	}
	body, err := marshal("android", statements)
	if err != nil {
		return nil, []error{err}
	}
	return body, nil
}

// normalizeFingerprints checks every fingerprint and returns them uppercase
// with duplicates removed, in the order given.
func normalizeFingerprints(where string, in []string) ([]string, []error) {
	var errs []error
	if len(in) == 0 {
		errs = append(errs, invalid("%s: at least one fingerprint is required", where))
	}
	if len(in) > maxFingerprints {
		errs = append(errs, invalid("%s: %d fingerprints, at most %d are allowed", where, len(in), maxFingerprints))
	}
	out := []string{}
	seen := map[string]bool{}
	for i, fp := range in {
		if !fingerprintPattern.MatchString(fp) {
			errs = append(errs, invalid("%s.fingerprints[%d]: %s must be 32 hex bytes separated by colons", where, i, show(fp)))
			continue
		}
		fp = strings.ToUpper(fp)
		if !seen[fp] {
			seen[fp] = true
			out = append(out, fp)
		}
	}
	return out, errs
}

// normalizeRelations returns the relations in a fixed order without
// duplicates. An empty list means handle_all_urls.
func normalizeRelations(where string, in []Relation) ([]Relation, []error) {
	if len(in) == 0 {
		return []Relation{RelationHandleAllURLs}, nil
	}
	var errs []error
	have := map[Relation]bool{}
	for i, r := range in {
		switch r {
		case RelationHandleAllURLs, RelationGetLoginCreds:
			have[r] = true
		default:
			errs = append(errs, invalid("%s.relations[%d]: %s is not a known relation", where, i, show(string(r))))
		}
	}
	out := []Relation{}
	for _, r := range []Relation{RelationHandleAllURLs, RelationGetLoginCreds} {
		if have[r] {
			out = append(out, r)
		}
	}
	return out, errs
}
