package uiapp

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"testing"
)

// Adapted from mygo-acrylic's acrylic_guard_test.go. Only sidebar functions
// sit on the system material: Render owns the Ground veil, and renderBody owns
// opaque content. The existing red navigation selection is intentional.
func sidebarAcrylicViolations(source []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "view.go", source, 0)
	if err != nil {
		return nil, err
	}
	var violations []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || (fn.Name.Name != "renderSidebar" && fn.Name.Name != "sidebarItem") {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name bytes.Buffer
			_ = printer.Fprint(&name, fset, call.Fun)
			switch name.String() {
			case "ui.Checkbox", "ui.Radio", "ui.Split", "ui.SplitVertical":
				violations = append(violations, fn.Name.Name+": "+name.String()+" adds an opaque widget surface")
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || method.Sel.Name != "Background" || len(call.Args) != 1 {
				return true
			}
			var value bytes.Buffer
			_ = printer.Fprint(&value, fset, call.Args[0])
			fill := value.String()
			containerFill := fn.Name.Name == "renderSidebar" && fill != "ui.Transparent"
			opaqueFill := fill == "c.Theme().Background" || fill == "c.Theme().Surface" ||
				fill == "c.Theme().SurfaceHover" || fill == "c.Theme().SurfacePressed"
			if colorCall, ok := call.Args[0].(*ast.CallExpr); ok {
				var constructor bytes.Buffer
				_ = printer.Fprint(&constructor, fset, colorCall.Fun)
				opaqueFill = opaqueFill || constructor.String() == "ui.Hex" || constructor.String() == "ui.RGB"
			}
			if containerFill || opaqueFill {
				violations = append(violations, fn.Name.Name+": an extra background blocks the Ground material")
			}
			return true
		})
	}
	return violations, nil
}

func TestSidebarAcrylicLayersUseGroundOnly(t *testing.T) {
	source, err := os.ReadFile("view.go")
	if err != nil {
		t.Fatal(err)
	}
	violations, err := sidebarAcrylicViolations(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
	// Verify the guard rejects a reintroduced layer instead of silently passing.
	for _, statement := range []string{
		`ui.Box(c).Background(c.Theme().Surface)`,
		`ui.Box(c).Background(ui.RGBA(18, 20, 28, 0.6))`,
		`ui.Checkbox(c, "", nil)`,
	} {
		bad := []byte("package uiapp; func renderSidebar() { " + statement + " }")
		violations, err := sidebarAcrylicViolations(bad)
		if err != nil || len(violations) != 1 {
			t.Fatalf("guard did not reject %q: %v, %v", statement, violations, err)
		}
	}
	allowed := []byte(`package uiapp; func sidebarItem() { ui.Box(c).Background(ui.RGBA(255, 255, 255, .08)) }`)
	if violations, err := sidebarAcrylicViolations(allowed); err != nil || len(violations) != 0 {
		t.Fatalf("semantic washes must remain allowed: %v, %v", violations, err)
	}
}
