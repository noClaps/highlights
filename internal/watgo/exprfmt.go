package watgo

import (
	"errors"
	"regexp"
	"strings"
)

// The generator builds expressions fully parenthesized, which keeps them
// correct while it composes them. tidyLine reprints each statement with
// only the parentheses Go's precedence needs, and rewrites x = x + y as
// x += y.

// expr is a parsed expression.
type expr struct {
	op   string // binary or unary operator; "" for a leaf or call
	leaf string // identifier, number, or rune literal
	fn   string // callee of a call or conversion, or the indexed slice
	args []*expr
	// index is set when fn[args[0]] indexes a slice rather than calling,
	// and slice when it is fn[args[0]:]
	index, slice bool
}

var errUnparsed = errors.New("unparsed")

type exprParser struct {
	s   string
	pos int
}

func parseExpr(s string) (*expr, error) {
	p := &exprParser{s: s}
	e, err := p.binary(1)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.s) {
		return nil, errUnparsed
	}
	return e, nil
}

// precedences of Go's binary operators
var precedence = map[string]int{
	"||": 1, "&&": 2,
	"==": 3, "!=": 3, "<": 3, "<=": 3, ">": 3, ">=": 3,
	"+": 4, "-": 4, "|": 4, "^": 4,
	"*": 5, "/": 5, "%": 5, "<<": 5, ">>": 5, "&": 5, "&^": 5,
}

func (p *exprParser) space() {
	for p.pos < len(p.s) && p.s[p.pos] == ' ' {
		p.pos++
	}
}

// operator returns the binary operator at the cursor.
func (p *exprParser) operator() string {
	p.space()
	rest := p.s[p.pos:]
	for _, op := range []string{"&&", "||", "==", "!=", "<=", ">=", "<<", ">>", "&^", "<", ">", "+", "-", "*", "/", "%", "&", "|", "^"} {
		if strings.HasPrefix(rest, op) {
			return op
		}
	}
	return ""
}

func (p *exprParser) binary(minPrec int) (*expr, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		op := p.operator()
		prec, ok := precedence[op]
		if !ok || prec < minPrec {
			return left, nil
		}
		p.pos += len(op)
		right, err := p.binary(prec + 1)
		if err != nil {
			return nil, err
		}
		left = &expr{op: op, args: []*expr{left, right}}
	}
}

var leafRe = regexp.MustCompile(`^(?:'(?:\\.|[^'\\])'|0x[0-9a-fA-F]+|[0-9]+|[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)`)

func (p *exprParser) unary() (*expr, error) {
	p.space()
	if p.pos >= len(p.s) {
		return nil, errUnparsed
	}
	switch p.s[p.pos] {
	case '^':
		// unary complement, kept apart from binary xor as "u^"
		p.pos++
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &expr{op: "u^", args: []*expr{x}}, nil
	case '!':
		p.pos++
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		if x.op == "!" {
			// !!x is x
			return x.args[0], nil
		}
		return &expr{op: "!", args: []*expr{x}}, nil
	case '(':
		p.pos++
		x, err := p.binary(1)
		if err != nil {
			return nil, err
		}
		p.space()
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return nil, errUnparsed
		}
		p.pos++
		return x, nil
	}
	m := leafRe.FindString(p.s[p.pos:])
	if m == "" {
		return nil, errUnparsed
	}
	p.pos += len(m)
	if p.pos < len(p.s) && p.s[p.pos] == '[' {
		p.pos++
		i, err := p.binary(1)
		if err != nil {
			return nil, err
		}
		p.space()
		slice := false
		if p.pos < len(p.s) && p.s[p.pos] == ':' {
			slice = true
			p.pos++
		}
		if p.pos >= len(p.s) || p.s[p.pos] != ']' {
			return nil, errUnparsed
		}
		p.pos++
		return &expr{fn: m, args: []*expr{i}, index: true, slice: slice}, nil
	}
	if p.pos < len(p.s) && p.s[p.pos] == '(' {
		p.pos++
		call := &expr{fn: m, args: []*expr{}}
		p.space()
		if p.pos < len(p.s) && p.s[p.pos] == ')' {
			p.pos++
			return call, nil
		}
		for {
			a, err := p.binary(1)
			if err != nil {
				return nil, err
			}
			call.args = append(call.args, a)
			p.space()
			if p.pos >= len(p.s) {
				return nil, errUnparsed
			}
			if p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.s[p.pos] == ')' {
				p.pos++
				return call, nil
			}
			return nil, errUnparsed
		}
	}
	return &expr{leaf: m}, nil
}

