// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package bearer_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	bearerPath  = "github.com/ostendorf-cloud/govue/internal/bearer"
	sessionPath = "github.com/ostendorf-cloud/govue/session"
)

// packageFiles parses the non test Go files of a package directory relative
// to this one.
func packageFiles(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	return files
}

// TestSessionAndCSRFShareThePredicate is the guard against drift. Both
// packages classify a request through this package's Classify, and neither
// reads the Authorization header on its own, so the two cannot disagree on
// which request is a bearer request.
func TestSessionAndCSRFShareThePredicate(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"../../session", "../../csrf"} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()

			imported, called := false, false
			for _, f := range packageFiles(t, dir) {
				for _, imp := range f.Imports {
					path, err := strconv.Unquote(imp.Path.Value)
					if err != nil {
						t.Fatalf("import path %s: %v", imp.Path.Value, err)
					}
					if path == bearerPath {
						if imp.Name != nil {
							t.Errorf("%s imports the predicate under the name %s", dir, imp.Name.Name)
						}
						imported = true
					}
					if filepath.Base(dir) == "csrf" && path == sessionPath {
						t.Errorf("csrf imports session")
					}
				}
				ast.Inspect(f, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.SelectorExpr:
						if id, ok := n.X.(*ast.Ident); ok && id.Name == "bearer" && n.Sel.Name == "Classify" {
							called = true
						}
					case *ast.BasicLit:
						if n.Kind == token.STRING && strings.Contains(strings.ToLower(n.Value), "authorization") {
							t.Errorf("%s names the Authorization header itself: %s", dir, n.Value)
						}
					}
					return true
				})
			}
			if !imported {
				t.Errorf("%s does not import %s", dir, bearerPath)
			}
			if !called {
				t.Errorf("%s does not call bearer.Classify", dir)
			}
		})
	}
}
