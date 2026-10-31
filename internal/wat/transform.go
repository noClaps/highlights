package wat

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Enum is an ordered `(enum $Name "member" ...)` definition.
type Enum struct {
	Names []string
	Index map[string]int
}

// Get returns a member's value.
func (e *Enum) Get(name string) (int, bool) {
	if e == nil {
		return 0, false
	}
	v, ok := e.Index[name]
	return v, ok
}

// Transformed is the expanded module text, resolved enums, and language
// name lookup.
type Transformed struct {
	Code string
	// Enums maps enum names such as `$Token` to their members.
	Enums map[string]*Enum
	// Languages lists each accepted name and its ID in table order.
	Languages []LanguageName
}

// LanguageName is one accepted language name or alias.
type LanguageName struct {
	Name string
	ID   int
}

// Options adjust the expanded text. The zero value matches
// scripts/build.ts exactly.
type Options struct {
	// EnumNames writes each enum.get as `i32.const N $Enum.member`, keeping
	// the name for the Go generator. The result is not standard WAT.
	EnumNames bool
}

// TransformFile reads and transforms a WAT file.
func TransformFile(path string) (*Transformed, error) {
	return TransformFileWith(path, Options{})
}

// TransformFileWith reads and transforms a WAT file with options.
func TransformFileWith(path string, opts Options) (*Transformed, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return TransformWith(path, string(content), opts)
}

// Transform expands WAT source:
//   - inline local imports
//   - derive language IDs, dispatch, and aliases (`language-table`)
//   - expand enums
//   - copy a function with globals fixed to constants (`func-variant`)
//   - resolve named values (`const`)
//   - emit CSS-variable tables, enum bitsets, byte sets, and keyword tables
//   - convert string operands for `i32.const` and `i64.const` to integers
//
// path names the module for relative imports and error messages.
func Transform(path, content string) (*Transformed, error) {
	return TransformWith(path, content, Options{})
}

// TransformWith is Transform with options.
func TransformWith(path, content string, opts Options) (result *Transformed, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
				return
			}
			panic(r)
		}
	}()
	return transform(path, content, opts), nil
}

var (
	localImportRe   = regexp.MustCompile(`\(\s*import\s+"(\.+/.+\.wat)"\s*\)`)
	moduleUnwrapRe  = regexp.MustCompile(`(?s)^\s*\(\s*module\s+(.+)\s*\)\s*$`)
	hostImportRe    = regexp.MustCompile(`\(\s*import +".+" +\( *func.+\)`)
	canonicalNameRe = regexp.MustCompile(`^"[a-z][a-z0-9_.-]*"$`)
	lexerFuncRe     = regexp.MustCompile(`^\$\w+$`)
	aliasRe         = regexp.MustCompile(`^"[a-z][a-z0-9_.+#-]*"$`)
	languageNamesRe = regexp.MustCompile(`\(const\s+\$mem\.languageNames\b`)
	enumRe          = regexp.MustCompile(`\s*\(\s*enum\s+(\$\w+)\s+([^)]+)\s*\)`)
	enumMemberRe    = regexp.MustCompile(`^"[\w.-]+"$`)
	localGetRe      = regexp.MustCompile(`^\(local\.get\s+\$\w+\)$`)
	funcNameRe      = regexp.MustCompile(`^\s*(\$\w+)`)
	funcVariantRe   = regexp.MustCompile(`^\s*(\$\w+)\s+(\$\w+)((?:\s*\(global\s+\$\w+\s+-?\d+\s*\))+)\s*$`)
	variantGlobalRe = regexp.MustCompile(`(\$\w+)\s+(-?\d+)`)
	localDeclRe     = regexp.MustCompile(`\(local\s+(\$\w+)\s+(\w+)\s*\)`)
	paramLocalRe    = regexp.MustCompile(`\((?:local|param)\s+(\$\w+)\s+(\w+)\s*\)`)
	loopOpenRe      = regexp.MustCompile(`\(loop\s+\$\w+`)
	constDefRe      = regexp.MustCompile(`\s*\(\s*const\s+(\$[\w.]+)\s+(\$[\w.]+|\d+)([+-]\d+)?\s*\)`)
	digitsRe        = regexp.MustCompile(`^\d+$`)
	constRefRe      = regexp.MustCompile(`(\$[\w.]+)([+-]\d+)?`)
	strayMemRe      = regexp.MustCompile(`\$mem\.[\w.]+`)
	cssTableRe      = regexp.MustCompile(`^\s*(\$\w+)\s+(\d+)\s+(\d+)\s*$`)
	lineCommentRe   = regexp.MustCompile(`;;[^\n]*`)
	bitsetHeadRe    = regexp.MustCompile(`^\s*(\$\w+)\s+(\$\w+)\s+(\d+)`)
	bitsetPredRe    = regexp.MustCompile(`\((\w+)((?:\s+"[\w.]+")+)\s*\)`)
	memberQuoteRe   = regexp.MustCompile(`"[\w.]+"`)
	enumMapHeadRe   = regexp.MustCompile(`^\s*(\$\w+)\s+(\$\w+)\s+(\d+)\s+(\S+)`)
	enumMapValueRe  = regexp.MustCompile(`\(value\s+(\S+)((?:\s+"[\w.]+")+)\s*\)`)
	nameExprRe      = regexp.MustCompile(`(?s)^\s*(\$\w+)\s+(.+)$`)
	keywordHeadRe   = regexp.MustCompile(`^\s*(\$\w+)\s+(\d+)\s+(\d+)\s*\(`)
	keywordGroupRe  = regexp.MustCompile(`\(group(?:\s+(\$[\w.-]+(?:\+\d+)?|-?\d+))?((?:\s+"[\w.$@#!-]+")+)\s*\)`)
	keywordWordRe   = regexp.MustCompile(`"[\w.$@#!-]+"`)
	keywordPoolRe   = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s*$`)
	bytesetRe       = regexp.MustCompile(`^\s*("(?:[^"\\]|\\.)*")\s+(\(local\.get\s+\$\w+\))\s*$`)
	moduleOpenRe    = regexp.MustCompile(`^\s*\(\s*module(\s+)`)
	bitsetGetRe     = regexp.MustCompile(`(?s)^\s*(\$\w+)\.(\w+)\s+(.+)$`)
	enumGetRe       = regexp.MustCompile(`(\(\s*)enum\.get\s+(\$\w+)\.([\w.-]+)`)
	strConstRe      = regexp.MustCompile(`i(32|64).const\s+(".+?")`)
	intRe           = regexp.MustCompile(`^-?\d+$`)
	enumValueRe     = regexp.MustCompile(`^(\$\w+)\.([\w.-]+?)(?:\+(\d+))?$`)
	hexPairRe       = regexp.MustCompile(`^[0-9a-fA-F]{2}`)
)

func fail(format string, args ...any) {
	panic(fmt.Errorf(format, args...))
}

func hexBytes(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		fmt.Fprintf(&sb, "\\%02x", c)
	}
	return sb.String()
}

// prependToModule replaces the first `(module<ws>` with `(module\n<text><ws>`.
func prependToModule(code, text string) string {
	m := moduleOpenRe.FindStringSubmatchIndex(code)
	if m == nil {
		return code
	}
	return "(module\n" + text + code[m[2]:m[3]] + code[m[1]:]
}

