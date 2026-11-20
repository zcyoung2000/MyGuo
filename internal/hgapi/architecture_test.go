package hgapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Keep the architectural boundary enforced: protocol fields and HTTP requests
// cannot quietly migrate back into synchronization or UI-facing services.
func TestBusinessPackagesDoNotOwnUpstreamHTTPOrSigning(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	internal := filepath.Dir(filepath.Dir(file))
	for _, pkg := range []string{"ipc", "catalog", "library", "auth"} {
		entries, err := os.ReadDir(filepath.Join(internal, pkg))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(internal, pkg, entry.Name())
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				v, _ := strconv.Unquote(imp.Path.Value)
				if v == "myguo/internal/sign" {
					t.Errorf("%s imports protocol signer", path)
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if name, ok := sel.X.(*ast.Ident); ok && name.Name == "http" && (sel.Sel.Name == "NewRequest" || sel.Sel.Name == "NewRequestWithContext") {
							t.Errorf("%s constructs upstream HTTP request", path)
						}
					}
				}
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					if strings.Contains(v, "fqnovel.com") || strings.HasPrefix(v, "/novel/") || strings.HasPrefix(v, "/reading/") {
						t.Errorf("%s owns wire endpoint %q", path, v)
					}
				}
				return true
			})
		}
	}
}
