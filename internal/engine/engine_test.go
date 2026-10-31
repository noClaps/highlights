package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestGeneratedCodeHasNoGoto checks that the lexers keep to control flow the
// generator writes as structured Go.
func TestGeneratedCodeHasNoGoto(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "engine_gen.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BranchStmt); ok && b.Tok == token.GOTO {
			t.Errorf("%s: goto %s", fset.Position(b.Pos()), b.Label.Name)
		}
		return true
	})
}
