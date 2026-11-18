// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package bearer_test

import (
	"go/ast"
	"go/constant"
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

// constString folds a string literal or a concatenation of string literals
// and package level string constants. It reports false for anything else.
func constString(e ast.Expr, consts map[string]string) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v := constant.MakeFromLiteral(e.Value, e.Kind, 0)
		return constant.StringVal(v), true
	case *ast.Ident:
		v, ok := consts[e.Name]
		return v, ok
	case *ast.ParenExpr:
		return constString(e.X, consts)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, okX := constString(e.X, consts)
		y, okY := constString(e.Y, consts)
		return x + y, okX && okY
	}
	return "", false
}

// stringConsts collects the package level string constants of the files.
func stringConsts(files []*ast.File) map[string]string {
	consts := map[string]string{}
	// Two passes, so a constant built from another one folds too.
	for range 2 {
		for _, f := range files {
			for _, decl := range f.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							if v, ok := constString(vs.Values[i], consts); ok {
								consts[name.Name] = v
							}
						}
					}
				}
			}
		}
	}
	return consts
}

// isHeaderField reports whether e is a selector ending in .Header, such as
// r.Header.
func isHeaderField(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Header"
}

func namesAuthorization(s string) bool {
	return strings.Contains(strings.ToLower(s), "authorization")
}

// TestSessionAndCSRFShareThePredicate is a tripwire against drift: both
// packages classify a request through this package's Classify, and neither
// reads the Authorization header on its own. It is syntactic and a
// determined rewrite can get past it. The real guard is behavioural: the
// contract tests in example/ send every header shape through both packages
// and pin that they agree.
func TestSessionAndCSRFShareThePredicate(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"../../session", "../../csrf"} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()

			files := packageFiles(t, dir)
			consts := stringConsts(files)
			imported, called := false, false
			for _, f := range files {
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
						if id, ok := n.X.(*ast.Ident); ok && id.Name == "http" && n.Sel.Name == "CanonicalHeaderKey" {
							t.Errorf("%s builds a header key itself", dir)
						}
					case *ast.BasicLit:
						if v, ok := constString(n, consts); ok && namesAuthorization(v) {
							t.Errorf("%s names the Authorization header itself: %q", dir, v)
						}
					case *ast.BinaryExpr:
						if v, ok := constString(n, consts); ok && namesAuthorization(v) {
							t.Errorf("%s builds the Authorization header name: %q", dir, v)
						}
					case *ast.IndexExpr:
						if isHeaderField(n.X) {
							t.Errorf("%s indexes a request header map directly", dir)
						}
					case *ast.CallExpr:
						sel, ok := n.Fun.(*ast.SelectorExpr)
						if !ok || !isHeaderField(sel.X) {
							return true
						}
						switch sel.Sel.Name {
						case "Get", "Values":
							if len(n.Args) != 1 {
								t.Errorf("%s calls Header.%s with %d arguments", dir, sel.Sel.Name, len(n.Args))
								return true
							}
							v, ok := constString(n.Args[0], consts)
							if !ok {
								t.Errorf("%s reads a header whose name is not a constant", dir)
							} else if namesAuthorization(v) {
								t.Errorf("%s reads the Authorization header itself", dir)
							}
						case "Clone":
							t.Errorf("%s copies the request headers", dir)
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
