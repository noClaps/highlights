// Package wat ports the WAT preprocessor from scripts/build.ts and parses the
// expanded module text for the Go code generator.
package wat

import (
	"fmt"
	"regexp"
	"strings"
)

// Form is one top-level form or bare atom with its source offsets.
type Form struct {
	Head  string
	Text  string
	Start int
	End   int
}

var formHeadRe = regexp.MustCompile(`^\s*([\w.$-]+)`)

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// SplitTopLevelForms splits text into top-level forms and bare atoms with
// their source offsets. It respects comments and string literals.
func SplitTopLevelForms(text string) []Form {
	var out []Form
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case c == ';' && i+1 < len(text) && text[i+1] == ';':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case isSpace(c):
			i++
		case c == '(':
			start := i
			depth := 0
			inStr, inComment := false, false
		scan:
			for ; i < len(text); i++ {
				d := text[i]
				switch {
				case inComment:
					if d == '\n' {
						inComment = false
					}
				case inStr:
					if d == '\\' {
						i++
					} else if d == '"' {
						inStr = false
					}
				case d == '"':
					inStr = true
				case d == ';' && i+1 < len(text) && text[i+1] == ';':
					inComment = true
				case d == '(':
					depth++
				case d == ')':
					depth--
					if depth == 0 {
						break scan
					}
				}
			}
			if depth != 0 {
				panic(fmt.Errorf("unterminated form"))
			}
			i++
			form := text[start:i]
			head := ""
			if m := formHeadRe.FindStringSubmatch(form[1:]); m != nil {
				head = m[1]
			}
			out = append(out, Form{Head: head, Text: form, Start: start, End: i})
		case c == '"':
			start := i
			i++
			for i < len(text) && text[i] != '"' {
				if text[i] == '\\' {
					i += 2
				} else {
					i++
				}
			}
			i++
			if i > len(text) {
				i = len(text)
			}
			out = append(out, Form{Text: text[start:i], Start: start, End: i})
		default:
			start := i
			for i < len(text) && !isSpace(text[i]) && text[i] != '(' && text[i] != ')' {
				i++
			}
			out = append(out, Form{Text: text[start:i], Start: start, End: i})
		}
	}
	return out
}

// Sexpr is an atom or a list of nested expressions.
type Sexpr struct {
	Atom   string
	List   []*Sexpr
	IsList bool
}

// Head returns the first atom of a list, or "".
func (s *Sexpr) Head() string {
	if s.IsList && len(s.List) > 0 && !s.List[0].IsList {
		return s.List[0].Atom
	}
	return ""
}

// ParseSexpr parses folded WAT into nested lists. It handles line and
// block comments and keeps quotes on string atoms.
func ParseSexpr(src string) []*Sexpr {
	root := &Sexpr{IsList: true}
	stack := []*Sexpr{root}
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ';' && i+1 < len(src) && src[i+1] == ';':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '(' && i+1 < len(src) && src[i+1] == ';':
			depth := 1
			i += 2
			for i < len(src) && depth > 0 {
				switch {
				case src[i] == '(' && i+1 < len(src) && src[i+1] == ';':
					depth++
					i += 2
				case src[i] == ';' && i+1 < len(src) && src[i+1] == ')':
					depth--
					i += 2
				default:
					i++
				}
			}
		case isSpace(c):
			i++
		case c == '(':
			list := &Sexpr{IsList: true}
			top := stack[len(stack)-1]
			top.List = append(top.List, list)
			stack = append(stack, list)
			i++
		case c == ')':
			if len(stack) == 1 {
				panic(fmt.Errorf("unbalanced parenthesis"))
			}
			stack = stack[:len(stack)-1]
			i++
		case c == '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' {
					j += 2
				} else {
					j++
				}
			}
			end := j + 1
			if end > len(src) {
				end = len(src)
			}
			top := stack[len(stack)-1]
			top.List = append(top.List, &Sexpr{Atom: src[i:end]})
			i = j + 1
		default:
			j := i
			for j < len(src) && !isSpace(src[j]) && src[j] != '(' && src[j] != ')' {
				j++
			}
			top := stack[len(stack)-1]
			top.List = append(top.List, &Sexpr{Atom: src[i:j]})
			i = j
		}
	}
	if len(stack) != 1 {
		panic(fmt.Errorf("unterminated parenthesis"))
	}
	return root.List
}

// stringLiteralEnd returns the end of a complete `"(?:[^"\\]|\\.)*"` match
// at i, or -1.
func stringLiteralEnd(code string, i int) int {
	j := i + 1
	for j < len(code) {
		switch code[j] {
		case '"':
			return j + 1
		case '\\':
			if j+1 >= len(code) {
				return -1
			}
			j += 2
		default:
			j++
		}
	}
	return -1
}

