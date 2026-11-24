// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package themeboot_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/themeboot"
)

var update = flag.Bool("update", false, "rewrite the scripts under testdata")

// defaultHash is the hash of boot.js as it was before WithDefaultMode
// existed. A site that sets no default mode keeps this exact script, so its
// policy does not change.
const defaultHash = "'sha256-FwxpckAHcwTN4yh0mUrpJdE4euE/YPl45NsgaZdnh1I='"

// pinnedModes are the modes whose script is written to testdata for the
// Vitest test in web/, which runs each one in a DOM. The file names carry
// the mode names, so a mode renamed on either side fails a test.
var pinnedModes = []themeboot.Mode{themeboot.ModeLight, themeboot.ModeDark}

func TestDefaultModeKeepsTheScriptOfToday(t *testing.T) {
	boot, err := os.ReadFile("boot.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, opts := range [][]themeboot.Option{nil, {themeboot.WithDefaultMode(themeboot.ModeSystem)}} {
		if got := themeboot.Script(opts...); got != string(boot) {
			t.Errorf("the system default changed the script:\n%s", got)
		}
		if got := themeboot.Hash(opts...); got != defaultHash {
			t.Errorf("Hash = %s, want the hash of today %s", got, defaultHash)
		}
	}
}

func TestDefaultModeScripts(t *testing.T) {
	for _, mode := range pinnedModes {
		t.Run(string(mode), func(t *testing.T) {
			src := themeboot.Script(themeboot.WithDefaultMode(mode))
			if !strings.Contains(src, "var mode = '"+string(mode)+"';") {
				t.Errorf("the script does not start from %s:\n%s", mode, src)
			}
			if !strings.Contains(src, "stored.mode === 'system'") {
				t.Errorf("a stored system choice would lose against the default %s:\n%s", mode, src)
			}
			if strings.Contains(src, "matchMedia") {
				t.Errorf("the script reads the system preference:\n%s", src)
			}
			golden(t, "boot-"+string(mode)+".js", src)
		})
	}
}

// TestTestdataHoldsOneScriptPerPinnedMode keeps the files the Vitest test
// reads equal to the modes: a renamed mode must not leave the old file
// behind for the web side to pass on.
func TestTestdataHoldsOneScriptPerPinnedMode(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "boot-*.js"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, mode := range pinnedModes {
		want[filepath.Join("testdata", "boot-"+string(mode)+".js")] = true
	}
	for _, f := range files {
		if !want[f] {
			t.Errorf("%s belongs to no mode", f)
		}
		delete(want, f)
	}
	for f := range want {
		t.Errorf("%s is missing, run go test -update", f)
	}
}

func TestDefaultModeChangesTheHash(t *testing.T) {
	seen := map[string]themeboot.Mode{}
	for _, mode := range []themeboot.Mode{themeboot.ModeSystem, themeboot.ModeLight, themeboot.ModeDark} {
		for _, opts := range [][]themeboot.Option{
			{themeboot.WithDefaultMode(mode)},
			{themeboot.WithDefaultMode(mode), themeboot.WithStorageKey("site:theme")},
		} {
			got := string(themeboot.InjectStatic([]byte(page), opts...))
			text := inlineScript(t, got)
			if text != themeboot.Script(opts...) {
				t.Errorf("%s: InjectStatic writes another script than Script", mode)
			}
			h := themeboot.Hash(opts...)
			if want := cspHash(text); h != want {
				t.Errorf("%s: Hash = %s, the injected script hashes to %s", mode, h, want)
			}
			if other, ok := seen[h]; ok {
				t.Errorf("%s and %s give the same hash, so the hash does not cover the option", mode, other)
			}
			seen[h] = mode
		}
	}
}

func TestDefaultModeCombinesWithTheStorageKey(t *testing.T) {
	a := themeboot.Script(themeboot.WithDefaultMode(themeboot.ModeDark), themeboot.WithStorageKey("site:theme"))
	b := themeboot.Script(themeboot.WithStorageKey("site:theme"), themeboot.WithDefaultMode(themeboot.ModeDark))
	if a != b {
		t.Error("the order of the options changes the script")
	}
	if !strings.Contains(a, "getItem('site:theme')") || !strings.Contains(a, "var mode = 'dark';") {
		t.Errorf("one of the two options is lost:\n%s", a)
	}
}

func TestWithDefaultModeRefusesAnUnknownMode(t *testing.T) {
	for _, bad := range []themeboot.Mode{"", "Dark", "auto", "dark'", "light dark"} {
		mustPanic(t, "WithDefaultMode("+string(bad)+")", func() { themeboot.WithDefaultMode(bad) })
	}
}

// golden compares got with testdata/name, and rewrites it with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the script, run go test -update\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}