func transform(path, content string, opts Options) *Transformed {
	enums := map[string]*Enum{}
	var languages []LanguageName
	languageIndex := map[string]int{}
	var languageNames []byte
	type bitsetInfo struct {
		base int
		bits map[string]int
	}
	bitsets := map[string]bitsetInfo{}
	var imports []string
	abs, _ := filepath.Abs(path)
	seen := map[string]bool{abs: true}

	var flatten func(modulePath, source string) string
	flatten = func(modulePath, source string) string {
		stripped := stripComments(source)
		return replaceAllSubmatchFunc(localImportRe, stripped, func(m []string) string {
			importPath := filepath.Clean(filepath.Join(filepath.Dir(modulePath), m[1]))
			if seen[importPath] {
				return ""
			}
			seen[importPath] = true
			data, err := os.ReadFile(importPath)
			if err != nil {
				panic(err)
			}
			inner := flatten(importPath, string(data))
			if mm := moduleUnwrapRe.FindStringSubmatch(inner); mm != nil {
				return mm[1]
			}
			return inner
		})
	}
	code := flatten(abs, content)
	code = hostImportRe.ReplaceAllStringFunc(code, func(i string) string {
		imports = append(imports, i)
		return ""
	})

	// Declaration order is the language ABI.
	code = ReplaceForm(code, "language-table", func(inner string, _ int) string {
		if len(languages) > 0 {
			fail("Duplicate language-table in %s", path)
		}
		entries := SplitTopLevelForms(inner)
		if len(entries) == 0 || len(entries) > 256 {
			fail("language-table needs 1–256 languages in %s", path)
		}
		var names, functions []string
		for id, entry := range entries {
			if entry.Head != "language" {
				fail("language-table expects language entries in %s", path)
			}
			parts := SplitTopLevelForms(entry.Text[1 : len(entry.Text)-1])[1:]
			var texts []string
			for _, p := range parts {
				texts = append(texts, p.Text)
			}
			if len(texts) < 1 || !canonicalNameRe.MatchString(texts[0]) {
				fail("Invalid canonical language name in %s", path)
			}
			name := texts[0]
			if len(texts) < 2 || !lexerFuncRe.MatchString(texts[1]) {
				fail("Language %s needs a lexer function in %s", name, path)
			}
			fn := texts[1]
			names = append(names, name)
			functions = append(functions, fn)
			all := append([]string{name}, texts[2:]...)
			sort.Strings(all)
			for _, quoted := range all {
				if !aliasRe.MatchString(quoted) {
					fail("Invalid language alias %s in %s", quoted, path)
				}
				alias := quoted[1 : len(quoted)-1]
				if _, ok := languageIndex[alias]; ok {
					fail("Duplicate language name %s in %s", quoted, path)
				}
				languageIndex[alias] = id
				languages = append(languages, LanguageName{alias, id})
			}
		}
		if languageNamesRe.MatchString(code) {
			languageNames = languageNamesData(languages, names)
		}
		out := "(enum $Language " + strings.Join(names, " ") + ")\n  (table $hlDispatch funcref\n    (elem " + strings.Join(functions, " ") + "))\n"
		if len(languageNames) > 0 {
			out += "  (data (i32.const $mem.languageNames) \"" + hexBytes(languageNames) + "\")"
		}
		return out
	})

	code = replaceAllSubmatchFunc(enumRe, code, func(m []string) string {
		key := m[1]
		if _, ok := enums[key]; ok {
			fail("Duplicate enum %s", key)
		}
		e := &Enum{Index: map[string]int{}}
		i := 0
		for _, l := range regexp.MustCompile(`\s+`).Split(m[2], -1) {
			l = strings.TrimSpace(l)
			if l == "" || !enumMemberRe.MatchString(l) {
				continue
			}
			name := l[1 : len(l)-1]
			if _, ok := e.Index[name]; !ok {
				e.Names = append(e.Names, name)
			}
			e.Index[name] = i
			i++
		}
		enums[key] = e
		return ""
	})

	// `(byte-switch (local.get $c) (case <byte>... body...) ...)`
	byteSwitches := 0
	code = ReplaceForm(code, "byte-switch", func(inner string, _ int) string {
		forms := SplitTopLevelForms(inner)
		if len(forms) == 0 || !localGetRe.MatchString(forms[0].Text) {
			fail("byte-switch needs a local.get scrutinee in %s", path)
		}
		scrutinee := forms[0].Text
		id := byteSwitches
		byteSwitches++
		type cs struct{ body, label string }
		var cases []cs
		owner := map[int]int{}
		for _, form := range forms[1:] {
			if form.Head != "case" {
				fail("byte-switch expects case forms in %s", path)
			}
			parts := SplitTopLevelForms(form.Text[5 : len(form.Text)-1])
			keys := 0
			bodyAt := 0
			for _, part := range parts {
				if strings.HasPrefix(part.Text, "(") {
					break
				}
				bodyAt = part.End
				var b []int
				if strings.HasPrefix(part.Text, "\"") {
					for _, x := range unescapeWatString(part.Text) {
						b = append(b, int(x))
					}
				} else {
					n, err := strconv.ParseFloat(part.Text, 64)
					if err != nil || n != math.Trunc(n) {
						fail("byte-switch case key %s is not a byte in %s", part.Text, path)
					}
					b = []int{int(n)}
				}
				if len(b) != 1 || b[0] < 0 || b[0] > 255 {
					fail("byte-switch case key %s is not a byte in %s", part.Text, path)
				}
				if _, ok := owner[b[0]]; ok {
					fail("byte-switch lists byte %d twice in %s", b[0], path)
				}
				owner[b[0]] = len(cases)
				keys++
			}
			if keys == 0 {
				fail("byte-switch case without keys in %s", path)
			}
			cases = append(cases, cs{
				body:  form.Text[5+bodyAt : len(form.Text)-1],
				label: fmt.Sprintf("$byteSwitch%d_%d", id, len(cases)),
			})
		}
		if len(cases) == 0 {
			fail("byte-switch has no cases in %s", path)
		}
		lo, hi := 256, -1
		for b := range owner {
			lo = min(lo, b)
			hi = max(hi, b)
		}
		fallback := fmt.Sprintf("$byteSwitch%d_default", id)
		var targets []string
		for b := lo; b <= hi; b++ {
			if c, ok := owner[b]; ok {
				targets = append(targets, cases[c].label)
			} else {
				targets = append(targets, fallback)
			}
		}
		out := fmt.Sprintf("(br_table %s %s (i32.sub %s (i32.const %d)))", strings.Join(targets, " "), fallback, scrutinee, lo)
		for _, c := range cases {
			out = "(block " + c.label + "\n" + out + ")\n" + c.body + "\n(br " + fallback + ")"
		}
		return "(block " + fallback + "\n" + out + ")"
	})

	// Save top-level lexer locals between streaming calls.
	lexers := map[string]bool{}
	for _, name := range streamLexers {
		lexers[name] = true
	}
	// `(func-variant $copy $base (global $g <int>) ...)`
	type variant struct {
		name, base string
		globals    [][2]string
	}
	var variants []variant
	code = ReplaceForm(code, "func-variant", func(inner string, _ int) string {
		m := funcVariantRe.FindStringSubmatch(inner)
		if m == nil {
			fail("Malformed func-variant in %s", path)
		}
		v := variant{name: m[1], base: m[2]}
		for _, g := range variantGlobalRe.FindAllStringSubmatch(m[3], -1) {
			v.globals = append(v.globals, [2]string{g[1], g[2]})
		}
		variants = append(variants, v)
		return ""
	})
	if len(variants) > 0 {
		code = ReplaceForm(code, "func", func(inner string, _ int) string {
			name := ""
			if m := funcNameRe.FindStringSubmatch(inner); m != nil {
				name = m[1]
			}
			out := "(func" + inner + ")"
			for _, v := range variants {
				if v.base != name {
					continue
				}
				loc := funcNameRe.FindStringIndex(inner)
				body := inner
				if loc != nil {
					body = " " + v.name + inner[loc[1]:]
				}
				for _, g := range v.globals {
					if globalSetOf(body, g[0]) {
						fail("func-variant %s fixes %s, which %s sets", v.name, g[0], name)
					}
					re := regexp.MustCompile(`\(\s*global\.get\s+` + regexp.QuoteMeta(g[0]) + `\s*\)`)
					body = re.ReplaceAllLiteralString(body, "(i32.const "+g[1]+")")
				}
				out += "\n  (func" + body + ")"
				if lexers[name] {
					lexers[v.name] = true
				}
			}
			return out
		})
	}

	streamStateOffset := 0
	code = ReplaceForm(code, "func", func(inner string, _ int) string {
		name := ""
		if m := funcNameRe.FindStringSubmatch(inner); m != nil {
			name = m[1]
		}
		if name == "" || !lexers[name] {
			return "(func" + inner + ")"
		}
		if strings.Contains(inner, "(return") {
			fail("%s cannot checkpoint locals with an early return", name)
		}
		const leading = "(call $lexEmitLeadingContinuation)"
		if !strings.Contains(inner, leading) {
			fail("%s has no leading-continuation checkpoint", name)
		}
		live := liveLocalsAtCheckpoint(inner)
		streamStateOffset = (streamStateOffset + 7) &^ 7
		base := streamStateOffset
		type slot struct {
			local, typ string
			at         int
		}
		var state []slot
		for _, m := range localDeclRe.FindAllStringSubmatch(inner, -1) {
			local, typ := m[1], m[2]
			if !live[local] {
				continue
			}
			if typ != "i32" && typ != "i64" {
				fail("%s carries a %s local (%s) across chunks", name, typ, local)
			}
			size := 4
			if typ == "i64" {
				size = 8
			}
			streamStateOffset = (streamStateOffset + size - 1) &^ (size - 1)
			at := streamStateOffset - base
			streamStateOffset += size
			state = append(state, slot{local, typ, at})
		}
		var load, save []string
		for _, s := range state {
			load = append(load, fmt.Sprintf("(local.set %s (%s.load offset=%d (global.get $streamWindow)))", s.local, s.typ, s.at))
			save = append(save, fmt.Sprintf("(%s.store offset=%d (global.get $streamWindow) (local.get %s))", s.typ, s.at, s.local))
		}
		headRe := regexp.MustCompile(`^(\s*` + regexp.QuoteMeta(name) + `\b(?:\s*\((?:param|result)\b[^)]*\))*)`)
		body := inner
		if loc := headRe.FindStringSubmatchIndex(body); loc != nil {
			body = body[:loc[3]] + "\n    (local $streamRoot i32)" + body[loc[3]:]
		}
		body = strings.Replace(body, leading, fmt.Sprintf("%s\n    (if (i32.and\n          (global.get $streaming)\n          (i32.eqz (global.get $streamDepth)))\n      (then\n        (global.set $streamWindow (i32.const $mem.streamState+%d))\n        (global.set $streamDepth (local.tee $streamRoot (i32.const 1)))\n        (if (i32.eqz (global.get $streamReset))\n          (then\n            %s))))", leading, base, strings.Join(load, "\n          ")), 1)
		exit := ""
		if len(state) > 0 {
			exit = fmt.Sprintf("(global.set $streamWindow (i32.const $mem.streamState+%d))\n        %s\n        (if (i32.gt_u (global.get $streamStateLo) (i32.const %d))\n          (then (global.set $streamStateLo (i32.const %d))))\n        (if (i32.lt_u (global.get $streamStateHi) (i32.const %d))\n          (then (global.set $streamStateHi (i32.const %d))))\n        ", base, strings.Join(save, "\n        "), base, base, streamStateOffset, streamStateOffset)
		}
		return "(func" + body + "\n    (if (local.get $streamRoot)\n      (then\n        " + exit + "(global.set $streamDepth (i32.const 0)))))"
	})
	code += fmt.Sprintf("\n(const $streamStateUsed %d)\n", streamStateOffset)

	// Nested lexers cannot yield: their callers' locals are not saved here.
	liveModule := strings.Contains(code, `(export "liveRun")`)
	code = ReplaceForm(code, "func", func(inner string, _ int) string {
		name := ""
		if m := funcNameRe.FindStringSubmatch(inner); m != nil {
			name = m[1]
		}
		if !liveModule || name == "" || (!lexers[name] && name != "$hlEcmaImpl" && name != "$hlAngularHtml") {
			return "(func" + inner + ")"
		}
		forms := SplitTopLevelForms(inner)
		var loopBlock *Form
		for i := range forms {
			f := &forms[i]
			if f.Head != "block" {
				continue
			}
			for _, child := range SplitTopLevelForms(f.Text[1 : len(f.Text)-1]) {
				if child.Head == "loop" {
					loopBlock = f
					break
				}
			}
			if loopBlock != nil {
				break
			}
		}
		if loopBlock == nil {
			return "(func" + strings.Replace(inner, "(call $lexEmitLeadingContinuation)", "(global.set $liveEntering (i32.const 0))\n(call $lexEmitLeadingContinuation)", 1) + ")"
		}
		var first *Form
		for i := range forms {
			f := &forms[i]
			if f.Head != "" && f.Head != "param" && f.Head != "result" && f.Head != "local" {
				first = f
				break
			}
		}
		carried := newLocalLiveness().seq(ParseSexpr(inner[loopBlock.Start:]), strSet{})
		offset := 0
		type slot struct {
			local, typ string
			at         int
		}
		var locals []slot
		for _, m := range paramLocalRe.FindAllStringSubmatch(inner, -1) {
			local, typ := m[1], m[2]
			if !carried[local] {
				continue
			}
			if typ != "i32" && typ != "i64" {
				fail("%s cannot suspend a %s local", name, typ)
			}
			size := 4
			if typ == "i64" {
				size = 8
			}
			offset = (offset + size - 1) &^ (size - 1)
			locals = append(locals, slot{local, typ, offset})
			offset += size
		}
		if offset > 512 {
			fail("%s exceeds live lexer locals", name)
		}
		var load, save []string
		for _, s := range locals {
			load = append(load, fmt.Sprintf("(local.set %s (%s.load offset=%d (i32.const $mem.liveLocals)))", s.local, s.typ, s.at))
			save = append(save, fmt.Sprintf("(%s.store offset=%d (i32.const $mem.liveLocals) (local.get %s))", s.typ, s.at, s.local))
		}
		ret := ""
		if strings.Contains(inner, "(result i32)") {
			ret = "(i32.const 0)"
		}
		block := loopBlock.Text
		if loc := loopOpenRe.FindStringIndex(block); loc != nil {
			block = block[:loc[1]] + fmt.Sprintf(`
      (if (i32.ge_u (global.get $ptr) (global.get $liveLimit))
        (then
          (if (local.get $liveRoot)
            (then
              %s
              (global.set $liveSuspended (i32.const 1))
              (return %s)))))`, strings.Join(save, "\n"), ret) + block[loc[1]:]
		}
		return "(func" + inner[:first.Start] + `
      (local $liveRoot i32)
      (local.set $liveRoot (global.get $liveEntering))
      (global.set $liveEntering (i32.const 0))
      (if (global.get $liveSuspended)
        (then
          (global.set $liveSuspended (i32.const 0))
          ` + strings.Join(load, "\n") + `)
        (else ` + inner[first.Start:loopBlock.Start] + `))
      ` + block + inner[loopBlock.End:] + ")"
	})

	// `(const $name <int|$other>[+-<int>])` defines a named value.
	type constExpr struct {
		value string
		bias  int
	}
	constExprs := map[string]constExpr{}
	var constOrder []string
	code = replaceAllSubmatchFunc(constDefRe, code, func(m []string) string {
		name := m[1]
		if _, ok := constExprs[name]; ok {
			fail("Duplicate const %s in %s", name, path)
		}
		bias := 0
		if m[3] != "" {
			bias, _ = strconv.Atoi(m[3])
		}
		constExprs[name] = constExpr{m[2], bias}
		constOrder = append(constOrder, name)
		return ""
	})
	constMap := map[string]int{}
	var resolveConst func(name string, pending map[string]bool) int
	resolveConst = func(name string, pending map[string]bool) int {
		if v, ok := constMap[name]; ok {
			return v
		}
		expr, ok := constExprs[name]
		if !ok {
			fail("Const '%s' is undefined in %s", name, path)
		}
		if pending[name] {
			fail("Const '%s' is cyclic in %s", name, path)
		}
		pending[name] = true
		var base int
		if digitsRe.MatchString(expr.value) {
			base, _ = strconv.Atoi(expr.value)
		} else {
			base = resolveConst(expr.value, pending)
		}
		constMap[name] = base + expr.bias
		return base + expr.bias
	}
	for _, name := range constOrder {
		resolveConst(name, map[string]bool{})
	}
	regionEnd := func(base int) int {
		end := math.MaxInt
		for k, v := range constMap {
			if v > base && strings.HasPrefix(k, "$mem.") {
				end = min(end, v)
			}
		}
		return end
	}
	streamStateBase := constMap["$mem.streamState"]
	if streamStateOffset > regionEnd(streamStateBase)-streamStateBase {
		fail("stream lexer state exceeds reserved memory")
	}
	languageNamesBase := constMap["$mem.languageNames"]
	if len(languageNames) > regionEnd(languageNamesBase)-languageNamesBase {
		fail("language names exceed reserved memory")
	}
	code = replaceAllSubmatchFunc(constRefRe, code, func(m []string) string {
		v, ok := constMap[m[1]]
		if !ok {
			return m[0]
		}
		bias := 0
		if m[2] != "" {
			bias, _ = strconv.Atoi(m[2])
		}
		return strconv.Itoa(v + bias)
	})
	if stray := strayMemRe.FindString(code); stray != "" {
		fail("Const '%s' is undefined in %s", stray, path)
	}

	// (css-variable-table $Enum <base> <end>)
	code = ReplaceForm(code, "css-variable-table", func(inner string, _ int) string {
		m := cssTableRe.FindStringSubmatch(inner)
		if m == nil {
			fail("Malformed css-variable-table in %s", path)
		}
		members := enums[m[1]]
		if members == nil {
			fail("css-variable-table references unknown enum '%s' in %s", m[1], path)
		}
		tableBase, _ := strconv.Atoi(m[2])
		rangeEnd, _ := strconv.Atoi(m[3])
		table := make([]byte, len(members.Names)*3)
		stringBase := tableBase + len(table)
		type suffix struct {
			s string
			i int
		}
		var suffixes []suffix
		for _, name := range members.Names {
			s := ""
			if name != "none" {
				s = strings.NewReplacer(".", "-", "_", "-").Replace(name)
			}
			suffixes = append(suffixes, suffix{s, members.Index[name]})
		}
		var strs []string
		for i, s := range suffixes {
			if s.s == "" {
				continue
			}
			contained := false
			for j, o := range suffixes {
				if i != j && strings.Contains(o.s, s.s) {
					contained = true
					break
				}
			}
			if !contained {
				strs = append(strs, s.s)
			}
		}
		blob := ""
		for len(strs) > 0 {
			best, overlap := 0, 0
			for i, s := range strs {
				for n := min(len(blob), len(s)); n > overlap; n-- {
					if strings.HasSuffix(blob, s[:n]) {
						best = i
						overlap = n
						break
					}
				}
			}
			blob += strs[best][overlap:]
			strs = append(strs[:best:best], strs[best+1:]...)
		}
		for _, s := range suffixes {
			ptr := stringBase + strings.Index(blob, s.s)
			table[s.i*3] = byte(ptr)
			table[s.i*3+1] = byte(ptr >> 8)
			table[s.i*3+2] = byte(len(s.s))
		}
		if stringBase+len(blob) > rangeEnd {
			fail("css-variable-table needs %d bytes, range holds %d in %s", stringBase+len(blob)-tableBase, rangeEnd-tableBase, path)
		}
		return fmt.Sprintf("(data (i32.const %d) \"%s\")\n  (data (i32.const %d) \"%s\")", tableBase, hexBytes(table), stringBase, blob)
	})

	// `(bitset $Name $Enum <base> (pred "member" ...) ...)`
	code = ReplaceForm(code, "bitset", func(raw string, _ int) string {
		inner := lineCommentRe.ReplaceAllString(raw, "")
		head := bitsetHeadRe.FindStringSubmatch(inner)
		if head == nil {
			fail("Malformed bitset in %s", path)
		}
		name, enumKey := head[1], head[2]
		if _, ok := bitsets[name]; ok {
			fail("Duplicate bitset %s", name)
		}
		members := enums[enumKey]
		if members == nil {
			fail("Bitset '%s' references unknown enum '%s' in %s", name, enumKey, path)
		}
		base, _ := strconv.Atoi(head[3])
		bytes := make([]byte, len(members.Names))
		bits := map[string]int{}
		var predOrder []string
		bit := 0
		for _, pm := range bitsetPredRe.FindAllStringSubmatch(inner, -1) {
			if bit > 7 {
				fail("Bitset '%s' has more than 8 predicates in %s", name, path)
			}
			pred := pm[1]
			if _, ok := bits[pred]; !ok {
				predOrder = append(predOrder, pred)
			}
			bits[pred] = 1 << bit
			bit++
			for _, q := range memberQuoteRe.FindAllString(pm[2], -1) {
				member := q[1 : len(q)-1]
				idx, ok := members.Index[member]
				if !ok {
					fail("Bitset '%s.%s': '%s' is not in enum '%s' in %s", name, pred, member, enumKey, path)
				}
				bytes[idx] |= byte(bits[pred])
			}
		}
		bitsets[name] = bitsetInfo{base, bits}
		return fmt.Sprintf(";; %s: %s\n  (data (i32.const %d) \"%s\")", name, strings.Join(predOrder, ", "), base, hexBytes(bytes))
	})

	// `(enum-map $Name $Enum <base> <default> (value <v> "member" ...) ...)`
	enumMaps := map[string]int{}
	code = ReplaceForm(code, "enum-map", func(raw string, _ int) string {
		inner := lineCommentRe.ReplaceAllString(raw, "")
		head := enumMapHeadRe.FindStringSubmatch(inner)
		if head == nil {
			fail("Malformed enum-map in %s", path)
		}
		name, enumKey := head[1], head[2]
		if _, ok := enumMaps[name]; ok {
			fail("Duplicate enum-map %s", name)
		}
		members := enums[enumKey]
		if members == nil {
			fail("enum-map '%s' references unknown enum '%s' in %s", name, enumKey, path)
		}
		base, _ := strconv.Atoi(head[3])
		bytes := make([]byte, len(members.Names))
		def := resolveEnumValue(head[4], enums)
		for i := range bytes {
			bytes[i] = byte(def)
		}
		seenMember := map[string]bool{}
		for _, vm := range enumMapValueRe.FindAllStringSubmatch(inner, -1) {
			v := resolveEnumValue(vm[1], enums)
			if v > 255 {
				fail("enum-map '%s': value %d exceeds a byte", name, v)
			}
			for _, q := range memberQuoteRe.FindAllString(vm[2], -1) {
				member := q[1 : len(q)-1]
				idx, ok := members.Index[member]
				if !ok || seenMember[member] {
					fail("enum-map '%s': '%s' is not in enum '%s' or is listed twice in %s", name, member, enumKey, path)
				}
				seenMember[member] = true
				bytes[idx] = byte(v)
			}
		}
		enumMaps[name] = base
		return fmt.Sprintf(";; %s: %d entries\n  (data (i32.const %d) \"%s\")", name, len(bytes), base, hexBytes(bytes))
	})

	code = ReplaceForm(code, "enum-map.get", func(inner string, _ int) string {
		m := nameExprRe.FindStringSubmatch(inner)
		if m == nil {
			fail("Malformed enum-map.get in %s", path)
		}
		base, ok := enumMaps[m[1]]
		if !ok {
			fail("enum-map '%s' is undefined in %s", m[1], path)
		}
		return fmt.Sprintf("(i32.load8_u offset=%d %s)", base, strings.TrimSpace(m[2]))
	})

	// `(keyword-table $Name <base> <end> (group <value>? "word" ...) ...)`
	type kwWord struct {
		word  string
		group int
		h     uint32
	}
	type pendingTable struct {
		name     string
		base     int
		rangeEnd int
		words    []kwWord
		values   []*int
	}
	var pendingTables []*pendingTable
	type kwTable struct {
		base, buckets, slots int
		values               int
		hasValues            bool
	}
	keywordTables := map[string]kwTable{}
	code = ReplaceForm(code, "keyword-table", func(raw string, _ int) string {
		inner := lineCommentRe.ReplaceAllString(raw, "")
		head := keywordHeadRe.FindStringSubmatch(inner)
		if head == nil {
			fail("Malformed keyword-table in %s", path)
		}
		name := head[1]
		for _, t := range pendingTables {
			if t.name == name {
				fail("Duplicate keyword-table %s", name)
			}
		}
		var words []kwWord
		zero := 0
		values := []*int{&zero}
		group := 0
		for _, gm := range keywordGroupRe.FindAllStringSubmatch(inner, -1) {
			group++
			if group > 31 {
				fail("keyword-table %s has more than 31 groups", name)
			}
			if gm[1] == "" {
				values = append(values, nil)
			} else {
				v := resolveEnumValue(gm[1], enums)
				values = append(values, &v)
			}
			for _, q := range keywordWordRe.FindAllString(gm[2], -1) {
				word := q[1 : len(q)-1]
				if len(word) < 2 || len(word) > 31 {
					fail("keyword-table %s: '%s' must be 2..31 bytes", name, word)
				}
				for _, w := range words {
					if w.word == word {
						fail("keyword-table %s: duplicate word '%s'", name, word)
					}
				}
				words = append(words, kwWord{word, group, keywordHash(word)})
			}
		}
		if len(words) == 0 {
			fail("keyword-table %s has no words", name)
		}
		for i, a := range words {
			for j, b := range words {
				if i != j && a.h == b.h {
					fail("keyword-table %s: '%s' and '%s' share a hash; match one directly", name, a.word, b.word)
				}
			}
		}
		base, _ := strconv.Atoi(head[2])
		rangeEnd, _ := strconv.Atoi(head[3])
		pendingTables = append(pendingTables, &pendingTable{name, base, rangeEnd, words, values})
		return "@@keyword-table " + name + "@@"
	})

	var keywordPool *[2]int
	code = ReplaceForm(code, "keyword-pool", func(inner string, _ int) string {
		m := keywordPoolRe.FindStringSubmatch(inner)
		if m == nil {
			fail("Malformed keyword-pool in %s", path)
		}
		if keywordPool != nil {
			fail("Duplicate keyword-pool in %s", path)
		}
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		keywordPool = &[2]int{a, b}
		return "@@keyword-pool@@"
	})
	if len(pendingTables) > 0 {
		if keywordPool == nil {
			fail("keyword-table needs a keyword-pool in %s", path)
		}
		var all []string
		for _, t := range pendingTables {
			for _, w := range t.words {
				all = append(all, w.word)
			}
		}
		pool := packKeywordPool(all)
		poolCapacity := min(16383, keywordPool[1]-keywordPool[0])
		if len(pool) > poolCapacity {
			fail("keyword-pool needs %d bytes, range holds %d", len(pool), poolCapacity)
		}
		for _, table := range pendingTables {
			hashes := make([]uint32, len(table.words))
			for i, w := range table.words {
				hashes[i] = w.h
			}
			placed := placeKeywordTable(hashes)
			if placed == nil {
				fail("keyword-table %s: no geometry places every word", table.name)
			}
			bytes := append([]byte{}, placed.disp...)
			for _, index := range placed.at {
				if index < 0 {
					bytes = append(bytes, 0, 0, 0)
					continue
				}
				w := table.words[index]
				desc := (len(w.word) << 19) | (w.group << 14) | strings.Index(pool, w.word)
				bytes = append(bytes, byte(desc), byte(desc>>8), byte(desc>>16))
			}
			allValues := true
			for _, v := range table.values {
				if v == nil {
					allValues = false
				}
			}
			valuesAt := table.base + len(bytes)
			if allValues {
				for _, vp := range table.values {
					v := *vp
					if v < -1 || v > 0x7fff {
						fail("keyword-table %s: group value %d is out of range", table.name, v)
					}
					bytes = append(bytes, byte(v), byte(v>>8))
				}
			}
			if table.rangeEnd > regionEnd(table.base) {
				fail("keyword-table %s range ends past the next region at %d", table.name, regionEnd(table.base))
			}
			if table.base+len(bytes) > table.rangeEnd {
				fail("keyword-table %s needs %d bytes, range holds %d", table.name, len(bytes), table.rangeEnd-table.base)
			}
			keywordTables[table.name] = kwTable{table.base, placed.buckets, placed.slots, valuesAt, allValues}
			code = strings.Replace(code, "@@keyword-table "+table.name+"@@",
				fmt.Sprintf(";; %s: %d words, %d buckets, %d slots, %d bytes\n  (data (i32.const %d) \"%s\")", table.name, len(table.words), placed.buckets, placed.slots, len(bytes), table.base, hexBytes(bytes)), 1)
		}
		code = strings.Replace(code, "@@keyword-pool@@",
			fmt.Sprintf(";; keyword pool: %d bytes\n  (data (i32.const %d) \"%s\")", len(pool), keywordPool[0], hexBytes([]byte(pool))), 1)
	} else {
		code = strings.Replace(code, "@@keyword-pool@@", "", 1)
	}

	code = ReplaceForm(code, "keyword-table.get", func(inner string, _ int) string {
		m := nameExprRe.FindStringSubmatch(inner)
		if m == nil {
			fail("Malformed keyword-table.get in %s", path)
		}
		t, ok := keywordTables[m[1]]
		if !ok {
			fail("keyword-table '%s' is undefined in %s", m[1], path)
		}
		return fmt.Sprintf("(call $lexKeywordLookup %s (i32.const %d) (i32.const %d) (i32.const %d))", strings.TrimSpace(m[2]), t.base, t.buckets-1, t.slots)
	})
	code = ReplaceForm(code, "keyword-table.value", func(inner string, _ int) string {
		m := nameExprRe.FindStringSubmatch(inner)
		if m == nil {
			fail("Malformed keyword-table.value in %s", path)
		}
		t, ok := keywordTables[m[1]]
		if !ok || !t.hasValues {
			fail("keyword-table '%s' has no group values in %s", m[1], path)
		}
		return fmt.Sprintf("(call $lexKeywordValue %s (i32.const %d) (i32.const %d) (i32.const %d))", strings.TrimSpace(m[2]), t.base, t.buckets-1, t.slots)
	})

	// `(byteset.get "bytes" (local.get $x))`
	byteSets := map[string]int{}
	var byteSetOrder []string
	byteSetBase, hasByteSets := constMap["$mem.byteSets"]
	byteSetCap := 0
	if hasByteSets {
		byteSetCap = (regionEnd(byteSetBase) - byteSetBase) >> 5
	}
	byteSetBranches := branchOperands(code, "byteset.get")
	code = ReplaceForm(code, "byteset.get", func(inner string, at int) string {
		m := bytesetRe.FindStringSubmatch(inner)
		if m == nil || !hasByteSets {
			fail("Malformed byteset.get in %s", path)
		}
		bytes := unescapeWatString(m[1])
		if len(bytes) == 0 {
			fail("Empty byteset.get in %s", path)
		}
		uniq := map[byte]bool{}
		var sorted []int
		for _, b := range bytes {
			if !uniq[b] {
				uniq[b] = true
				sorted = append(sorted, int(b))
			}
		}
		sort.Ints(sorted)
		parts := make([]string, len(sorted))
		for i, b := range sorted {
			parts[i] = strconv.Itoa(b)
		}
		key := strings.Join(parts, ",")
		index, ok := byteSets[key]
		if !ok {
			index = len(byteSets)
			if index >= byteSetCap {
				fail("More than %d distinct byte sets in %s", byteSetCap, path)
			}
			byteSets[key] = index
			byteSetOrder = append(byteSetOrder, key)
		}
		bit := index & 7
		load := fmt.Sprintf("(i32.load8_u offset=%d %s)", byteSetBase+(index>>3)*256, m[2])
		if byteSetBranches[at] {
			return fmt.Sprintf("(i32.and %s (i32.const %d))", load, 1<<bit)
		}
		if bit == 0 {
			return fmt.Sprintf("(i32.and %s (i32.const 1))", load)
		}
		if bit == 7 {
			return fmt.Sprintf("(i32.shr_u %s (i32.const 7))", load)
		}
		return fmt.Sprintf("(i32.and (i32.shr_u %s (i32.const %d)) (i32.const 1))", load, bit)
	})
	if len(byteSets) > 0 {
		tables := make([]byte, ((len(byteSets)+7)>>3)*256)
		for _, key := range byteSetOrder {
			index := byteSets[key]
			for _, p := range strings.Split(key, ",") {
				b, _ := strconv.Atoi(p)
				tables[(index>>3)*256+b] |= 1 << (index & 7)
			}
		}
		code = prependToModule(code, fmt.Sprintf("  ;; byte-set tables: %d sets\n  (data (i32.const %d) \"%s\")", len(byteSets), byteSetBase, hexBytes(tables)))
	}

	code = ReplaceForm(code, "bitset.get", func(inner string, _ int) string {
		m := bitsetGetRe.FindStringSubmatch(inner)
		if m == nil {
			fail("Malformed bitset.get in %s", path)
		}
		table, ok := bitsets[m[1]]
		mask, ok2 := table.bits[m[2]]
		if !ok || !ok2 {
			fail("Bitset '%s.%s' is undefined in %s", m[1], m[2], path)
		}
		return fmt.Sprintf("(i32.and (i32.load8_u offset=%d %s) (i32.const %d))", table.base, strings.TrimSpace(m[3]), mask)
	})

	code = replaceAllSubmatchFunc(enumGetRe, code, func(m []string) string {
		i, ok := enums[m[2]].Get(m[3])
		if !ok {
			fail("Enum '%s.%s' is undefined in %s", m[2], m[3], path)
		}
		if opts.EnumNames {
			return m[1] + "i32.const " + strconv.Itoa(i) + " " + m[2] + "." + m[3]
		}
		return m[1] + "i32.const " + strconv.Itoa(i)
	})
	code = replaceAllSubmatchFunc(strConstRe, code, func(m []string) string {
		bits, str := m[1], m[2]
		var chars string
		if err := json.Unmarshal([]byte(str), &chars); err != nil {
			panic(err)
		}
		units := []rune(chars)
		n, _ := strconv.Atoi(bits)
		bad := len(units) > n/8
		for _, r := range units {
			if r > 0xff {
				bad = true
			}
		}
		if bad {
			fail("Could not convert '%s' to i%s in %s", chars, bits, path)
		}
		hex := "0x"
		for i := len(units) - 1; i >= 0; i-- {
			hex += fmt.Sprintf("%02x", units[i])
		}
		return "i" + bits + ".const " + hex
	})
	code = WrapCompoundNegations(code)
	limit := 65536
	if v, ok := constMap["$mem.input"]; ok {
		limit = v
	}
	checkDataSegments(code, path, limit)
	if len(imports) > 0 {
		lines := make([]string, len(imports))
		for i, imp := range imports {
			lines[i] = "  " + imp
		}
		code = prependToModule(code, strings.Join(lines, "\n"))
	}
	code = MarkSimdReachers(code)
	return &Transformed{Code: code, Enums: enums, Languages: languages}
}

