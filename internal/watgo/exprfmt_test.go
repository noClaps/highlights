package watgo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"strconv"
	"testing"
)

// eval evaluates an expression of uint32 variables with Go's own parser,
// so the test checks tidyExpr against the language's precedence.
func eval(t *testing.T, src string, vars map[string]uint32) uint32 {
	t.Helper()
	e, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	var walk func(ast.Expr) uint32
	b := func(v bool) uint32 {
		if v {
			return 1
		}
		return 0
	}
	walk = func(e ast.Expr) uint32 {
		switch x := e.(type) {
		case *ast.ParenExpr:
			return walk(x.X)
		case *ast.Ident:
			return vars[x.Name]
		case *ast.BasicLit:
			n, _ := strconv.ParseUint(x.Value, 0, 32)
			return uint32(n)
		case *ast.UnaryExpr:
			return b(walk(x.X) == 0)
		case *ast.BinaryExpr:
			l, r := walk(x.X), walk(x.Y)
			switch x.Op {
			case token.ADD:
				return l + r
			case token.SUB:
				return l - r
			case token.MUL:
				return l * r
			case token.AND:
				return l & r
			case token.OR:
				return l | r
			case token.XOR:
				return l ^ r
			case token.SHL:
				return l << (r & 31)
			case token.SHR:
				return l >> (r & 31)
			case token.EQL:
				return b(l == r)
			case token.NEQ:
				return b(l != r)
			case token.LSS:
				return b(l < r)
			case token.GTR:
				return b(l > r)
			case token.LAND:
				return b(l != 0 && r != 0)
			case token.LOR:
				return b(l != 0 || r != 0)
			}
		}
		t.Fatalf("unexpected %T in %s", e, src)
		return 0
	}
	return walk(e)
}

// randomExpr builds a fully parenthesized expression, as the generator
// does. Comparisons and logic only combine with each other, so the Go
// form type-checks as bool where it must.
func randomExpr(r *rand.Rand, depth int, boolean bool) string {
	if boolean {
		switch {
		case depth > 0 && r.Intn(3) == 0:
			op := []string{"&&", "||"}[r.Intn(2)]
			return "(" + randomExpr(r, depth-1, true) + " " + op + " " + randomExpr(r, depth-1, true) + ")"
		case depth > 0 && r.Intn(4) == 0:
			return "!(" + randomExpr(r, depth-1, true) + ")"
		}
		op := []string{"==", "!=", "<", ">"}[r.Intn(4)]
		return "(" + randomExpr(r, depth-1, false) + " " + op + " " + randomExpr(r, depth-1, false) + ")"
	}
	if depth <= 0 || r.Intn(4) == 0 {
		if r.Intn(2) == 0 {
			return []string{"a", "b", "c"}[r.Intn(3)]
		}
		return strconv.Itoa(r.Intn(40))
	}
	op := []string{"+", "-", "*", "&", "|", "^", "<<", ">>"}[r.Intn(8)]
	return "(" + randomExpr(r, depth-1, false) + " " + op + " " + randomExpr(r, depth-1, false) + ")"
}

func TestTidyExprKeepsMeaning(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		boolean := r.Intn(2) == 0
		src := randomExpr(r, 4, boolean)
		tidy := tidyExpr(src)
		for k := 0; k < 4; k++ {
			vars := map[string]uint32{"a": r.Uint32(), "b": uint32(r.Intn(64)), "c": r.Uint32()}
			if got, want := eval(t, tidy, vars), eval(t, src, vars); got != want {
				t.Fatalf("%s\nprinted as %s\n%v: got %d, want %d", src, tidy, vars, got, want)
			}
		}
	}
}

func TestTidyLine(t *testing.T) {
	for _, c := range [][2]string{
		{"\tx = x + 1", "\tx++"},
		{"\tm.Ptr = m.Ptr - 1", "\tm.Ptr--"},
		{"\tx = x | (y & 4)", "\tx |= y & 4"},
		{"\tif ((c == 'a') || (c == 'b')) {", "\tif c == 'a' || c == 'b' {"},
		{"\treturn ((a >> 5) & 1)", "\treturn (a >> 5) & 1"},
	} {
		if got := tidyLine(c[0]); got != c[1] {
			t.Errorf("tidyLine(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}
