// Package watgo translates an expanded WAT module into Go source. Each Wasm
// function becomes a method on a Machine that owns the linear memory and
// globals, so the lexers run as native Go code.
package watgo

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"highlights/internal/wat"
)

// ValType is a Wasm value type.
type ValType string

const (
	I32  ValType = "i32"
	I64  ValType = "i64"
	V128 ValType = "v128"
)

func (t ValType) goType() string {
	switch t {
	case I32:
		return "uint32"
	case I64:
		return "uint64"
	case V128:
		return "rt.V128"
	}
	panic(fmt.Sprintf("unsupported type %q", string(t)))
}

// Func is a parsed function.
type Func struct {
	Name    string // $name, or "" for unnamed functions
	Index   int    // index in the function space, after imports
	Exports []string
	Params  []Local
	Results []ValType
	Locals  []Local
	Body    []*wat.Sexpr
	GoName  string
	Import  *Import
}

// Local is a parameter or local declaration.
type Local struct {
	Name string // $name, or "" when unnamed
	Type ValType
}

// Import is an imported host function.
type Import struct {
	Module, Field string
}

// Global is a module global.
type Global struct {
	Name    string
	Exports []string
	Type    ValType
	Mutable bool
	Init    uint64
	GoName  string
}

// Segment is an active data segment.
type Segment struct {
	Offset int
	Data   []byte
}

// Module is a parsed module.
type Module struct {
	Funcs     []*Func
	FuncByRef map[string]*Func // $name or decimal index
	Globals   []*Global
	GlobalBy  map[string]*Global
	Data      []Segment
	Pages     int
	MemExport string
	Table     []string // function references for call_indirect

	pendingExports       [][2]string
	pendingGlobalExports [][2]string
}

// Parse reads an expanded WAT module.
func Parse(code string) (*Module, error) {
	var mod *Module
	err := catch(func() { mod = parseModule(code) })
	return mod, err
}

func catch(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
				return
			}
			err = fmt.Errorf("%v", r)
		}
	}()
	f()
	return nil
}

func failf(format string, args ...any) {
	panic(fmt.Errorf(format, args...))
}

func atom(s *wat.Sexpr) string {
	if s.IsList {
		return ""
	}
	return s.Atom
}

func parseModule(code string) *Module {
	top := wat.ParseSexpr(code)
	var moduleForm *wat.Sexpr
	for _, s := range top {
		if s.Head() == "module" {
			moduleForm = s
		}
	}
	if moduleForm == nil {
		failf("no module")
	}
	m := &Module{FuncByRef: map[string]*Func{}, GlobalBy: map[string]*Global{}}
	var funcs []*Func
	// imports come first in the function index space
	for _, f := range moduleForm.List[1:] {
		if f.Head() == "import" {
			fn := f.List[3]
			if fn.Head() != "func" {
				failf("unsupported import %v", f)
			}
			parsed := parseFunc(fn)
			parsed.Import = &Import{unquote(f.List[1].Atom), unquote(f.List[2].Atom)}
			funcs = append(funcs, parsed)
		}
	}
	for _, f := range moduleForm.List[1:] {
		switch f.Head() {
		case "import":
		case "func":
			funcs = append(funcs, parseFunc(f))
		case "memory":
			for _, part := range f.List[1:] {
				if part.IsList && part.Head() == "export" {
					m.MemExport = unquote(part.List[1].Atom)
				} else if !part.IsList && part.Atom[0] != '$' {
					n, err := strconv.Atoi(part.Atom)
					if err != nil {
						failf("bad memory size %q", part.Atom)
					}
					if m.Pages == 0 {
						m.Pages = n
					}
				}
			}
		case "global":
			m.Globals = append(m.Globals, parseGlobal(f, len(m.Globals)))
		case "data":
			m.Data = append(m.Data, parseData(f))
		case "table":
			for _, part := range f.List[1:] {
				if part.IsList && part.Head() == "elem" {
					for _, ref := range part.List[1:] {
						m.Table = append(m.Table, ref.Atom)
					}
				}
			}
		case "export":
			name := unquote(f.List[1].Atom)
			desc := f.List[2]
			switch desc.Head() {
			case "func":
				m.pendingExports = append(m.pendingExports, [2]string{desc.List[1].Atom, name})
			case "global":
				m.pendingGlobalExports = append(m.pendingGlobalExports, [2]string{desc.List[1].Atom, name})
			case "memory":
				m.MemExport = name
			default:
				failf("unsupported export %s", desc.Head())
			}
		case "type", "elem":
			failf("unsupported module field %s", f.Head())
		default:
			failf("unsupported module field %q", f.Head())
		}
	}
	for i, f := range funcs {
		f.Index = i
		m.FuncByRef[strconv.Itoa(i)] = f
		if f.Name != "" {
			m.FuncByRef[f.Name] = f
		}
	}
	for _, e := range m.pendingExports {
		f := m.FuncByRef[e[0]]
		if f == nil {
			failf("export of unknown function %s", e[0])
		}
		f.Exports = append(f.Exports, e[1])
	}
	for i, g := range m.Globals {
		m.GlobalBy[strconv.Itoa(i)] = g
		if g.Name != "" {
			m.GlobalBy[g.Name] = g
		}
	}
	for _, e := range m.pendingGlobalExports {
		g := m.GlobalBy[e[0]]
		if g == nil {
			failf("export of unknown global %s", e[0])
		}
		g.Exports = append(g.Exports, e[1])
	}
	m.Funcs = funcs
	used := map[string]bool{}
	for _, f := range funcs {
		switch {
		case f.Name != "":
			f.GoName = "F" + goIdent(f.Name[1:])
		case len(f.Exports) > 0:
			f.GoName = "X" + goIdent(f.Exports[0])
		default:
			f.GoName = fmt.Sprintf("Func%d", f.Index)
		}
		for used[f.GoName] {
			f.GoName += "_"
		}
		used[f.GoName] = true
	}
	for i, g := range m.Globals {
		switch {
		case g.Name != "":
			g.GoName = "G" + goIdent(g.Name[1:])
		case len(g.Exports) > 0:
			g.GoName = "GX" + goIdent(g.Exports[0])
		default:
			g.GoName = fmt.Sprintf("Global%d", i)
		}
	}
	return m
}

