package wat

// Ports of the preprocessor tests in vendor/pierre-highlights/test/wasm_test.ts.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func srcDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "vendor", "pierre-highlights")
}

// synthetic builds a module with just enough of the stream machinery for a
// lexer, as wasm_test.ts does.
func synthetic(body string) (*Transformed, error) {
	return Transform(filepath.Join(srcDir(), "test", "preprocessor.wat"), `(module
      (memory 2)
      (global $streaming (mut i32) (i32.const 0))
      (global $streamDepth (mut i32) (i32.const 0))
      (global $streamReset (mut i32) (i32.const 0))
      (global $streamWindow (mut i32) (i32.const 0))
      (global $streamStateLo (mut i32) (i32.const 0))
      (global $streamStateHi (mut i32) (i32.const 0))
      (const $mem.streamState 8864)
      (const $mem.bashWords 10144)
      (const $mem.byteSets 1424)
      (func $lexEmitLeadingContinuation)
      `+body+`)`)
}

func mustSynthetic(t *testing.T, body string) *Transformed {
	t.Helper()
	r, err := synthetic(body)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func wantError(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error matching %q", pattern)
	}
	if !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error %q does not match %q", err, pattern)
	}
}

func enumOf(t *testing.T, r *Transformed, name string) map[string]int {
	t.Helper()
	e := r.Enums[name]
	if e == nil {
		return nil
	}
	out := map[string]int{}
	for _, n := range e.Names {
		out[n] = e.Index[n]
	}
	return out
}

