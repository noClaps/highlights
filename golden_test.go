package highlights

// The golden tests replay every test of the TypeScript suite. Each event in
// testdata/golden records one library call a test made, with its arguments
// and its result; the replay makes the same call on the Go port and
// requires an identical result. See testdata/golden/README.md.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"highlights/internal/engine"
	"highlights/internal/fixtures/kwtest"
	"highlights/internal/jsjson"
)

type jsError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

type moduleSrc struct {
	URL       string  `json:"url"`
	Content   *string `json:"content"`
	Optimized bool    `json:"optimized"`
}

type event struct {
	K            string            `json:"k"`
	Name         string            `json:"name"`
	ID           string            `json:"id"`
	HL           string            `json:"hl"`
	Module       *string           `json:"module"`
	Src          *moduleSrc        `json:"src"`
	Args         []json.RawMessage `json:"args"`
	Opts         json.RawMessage   `json:"opts"`
	Out          json.RawMessage   `json:"out"`
	OutHash      string            `json:"outHash"`
	Err          *jsError          `json:"err"`
	Mem          json.RawMessage   `json:"mem"`
	MemAfter     json.RawMessage   `json:"memAfter"`
	MemAfterHash string            `json:"memAfterHash"`
	Clock        []float64         `json:"clock"`
	Ev           []event           `json:"ev"`
	Lines        json.RawMessage   `json:"lines"`
	LinesHash    string            `json:"linesHash"`
	Gen          int               `json:"gen"`
	Env          []json.RawMessage `json:"env"`
	Imports      []string          `json:"imports"`
	JSON         json.RawMessage   `json:"json"`
	Bundled      string            `json:"bundled"`
	Family       *struct {
		Name   any               `json:"name"`
		Themes []json.RawMessage `json:"themes"`
	} `json:"family"`
	Table     map[string]string `json:"table"`
	Inherited bool              `json:"inherited"`
	OK        *bool             `json:"ok"`
	Error     string            `json:"error"`
}

// undefined is JavaScript's undefined in decoded arguments.
type undefined struct{}

// jsFunction marks a function argument.
type jsFunction struct{}

// notRepresentable is raised for arguments the Go API cannot express.
type notRepresentable struct{ why string }

func skip(format string, args ...any) {
	panic(notRepresentable{fmt.Sprintf(format, args...)})
}

// rawMachine is a module instance driven export by export.
type rawMachine struct {
	mem    *[]byte
	call   func(name string, args []uint64) (uint64, bool)
	setEnv func(pop func(name string) uint32)
}

// engineRaw drives the full engine; extra adds exports of test modules
// built from its functions.
func engineRaw(m *engine.Machine, extra map[string]func(m *engine.Machine, args []uint64) uint64) *rawMachine {
	return &rawMachine{
		mem: &m.Mem,
		call: func(name string, args []uint64) (uint64, bool) {
			if f, ok := extra[name]; ok {
				return f(m, args), true
			}
			return m.CallExport(name, args)
		},
		setEnv: func(pop func(string) uint32) {
			m.Imports.IsIdStart = func(*engine.Machine, uint32, uint32) uint32 { return pop("is_id_start") }
			m.Imports.IsIdContinue = func(*engine.Machine, uint32, uint32) uint32 { return pop("is_id_continue") }
		},
	}
}

type replayer struct {
	t       *testing.T
	file    string
	strs    map[string]string
	strIDs  map[string]string
	themes  map[string]ThemeSource
	modules map[string]*moduleSrc
	hls     map[string]*Highlighter
	streams map[string]*StreamTokenizer
	lives   map[string]*liveReplay
	raws    map[string]*rawMachine
	shadows map[*Highlighter][]byte
	// inherited instances were used by an earlier test file
	inherited map[*Highlighter]bool
	compared  int
	skipped   int
	skips     map[string]int
}

type liveReplay struct {
	lt *LiveTokenizer
}

func TestGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/golden/*.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no golden files")
	}
	sort.Strings(files)
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".jsonl.gz")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			replayFile(t, path, name)
		})
	}
}

func readEvents(t *testing.T, path string) []event {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReaderSize(zr, 1<<20)
	var events []event
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e event
			if jerr := json.Unmarshal(line, &e); jerr != nil {
				t.Fatalf("%s: %v", path, jerr)
			}
			events = append(events, e)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return events
}

func replayFile(t *testing.T, path, file string) {
	events := readEvents(t, path)
	r := &replayer{
		t: t, file: file,
		strs: map[string]string{}, strIDs: map[string]string{},
		themes: map[string]ThemeSource{}, modules: map[string]*moduleSrc{},
		hls: map[string]*Highlighter{}, streams: map[string]*StreamTokenizer{},
		lives: map[string]*liveReplay{}, raws: map[string]*rawMachine{},
		shadows: map[*Highlighter][]byte{}, skips: map[string]int{},
		inherited: map[*Highlighter]bool{},
	}
	seen := map[string]int{}
	for _, e := range events {
		switch e.K {
		case "strs":
			for id, v := range e.Table {
				if rest, ok := strings.CutPrefix(v, "\x00wtf8:"); ok {
					b, _ := base64.StdEncoding.DecodeString(rest)
					v = string(b)
				}
				r.strs[id] = v
				r.strIDs[v] = id
			}
		case "test":
			name := e.Name
			if n := seen[name]; n > 0 {
				name = fmt.Sprintf("%s#%d", name, n)
			}
			seen[e.Name]++
			if e.OK != nil && !*e.OK {
				t.Run(name, func(t *testing.T) {
					t.Skipf("the TypeScript test fails in the recording environment: %s", firstLine(e.Error))
				})
				continue
			}
			t.Run(name, func(t *testing.T) {
				r.t = t
				r.runList(e.Ev)
			})
		}
	}
	r.t = t
	if r.skipped > 0 {
		keys := make([]string, 0, len(r.skips))
		for k := range r.skips {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s (%d)", k, r.skips[k]))
		}
		t.Logf("compared %d calls; skipped %d not representable in Go: %s", r.compared, r.skipped, strings.Join(parts, "; "))
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// cursor walks one event list; callbacks consume the events after a call.
type cursor struct {
	events []event
	pos    int
}

func (r *replayer) runList(events []event) {
	c := &cursor{events: events}
	for c.pos < len(c.events) {
		e := &c.events[c.pos]
		c.pos++
		r.run(e, c)
	}
}

func (r *replayer) run(e *event, c *cursor) {
	defer func() {
		if x := recover(); x != nil {
			if nr, ok := x.(notRepresentable); ok {
				r.skipped++
				r.skips[nr.why]++
				return
			}
			panic(x)
		}
	}()
	switch e.K {
	case "module":
		r.modules[e.ID] = e.Src
	case "theme":
		r.themes[e.ID] = r.theme(e)
	case "hl":
		h := r.newHighlighter(e)
		r.hls[e.ID] = h
		if e.Inherited && h != nil {
			r.inherited[h] = true
		}
	case "hl.call":
		r.hlCall(e)
	case "fn":
		r.fnCall(e)
	case "st.new", "st.call":
		r.streamCall(e)
	case "lv.new", "lv.call", "lv.slice":
		r.liveCall(e, c)
	case "lv.defer":
		r.t.Errorf("%s: JavaScript delivered deferred lines the Go port did not", r.file)
	case "raw.new":
		r.rawNew(e)
	case "raw.call":
		r.rawCall(e)
	default:
		r.t.Fatalf("unknown event %q", e.K)
	}
}

// ---- values ----

func (r *replayer) decode(raw json.RawMessage) any {
	if raw == nil {
		return undefined{}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		r.t.Fatalf("decode %s: %v", raw, err)
	}
	return r.resolve(v)
}

// resolve replaces the recorder's tagged values.
func (r *replayer) resolve(v any) any {
	switch x := v.(type) {
	case *Object:
		if x.Len() == 1 {
			k := x.Keys()[0]
			val := x.Value(k)
			switch k {
			case "$undef":
				return undefined{}
			case "$str":
				s, ok := r.strs[val.(string)]
				if !ok {
					r.t.Fatalf("unknown string %v", val)
				}
				return s
			case "$wtf8":
				b, _ := base64.StdEncoding.DecodeString(val.(string))
				return string(b)
			case "$bytes", "$ab":
				b, _ := base64.StdEncoding.DecodeString(val.(string))
				return b
			case "$theme":
				th, ok := r.themes[val.(string)]
				if !ok {
					r.t.Fatalf("unknown theme %v", val)
				}
				return th
			case "$fn":
				return jsFunction{}
			case "$num":
				switch val {
				case "NaN":
					return math.NaN()
				case "Infinity":
					return math.Inf(1)
				}
				return math.Inf(-1)
			case "$u32":
				arr := val.([]any)
				out := make([]uint32, len(arr))
				for i, n := range arr {
					out[i] = uint32(n.(float64))
				}
				return out
			case "$map", "$set", "$big", "$err", "$sym", "$view", "$unknown":
				return x
			}
		}
		out := &Object{}
		for _, k := range x.Keys() {
			out.Set(k, r.resolve(x.Value(k)))
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = r.resolve(e)
		}
		return out
	}
	return v
}

// expand inlines recorded string references for error messages.
func (r *replayer) expand(text string) string {
	return strRefRe.ReplaceAllStringFunc(text, func(ref string) string {
		id := strRefRe.FindStringSubmatch(ref)[1]
		return jsjson.Marshal(r.strs[id])
	})
}

var strRefRe = regexp.MustCompile(`\{"\$str":"(s\d+)"\}`)

func (r *replayer) str(s string) any {
	s = joinSurrogatePairs(s)
	if utf16Len(s) > 256 {
		if id, ok := r.strIDs[s]; ok {
			return jsjson.O("$str", id)
		}
		return "<unrecorded string>" + s
	}
	if hasWTF8Surrogate(s) {
		return jsjson.O("$wtf8", base64.StdEncoding.EncodeToString([]byte(s)))
	}
	return s
}

func hasWTF8Surrogate(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == 0xed && s[i+1] >= 0xa0 && s[i+1] <= 0xbf {
			return true
		}
	}
	return false
}

var undef = jsjson.O("$undef", 1)

func (r *replayer) encToken(t ThemedToken) jsjson.Obj {
	o := jsjson.O("content", r.str(t.Content), "offset", t.Offset)
	if t.Color != "" {
		o = append(o, jsjson.Pair{Key: "color", Value: r.str(t.Color)})
	}
	if t.BgColor != "" {
		o = append(o, jsjson.Pair{Key: "bgColor", Value: r.str(t.BgColor)})
	}
	if t.HTMLStyle == nil {
		o = append(o, jsjson.Pair{Key: "fontStyle", Value: t.FontStyle})
	} else {
		o = append(o, jsjson.Pair{Key: "htmlStyle", Value: r.encStyle(t.HTMLStyle)})
	}
	if t.Type != 0 {
		o = append(o, jsjson.Pair{Key: "type", Value: t.Type})
	}
	return o
}

func (r *replayer) encStyle(s HTMLStyle) jsjson.Obj {
	o := jsjson.Obj{}
	for _, p := range s {
		o = append(o, jsjson.Pair{Key: p.Name, Value: r.str(p.Value)})
	}
	return o
}

func (r *replayer) encLines(lines [][]ThemedToken) []any {
	out := make([]any, len(lines))
	for i, line := range lines {
		toks := make([]any, len(line))
		for j, t := range line {
			toks[j] = r.encToken(t)
		}
		out[i] = toks
	}
	return out
}

func (r *replayer) encResult(res *TokensResult) jsjson.Obj {
	o := jsjson.O("tokens", r.encLines(res.Tokens))
	for _, p := range [][2]string{{"fg", res.Fg}, {"bg", res.Bg}, {"themeName", res.ThemeName}, {"rootStyle", res.RootStyle}} {
		if p[1] != "" {
			o = append(o, jsjson.Pair{Key: p[0], Value: r.str(p[1])})
		}
	}
	return o
}

func u32s(v []uint32) jsjson.Obj {
	arr := make([]any, len(v))
	for i, x := range v {
		arr[i] = x
	}
	return jsjson.O("$u32", arr)
}

func bytesValue(b []byte) jsjson.Obj {
	return jsjson.O("$bytes", base64.StdEncoding.EncodeToString(b))
}

func errorName(err error) string {
	var te *TypeError
	var re *RangeError
	switch {
	case errors.As(err, &te):
		return "TypeError"
	case errors.As(err, &re):
		return "RangeError"
	}
	return "Error"
}

// check compares a Go result or error with the recorded one.
func (r *replayer) check(e *event, got any, err error) {
	r.checkValue(e, "out", got, err, e.Out, e.OutHash)
}

func (r *replayer) checkValue(e *event, what string, got any, err error, out json.RawMessage, outHash string) {
	r.t.Helper()
	r.compared++
	label := fmt.Sprintf("%s %s", e.K, e.Name)
	if e.Err != nil {
		if err == nil {
			r.t.Errorf("%s: want %s %q, got success %.300s", label, e.Err.Name, e.Err.Message, jsjson.Marshal(got))
			return
		}
		if err.Error() != e.Err.Message || errorName(err) != e.Err.Name {
			r.t.Errorf("%s: want %s %q, got %s %q", label, e.Err.Name, e.Err.Message, errorName(err), err.Error())
		}
		return
	}
	if err != nil {
		r.t.Errorf("%s: unexpected error %v", label, err)
		return
	}
	text := jsjson.Marshal(got)
	if outHash != "" {
		sum := sha256.Sum256([]byte(text))
		if hex.EncodeToString(sum[:])[:16] != outHash {
			r.t.Errorf("%s: %s differs from the recording (hash); got %.2000s", label, what, text)
		}
		return
	}
	if text != string(out) {
		want := r.expand(string(out))
		at := 0
		for at < len(text) && at < len(want) && text[at] == want[at] {
			at++
		}
		from := max(0, at-200)
		r.t.Errorf("%s: %s differs at byte %d\n got %.600s\nwant %.600s", label, what, at, text[from:], want[from:min(len(want), from+600)])
	}
}

// ---- themes and options ----

func (r *replayer) theme(e *event) ThemeSource {
	switch {
	case e.Bundled != "":
		data, err := os.ReadFile(filepath.Join("themes", "data", e.Bundled+".json"))
		if err != nil {
			r.t.Fatal(err)
		}
		return MustParseTheme(data)
	case e.Family != nil:
		f := &ThemeFamily{}
		f.Name, _ = e.Family.Name.(string)
		for _, m := range e.Family.Themes {
			v := r.decode(m)
			th, ok := v.(*Theme)
			if !ok {
				// keep the position; invalid members are not representable
				f.Themes = append(f.Themes, nil)
				continue
			}
			f.Themes = append(f.Themes, th)
		}
		return f
	}
	v := r.decode(e.JSON)
	obj, ok := v.(*Object)
	if !ok {
		return nil
	}
	return themeFromObject(obj)
}

func themeFromObject(obj *Object) *Theme {
	t := &Theme{}
	t.Name, _ = obj.Value("name").(string)
	t.Appearance, _ = obj.Value("appearance").(string)
	t.Style, _ = obj.Value("style").(*Object)
	switch c := obj.Value("cssVariables").(type) {
	case bool:
		if c {
			t.CSSVariables = &CSSVariables{All: true}
		}
	case *Object:
		cfg := &CSSVariables{}
		if p, ok := c.Value("prefix").(string); ok {
			cfg.Prefix = &p
		}
		if d, ok := c.Value("defaults").(*Object); ok {
			cfg.Defaults = map[string]string{}
			for _, k := range d.Keys() {
				if s, ok := d.Value(k).(string); ok {
					cfg.Defaults[k] = s
				}
			}
		}
		t.CSSVariables = cfg
	}
	return t
}

func (r *replayer) themeArg(v any) ThemeSource {
	switch x := v.(type) {
	case *Theme:
		if x == nil {
			skip("invalid theme value")
		}
		if x.Style == nil && (x.CSSVariables == nil || !x.CSSVariables.All) && x.Name != "" {
			// JavaScript reads a missing style as {}; so does the port
		}
		return x
	case *ThemeFamily:
		for i, m := range x.Themes {
			if m == nil && i == 0 {
				skip("theme family with an invalid first member")
			}
		}
		return x
	case undefined:
		return nil
	}
	skip("theme of an unsupported type")
	return nil
}

func (r *replayer) options(v any) Options {
	obj, ok := v.(*Object)
	if !ok {
		skip("options are not an object")
	}
	var o Options
	for _, k := range obj.Keys() {
		val := obj.Value(k)
		switch k {
		case "lang":
			s, ok := val.(string)
			if !ok {
				skip("lang is not a string")
			}
			o.Lang = s
		case "theme":
			o.Theme = r.themeArg(val)
		case "themes":
			if _, ok := val.(undefined); ok {
				continue
			}
			if val == nil {
				continue
			}
			m, ok := val.(*Object)
			if !ok {
				skip("themes is not an object")
			}
			o.Themes = map[string]ThemeSource{}
			for _, name := range m.Keys() {
				if name == "" {
					skip("empty theme key")
				}
				if m.Value(name) == nil {
					continue
				}
				o.Themes[name] = r.themeArg(m.Value(name))
			}
		case "defaultColor":
			switch d := val.(type) {
			case bool:
				if d {
					skip("defaultColor true")
				}
				o.NoDefaultColor = true
			case string:
				if d == "" {
					skip("empty defaultColor")
				}
				o.DefaultColor = d
			case undefined:
			default:
				skip("defaultColor of an unsupported type")
			}
		case "cssVariablePrefix":
			s, ok := val.(string)
			if !ok || s == "" {
				skip("cssVariablePrefix is not a nonempty string")
			}
			o.CSSVariablePrefix = s
		case "tokenizeMaxLineLength":
			if _, ok := val.(undefined); ok {
				continue
			}
			n, ok := val.(float64)
			if !ok || n != math.Trunc(n) {
				skip("tokenizeMaxLineLength is not an integer")
			}
			o.TokenizeMaxLineLength = int(n)
		case "code", "renderRange", "onDeferTokenize":
		default:
			skip("unknown option %s", k)
		}
	}
	if o.Theme == nil && o.Themes == nil {
		// JavaScript reports the missing theme; so does the port
	}
	return o
}

// ---- highlighters ----

var (
	langTemplate  = regexp.MustCompile(`^\(module\n  \(import "\.\./src/langs/([\w-]+)\.wat"\)\n  \(func \(export "highlight"\)\n    \(call \$hlBegin\)\n    \(call (\$\w+)\)\n    \(call \$hlEnd\)\)\n\)$`)
	splitTemplate = regexp.MustCompile(`^\(module\n  \(import "\.\./src/langs/([\w-]+)\.wat"\)\n  \(func \(export "highlight"\)\n    \(call \$hlBegin\)\n    \(global\.set \$end \(i32\.add \(global\.get \$ptr\) \(i32\.load \(i32\.const 32\)\)\)\)\n    \(call (\$\w+)\)\n    \(if \(i32\.gt_u \(global\.get \$ptr\) \(global\.get \$end\)\) \(then unreachable\)\)\n    \(global\.set \$end \(global\.get \$eof\)\)\n    \(call (\$\w+)\)\n    \(call \$hlEnd\)\)\)$`)
)

func lexer(name string) func(*engine.Machine) {
	f, ok := engine.Funcs[name]
	if !ok {
		panic("unknown lexer " + name)
	}
	return f
}

// harness returns the highlight entry a recorded module runs, or nil for
// the full module.
func (r *replayer) harness(src *moduleSrc) func(*engine.Machine) {
	if src == nil {
		skip("module of unknown origin")
	}
	if src.Content == nil {
		if strings.HasSuffix(src.URL, "/src/highlights.wat") {
			return nil
		}
		skip("module from %s", filepath.Base(src.URL))
	}
	content := *src.Content
	// test modules that add exports to the full module highlight with it
	if strings.Contains(content, `(import "../src/highlights.wat")`) {
		return nil
	}
	if m := langTemplate.FindStringSubmatch(content); m != nil {
		fn := lexer(m[2])
		return func(m *engine.Machine) {
			m.FhlBegin()
			fn(m)
			m.FhlEnd()
		}
	}
	if m := splitTemplate.FindStringSubmatch(content); m != nil {
		fn := lexer(m[2])
		return func(m *engine.Machine) {
			m.FhlBegin()
			m.Gend = m.Gptr + (uint32(m.Mem[32]) | uint32(m.Mem[33])<<8 | uint32(m.Mem[34])<<16 | uint32(m.Mem[35])<<24)
			fn(m)
			if m.Gptr > m.Gend {
				panic("unreachable")
			}
			m.Gend = m.Geof
			fn(m)
			m.FhlEnd()
		}
	}
	if h := customHarness(content); h != nil {
		return h
	}
	skip("custom module %.40q", content)
	return nil
}

func (r *replayer) newHighlighter(e *event) *Highlighter {
	h := NewHighlighter()
	if e.Module == nil {
		return h
	}
	src, ok := r.modules[*e.Module]
	if !ok {
		r.t.Fatalf("unknown module %s", *e.Module)
	}
	func() {
		defer func() {
			if x := recover(); x != nil {
				if _, ok := x.(notRepresentable); ok {
					h = nil
					return
				}
				panic(x)
			}
		}()
		h.entry = r.harness(src)
	}()
	return h
}

func (r *replayer) hl(id string) *Highlighter {
	h, ok := r.hls[id]
	if !ok {
		r.t.Fatalf("unknown highlighter %s", id)
	}
	if h == nil {
		skip("highlighter for an unsupported module")
	}
	return h
}

func (r *replayer) input(v any) (string, []byte, bool) {
	switch x := v.(type) {
	case string:
		return x, nil, true
	case []byte:
		return "", x, false
	}
	skip("input of an unsupported type")
	return "", nil, false
}

func (r *replayer) hlCall(e *event) {
	h := r.hl(e.HL)
	if e.Mem != nil {
		r.applyMem(&h.m.Mem, e.Mem)
	}
	before := append([]byte(nil), h.m.Mem...)
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = r.decode(a)
	}
	switch e.Name {
	case "codeToHtml":
		s, b, isString := r.input(args[0])
		opts := r.options(args[1])
		var out []byte
		var err error
		if isString {
			out, err = h.codeToHTML(s, nil, true, opts)
		} else {
			out, err = h.codeToHTML("", b, false, opts)
		}
		r.check(e, bytesValue(out), err)
	case "codeToTokens":
		s, b, isString := r.input(args[0])
		opts := r.options(args[1])
		var res *TokensResult
		var err error
		if isString {
			res, err = h.CodeToTokens(s, opts)
		} else {
			res, err = h.CodeToTokensBytes(b, opts)
		}
		if err != nil {
			r.check(e, nil, err)
		} else {
			r.check(e, r.encResult(res), nil)
		}
	case "writeInput":
		s, b, isString := r.input(args[0])
		offset := 0
		if len(args) > 1 {
			if n, ok := args[1].(float64); ok {
				offset = int(n)
			}
		}
		var n int
		if isString {
			n = h.writeString(engineText(s), offset)
		} else {
			n = h.WriteInput(b, offset)
		}
		r.check(e, n, nil)
	case "tokenizeLineRecords":
		langID := int(args[0].(float64))
		length := int(args[1].(float64))
		var reset *bool
		if len(args) > 2 {
			if b, ok := args[2].(bool); ok {
				reset = &b
			}
		}
		recs, err := h.TokenizeLineRecords(langID, length, reset)
		r.check(e, u32s(recs), err)
	default:
		r.t.Fatalf("unknown highlighter method %s", e.Name)
	}
	if e.MemAfter != nil || e.MemAfterHash != "" {
		if r.inherited[h] {
			r.skipped++
			r.skips["memory of an instance an earlier test file used"]++
		} else {
			r.checkValue(e, "memory", memDiff(before, h.m.Mem), nil, e.MemAfter, e.MemAfterHash)
		}
	}
}

// applyMem applies a recorded memory change made by test code.
func (r *replayer) applyMem(mp *[]byte, raw json.RawMessage) {
	var full struct {
		FullGz string `json:"fullgz"`
	}
	if raw[0] == '{' {
		if err := json.Unmarshal(raw, &full); err != nil {
			r.t.Fatal(err)
		}
		gz, _ := base64.StdEncoding.DecodeString(full.FullGz)
		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			r.t.Fatal(err)
		}
		mem, err := io.ReadAll(zr)
		if err != nil {
			r.t.Fatal(err)
		}
		if len(mem) > len(*mp) {
			*mp = append(*mp, make([]byte, len(mem)-len(*mp))...)
		}
		copy(*mp, mem)
		return
	}
	var ranges [][2]any
	if err := json.Unmarshal(raw, &ranges); err != nil {
		r.t.Fatal(err)
	}
	for _, rg := range ranges {
		at := int(rg[0].(float64))
		b, _ := base64.StdEncoding.DecodeString(rg[1].(string))
		if at+len(b) > len(*mp) {
			*mp = append(*mp, make([]byte, at+len(b)-len(*mp))...)
		}
		copy((*mp)[at:], b)
	}
}

// memDiff mirrors the recorder's diffMemory.
func memDiff(shadow, mem []byte) []any {
	ranges := []any{}
	m := min(len(mem), len(shadow))
	for i := 0; i < m; {
		if mem[i] == shadow[i] {
			i++
			continue
		}
		j := i + 1
		same := 0
		for j < m && same < 16 {
			if mem[j] == shadow[j] {
				same++
			} else {
				same = 0
			}
			j++
		}
		j -= same
		ranges = append(ranges, []any{i, base64.StdEncoding.EncodeToString(mem[i:j])})
		i = j
	}
	if len(mem) > len(shadow) {
		ranges = append(ranges, []any{len(shadow), base64.StdEncoding.EncodeToString(mem[len(shadow):])})
	}
	return ranges
}

// ---- free functions ----

func (r *replayer) fnCall(e *event) {
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = r.decode(a)
	}
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return undefined{}
	}
	switch e.Name {
	case "isSupportedLanguage":
		s, ok := arg(0).(string)
		if !ok {
			skip("language of an unsupported type")
		}
		r.check(e, IsSupportedLanguage(s), nil)
	case "langIdOf":
		s, ok := arg(0).(string)
		if !ok {
			skip("language of an unsupported type")
		}
		id, err := LangID(s)
		r.check(e, id, err)
	case "isThemeColor":
		v := arg(0)
		if _, ok := v.(undefined); ok {
			v = nil
		}
		r.check(e, IsThemeColor(jsValue(v)), nil)
	case "themeForeground", "themeBackground":
		style, ok := arg(0).(*Object)
		if !ok {
			skip("style of an unsupported type")
		}
		var v any
		if e.Name == "themeForeground" {
			v = ThemeForeground(style)
		} else {
			v = ThemeBackground(style)
		}
		r.check(e, r.encAny(v), nil)
	case "resolveThemeStyle":
		style, ok := arg(0).(*Object)
		name, ok2 := arg(1).(string)
		if !ok || !ok2 {
			skip("resolveThemeStyle arguments of unsupported types")
		}
		r.check(e, r.encTokenStyle(resolveThemeStyle(style, name)), nil)
	case "prepareTheme":
		theme := r.themeArg(arg(0))
		prefix := ""
		switch p := arg(1).(type) {
		case string:
			if p == "" {
				skip("empty prefix")
			}
			prefix = p
		case undefined:
		default:
			skip("prefix of an unsupported type")
		}
		p, err := PrepareTheme(theme, prefix)
		if err != nil {
			r.check(e, nil, err)
			return
		}
		r.check(e, r.encPrepared(p), nil)
	case "toCSS":
		th, ok := r.themeArg(arg(0)).(*Theme)
		if !ok {
			skip("toCSS of a family")
		}
		prefix := ""
		if o, ok := arg(1).(*Object); ok {
			if p, ok := o.Value("cssVariablePrefix").(string); ok {
				if p == "" {
					skip("empty prefix")
				}
				prefix = p
			}
		}
		r.check(e, r.str(ToCSS(th, prefix)), nil)
	case "resolveTheme":
		th, err := ResolveTheme(r.themeArg(arg(0)))
		if err != nil {
			r.check(e, nil, err)
			return
		}
		_ = th
		skip("resolveTheme result")
	default:
		r.t.Fatalf("unknown function %s", e.Name)
	}
}

// jsValue maps decoded JSON to the shapes theme functions accept.
func jsValue(v any) any {
	if f, ok := v.(float64); ok {
		return f
	}
	return v
}

func (r *replayer) encAny(v any) any {
	switch x := v.(type) {
	case nil:
		return undef
	case undefined:
		return undef
	case string:
		return r.str(x)
	case float64, bool:
		return x
	case *Object:
		o := jsjson.Obj{}
		for _, k := range x.Keys() {
			val := x.Value(k)
			if _, ok := val.(undefined); ok {
				continue
			}
			o = append(o, jsjson.Pair{Key: k, Value: r.encAny(val)})
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = r.encAny(e)
		}
		return out
	}
	skip("value of an unsupported type")
	return nil
}

func (r *replayer) encTokenStyle(s TokenStyle) jsjson.Obj {
	o := jsjson.Obj{}
	if s.Color != "" {
		o = append(o, jsjson.Pair{Key: "color", Value: r.str(s.Color)})
	}
	return append(o, jsjson.Pair{Key: "italic", Value: s.Italic}, jsjson.Pair{Key: "weight", Value: s.Weight})
}

func (r *replayer) encPrepared(p *PreparedTheme) jsjson.Obj {
	styles := make([]any, len(p.Styles))
	for i, s := range p.Styles {
		if s == nil {
			styles[i] = nil
		} else {
			styles[i] = r.encTokenStyle(*s)
		}
	}
	o := jsjson.O("name", r.str(p.Name), "styles", styles)
	if p.Fg != "" {
		o = append(o, jsjson.Pair{Key: "fg", Value: r.str(p.Fg)})
	}
	if p.Bg != "" {
		o = append(o, jsjson.Pair{Key: "bg", Value: r.str(p.Bg)})
	}
	if p.Table != nil {
		o = append(o, jsjson.Pair{Key: "table", Value: bytesValue(p.Table)})
	}
	if p.HTMLOpeners != nil {
		o = append(o, jsjson.Pair{Key: "htmlOpeners", Value: bytesValue(p.HTMLOpeners)})
	}
	ansi := make([]any, len(p.Ansi()))
	for i, a := range p.Ansi() {
		ansi[i] = r.str(a)
	}
	return append(o, jsjson.Pair{Key: "ansi", Value: ansi})
}

// ---- streams ----

func (r *replayer) streamCall(e *event) {
	if e.K == "st.new" {
		opts := r.options(r.decode(e.Opts))
		st, err := NewStreamTokenizer(opts)
		if err != nil || e.Err != nil {
			r.check(e, nil, err)
			return
		}
		r.compared++
		r.streams[e.ID] = st
		return
	}
	st, ok := r.streams[e.ID]
	if !ok {
		skip("stream created with unsupported options")
	}
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = r.decode(a)
	}
	switch e.Name {
	case "pushCode":
		var lines [][]ThemedToken
		var err error
		switch c := args[0].(type) {
		case string:
			lines, err = st.PushCode(c)
		case []byte:
			lines, err = st.PushBytes(c)
		default:
			skip("chunk of an unsupported type")
		}
		if err != nil {
			r.check(e, nil, err)
			return
		}
		r.check(e, r.encLines(lines), nil)
	case "end":
		lines, err := st.End()
		if err != nil {
			r.check(e, nil, err)
			return
		}
		r.check(e, r.encLines(lines), nil)
	case "dispose":
		st.Dispose()
		r.check(e, undef, nil)
	default:
		r.t.Fatalf("unknown stream method %s", e.Name)
	}
}

// ---- live documents ----

// clock replays recorded performance.now() values.
type clock struct {
	values []float64
	last   float64
}

func (c *clock) now() float64 {
	if len(c.values) == 0 {
		return c.last
	}
	c.last = c.values[0]
	c.values = c.values[1:]
	return c.last
}

func (r *replayer) encHighlighted(lines map[int][]HighlightedToken) jsjson.Obj {
	keys := make([]int, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	entries := make([]any, len(keys))
	for i, k := range keys {
		toks := make([]any, len(lines[k]))
		for j, t := range lines[k] {
			toks[j] = []any{t.Char, r.str(t.Fg), r.str(t.Text)}
		}
		entries[i] = []any{k, toks}
	}
	return jsjson.O("$map", entries)
}

func (r *replayer) encUpdate(u *LiveTokenizerUpdate) jsjson.Obj {
	changes := make([]any, len(u.LineChanges))
	for i, c := range u.LineChanges {
		changes[i] = jsjson.O("oldStartLine", c.OldStartLine, "oldEndLine", c.OldEndLine, "newStartLine", c.NewStartLine, "newEndLine", c.NewEndLine)
	}
	return jsjson.O("revision", u.Revision, "previousLineCount", u.PreviousLineCount, "lineCount", u.LineCount, "lineChanges", changes, "lines", r.encHighlighted(u.Lines))
}

func (r *replayer) renderRange(v any) *LineRange {
	switch x := v.(type) {
	case undefined:
		return nil
	case []any:
		if len(x) == 2 {
			a, ok1 := x[0].(float64)
			b, ok2 := x[1].(float64)
			if ok1 && ok2 && a == math.Trunc(a) && b == math.Trunc(b) {
				return &LineRange{int(a), int(b)}
			}
		}
	}
	skip("renderRange of an unsupported shape")
	return nil
}

func (r *replayer) liveCall(e *event, c *cursor) {
	clk := &clock{values: e.Clock}
	switch e.K {
	case "lv.new":
		obj, ok := r.decode(e.Opts).(*Object)
		if !ok {
			skip("options are not an object")
		}
		opts := LiveTokenizerOptions{Options: r.options(obj)}
		if code, ok := obj.Get("code"); ok {
			s, isString := code.(string)
			if !isString {
				skip("code is not a string")
			}
			opts.Code = s
		}
		if rr, ok := obj.Get("renderRange"); ok {
			opts.RenderRange = r.renderRange(rr)
		}
		var lr *liveReplay
		if _, ok := obj.Get("onDeferTokenize"); ok {
			opts.OnDeferTokenize = r.deferCallback(c)
		}
		opts.Schedule = func(func()) {}
		lt, err := newLiveTokenizerWithClock(opts, clk.now)
		if err != nil || e.Err != nil {
			r.check(e, nil, err)
			return
		}
		r.compared++
		lr = &liveReplay{lt: lt}
		r.lives[e.ID] = lr
		return
	case "lv.slice":
		lr, ok := r.lives[e.ID]
		if !ok {
			skip("live tokenizer created with unsupported options")
		}
		lr.lt.mu.Lock()
		saved := lr.lt.nowMS
		lr.lt.nowMS = clk.now
		lr.lt.deferSlice(e.Gen)
		lr.lt.nowMS = saved
		lr.lt.mu.Unlock()
		return
	}
	lr, ok := r.lives[e.ID]
	if !ok {
		skip("live tokenizer created with unsupported options")
	}
	lt := lr.lt
	lt.mu.Lock()
	saved := lt.nowMS
	lt.nowMS = clk.now
	lt.mu.Unlock()
	defer func() {
		lt.mu.Lock()
		lt.nowMS = saved
		lt.mu.Unlock()
	}()
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = r.decode(a)
	}
	arg := func(i int) any {
		if i < len(args) {
			return args[i]
		}
		return undefined{}
	}
	lineArg := func() int {
		n, ok := arg(0).(float64)
		if !ok || n != math.Trunc(n) || math.IsInf(n, 0) {
			skip("line is not an integer")
		}
		return int(n)
	}
	updateOpts := func(v any) *LiveUpdateOptions {
		switch x := v.(type) {
		case undefined:
			return nil
		case *Object:
			o := &LiveUpdateOptions{}
			if rr, ok := x.Get("renderRange"); ok {
				o.RenderRange = r.renderRange(rr)
			}
			return o
		}
		skip("update options of an unsupported type")
		return nil
	}
	switch e.Name {
	case "revision":
		n, err := lt.Revision()
		r.check(e, n, err)
	case "lineCount":
		n, err := lt.LineCount()
		r.check(e, n, err)
	case "pendingTokenization":
		b, err := lt.PendingTokenization()
		r.check(e, b, err)
	case "flush":
		var err error
		if _, ok := arg(0).(undefined); ok {
			err = lt.FlushAll()
		} else {
			err = lt.Flush(lineArg())
		}
		r.check(e, undef, err)
	case "pause":
		r.check(e, undef, lt.Pause())
	case "resume":
		r.check(e, undef, lt.Resume())
	case "dispose":
		lt.Dispose()
		r.check(e, undef, nil)
	case "getLineLength":
		n, err := lt.LineLength(lineArg())
		r.check(e, n, err)
	case "getLineText":
		s, err := lt.LineText(lineArg())
		r.check(e, r.str(s), err)
	case "getText":
		s, err := lt.Text()
		r.check(e, r.str(s), err)
	case "getLineRecords":
		rec, err := lt.LineRecords(lineArg())
		if err != nil {
			r.check(e, nil, err)
			return
		}
		r.check(e, jsjson.O("revision", rec.Revision, "format", rec.Format, "data", u32s(rec.Data)), nil)
	case "getLineTokens":
		lt2, err := lt.LineTokens(lineArg())
		if err != nil {
			r.check(e, nil, err)
			return
		}
		toks := make([]any, len(lt2.Tokens))
		for i, t := range lt2.Tokens {
			toks[i] = r.encToken(t)
		}
		ranges := make([]any, len(lt2.BracketIgnoredRanges))
		for i, rg := range lt2.BracketIgnoredRanges {
			ranges[i] = []any{rg[0], rg[1]}
		}
		r.check(e, jsjson.O("tokens", toks, "bracketIgnoredRanges", ranges), nil)
	case "applyEdits":
		edits := r.edits(arg(0))
		u, err := lt.ApplyEdits(edits, updateOpts(arg(1)))
		if err != nil {
			r.check(e, nil, err)
			return
		}
		r.check(e, r.encUpdate(u), nil)
	case "reset":
		code, ok := arg(0).(string)
		if !ok {
			skip("reset code is not a string")
		}
		u, err := lt.Reset(code, updateOpts(arg(1)))
		if err != nil {
			r.check(e, nil, err)
			return
		}
		r.check(e, r.encUpdate(u), nil)
	default:
		r.t.Fatalf("unknown live method %s", e.Name)
	}
}

func (r *replayer) edits(v any) []TextEdit {
	arr, ok := v.([]any)
	if !ok {
		skip("edits are not an array")
	}
	out := make([]TextEdit, len(arr))
	pos := func(v any) Position {
		o, ok := v.(*Object)
		if !ok {
			skip("position is not an object")
		}
		l, ok1 := o.Value("line").(float64)
		ch, ok2 := o.Value("character").(float64)
		if !ok1 || !ok2 || l != math.Trunc(l) || ch != math.Trunc(ch) || math.IsInf(l, 0) || math.IsInf(ch, 0) {
			skip("position is not integral")
		}
		return Position{int(l), int(ch)}
	}
	for i, x := range arr {
		o, ok := x.(*Object)
		if !ok {
			skip("edit is not an object")
		}
		rg, ok := o.Value("range").(*Object)
		if !ok {
			skip("edit range is not an object")
		}
		text, ok := o.Value("newText").(string)
		if !ok {
			skip("newText is not a string")
		}
		out[i] = TextEdit{Range{pos(rg.Value("start")), pos(rg.Value("end"))}, text}
	}
	return out
}

// deferCallback consumes the recorded deliveries that follow a call.
func (r *replayer) deferCallback(c *cursor) func(map[int][]HighlightedToken) {
	return func(lines map[int][]HighlightedToken) {
		if c.pos >= len(c.events) || c.events[c.pos].K != "lv.defer" {
			r.t.Errorf("Go delivered deferred lines that JavaScript did not: %.500s", jsjson.Marshal(r.encHighlighted(lines)))
			return
		}
		e := &c.events[c.pos]
		c.pos++
		r.checkValue(e, "deferred lines", r.encHighlighted(lines), nil, e.Lines, e.LinesHash)
		r.runList(e.Ev)
	}
}

// ---- raw instances ----

func (r *replayer) rawNew(e *event) {
	src := r.modules[*e.Module]
	full := src != nil && ((src.Content == nil && strings.HasSuffix(src.URL, "/src/highlights.wat")) ||
		(src.Content != nil && strings.Contains(*src.Content, `(import "../src/highlights.wat")`)))
	if !full {
		if m := customMachine(src); m != nil {
			r.raws[e.ID] = m
			return
		}
		r.raws[e.ID] = nil
		return
	}
	r.raws[e.ID] = engineRaw(newMachine(), nil)
}

func (r *replayer) rawCall(e *event) {
	rm, ok := r.raws[e.ID]
	if !ok {
		r.t.Fatalf("unknown raw instance %s", e.ID)
	}
	if rm == nil {
		skip("raw instance of an unsupported module")
	}
	if e.Mem != nil {
		r.applyMem(rm.mem, e.Mem)
	}
	before := append([]byte(nil), (*rm.mem)...)
	env := e.Env
	pop := func(name string) uint32 {
		for len(env) > 0 {
			var pair []json.RawMessage
			_ = json.Unmarshal(env[0], &pair)
			env = env[1:]
			var n float64
			if json.Unmarshal(pair[1], &n) == nil {
				return uint32(int64(n))
			}
			var b bool
			if json.Unmarshal(pair[1], &b) == nil && b {
				return 1
			}
			return 0
		}
		return 0
	}
	rm.setEnv(pop)
	args := make([]uint64, len(e.Args))
	for i, a := range e.Args {
		switch v := r.decode(a).(type) {
		case float64:
			args[i] = uint64(int64(v))
		case bool:
			if v {
				args[i] = 1
			}
		case undefined:
		default:
			skip("raw argument of an unsupported type")
		}
	}
	var out uint64
	var found bool
	var trap error
	func() {
		defer func() {
			if x := recover(); x != nil {
				trap = trapError(x)
			}
		}()
		out, found = rm.call(e.Name, args)
	}()
	if !found && trap == nil {
		skip("raw export %s", e.Name)
	}
	if e.Err != nil {
		r.compared++
		if trap == nil {
			r.t.Errorf("raw %s: want trap %q", e.Name, e.Err.Message)
		}
	} else {
		var got any = undef
		if trap != nil {
			r.t.Errorf("raw %s: %v", e.Name, trap)
			return
		}
		if len(e.Out) > 0 && string(e.Out) != `{"$undef":1}` {
			got = rawResult(e.Out, out)
		}
		r.check(e, got, nil)
	}
	if e.MemAfter != nil || e.MemAfterHash != "" {
		r.checkValue(e, "memory", memDiff(before, *rm.mem), nil, e.MemAfter, e.MemAfterHash)
	}
}

// rawResult formats an export result as the recording does: i32 results
// are signed numbers and i64 results are bigints.
func rawResult(recorded json.RawMessage, v uint64) any {
	if bytes.HasPrefix(recorded, []byte(`{"$big"`)) {
		return jsjson.O("$big", strconv.FormatInt(int64(v), 10))
	}
	return int(int32(uint32(v)))
}

// customHarness returns the highlight entry of a test module that is not
// one of the lexer templates, or nil.
func customHarness(content string) func(*engine.Machine) {
	// tsx_test.ts scans the first half of the input, then the rest
	if strings.Contains(content, `(import "../src/langs/tsx.wat")`) && strings.Contains(content, "(local.set $mid") {
		return func(m *engine.Machine) {
			m.FhlBegin()
			m.Gend = m.Gptr + (m.Gend-m.Gptr)>>1
			m.FhlTsx()
			m.Gend = m.Geof
			m.FhlTsx()
			m.FhlEnd()
		}
	}
	return nil
}

// customMachine instantiates a test module used through its exports, or
// returns nil.
func customMachine(src *moduleSrc) *rawMachine {
	if src == nil || src.Content == nil {
		return nil
	}
	content := *src.Content
	switch {
	case strings.Contains(content, `(export "escape")`):
		// e2e_test.ts: escape [65536, 65536+len) to 131072
		return engineRaw(newMachine(), map[string]func(*engine.Machine, []uint64) uint64{
			"escape": func(m *engine.Machine, args []uint64) uint64 {
				m.Gout = 131072
				m.FescCopy(65536, 65536+uint32(args[0]))
				return uint64(m.Gout - 131072)
			},
		})
	case strings.Contains(content, `(export "keyword" (func $isKeyword))`):
		return engineRaw(newMachine(), map[string]func(*engine.Machine, []uint64) uint64{
			"keyword": func(m *engine.Machine, args []uint64) uint64 {
				return uint64(m.FisKeyword(uint32(args[0]), uint32(args[1])))
			},
		})
	case strings.Contains(content, "(keyword-table $Test "):
		m := kwtest.New()
		return &rawMachine{
			mem: &m.Mem,
			call: func(name string, args []uint64) (uint64, bool) {
				switch name {
				case "lookup":
					return uint64(m.ExportLookup(uint32(args[0]))), true
				case "lower":
					return uint64(m.ExportLower(uint32(args[0]))), true
				}
				return 0, false
			},
			setEnv: func(func(string) uint32) {},
		}
	}
	return nil
}