// matchFormOpen reports the length of `\(\s*head(?=[\s(])` at i, or -1.
func matchFormOpen(code string, i int, head string) int {
	if code[i] != '(' {
		return -1
	}
	j := i + 1
	for j < len(code) && isSpace(code[j]) {
		j++
	}
	if !strings.HasPrefix(code[j:], head) {
		return -1
	}
	j += len(head)
	if j >= len(code) || !(isSpace(code[j]) || code[j] == '(') {
		return -1
	}
	return j - i
}

// ReplaceForm replaces each `(head ...)` form with fn(innerText, offset),
// where offset is the form's start in code. It skips quoted strings when
// matching and preserves nested forms and operands.
func ReplaceForm(code, head string, fn func(inner string, at int) string) string {
	var out strings.Builder
	last := 0
	i := 0
	for i < len(code) {
		c := code[i]
		if c == '"' {
			if end := stringLiteralEnd(code, i); end >= 0 {
				i = end
				continue
			}
			i++
			continue
		}
		if c != '(' {
			i++
			continue
		}
		n := matchFormOpen(code, i, head)
		if n < 0 {
			i++
			continue
		}
		at := i
		depth := 0
		inStr, inComment := false, false
		j := at
	scan:
		for ; j < len(code); j++ {
			d := code[j]
			if inComment {
				if d == '\n' {
					inComment = false
				}
				continue
			}
			if inStr {
				if d == '\\' {
					j++
				} else if d == '"' {
					inStr = false
				}
				continue
			}
			switch {
			case d == '"':
				inStr = true
			case d == ';' && j+1 < len(code) && code[j+1] == ';':
				inComment = true
			case d == '(':
				depth++
			case d == ')':
				depth--
				if depth == 0 {
					break scan
				}
			}
		}
		if depth != 0 {
			panic(fmt.Errorf("unterminated (%s ...) form at offset %d", head, at))
		}
		out.WriteString(code[last:at])
		out.WriteString(fn(code[at+n:j], at))
		last = j + 1
		i = last
	}
	out.WriteString(code[last:])
	return out.String()
}

// stripComments removes WAT line comments and nested block comments. It
// preserves string literals and line breaks.
func stripComments(src string) string {
	var out strings.Builder
	last := 0
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '"':
			i++
			for i < len(src) && src[i] != '"' {
				if src[i] == '\\' {
					i += 2
				} else {
					i++
				}
			}
			i++
		case c == ';' && i+1 < len(src) && src[i+1] == ';':
			out.WriteString(src[last:i])
			for i < len(src) && src[i] != '\n' {
				i++
			}
			last = i
		case c == '(' && i+1 < len(src) && src[i+1] == ';':
			out.WriteString(src[last:i])
			depth := 1
			i += 2
			for i < len(src) && depth > 0 {
				switch {
				case src[i] == '(' && i+1 < len(src) && src[i+1] == ';':
					depth++
					i += 2
				case src[i] == ';' && i+1 < len(src) && src[i+1] == ')':
					depth--
					i += 2
				default:
					i++
				}
			}
			out.WriteString(" ")
			last = i
		default:
			i++
		}
	}
	if last < len(src) {
		out.WriteString(src[last:])
	}
	return out.String()
}

// branchOperands finds forms whose values only decide a branch: `if`
// conditions, the last operand of `br_if`, and operands of `i32.eqz`.
func branchOperands(code, head string) map[int]bool {
	found := map[int]bool{}
	type frame struct {
		head     string
		lastAt   int
		lastHead string
	}
	var stack []*frame
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '"':
			for i++; i < len(code) && code[i] != '"'; i++ {
				if code[i] == '\\' {
					i++
				}
			}
		case c == ';' && i+1 < len(code) && code[i+1] == ';':
			for i < len(code) && code[i] != '\n' {
				i++
			}
		case c == '(':
			end := i + 64
			if end > len(code) {
				end = len(code)
			}
			name := ""
			if m := formNameRe.FindStringSubmatch(code[i:end]); m != nil {
				name = m[1]
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.lastAt = i
				parent.lastHead = name
				if name == head && (parent.head == "if" || parent.head == "i32.eqz") {
					found[i] = true
				}
			}
			stack = append(stack, &frame{head: name, lastAt: -1})
		case c == ')':
			if len(stack) > 0 {
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if f.head == "br_if" && f.lastHead == head {
					found[f.lastAt] = true
				}
			}
		}
	}
	return found
}

var formNameRe = regexp.MustCompile(`^\(\s*([^\s()]+)`)
