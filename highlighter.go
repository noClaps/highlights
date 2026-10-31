package highlights

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"highlights/internal/engine"
	rt "highlights/internal/wasmrt"
)

const (
	pageSize = rt.PageSize
	themePtr = 64    // $mem.themeTable in src/memory.wat
	inputPtr = 81920 // $mem.input in internal/engine/wat/memory.wat
)

// Options configure highlighting and tokenization.
type Options struct {
	// Lang is a built-in language name or alias, or "ansi".
	Lang string
	ThemeOptions
	// TokenizeMaxLineLength turns lines at or above this UTF-16 length into
	// one token without syntax styles. 0 disables the limit.
	TokenizeMaxLineLength int
}

type languageName struct {
	name string
	id   int
}

var languageIDs = func() map[string]int {
	m := make(map[string]int, len(languageTable))
	for _, l := range languageTable {
		m[l.name] = l.id
	}
	return m
}()

// Languages returns every accepted language name and alias with its lexer
// ID, sorted by ID and then name.
func Languages() map[string]int {
	out := make(map[string]int, len(languageIDs))
	for k, v := range languageIDs {
		out[k] = v
	}
	return out
}

// LangID resolves the lexer ID for a name or alias, ignoring case.
func LangID(lang string) (int, error) {
	id, ok := languageIDs[strings.ToLower(lang)]
	if !ok {
		return 0, newError(ErrUnknownLanguage, "unknown lang: "+lang)
	}
	return id, nil
}

// IsSupportedLanguage reports whether a name or alias has a built-in lexer,
// including plain text, or is "ansi".
func IsSupportedLanguage(lang string) bool {
	_, ok := languageIDs[strings.ToLower(lang)]
	return ok || isAnsi(lang)
}

// Highlighter owns one engine instance. It is not safe for concurrent use;
// the package-level functions pool instances for concurrent callers.
type Highlighter struct {
	m *engine.Machine
	// entry replaces the highlight export, for lexer test harnesses
	entry           func(*engine.Machine)
	themeWritten    []byte
	htmlPrefix      string
	htmlPrefixSet   bool
	htmlPrefixBytes []byte
}

// NewHighlighter creates a highlighter with its own engine instance.
func NewHighlighter() *Highlighter {
	return &Highlighter{m: newMachine()}
}

func newMachine() *engine.Machine {
	m := engine.New()
	m.Imports.IsIdStart = func(m *engine.Machine, ptr, n uint32) uint32 { return idClass(m.Mem, ptr, n, 0) }
	m.Imports.IsIdContinue = func(m *engine.Machine, ptr, n uint32) uint32 { return idClass(m.Mem, ptr, n, 2) }
	return m
}

// pages returns the instance's memory size in Wasm pages.
func (h *Highlighter) pages() int { return len(h.m.Mem) / pageSize }

func (h *Highlighter) growMemoryIfNeeded(n int) {
	needed := (inputPtr + n + pageSize - 1) / pageSize
	if pages := len(h.m.Mem) / pageSize; needed > pages {
		rt.Grow(&h.m.Mem, uint32(needed-pages))
	}
}

// writeInput writes bytes at an offset in the input buffer and returns
// their length.
func (h *Highlighter) writeInput(input []byte, offset int) int {
	h.growMemoryIfNeeded(offset + len(input) + 96)
	copy(h.m.Mem[inputPtr+offset:], input)
	return len(input)
}

// writeString writes valid UTF-8 text. Memory grows as the TypeScript host
// grows it around TextEncoder.encodeInto: first by the UTF-16 length, then,
// if the bytes do not fit, by three bytes per remaining UTF-16 unit.
func (h *Highlighter) writeString(input string, offset int) int {
	h.growMemoryIfNeeded(offset + utf16Len(input) + 96)
	if avail := len(h.m.Mem) - 96 - (inputPtr + offset); len(input) > avail {
		written := 0
		for written < len(input) {
			_, size := utf8.DecodeRuneInString(input[written:])
			if written+size > avail {
				break
			}
			written += size
		}
		h.growMemoryIfNeeded(offset + written + utf16Len(input[written:])*3 + 96)
	}
	copy(h.m.Mem[inputPtr+offset:], input)
	return len(input)
}

