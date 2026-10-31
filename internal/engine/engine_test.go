package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The generated engine is held to these rules of hand-written Go, beyond
// what gofmt, go vet and staticcheck check.
var styleRules = []struct {
	pattern *regexp.Regexp
	reason  string
}{
	{regexp.MustCompile(`\bif (true|false)\b`), "a constant condition keeps only the arm it takes"},
	{regexp.MustCompile(`(&&|\|\|) (true|false)\b|\b(true|false) (&&|\|\|)`), "a constant operand folds away"},
	{regexp.MustCompile(`rt\.B2u\((true|false)\)`), "a constant bool is a number"},
	{regexp.MustCompile(`!!`), "a double negation cancels"},
	{regexp.MustCompile(`(?m)^\t_ = `), "an unread variable is not declared"},
	{regexp.MustCompile(`bits\.LeadingZeros32\([^()]*\)\)\s*>>\s*5`), "x == 0 is not a bit trick"},
	{regexp.MustCompile(`\brt\.(Ld|St)\d|\brt\.Rot[lr]|\brt\.(Clz|Ctz|Popcnt|Copy)\b`), "encoding/binary, math/bits and copy have these"},
	{regexp.MustCompile(`\brt\.Sel(32|64|V)\(`), "Go chooses with if, not a select helper"},
	{regexp.MustCompile(`rt\.Fill\(m\.Mem, [^,]+, 0,`), "clear zeroes memory"},
	{regexp.MustCompile(`(?m)^\s*if [^\n]*\{\n\s*continue \w+\n\s*\}\n\s*break\n`), "a do-while loop ends with if !c { break }"},
}

func TestGeneratedCodeStyle(t *testing.T) {
	src, err := os.ReadFile("engine_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "engine_gen.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BranchStmt:
			if n.Tok == token.GOTO {
				t.Errorf("%s: goto %s: the lexers keep to structured control flow", fset.Position(n.Pos()), n.Label.Name)
			}
		case *ast.BinaryExpr:
			// a bool made a number only to be compared with 0 or 1 is a
			// condition written as arithmetic
			if (n.Op == token.EQL || n.Op == token.NEQ) && (isZeroOrOne(n.X) || isZeroOrOne(n.Y)) && (boolAsNumber(n.X) || boolAsNumber(n.Y)) {
				t.Errorf("%s: a condition tests as a bool, not as a number", fset.Position(n.Pos()))
			}
		}
		return true
	})
	for _, r := range styleRules {
		for _, loc := range r.pattern.FindAllIndex(src, 3) {
			line := 1 + strings.Count(string(src[:loc[0]]), "\n")
			t.Errorf("engine_gen.go:%d: %q: %s", line, src[loc[0]:loc[1]], r.reason)
		}
	}
}

func isZeroOrOne(e ast.Expr) bool {
	l, ok := e.(*ast.BasicLit)
	return ok && (l.Value == "0" || l.Value == "1")
}

// boolAsNumber reports rt.B2u(c), or an and, or, or xor with one as an
// operand.
func boolAsNumber(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.ParenExpr:
		return boolAsNumber(e.X)
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "rt" && sel.Sel.Name == "B2u"
	case *ast.BinaryExpr:
		if e.Op == token.AND || e.Op == token.OR || e.Op == token.XOR {
			return boolAsNumber(e.X) || boolAsNumber(e.Y)
		}
	}
	return false
}