func goIdent(s string) string {
	var sb strings.Builder
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteString("_")
		}
	}
	return sb.String()
}

func unquote(s string) string {
	return string(decodeString(s))
}

func parseValType(s string) ValType {
	switch s {
	case "i32", "i64", "v128":
		return ValType(s)
	}
	failf("unsupported value type %q", s)
	return ""
}

func parseFunc(f *wat.Sexpr) *Func {
	fn := &Func{}
	rest := f.List[1:]
	if len(rest) > 0 && !rest[0].IsList && strings.HasPrefix(rest[0].Atom, "$") {
		fn.Name = rest[0].Atom
		rest = rest[1:]
	}
	i := 0
	for ; i < len(rest); i++ {
		part := rest[i]
		if !part.IsList {
			break
		}
		switch part.Head() {
		case "export":
			fn.Exports = append(fn.Exports, unquote(part.List[1].Atom))
		case "param", "local":
			decl := part.List[1:]
			var locals []Local
			if len(decl) == 2 && strings.HasPrefix(decl[0].Atom, "$") {
				locals = []Local{{decl[0].Atom, parseValType(decl[1].Atom)}}
			} else {
				for _, d := range decl {
					locals = append(locals, Local{"", parseValType(d.Atom)})
				}
			}
			if part.Head() == "param" {
				fn.Params = append(fn.Params, locals...)
			} else {
				fn.Locals = append(fn.Locals, locals...)
			}
		case "result":
			for _, d := range part.List[1:] {
				fn.Results = append(fn.Results, parseValType(d.Atom))
			}
		case "type":
			failf("type uses are unsupported")
		default:
			goto body
		}
	}
body:
	fn.Body = rest[i:]
	if len(fn.Results) > 1 {
		failf("multi-value results are unsupported")
	}
	return fn
}

func parseGlobal(f *wat.Sexpr, index int) *Global {
	g := &Global{}
	rest := f.List[1:]
	if len(rest) > 0 && !rest[0].IsList && strings.HasPrefix(rest[0].Atom, "$") {
		g.Name = rest[0].Atom
		rest = rest[1:]
	}
	for len(rest) > 0 && rest[0].IsList && rest[0].Head() == "export" {
		g.Exports = append(g.Exports, unquote(rest[0].List[1].Atom))
		rest = rest[1:]
	}
	if rest[0].IsList && rest[0].Head() == "mut" {
		g.Mutable = true
		g.Type = parseValType(rest[0].List[1].Atom)
	} else {
		g.Type = parseValType(rest[0].Atom)
	}
	init := rest[1]
	switch init.Head() {
	case "i32.const", "i64.const":
		g.Init = parseInt(init.List[1].Atom)
		if g.Type == I32 {
			g.Init &= 0xffffffff
		}
	default:
		failf("unsupported global initializer %s", init.Head())
	}
	return g
}

func parseData(f *wat.Sexpr) Segment {
	rest := f.List[1:]
	if len(rest) > 0 && !rest[0].IsList {
		failf("named or passive data segments are unsupported")
	}
	off := rest[0]
	if off.Head() == "offset" {
		off = off.List[1]
	}
	if off.Head() != "i32.const" {
		failf("unsupported data offset")
	}
	seg := Segment{Offset: int(parseInt(off.List[1].Atom))}
	for _, s := range rest[1:] {
		seg.Data = append(seg.Data, decodeString(s.Atom)...)
	}
	return seg
}

// parseInt parses a Wasm integer literal into its two's-complement bits.
func parseInt(s string) uint64 {
	s = strings.ReplaceAll(s, "_", "")
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		failf("bad integer literal %q", s)
	}
	if neg {
		return -v
	}
	return v
}

// decodeString decodes a quoted WAT string literal.
func decodeString(lit string) []byte {
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		failf("bad string literal %q", lit)
	}
	s := lit[1 : len(lit)-1]
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(s) {
			failf("bad escape in %q", lit)
		}
		switch e := s[i]; e {
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case '"', '\'', '\\':
			out = append(out, e)
		case 'u':
			end := strings.IndexByte(s[i:], '}')
			if i+1 >= len(s) || s[i+1] != '{' || end < 0 {
				failf("bad unicode escape in %q", lit)
			}
			cp, err := strconv.ParseUint(s[i+2:i+end], 16, 32)
			if err != nil {
				failf("bad unicode escape in %q", lit)
			}
			out = utf8.AppendRune(out, rune(cp))
			i += end
		default:
			if i+1 >= len(s) {
				failf("bad escape in %q", lit)
			}
			v, err := strconv.ParseUint(s[i:i+2], 16, 8)
			if err != nil {
				failf("bad escape in %q", lit)
			}
			out = append(out, byte(v))
			i++
		}
	}
	return out
}