// trapError converts an engine trap into an error.
func trapError(r any) error {
	if e, ok := r.(error); ok {
		var trap rt.Trap
		if errors.As(e, &trap) {
			return e
		}
		return fmt.Errorf("wasm trap: %w", e)
	}
	return fmt.Errorf("wasm trap: %v", r)
}

// run lexes the first inputLength bytes. Modes: 0 inline colors, 1 CSS
// variables, 2 packed themes, 3 UTF-16 line records, 4 prepared HTML
// openers. A non-nil reset selects streaming mode.
func (h *Highlighter) run(langID, mode, inputLength int, reset *bool) (err error) {
	mem := h.m.Mem
	mem[0] = byte(langID)
	mem[1] = byte(mode)
	binary.LittleEndian.PutUint32(mem[2:], uint32(inputLength))
	mem[inputPtr+inputLength] = 0 // NUL sentinel
	defer func() {
		if r := recover(); r != nil {
			err = trapError(r)
		}
	}()
	if reset == nil && h.entry != nil {
		h.entry(h.m)
	} else if reset == nil {
		h.m.ExportHighlight()
	} else if *reset {
		h.m.ExportHighlightStream(1)
	} else {
		h.m.ExportHighlightStream(0)
	}
	return nil
}

func (h *Highlighter) output() []byte {
	mem := h.m.Mem
	start := binary.LittleEndian.Uint32(mem[6:])
	n := binary.LittleEndian.Uint32(mem[10:])
	return mem[start : start+n]
}

// CodeToHTML returns a `<pre class="highlights">` fragment. With Themes,
// each span applies the default theme inline and the other themes as CSS
// custom properties.
func (h *Highlighter) CodeToHTML(code string, opts Options) (string, error) {
	out, err := h.codeToHTML(code, nil, true, opts)
	return string(out), err
}

// CodeToHTMLBytes highlights UTF-8 bytes. Malformed sequences pass through
// unchanged. The result is a fresh copy.
func (h *Highlighter) CodeToHTMLBytes(code []byte, opts Options) ([]byte, error) {
	out, err := h.codeToHTML("", code, false, opts)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), out...), nil
}

// codeToHTML returns a view of engine memory, valid until the next call.
func (h *Highlighter) codeToHTML(s string, b []byte, isString bool, opts Options) ([]byte, error) {
	prefix := opts.prefix()
	if isAnsi(opts.Lang) {
		set, err := resolveOptionThemes(&opts.ThemeOptions)
		if err != nil {
			return nil, err
		}
		var code string
		if isString {
			code, _ = normalizeText(s)
		} else {
			code, _ = decodeUTF8(b)
		}
		return []byte(engineText(ansiHTML(code, set, prefix))), nil
	}
	langID, err := LangID(opts.Lang)
	if err != nil {
		return nil, err
	}
	var table, blob []byte
	mode := 0
	if opts.Themes != nil {
		set, err := resolveOptionThemes(&opts.ThemeOptions)
		if err != nil {
			return nil, err
		}
		blob = set.multiThemeBlob(prefix)
		mode = 2
		if blob == nil {
			blob = set.multiThemeHTMLOpeners(prefix)
			mode = 4
		}
	} else {
		prepared, err := PrepareTheme(opts.Theme, prefix)
		if err != nil {
			return nil, err
		}
		table = prepared.Table
		blob = prepared.HTMLOpeners
		if blob != nil {
			mode = 4
		}
	}
	var inputLength int
	if isString {
		inputLength = h.writeString(engineText(s), 0)
	} else {
		inputLength = h.writeInput(b, 0)
	}
	le := binary.LittleEndian
	switch {
	case blob != nil:
		h.growMemoryIfNeeded(inputLength + len(blob) + 96)
		blobPtr := (inputPtr + inputLength + 47) &^ 15
		copy(h.m.Mem[blobPtr:], blob)
		le.PutUint32(h.m.Mem[14:], uint32(blobPtr))
		le.PutUint32(h.m.Mem[18:], uint32(len(blob)))
	case table == nil:
		if !h.htmlPrefixSet || h.htmlPrefix != prefix {
			h.htmlPrefixBytes = []byte(escapeAttribute(prefix))
			h.htmlPrefix = prefix
			h.htmlPrefixSet = true
		}
		bytes := h.htmlPrefixBytes
		h.growMemoryIfNeeded(inputLength + len(bytes) + 96)
		prefixPtr := (inputPtr + inputLength + 47) &^ 15
		copy(h.m.Mem[prefixPtr:], bytes)
		le.PutUint32(h.m.Mem[14:], uint32(prefixPtr))
		le.PutUint32(h.m.Mem[18:], uint32(len(bytes)))
		mode = 1
	default:
		if h.themeWritten == nil || &h.themeWritten[0] != &table[0] {
			copy(h.m.Mem[themePtr:], table)
			clear(h.m.Mem[themePtr+len(table) : themePtr+themeTableBytes])
			h.themeWritten = table
		}
		mode = 0
	}
	if err := h.run(langID, mode, inputLength, nil); err != nil {
		return nil, err
	}
	return h.output(), nil
}