// print writes an expression with the parentheses precedence needs. Mixed
// && and || keep theirs, for clarity.
func (e *expr) print() string {
	switch {
	case e.leaf != "":
		return e.leaf
	case e.index && e.slice:
		return e.fn + "[" + e.args[0].print() + ":]"
	case e.index:
		return e.fn + "[" + e.args[0].print() + "]"
	case e.fn != "":
		parts := make([]string, len(e.args))
		for i, a := range e.args {
			parts[i] = a.print()
		}
		return e.fn + "(" + strings.Join(parts, ", ") + ")"
	case e.op == "u^":
		x := e.args[0]
		if x.op != "" && x.op != "!" && x.op != "u^" {
			return "^(" + x.print() + ")"
		}
		return "^" + x.print()
	case e.op == "!":
		x := e.args[0]
		if x.op != "" && x.op != "!" {
			return "!(" + x.print() + ")"
		}
		return "!" + x.print()
	}
	prec := precedence[e.op]
	side := func(x *expr, right bool) string {
		s := x.print()
		if x.op == "" || x.op == "!" || x.op == "u^" {
			return s
		}
		xp := precedence[x.op]
		paren := false
		switch {
		case xp < prec:
			paren = true
		case (e.op == "||" && x.op == "&&") || (e.op == "&&" && x.op == "||"):
			paren = true
		case xp > prec:
		case prec == 3:
			// a comparison of comparisons reads better in parentheses
			paren = true
		case x.op != e.op:
			// different operators of one precedence: only a - b + c reads
			// plainly, and only on the left, where Go groups it
			paren = right || !(addSub(e.op) && addSub(x.op))
		case right:
			// regrouping a right operand is safe for associative operators
			paren = !associative[e.op]
		}
		if paren {
			return "(" + s + ")"
		}
		return s
	}
	return side(e.args[0], false) + " " + e.op + " " + side(e.args[1], true)
}

var associative = map[string]bool{"&&": true, "||": true, "+": true, "*": true, "|": true, "&": true, "^": true}

func addSub(op string) bool { return op == "+" || op == "-" }

// tidyExpr reprints one expression, or returns it unchanged when it is of
// a shape the parser does not know.
func tidyExpr(s string) string {
	e, err := parseExpr(s)
	if err != nil {
		return s
	}
	return e.print()
}

var (
	assignRe = regexp.MustCompile(`^(\t*)([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?(?:\[[^\[\]]*\])?) = (.+)$`)
	ifRe     = regexp.MustCompile(`^(\t*(?:\} else )?if )(.+)( \{)$`)
	returnRe = regexp.MustCompile(`^(\t*return )(.+)$`)
	switchRe = regexp.MustCompile(`^(\t*switch )(.+)( \{)$`)
	callRe   = regexp.MustCompile(`^(\t*)([A-Za-z_][A-Za-z0-9_.]*\(.*\))$`)
)

// tidyLine reprints the expressions of one generated statement.
func tidyLine(line string) string {
	if m := assignRe.FindStringSubmatch(line); m != nil {
		indent, target, value := m[1], m[2], m[3]
		e, err := parseExpr(value)
		if err != nil {
			return line
		}
		op, rhs := "", e
		if e.op != "" && e.op != "!" && e.op != "u^" && precedence[e.op] >= 4 && e.args[0].leaf == target {
			op, rhs = e.op, e.args[1]
		}
		// x += B2u(c) counts c: if c { x++ }
		if rhs.fn == "rt.B2u" && !rhs.index && len(rhs.args) == 1 && (op == "+" || op == "-") {
			return indent + "if " + rhs.args[0].print() + " {\n" + indent + "\t" + target + op + op + "\n" + indent + "}"
		}
		// Go has no conditional expression: a select assigns in if/else
		if isSelect(rhs) {
			return indent + selectStmt(indent, target, op, rhs) + "\n" + indent + "}"
		}
		if s := update(target, op, rhs); s != "" {
			return indent + s
		}
		return ""
	}
	for _, re := range []*regexp.Regexp{ifRe, switchRe} {
		if m := re.FindStringSubmatch(line); m != nil {
			return m[1] + tidyExpr(m[2]) + m[3]
		}
	}
	if m := returnRe.FindStringSubmatch(line); m != nil {
		return m[1] + tidyExpr(m[2])
	}
	if m := callRe.FindStringSubmatch(line); m != nil {
		return m[1] + tidyExpr(m[2])
	}
	return line
}