func fullModule(t *testing.T) *Transformed {
	t.Helper()
	r, err := TransformFile(filepath.Join(srcDir(), "src", "highlights.wat"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCommentsCannotDefineImportOrReferenceForms(t *testing.T) {
	r := mustSynthetic(t, `
    ;; (enum $Ghost "a" "b")
    ;; (language-table (language "ghost" $missing))
    (; (enum $Block "c") (byteset.get "ab" (local.get $x)) ;)
    (enum $Real "x" "y")
    (func $f (result i32)
      ;; (import "env" "old_hook" (func $old (param i32)))
      (drop (i32.const ";;"))
      (enum.get $Real.y))`)
	if r.Enums["$Ghost"] != nil || r.Enums["$Block"] != nil {
		t.Fatal("commented enums were defined")
	}
	if got := enumOf(t, r, "$Real"); fmt.Sprint(got) != "map[x:0 y:1]" {
		t.Fatalf("$Real = %v", got)
	}
	if strings.Contains(r.Code, "old_hook") {
		t.Fatal("commented import survived")
	}
	if !strings.Contains(r.Code, "i32.const 0x3b3b") {
		t.Fatal("string constant was not converted")
	}
}

func TestLanguageRegistrationDerivesIDsAliasesAndDispatch(t *testing.T) {
	r := mustSynthetic(t, `
    (language-table
      (language "plain" $zero "text")
      (language "cpp" $one "c++" "cc")
      (language "csharp" $two "c#")
      (language "fsharp" $two "f#"))
    (type $value (func (result i32)))
    (func $zero (result i32) (i32.const 10))
    (func $one (result i32) (i32.const 20))
    (func $two (result i32) (i32.const 30))
    (func (export "dispatch") (param $id i32) (result i32)
      (call_indirect (type $value) (local.get $id)))`)
	if got := fmt.Sprint(enumOf(t, r, "$Language")); got != "map[cpp:1 csharp:2 fsharp:3 plain:0]" {
		t.Fatalf("$Language = %s", got)
	}
	want := []LanguageName{{"plain", 0}, {"text", 0}, {"c++", 1}, {"cc", 1}, {"cpp", 1}, {"c#", 2}, {"csharp", 2}, {"f#", 3}, {"fsharp", 3}}
	if fmt.Sprint(r.Languages) != fmt.Sprint(want) {
		t.Fatalf("languages = %v", r.Languages)
	}
	if !strings.Contains(r.Code, "(table $hlDispatch funcref\n    (elem $zero $one $two $two))") {
		t.Fatalf("dispatch table missing:\n%s", r.Code)
	}
}

func TestLanguageRegistrationRejectsAmbiguousOrMalformedEntries(t *testing.T) {
	for _, body := range []string{
		`(language "js" $f "js")`,
		`(language "js" $f "shared") (language "ts" $g "shared")`,
		`(language "js" $f "ts") (language "ts" $g)`,
	} {
		_, err := synthetic("(language-table " + body + ")")
		wantError(t, err, "Duplicate language name")
	}
	for _, c := range [][2]string{
		{`(entry "js" $f)`, "expects language entries"},
		{`(language JS $f)`, "canonical language name"},
		{`(language "JS" $f)`, "canonical language name"},
		{`(language "js")`, "needs a lexer function"},
		{`(language "js" 42)`, "needs a lexer function"},
		{`(language "js" $f "JavaScript")`, "Invalid language alias"},
		{`(language "js" $f "two words")`, "Invalid language alias"},
	} {
		_, err := synthetic("(language-table " + c[0] + ")")
		wantError(t, err, c[1])
	}
	_, err := synthetic(`
    (language-table (language "js" $f))
    (language-table (language "ts" $g))`)
	wantError(t, err, "Duplicate language-table")
	_, err = synthetic(`
    (enum $Language "js")
    (language-table (language "ts" $g))`)
	wantError(t, err, "Duplicate enum")
}

func TestLanguageIDsFitTheControlByteAndPartialFixturesNeedNoTable(t *testing.T) {
	var entries []string
	for i := 0; i < 257; i++ {
		entries = append(entries, fmt.Sprintf(`(language "l%d" $f)`, i))
	}
	full := mustSynthetic(t, "(language-table "+strings.Join(entries[:256], " ")+")")
	found := false
	for _, l := range full.Languages {
		if l.Name == "l255" && l.ID == 255 {
			found = true
		}
	}
	if !found {
		t.Fatal("l255 is not 255")
	}
	_, err := synthetic("(language-table " + strings.Join(entries, " ") + ")")
	wantError(t, err, "1–256 languages")
	_, err = synthetic("(language-table )")
	wantError(t, err, "1–256 languages")
	partial := mustSynthetic(t, `(enum $Local "a")`)
	if len(partial.Languages) != 0 {
		t.Fatalf("partial fixture has languages %v", partial.Languages)
	}
}

func TestStreamLexersMustBranchByLabelName(t *testing.T) {
	lexer := func(labels string) string {
		return `
    (func $hlRust
      (local $x i32) (local $i i32)
      (call $lexEmitLeadingContinuation)
      (block $outer
        (block $inner (br_table ` + labels + ` (local.get $i)))
        (local.set $x (i32.const 0)))
      (block $done
        (loop $l
          (local.set $i (i32.add (local.get $i) (local.get $x)))
          (br_if $done (i32.gt_u (local.get $i) (i32.const 10)))
          (br $l))))`
	}
	named := mustSynthetic(t, lexer("$outer $inner")).Code
	if !regexp.MustCompile(`i32\.load offset=\d+ \(global\.get \$streamWindow\)`).MatchString(named) {
		t.Fatal("no checkpoint load")
	}
	_, err := synthetic(lexer("1 0"))
	wantError(t, err, "branch by index")
}

func TestANestedBlockMayShadowAnOuterLabel(t *testing.T) {
	mustSynthetic(t, `
    (func $hlRust
      (local $x i32)
      (call $lexEmitLeadingContinuation)
      (block $a
        (br_if $a (local.get $x))
        (block $a (br $a))
        (local.set $x (i32.const 1))))`)
}

func TestDataSegmentsCountUTF8Bytes(t *testing.T) {
	_, err := synthetic(`
        (data (i32.const 100) "éé")
        (data (i32.const 102) "x")`)
	wantError(t, err, "overlap")
	mustSynthetic(t, `
    (data (i32.const 100) "éé")
    (data (i32.const 104) "x")`)
}

func TestTokenEnumCannotOutgrowTheEmitterRegions(t *testing.T) {
	names := func(n int) string {
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, fmt.Sprintf(`"t%d"`, i))
		}
		return strings.Join(parts, " ")
	}
	r := mustSynthetic(t, "(enum $Token "+names(74)+")")
	_, err := ListTokenTypes(r.Enums)
	wantError(t, err, "span cache holds 73")
	ok := mustSynthetic(t, "(enum $Token "+names(73)+")")
	list, err := ListTokenTypes(ok.Enums)
	if err != nil || len(list) != 73 {
		t.Fatalf("73 tokens: %v %v", len(list), err)
	}
}

func TestSimdMarkersResolveNumericCallsAfterImportedFunctions(t *testing.T) {
	for _, callee := range []string{"$simd", "1"} {
		r, err := Transform(filepath.Join(srcDir(), "test", "simd-imports.wat"), `(module
        (import "env" "hook" (func $hook))
        (func $simd (drop (v128.const i32x4 0 0 0 0)))
        (func $direct (call `+callee+`))
        (func (export "run") (call 2))
        (func $scalar (call 0)))`)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []string{`\(func \$direct\s+\(local v128\)`, `\(func \(export "run"\)\s+\(local v128\)`, `\(func \$scalar\s+\(call 0\)`} {
			if !regexp.MustCompile(re).MatchString(r.Code) {
				t.Fatalf("%s: %s not in\n%s", callee, re, r.Code)
			}
		}
	}
}

// The JavaScriptCore workarounds keep their invariants in the preprocessed
// source, which the Go engine is generated from.
func TestEveryCallerOfASimdFunctionUsesSimdItself(t *testing.T) {
	code := fullModule(t).Code
	usesSimd := regexp.MustCompile(`\b(?:v128|[if](?:8x16|16x8|32x4|64x2))\.|\(local\b[^()]*\bv128\b`)
	imported := len(regexp.MustCompile(`\(import\b[^\n]*\(func\b`).FindAllString(code, -1))
	starts := regexp.MustCompile(`(?m)^\s*\(func(?:\s+(\$[^\s()]+))?`).FindAllStringSubmatchIndex(code, -1)
	type fn struct{ name, body string }
	var funcs []fn
	for i, m := range starts {
		end := len(code)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		name := fmt.Sprintf("#%d", i+imported)
		if m[2] >= 0 {
			name = code[m[2]:m[3]]
		}
		body := regexp.MustCompile(`"(?:[^"\\]|\\.)*"`).ReplaceAllString(code[m[0]:end], `""`)
		funcs = append(funcs, fn{name, body})
	}
	if len(funcs) <= 300 {
		t.Fatalf("found %d functions", len(funcs))
	}
	simd := map[string]bool{}
	for i, f := range funcs {
		if usesSimd.MatchString(f.body) {
			simd[f.name] = true
			simd[fmt.Sprintf("#%d", i+imported)] = true
		}
	}
	if len(simd) <= 40 {
		t.Fatalf("found %d SIMD scanners", len(simd))
	}
	callRe := regexp.MustCompile(`\bcall\s+(\$[^\s()]+|\d+)`)
	for _, f := range funcs {
		if simd[f.name] {
			continue
		}
		for _, m := range callRe.FindAllStringSubmatch(f.body, -1) {
			c := m[1]
			if c[0] != '$' {
				c = "#" + c
			}
			if simd[c] {
				t.Errorf("%s calls %s without using SIMD", f.name, c)
			}
		}
		if strings.Contains(f.body, "call_indirect") {
			t.Errorf("%s uses call_indirect without using SIMD", f.name)
		}
	}
}

func TestNoEqzNegatesACompoundCondition(t *testing.T) {
	code := fullModule(t).Code
	if regexp.MustCompile(`\(\s*i32\.eqz\s*\(\s*i32\.(?:and|or)\b`).MatchString(code) {
		t.Fatal("a compound negation survived")
	}
	if !regexp.MustCompile(`\(i32\.shr_u\s*\(i32\.clz`).MatchString(code) {
		t.Fatal("rewritten form missing")
	}
}

func TestRewritesPreserveSimdByteValuesMixedLanesAndQuotedData(t *testing.T) {
	var stores []string
	for b := 0; b < 256; b++ {
		lanes := strings.TrimSpace(strings.Repeat(fmt.Sprintf("%d ", b), 16))
		stores = append(stores, fmt.Sprintf("(v128.store offset=%d (local.get $p)\n      (v128.const i8x16 %s))", b*16, lanes))
	}
	data := strings.Join([]string{
		"v128.const i32x4 0x01010101 0x01010101 0x01010101 0x01010101",
		"(v128.const i32x4 0x01010101 0x01010101 0x01010101 0x01010101)",
		"(i32.eqz (i32.or (i32.const 0) (i32.const 1)))",
		`"escaped" \ (i32.eqz (i32.and (i32.const 0) (i32.const 1)))`,
		"(i32.eqz",
	}, "\n")
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(data)
	source := `(module
      (memory (export "memory") 1)
      (data (i32.const 5000) "` + quoted + `")
      (func (export "write") (param $p i32)
        ` + strings.Join(stores, "\n") + `
        (v128.store offset=4096 (local.get $p)
          (v128.const i32x4 0x01020304 0x01020304 0x01020304 0x01020304))
        (v128.store offset=4112 (local.get $p)
          (v128.const i32x4 0x01010101 0x02020202 0x03030303 0x04040404))))`
	r, err := Transform(filepath.Join(srcDir(), "test", "splats.wat"), source)
	if err != nil {
		t.Fatal(err)
	}
	// the data segment and every vector constant survive unchanged
	if !strings.Contains(r.Code, `"`+quoted+`"`) {
		t.Fatal("quoted data changed")
	}
	for _, s := range stores {
		if !strings.Contains(r.Code, s) {
			t.Fatalf("store changed: %s", s)
		}
	}
}