// tokenizeLineRecords lexes inputLength bytes already written with
// writeInput and returns UTF-16 token records: (endUtf16, tokenID) pairs,
// with tokenID 0xffffffff marking a line terminator. A non-nil reset starts
// or continues a stream. The slice aliases engine memory.
func (h *Highlighter) tokenizeLineRecords(langID, inputLength int, reset *bool) ([]uint32, error) {
	if err := h.run(langID, 3, inputLength, reset); err != nil {
		return nil, err
	}
	out := h.output()
	recs := make([]uint32, len(out)/4)
	for i := range recs {
		recs[i] = binary.LittleEndian.Uint32(out[i*4:])
	}
	return recs, nil
}

// CodeToTokens tokenizes code into Shiki-compatible themed tokens, one
// slice per line. Malformed UTF-8 is replaced before lexing. Lone
// surrogates encoded as WTF-8 stay in token content, as in JavaScript
// strings, and lex as U+FFFD.
func (h *Highlighter) CodeToTokens(code string, opts Options) (*TokensResult, error) {
	content, engine := normalizeText(code)
	return h.codeToTokens(content, engine, opts)
}

// CodeToTokensBytes is CodeToTokens for UTF-8 bytes.
func (h *Highlighter) CodeToTokensBytes(code []byte, opts Options) (*TokensResult, error) {
	s, _ := decodeUTF8(code)
	return h.codeToTokens(s, s, opts)
}

func (h *Highlighter) codeToTokens(code, engine string, opts Options) (*TokensResult, error) {
	set, err := resolveOptionThemes(&opts.ThemeOptions)
	if err != nil {
		return nil, err
	}
	prefix := opts.prefix()
	var tokens [][]ThemedToken
	if isAnsi(opts.Lang) {
		tokens = ansiTokens(code, set, prefix, opts.TokenizeMaxLineLength, newAnsiState(), 0)
	} else {
		langID, err := LangID(opts.Lang)
		if err != nil {
			return nil, err
		}
		recs, err := h.tokenizeLineRecords(langID, h.writeString(engine, 0), nil)
		if err != nil {
			return nil, err
		}
		tokens = set.lineRecordsToTokens(newUTF16Text(code), recs, prefix, opts.TokenizeMaxLineLength, 0)
	}
	meta := set.meta(prefix)
	return &TokensResult{Tokens: tokens, Fg: meta.fg, Bg: meta.bg, ThemeName: meta.themeName, RootStyle: meta.rootStyle}, nil
}

// The package-level functions share pooled instances. Memory cannot shrink,
// so instances above 8 MiB are not returned to the pool.
var sharedPool = sync.Pool{New: func() any { return NewHighlighter() }}

func getShared() *Highlighter { return sharedPool.Get().(*Highlighter) }

func putShared(h *Highlighter) {
	if h.pages() <= 128 {
		sharedPool.Put(h)
	}
}

