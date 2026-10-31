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
	{regexp.MustCompile(`rt\.B2u\((?:[^()]|\([^()]*\))*\)(&1)? (!=|==) [01]\b`), "a condition tests as a bool, not as a number"},
	{regexp.MustCompile(`\brt\.(Ld|St)\d|\brt\.Rot[lr]|\brt\.(Clz|Ctz|Popcnt)`), "encoding/binary and math/bits have these"},
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
		if b, ok := n.(*ast.BranchStmt); ok && b.Tok == token.GOTO {
			t.Errorf("%s: goto %s: the lexers keep to structured control flow", fset.Position(b.Pos()), b.Label.Name)
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