// globalSetOf tests `global\.set\s+<name>(?![\w.])`.
func globalSetOf(body, name string) bool {
	for i := 0; ; {
		j := strings.Index(body[i:], "global.set")
		if j < 0 {
			return false
		}
		k := i + j + len("global.set")
		p := k
		for p < len(body) && isSpace(body[p]) {
			p++
		}
		if p > k && strings.HasPrefix(body[p:], name) {
			e := p + len(name)
			if e >= len(body) || !(isWordByte(body[e]) || body[e] == '.') {
				return true
			}
		}
		i = k
	}
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func replaceAllSubmatchFunc(re *regexp.Regexp, s string, fn func([]string) string) string {
	var out strings.Builder
	last := 0
	for _, loc := range re.FindAllStringSubmatchIndex(s, -1) {
		out.WriteString(s[last:loc[0]])
		groups := make([]string, len(loc)/2)
		for i := range groups {
			if loc[2*i] >= 0 {
				groups[i] = s[loc[2*i]:loc[2*i+1]]
			}
		}
		out.WriteString(fn(groups))
		last = loc[1]
	}
	out.WriteString(s[last:])
	return out.String()
}

// unescapeWatString decodes a WAT string into bytes. It rejects non-ASCII
// characters because byte sets index individual bytes.
func unescapeWatString(literal string) []byte {
	var out []byte
	named := map[byte]byte{'n': 10, 't': 9, 'r': 13, '"': 34, '\'': 39, '\\': 92}
	for i := 1; i < len(literal)-1; i++ {
		c := literal[i]
		if c != '\\' {
			if c > 0x7f {
				fail("non-ASCII byte in WAT string %s", literal)
			}
			out = append(out, c)
			continue
		}
		next := byte(0)
		if i+1 < len(literal) {
			next = literal[i+1]
		}
		if v, ok := named[next]; ok {
			out = append(out, v)
			i++
		} else if i+3 <= len(literal) && hexPairRe.MatchString(literal[i+1:i+3]) {
			v, _ := strconv.ParseUint(literal[i+1:i+3], 16, 8)
			out = append(out, byte(v))
			i += 2
		} else {
			fail("bad escape in WAT string %s", literal)
		}
	}
	return out
}

// resolveEnumValue resolves a decimal number or enum reference such as
// `$Token.operator`, with an optional `+N` offset.
func resolveEnumValue(text string, enums map[string]*Enum) int {
	if intRe.MatchString(text) {
		v, _ := strconv.Atoi(text)
		return v
	}
	m := enumValueRe.FindStringSubmatch(text)
	if m != nil {
		if v, ok := enums[m[1]].Get(m[2]); ok {
			bias := 0
			if m[3] != "" {
				bias, _ = strconv.Atoi(m[3])
			}
			return v + bias
		}
	}
	fail("unknown enum value '%s'", text)
	return 0
}

// keywordHash hashes the first two bytes, last byte, and word length. It
// must match $lexKeywordLookup in src/common.wat.
func keywordHash(w string) uint32 {
	h := uint32(w[0]) | uint32(w[1])<<8 | uint32(w[len(w)-1])<<16 | uint32(len(w))<<24
	h = (h ^ (h >> 16)) * 0xe51fac89
	return h ^ (h >> 24)
}

// keywordSlot computes a word's slot from its hash and bucket displacement.
func keywordSlot(h uint32, d int, slots int) int {
	x := h + uint32(d)*((h>>12)|1)
	rotated := x<<16 | x>>16
	return int((uint64(rotated) * uint64(slots)) >> 32)
}

type placement struct {
	buckets, slots int
	disp           []byte
	at             []int
}

func placeKeywords(hashes []uint32, buckets, slots int) *placement {
	byBucket := map[int][]int{}
	var bucketOrder []int
	for i, h := range hashes {
		b := int(h) & (buckets - 1)
		if _, ok := byBucket[b]; !ok {
			bucketOrder = append(bucketOrder, b)
		}
		byBucket[b] = append(byBucket[b], i)
	}
	disp := make([]byte, buckets)
	at := make([]int, slots)
	for i := range at {
		at[i] = -1
	}
	sort.SliceStable(bucketOrder, func(a, b int) bool {
		return len(byBucket[bucketOrder[a]]) > len(byBucket[bucketOrder[b]])
	})
	for _, b := range bucketOrder {
		ws := byBucket[b]
		placed := false
		for d := 0; d < 256 && !placed; d++ {
			s := make([]int, len(ws))
			uniq := map[int]bool{}
			free := true
			for k, i := range ws {
				s[k] = keywordSlot(hashes[i], d, slots)
				uniq[s[k]] = true
				if at[s[k]] != -1 {
					free = false
				}
			}
			if len(uniq) == len(ws) && free {
				for k, i := range ws {
					at[s[k]] = i
				}
				disp[b] = byte(d)
				placed = true
			}
		}
		if !placed {
			return nil
		}
	}
	return &placement{buckets: buckets, slots: slots, disp: disp, at: at}
}

func placeKeywordTable(hashes []uint32) *placement {
	n := len(hashes)
	step := max(1, n/64)
	type geometry struct{ buckets, slots int }
	var geometries []geometry
	for slots := n; slots <= 2*n+4; slots += step {
		for buckets := 4; buckets <= 512; buckets *= 2 {
			geometries = append(geometries, geometry{buckets, slots})
		}
	}
	sort.SliceStable(geometries, func(a, b int) bool {
		return geometries[a].buckets+3*geometries[a].slots < geometries[b].buckets+3*geometries[b].slots
	})
	for _, g := range geometries {
		if p := placeKeywords(hashes, g.buckets, g.slots); p != nil {
			return p
		}
	}
	return nil
}

// packKeywordPool packs distinct keywords into one string.
func packKeywordPool(all []string) string {
	uniq := map[string]bool{}
	var distinct []string
	for _, w := range all {
		if !uniq[w] {
			uniq[w] = true
			distinct = append(distinct, w)
		}
	}
	sort.SliceStable(distinct, func(i, j int) bool {
		a, b := distinct[i], distinct[j]
		if len(a) == len(b) {
			return a < b
		}
		return len(a) > len(b)
	})
	var words []string
	for _, w := range distinct {
		contained := false
		for _, o := range distinct {
			if o != w && strings.Contains(o, w) {
				contained = true
				break
			}
		}
		if !contained {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return ""
	}
	pool := words[0]
	words = words[1:]
	for len(words) > 0 {
		best, overlap := 0, 0
		for i, w := range words {
			for n := min(len(pool), len(w)-1); n > overlap; n-- {
				if strings.HasSuffix(pool, w[:n]) {
					best = i
					overlap = n
					break
				}
			}
		}
		pool += words[best][overlap:]
		words = append(words[:best:best], words[best+1:]...)
	}
	return pool
}

// languageNamesData builds the name table searched by $languageByName.
func languageNamesData(languages []LanguageName, canonical []string) []byte {
	isCanonical := map[string]bool{}
	for _, q := range canonical {
		isCanonical[q[1:len(q)-1]] = true
	}
	const maxLength = 18
	var names []LanguageName
	for _, l := range languages {
		if l.ID != 0 {
			names = append(names, l)
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		a, b := names[i].Name, names[j].Name
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		ca, cb := isCanonical[a], isCanonical[b]
		if ca != cb {
			return ca
		}
		return a < b
	})
	for _, n := range names {
		if len(n.Name) > maxLength {
			fail("Language name \"%s\" exceeds %d bytes", n.Name, maxLength)
		}
	}
	var offsets []int
	var records []byte
	for length := 0; length <= maxLength+1; length++ {
		offsets = append(offsets, 2*(maxLength+2)+len(records))
		for _, n := range names {
			if len(n.Name) == length {
				records = append(records, byte(n.ID))
				records = append(records, n.Name...)
			}
		}
	}
	var out []byte
	for _, o := range offsets {
		out = append(out, byte(o&255), byte(o>>8))
	}
	return append(out, records...)
}

var compoundArgRe = regexp.MustCompile(`^\(\s*i32\.(?:and|or)\b`)

// WrapCompoundNegations replaces `(i32.eqz X)` for and/or expressions with
// `(i32.shr_u (i32.clz X) (i32.const 5))`.
func WrapCompoundNegations(code string) string {
	return ReplaceForm(code, "i32.eqz", func(inner string, _ int) string {
		arg := strings.TrimSpace(WrapCompoundNegations(inner))
		if !compoundArgRe.MatchString(arg) {
			return "(i32.eqz " + arg + ")"
		}
		return "(i32.shr_u (i32.clz " + arg + ") (i32.const 5))"
	})
}

var (
	simdOpRe     = regexp.MustCompile(`\b(?:v128|[if](?:8x16|16x8|32x4|64x2))\.|\(\s*local\b[^()]*\bv128\b`)
	funcOpenRe   = regexp.MustCompile(`^\(\s*func(?:\s+(\$[^\s()]+))?`)
	stringBlank  = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	callRe       = regexp.MustCompile(`\bcall\s+(\$[^\s()]+|\d+)`)
	callIndirect = regexp.MustCompile(`\bcall_indirect\b`)
	funcImportRe = regexp.MustCompile(`\(\s*func\b`)
)

// MarkSimdReachers adds an unused `v128` local to scalar functions that can
// call SIMD code (a JavaScriptCore workaround kept for output parity).
func MarkSimdReachers(code string) string {
	header := map[string]bool{"export": true, "type": true, "param": true, "result": true, "local": true}
	var moduleForm *Form
	top := SplitTopLevelForms(code)
	for i := range top {
		if top[i].Head == "module" {
			moduleForm = &top[i]
			break
		}
	}
	if moduleForm == nil {
		fail("markSimdReachers: no module")
	}
	innerAt := moduleForm.Start + strings.Index(moduleForm.Text, "module") + len("module")
	inner := code[innerAt : moduleForm.End-1]
	forms := SplitTopLevelForms(inner)
	imported := 0
	for _, f := range forms {
		if f.Head == "import" && funcImportRe.MatchString(f.Text) {
			imported++
		}
	}
	type fn struct {
		form     Form
		openLen  int
		name     string
		simd     bool
		callees  []string
		indirect bool
	}
	var funcs []fn
	for _, f := range forms {
		if f.Head != "func" {
			continue
		}
		open := funcOpenRe.FindStringSubmatch(f.Text)
		if open == nil {
			fail("markSimdReachers: malformed func")
		}
		text := stringBlank.ReplaceAllLiteralString(f.Text, `""`)
		var callees []string
		for _, m := range callRe.FindAllStringSubmatch(text, -1) {
			callees = append(callees, m[1])
		}
		funcs = append(funcs, fn{f, len(open[0]), open[1], simdOpRe.MatchString(text), callees, callIndirect.MatchString(text)})
	}
	index := map[string]int{}
	for i, f := range funcs {
		index[f.name] = i
	}
	resolve := func(callee string) (int, bool) {
		if digitsRe.MatchString(callee) {
			n, _ := strconv.Atoi(callee)
			return n - imported, true
		}
		i, ok := index[callee]
		return i, ok
	}
	reaches := map[int]bool{}
	for i, f := range funcs {
		if f.simd {
			reaches[i] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for i, f := range funcs {
			if reaches[i] {
				continue
			}
			callsReacher := false
			for _, c := range f.callees {
				if j, ok := resolve(c); ok && reaches[j] {
					callsReacher = true
					break
				}
			}
			if f.indirect || callsReacher {
				reaches[i] = true
				changed = true
			}
		}
	}
	out := code
	for i := len(funcs) - 1; i >= 0; i-- {
		f := funcs[i]
		if f.simd || !reaches[i] {
			continue
		}
		body := f.form.Text[f.openLen : len(f.form.Text)-1]
		firstStart := len(body)
		for _, x := range SplitTopLevelForms(body) {
			if !header[x.Head] && !strings.HasPrefix(x.Text, "(;") {
				firstStart = x.Start
				break
			}
		}
		at := innerAt + f.form.Start + f.openLen + firstStart
		out = out[:at] + " (local v128)" + out[at:]
	}
	return out
}

// checkDataSegments checks that data segments fit below the input without
// overlap.
func checkDataSegments(code, path string, limit int) {
	type seg struct{ base, end int }
	var segments []seg
	open := regexp.MustCompile(`\(\s*data\s+\(\s*i32\.const\s+(\d+)\s*\)`)
	pos := 0
	for {
		loc := open.FindStringSubmatchIndex(code[pos:])
		if loc == nil {
			break
		}
		base, _ := strconv.Atoi(code[pos+loc[2] : pos+loc[3]])
		depth := 1
		i := pos + loc[1]
		inStr, inComment := false, false
		length := 0
		for ; i < len(code) && depth != 0; i++ {
			c := code[i]
			if inComment {
				if c == '\n' {
					inComment = false
				}
			} else if inStr {
				if c == '\\' {
					if i+3 <= len(code) && hexPairRe.MatchString(code[i+1:i+3]) {
						i += 2
					} else {
						i++
					}
					length++
				} else if c == '"' {
					inStr = false
				} else {
					length++
				}
			} else if c == '"' {
				inStr = true
			} else if c == ';' && i+1 < len(code) && code[i+1] == ';' {
				inComment = true
			} else if c == '(' {
				depth++
			} else if c == ')' {
				depth--
			}
		}
		segments = append(segments, seg{base, base + length})
		pos = i
	}
	sort.SliceStable(segments, func(a, b int) bool { return segments[a].base < segments[b].base })
	for i, s := range segments {
		if s.end > limit {
			fail("data segment [%d:%d) overflows the static region in %s", s.base, s.end, path)
		}
		if i+1 < len(segments) && segments[i+1].base < s.end {
			n := segments[i+1]
			fail("data segments [%d:%d) and [%d:%d) overlap in %s", s.base, s.end, n.base, n.end, path)
		}
	}
}

// TokenSlotLimit is the maximum token count supported by the fixed memory
// regions.
const TokenSlotLimit = 73

// ListTokenTypes returns `$Token` names in table order.
func ListTokenTypes(enums map[string]*Enum) ([]string, error) {
	hl := enums["$Token"]
	if hl == nil {
		return nil, fmt.Errorf("no $Token enum found")
	}
	for i, name := range hl.Names {
		if hl.Index[name] != i {
			return nil, fmt.Errorf("$Token enum is not dense at %s", name)
		}
	}
	if len(hl.Names) > TokenSlotLimit {
		return nil, fmt.Errorf("$Token has %d members; the emitter span cache holds %d (see $mem.emitterSpanCache in src/memory.wat, the 384-byte theme table, and lib/highlighter.ts themeBytes)", len(hl.Names), TokenSlotLimit)
	}
	return hl.Names, nil
}