// CodeToHTML highlights code with a pooled highlighter.
func CodeToHTML(code string, opts Options) (string, error) {
	h := getShared()
	defer putShared(h)
	return h.CodeToHTML(code, opts)
}

// CodeToHTMLBytes highlights UTF-8 bytes with a pooled highlighter.
func CodeToHTMLBytes(code []byte, opts Options) ([]byte, error) {
	h := getShared()
	defer putShared(h)
	return h.CodeToHTMLBytes(code, opts)
}

// CodeToTokens tokenizes code with a pooled highlighter.
func CodeToTokens(code string, opts Options) (*TokensResult, error) {
	h := getShared()
	defer putShared(h)
	return h.CodeToTokens(code, opts)
}

// CodeToTokensBytes tokenizes UTF-8 bytes with a pooled highlighter.
func CodeToTokensBytes(code []byte, opts Options) (*TokensResult, error) {
	h := getShared()
	defer putShared(h)
	return h.CodeToTokensBytes(code, opts)
}

// One completed stream instance can be reused by the next stream.
var (
	streamPoolMu sync.Mutex
	streamPool   *Highlighter
)

// StreamTokenizer converts text or UTF-8 chunks to themed token lines, one
// slice per completed line. An isolated engine instance scans each
// completed chunk once and keeps lexer state between chunks.
type StreamTokenizer struct {
	hl               *Highlighter
	langID           int
	ansi             *ansiState
	set              *themeSet
	prefix           string
	maxLineLength    int
	pendingBytes     []byte
	pendingSurrogate string // an incomplete code point at the end of a text chunk
	tail             string
	streamChar       int
	streamStarted    bool
}

// NewStreamTokenizer validates the options and takes an engine instance.
func NewStreamTokenizer(opts Options) (*StreamTokenizer, error) {
	s := &StreamTokenizer{}
	if isAnsi(opts.Lang) {
		s.ansi = newAnsiState()
	} else {
		id, err := LangID(opts.Lang)
		if err != nil {
			return nil, err
		}
		s.langID = id
	}
	set, err := resolveOptionThemes(&opts.ThemeOptions)
	if err != nil {
		return nil, err
	}
	s.set = set
	streamPoolMu.Lock()
	s.hl = streamPool
	streamPool = nil
	streamPoolMu.Unlock()
	if s.hl == nil {
		s.hl = NewHighlighter()
	}
	s.prefix = opts.prefix()
	s.maxLineLength = opts.TokenizeMaxLineLength
	return s, nil
}

// PushBytes appends UTF-8 bytes and returns tokens for each completed line,
// with offsets relative to the full input. Incomplete UTF-8 stays buffered.
func (s *StreamTokenizer) PushBytes(chunk []byte) ([][]ThemedToken, error) {
	if s.hl == nil {
		return nil, ErrStreamEnded
	}
	if len(chunk) == 0 {
		return [][]ThemedToken{}, nil
	}
	end := lastIndexByte(chunk, '\n') + 1
	if end == 0 {
		s.pendingBytes = append(s.pendingBytes, chunk...)
		return [][]ThemedToken{}, nil
	}
	if len(s.pendingBytes) > 0 {
		end += len(s.pendingBytes)
		s.pendingBytes = append(s.pendingBytes, chunk...)
		chunk = s.pendingBytes
		s.pendingBytes = nil
	}
	prefix := s.tail + s.pendingSurrogate
	lines, err := s.tokenizeBytes(chunk[:end], prefix)
	if err != nil {
		return nil, err
	}
	s.tail = ""
	s.pendingSurrogate = ""
	if end < len(chunk) {
		s.pendingBytes = append([]byte(nil), chunk[end:]...)
	}
	return lines[:len(lines)-1], nil
}

