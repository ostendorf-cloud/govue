// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package account

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// secretComparers are the functions that compare a derived hash or a
// computed code with what a caller supplied.
var secretComparers = map[string]string{
	"VerifyPassword": "password.go",
	"VerifyTOTPCode": "totp.go",
}

// variableTimeCalls return early on the first differing byte or compare in
// ways that do, so none of them may appear in the package.
var variableTimeCalls = map[string]bool{
	"bytes.Equal":       true,
	"bytes.Compare":     true,
	"slices.Equal":      true,
	"slices.Compare":    true,
	"reflect.DeepEqual": true,
	"strings.Compare":   true,
	"strings.EqualFold": true,
}

// TestConstantTimeCompare enforces in the source what a test cannot measure
// reliably: a hash and a code are compared with
// subtle.ConstantTimeCompare and never with ==, != or a variable time
// helper. Inside a comparer every == and != has a literal, nil or the
// ConstantTimeCompare call on one side, so a raw compare of two values
// fails here.
func TestConstantTimeCompare(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = file
	}

	found := map[string]bool{}

	for name, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && variableTimeCalls[selectorName(call.Fun)] {
				t.Errorf("%s: %s is not constant time", fset.Position(call.Pos()), selectorName(call.Fun))
			}

			return true
		})

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || secretComparers[fn.Name.Name] != name {
				continue
			}

			found[fn.Name.Name] = true
			checkComparer(t, fset, fn)
		}
	}

	for name := range secretComparers {
		if !found[name] {
			t.Errorf("comparer %s not found", name)
		}
	}
}

// checkComparer fails on any equality in fn that is not against a literal,
// nil or a subtle.ConstantTimeCompare result, and on a comparer that never
// calls subtle.ConstantTimeCompare.
func checkComparer(t *testing.T, fset *token.FileSet, fn *ast.FuncDecl) {
	t.Helper()

	usesSubtle := false

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if selectorName(node.Fun) == "subtle.ConstantTimeCompare" {
				usesSubtle = true
			}
		case *ast.BinaryExpr:
			if node.Op != token.EQL && node.Op != token.NEQ {
				return true
			}
			if !safeOperand(node.X) && !safeOperand(node.Y) {
				t.Errorf("%s: %s compares two values with %s, use subtle.ConstantTimeCompare",
					fset.Position(node.Pos()), fn.Name.Name, node.Op)
			}
		}

		return true
	})

	if !usesSubtle {
		t.Errorf("%s does not call subtle.ConstantTimeCompare", fn.Name.Name)
	}
}

// safeOperand is a side of an equality that carries no secret: a literal,
// nil, or the result of subtle.ConstantTimeCompare.
func safeOperand(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return e.Name == "nil"
	case *ast.CallExpr:
		return selectorName(e.Fun) == "subtle.ConstantTimeCompare"
	case *ast.ParenExpr:
		return safeOperand(e.X)
	}

	return false
}

// selectorName renders pkg.Func for a selector, or "" for anything else.
func selectorName(expr ast.Expr) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}

	return pkg.Name + "." + sel.Sel.Name
}