func isSelect(e *expr) bool {
	return (e.fn == "rt.Sel32" || e.fn == "rt.Sel64") && !e.index && len(e.args) == 3
}

// selectStmt writes target op= sel as an if statement without its closing
// brace; a select in the else arm continues it as an else if.
func selectStmt(indent, target, op string, sel *expr) string {
	c, a, b := sel.args[0], sel.args[1], sel.args[2]
	then := update(target, op, a)
	if isSelect(b) && then != "" {
		return "if " + c.print() + " {\n" + indent + "\t" + then + "\n" + indent + "} else " + selectStmt(indent, target, op, b)
	}
	els := update(target, op, b)
	switch {
	case els == "":
		return "if " + c.print() + " {\n" + indent + "\t" + then
	case then == "":
		return "if " + negate(c.print()) + " {\n" + indent + "\t" + els
	}
	return "if " + c.print() + " {\n" + indent + "\t" + then + "\n" + indent + "} else {\n" + indent + "\t" + els
}

// update writes target op= v as a statement: x++ or x-- for a step of
// one, x &^= y for a mask cleared, and "" when it changes nothing.
func update(target, op string, v *expr) string {
	if op == "" && v.op != "" && v.op != "!" && v.op != "u^" && precedence[v.op] >= 4 && v.args[0].leaf == target {
		op, v = v.op, v.args[1]
	}
	minusOne := v.leaf == "0xffffffff" || v.leaf == "4294967295" || v.leaf == "0xffffffffffffffff" || v.leaf == "18446744073709551615"
	switch {
	case op == "" && v.leaf == target:
		return ""
	case op == "":
		return target + " = " + v.print()
	case v.leaf == "0" && (op == "+" || op == "-" || op == "|" || op == "^" || op == "<<" || op == ">>"):
		return ""
	case v.leaf == "1" && (op == "+" || op == "-"):
		return target + op + op
	case minusOne && op == "+":
		return target + "--"
	case minusOne && op == "-":
		return target + "++"
	case op == "&" && v.op == "u^":
		return target + " &^= " + v.args[0].print()
	}
	return target + " " + op + "= " + v.print()
}

var tempAssignRe = regexp.MustCompile(`^(\t*)(t\d+) = (.+)$`)

// foldTemps moves a call's result temporary into the statement that
// follows when that statement only stores, returns, or tests it, so the
// order of evaluation stays the same.
func foldTemps(lines []string, uses map[string]int) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		m := tempAssignRe.FindStringSubmatch(lines[i])
		if m == nil || i+1 >= len(lines) || uses[m[2]] != 2 {
			out = append(out, lines[i])
			continue
		}
		indent, temp, value := m[1], m[2], m[3]
		next := lines[i+1]
		var folded string
		switch {
		case next == indent+"return "+temp:
			folded = indent + "return " + value
		case strings.HasPrefix(next, indent) && strings.HasSuffix(next, " = "+temp) && assignRe.MatchString(next) && !strings.Contains(strings.TrimPrefix(next, indent), "\t"):
			folded = strings.TrimSuffix(next, temp) + value
		case next == indent+"if "+temp+" != 0 {":
			folded = indent + "if " + value + " != 0 {"
		case next == indent+"if "+temp+" == 0 {":
			folded = indent + "if " + value + " == 0 {"
		case next == indent+"if "+temp+" {":
			folded = indent + "if " + value + " {"
		case next == indent+"if !"+temp+" {":
			folded = indent + "if !" + value + " {"
		}
		if folded == "" {
			out = append(out, lines[i])
			continue
		}
		out = append(out, folded)
		i++
	}
	return out
}