// Push appends text and returns tokens for each completed line. A
// nonempty chunk first flushes buffered bytes. The final line stays
// buffered until a newline or End.
func (s *StreamTokenizer) Push(chunk string) ([][]ThemedToken, error) {
	if s.hl == nil {
		return nil, ErrStreamEnded
	}
	if len(chunk) == 0 {
		return [][]ThemedToken{}, nil
	}
	if len(s.pendingBytes) > 0 {
		decoded, _ := decodeUTF8(s.pendingBytes)
		chunk = decoded + chunk
		s.pendingBytes = nil
	}
	chunk = s.pendingSurrogate + chunk
	s.pendingSurrogate = ""
	// keep an incomplete trailing code point for the next chunk
	if cut := incompleteSuffix(chunk); cut > 0 {
		s.pendingSurrogate = chunk[len(chunk)-cut:]
		chunk = chunk[:len(chunk)-cut]
	}
	end := strings.LastIndexByte(chunk, '\n') + 1
	if end == 0 {
		s.tail += chunk
		return [][]ThemedToken{}, nil
	}
	code := s.tail + chunk[:end]
	s.tail = chunk[end:]
	lines, err := s.tokenizeText(code)
	if err != nil {
		return nil, err
	}
	return lines[:len(lines)-1], nil
}

// End finishes decoding and returns the remaining lines. A trailing line
// terminator produces a final empty line, as in CodeToTokens.
func (s *StreamTokenizer) End() ([][]ThemedToken, error) {
	if s.hl == nil {
		return nil, ErrStreamEnded
	}
	defer s.Close()
	if len(s.pendingBytes) > 0 {
		return s.tokenizeBytes(s.pendingBytes, s.tail+s.pendingSurrogate)
	}
	s.tail += s.pendingSurrogate
	s.pendingSurrogate = ""
	if s.tail == "" {
		return [][]ThemedToken{{}}, nil
	}
	code := s.tail
	s.tail = ""
	return s.tokenizeText(code)
}

// Close releases the engine instance and discards buffered input; later
// calls return ErrStreamEnded. It always returns nil.
func (s *StreamTokenizer) Close() error {
	hl := s.hl
	if hl == nil {
		return nil
	}
	streamPoolMu.Lock()
	if streamPool == nil && hl.pages() <= 16 {
		streamPool = hl
	}
	streamPoolMu.Unlock()
	s.hl = nil
	s.pendingBytes = nil
	s.pendingSurrogate = ""
	s.tail = ""
	return nil
}

func (s *StreamTokenizer) tokenizeBytes(input []byte, prefix string) ([][]ThemedToken, error) {
	code, _ := decodeUTF8(input)
	return s.tokenizeText(prefix + code)
}

func (s *StreamTokenizer) tokenizeText(raw string) ([][]ThemedToken, error) {
	code, engine := normalizeText(raw)
	var lines [][]ThemedToken
	if s.ansi == nil {
		n := s.hl.writeString(engine, 0)
		reset := !s.streamStarted
		recs, err := s.hl.tokenizeLineRecords(s.langID, n, &reset)
		if err != nil {
			return nil, err
		}
		s.streamStarted = true
		lines = s.set.lineRecordsToTokens(newUTF16Text(code), recs, s.prefix, s.maxLineLength, s.streamChar)
	} else {
		lines = ansiTokens(code, s.set, s.prefix, s.maxLineLength, s.ansi, s.streamChar)
	}
	s.streamChar += utf16Len(code)
	return lines, nil
}

func lastIndexByte(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// incompleteSuffix returns the length of a trailing incomplete but valid
// UTF-8 prefix or a WTF-8 high surrogate, which a later chunk can complete.
func incompleteSuffix(s string) int {
	if n := len(s); n >= 3 {
		if hi := surrogateAt(s, n-3); hi >= 0xd800 && hi < 0xdc00 {
			return 3
		}
	}
	for n := 1; n <= 3 && n <= len(s); n++ {
		c := s[len(s)-n]
		if c < 0x80 {
			return 0
		}
		if c >= 0xc0 {
			need := 0
			switch {
			case c >= 0xf0:
				need = 4
			case c >= 0xe0:
				need = 3
			default:
				need = 2
			}
			if need > n && !utf8.FullRuneInString(s[len(s)-n:]) {
				return n
			}
			return 0
		}
	}
	return 0
}
