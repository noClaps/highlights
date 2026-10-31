package highlights

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// TokenNames lists `$Token` names in slot order; a raw record's token ID
// indexes it.
var TokenNames = TokenTypes

// Position is a zero-based UTF-16 position; Character may equal the line
// length.
type Position struct {
	Line      int
	Character int
}

// Range is an end-exclusive range.
type Range struct {
	Start, End Position
}

// TextEdit replaces a range of the pre-edit document.
type TextEdit struct {
	Range   Range
	NewText string
}

// LiveLineChange is one coalesced half-open span of changed or
// re-tokenized lines.
type LiveLineChange struct {
	OldStartLine, OldEndLine int
	NewStartLine, NewEndLine int
}

// HighlightedToken is one styled run: its UTF-16 start column, CSS color,
// and text. Unstyled runs use the theme foreground, or "" if none is
// defined. Colors come from the first resolved theme.
type HighlightedToken struct {
	Char int
	Fg   string
	Text string
}

// LineRange is a half-open [Start, End) range of post-edit line numbers.
type LineRange struct {
	Start, End int
}

// LiveUpdateOptions configure one update. With a RenderRange, synchronous
// tokenization targets those lines with a one-millisecond budget and
// OnDeferTokenize receives unfinished lines later.
type LiveUpdateOptions struct {
	RenderRange *LineRange
}

// LiveTokenizerOptions are tokenization options plus the initial document.
type LiveTokenizerOptions struct {
	Options
	// Code is the initial document.
	Code string
	// OnDeferTokenize receives completed lines outside RenderRange during
	// an update, then all deferred lines in background batches. Line
	// numbers refer to the document at delivery time.
	OnDeferTokenize func(lines map[int][]HighlightedToken)
	// RenderRange requests initial tokenization of these lines within the
	// time budget.
	RenderRange *LineRange
	// Schedule runs background work. Nil runs each batch on a new goroutine;
	// the tokenizer's lock keeps it from overlapping other calls.
	Schedule func(task func())
	// Now reads the clock for time budgets. Nil uses time.Now.
	Now func() time.Time
}

// LiveTokenizerUpdate is the result of a successful update batch.
type LiveTokenizerUpdate struct {
	Revision          int
	PreviousLineCount int
	LineCount         int
	LineChanges       []LiveLineChange
	// Lines holds lines completed within RenderRange, keyed by post-edit
	// line number. It is empty without a RenderRange.
	Lines map[int][]HighlightedToken
}

// LiveTokenRecords is a copy of one line's token records. Packed24 stores
// one `(tokenId << 24) | endUtf16` word per token; Wide32 stores
// [endUtf16, tokenId] pairs when line ends exceed 24 bits. Each record
// starts at the previous end, or 0 for the first record.
type LiveTokenRecords struct {
	Revision int
	Format   string // "packed24" or "wide32"
	Data     []uint32
}

// LineTokens are themed tokens for one line plus the string, comment, and
// regex ranges that bracket matching ignores. Offsets are line-relative.
type LineTokens struct {
	Tokens               []ThemedToken
	BracketIgnoredRanges [][2]int
}

// ErrDisposed is returned after Dispose.
var ErrDisposed = errors.New("tokenizer is disposed")

var errUpdating = errors.New("applyEdits and reset cannot be called from onDeferTokenize during an update; use pause, flush, or the reads instead")

type normalizedEdit struct {
	sl, sc, el, ec int
	newText        string
}

type materializing struct {
	line, byte int
	text       string
	record     int
	start      int
	tokens     []HighlightedToken
}

// LiveTokenizer is an editable document with an incremental tokenizer and
// its own engine instance. Edits use UTF-16 positions and can split or
// join `\r\n`, `\n`, or lone `\r` terminators. Lexers receive each line
// break as `\n`.
//
// Tokenization starts at each changed region and stops when the outgoing
// lexer state matches the previous state. A RenderRange limits synchronous
// work; background batches deliver the rest through OnDeferTokenize.
//
// Methods are safe for concurrent use. OnDeferTokenize runs without the
// lock held, so it may call any method.
type LiveTokenizer struct {
	mu              sync.Mutex
	hl              *Highlighter
	langID          int
	set             *themeSet
	prefix          string
	maxLineLength   int
	onDeferTokenize func(map[int][]HighlightedToken)
	schedule        func(func())
	now             func() time.Time
	revision        int
	deferGeneration int
	materializing   *materializing
	paused          bool
	updating        bool
}

func checkRenderRange(r *LineRange) error {
	if r != nil && (r.Start < 0 || r.End < r.Start) {
		return &TypeError{"renderRange must be [startLine, endLine] with 0 <= startLine <= endLine"}
	}
	return nil
}

// eol returns a line's terminator from its descriptor flags: byte-count
// bits 0-1 (0 none, 1 one byte, 2 CRLF) plus bit 4 marking a one-byte CR.
func eol(flags uint32) string {
	switch flags & 3 {
	case 0:
		return ""
	case 2:
		return "\r\n"
	}
	if flags&16 != 0 {
		return "\r"
	}
	return "\n"
}

// NewLiveTokenizer creates a tokenizer for an initial document.
func NewLiveTokenizer(opts LiveTokenizerOptions) (*LiveTokenizer, error) {
	t := &LiveTokenizer{}
	// an editor needs tokens that cover the raw text, so `ansi` keeps its
	// escape sequences and uses the plain lexer
	if !isAnsi(opts.Lang) {
		id, err := LangID(opts.Lang)
		if err != nil {
			return nil, err
		}
		t.langID = id
	}
	set, err := resolveOptionThemes(&opts.ThemeOptions)
	if err != nil {
		return nil, err
	}
	t.set = set
	t.prefix = opts.prefix()
	t.maxLineLength = opts.TokenizeMaxLineLength
	t.onDeferTokenize = opts.OnDeferTokenize
	t.schedule = opts.Schedule
	t.now = opts.Now
	if t.now == nil {
		t.now = time.Now
	}
	if err := checkRenderRange(opts.RenderRange); err != nil {
		return nil, err
	}
	hl, err := createStaged(opts.Code, t.langID)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hl = hl
	if _, err := t.runSlice(hl, opts.RenderRange); err != nil {
		return nil, err
	}
	return t, nil
}

// guard runs an engine call and converts a trap to an error.
func guard(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = trapError(r)
		}
	}()
	f()
	return nil
}

// createStaged instantiates an engine and stages code into its line table.
func createStaged(code string, langID int) (*Highlighter, error) {
	hl := NewHighlighter()
	bytes := toLiveText(code)
	err := guard(func() {
		ptr := hl.m.ExportLiveStage(uint32(len(bytes)))
		copy(hl.m.Mem[ptr:], bytes)
		hl.m.ExportLiveInitDoc(ptr, uint32(len(bytes)), uint32(langID))
	})
	return hl, err
}

func (t *LiveTokenizer) live() (*Highlighter, error) {
	if t.hl == nil {
		return nil, ErrDisposed
	}
	return t.hl, nil
}

// Revision is a monotonic count of successful mutating batches.
func (t *LiveTokenizer) Revision() (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.live(); err != nil {
		return 0, err
	}
	return t.revision, nil
}

// LineCount returns the number of lines; a trailing terminator adds one.
func (t *LiveTokenizer) LineCount() (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hl, err := t.live()
	if err != nil {
		return 0, err
	}
	return int(hl.m.ExportLiveLineCount()), nil
}

// PendingTokenization reports whether deferred scanning or token
// materialization remains.
func (t *LiveTokenizer) PendingTokenization() (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hl, err := t.live()
	if err != nil {
		return false, err
	}
	return t.pending(hl), nil
}

func (t *LiveTokenizer) pending(hl *Highlighter) bool {
	return t.materializing != nil || hl.m.ExportLiveStats(9) != 0
}

// Flush completes pending lines before endLine and delivers them through
// OnDeferTokenize. It clears any pause and schedules remaining work in the
// background.
func (t *LiveTokenizer) Flush(endLine int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := checkRenderRange(&LineRange{0, endLine}); err != nil {
		return err
	}
	return t.flush(endLine, false)
}

// FlushAll completes every pending line.
func (t *LiveTokenizer) FlushAll() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.flush(0, true)
}

func (t *LiveTokenizer) flush(endLine int, all bool) error {
	hl, err := t.live()
	if err != nil {
		return err
	}
	t.paused = false
	t.deferGeneration++
	generation := t.deferGeneration
	count := int(hl.m.ExportLiveLineCount())
	end := count
	if !all {
		end = min(endLine, count)
	}
	_, err = t.workSlice(hl, end, time.Time{}, nil)
	if generation == t.deferGeneration && t.hl == hl && !t.paused && t.pending(hl) {
		t.scheduleSlice(generation)
	}
	return err
}

// Pause stops background tokenization and keeps the pending work.
func (t *LiveTokenizer) Pause() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.live(); err != nil {
		return err
	}
	if t.paused {
		return nil
	}
	t.paused = true
	t.deferGeneration++
	return nil
}

// Resume restarts background tokenization suspended by Pause.
func (t *LiveTokenizer) Resume() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resume()
}

func (t *LiveTokenizer) resume() error {
	hl, err := t.live()
	if err != nil {
		return err
	}
	if !t.paused {
		return nil
	}
	t.paused = false
	if t.pending(hl) {
		t.scheduleSlice(t.deferGeneration)
	}
	return nil
}

// Dispose releases the engine instance and drops deferred work; later
// calls return ErrDisposed.
func (t *LiveTokenizer) Dispose() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.hl == nil {
		return
	}
	t.deferGeneration++
	t.hl = nil
	t.materializing = nil
}

func (t *LiveTokenizer) checkLine(hl *Highlighter, line int) error {
	if line < 0 || line >= int(hl.m.ExportLiveLineCount()) {
		return &RangeError{fmt.Sprintf("line %d out of range", line)}
	}
	return nil
}

// LineLength returns the UTF-16 length of a line, excluding its terminator.
func (t *LiveTokenizer) LineLength(line int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hl, err := t.live()
	if err != nil {
		return 0, err
	}
	if err := t.checkLine(hl, line); err != nil {
		return 0, err
	}
	return int(hl.m.ExportLiveLineLen(uint32(line))), nil
}

// LineText returns a line's text, excluding its terminator. Lone
// surrogates are kept as WTF-8.
func (t *LiveTokenizer) LineText(line int) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hl, err := t.live()
	if err != nil {
		return "", err
	}
	if err := t.checkLine(hl, line); err != nil {
		return "", err
	}
	return lineText(hl, line), nil
}

// Text returns the whole document, reconstructing each line terminator.
func (t *LiveTokenizer) Text() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hl, err := t.live()
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	count := int(hl.m.ExportLiveLineCount())
	for line := 0; line < count; line++ {
		sb.WriteString(lineText(hl, line))
		sb.WriteString(eol(hl.m.ExportLiveLineFlags(uint32(line))))
	}
	return sb.String(), nil
}

func lineText(hl *Highlighter, line int) string {
	ptr := hl.m.ExportLiveLineTextPtr(uint32(line))
	n := hl.m.ExportLiveLineByteLen(uint32(line))
	if n == 0 {
		return ""
	}
	return string(hl.m.Mem[ptr : ptr+n])
}

func lineRecords(hl *Highlighter, line int, count int) (data []uint32, wide bool) {
	wide = hl.m.ExportLiveLineFlags(uint32(line))&4 != 0
	if count == 0 {
		return nil, wide
	}
	ptr := int(hl.m.ExportLiveLineTokPtr(uint32(line)))
	n := count
	if wide {
		n *= 2
	}
	data = make([]uint32, n)
	for i := range data {
		data[i] = binary.LittleEndian.Uint32(hl.m.Mem[ptr+4*i:])
	}
	return data, wide
}

// LineRecords returns a copy of one line's records.
func (t *LiveTokenizer) LineRecords(line int) (*LiveTokenRecords, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hl, err := t.live()
	if err != nil {
		return nil, err
	}
	if err := t.checkLine(hl, line); err != nil {
		return nil, err
	}
	data, wide := lineRecords(hl, line, int(hl.m.ExportLiveLineTokCount(uint32(line))))
	format := "packed24"
	if wide {
		format = "wide32"
	}
	if data == nil {
		data = []uint32{}
	}
	return &LiveTokenRecords{Revision: t.revision, Format: format, Data: data}, nil
}

func recordAt(data []uint32, wide bool, r int) (end, hl int) {
	if wide {
		return int(data[r*2]), int(data[r*2+1])
	}
	return int(data[r] & 0xffffff), int(data[r] >> 24)
}

// LineTokens returns themed tokens and bracket-ignored ranges for a line.
// At TokenizeMaxLineLength it returns one token without syntax styles.
// Pending lines return their previous tokens; a new line with no records
// returns its text as one unstyled token.
func (t *LiveTokenizer) LineTokens(line int) (*LineTokens, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hl, err := t.live()
	if err != nil {
		return nil, err
	}
	if err := t.checkLine(hl, line); err != nil {
		return nil, err
	}
	text := newUTF16Text(lineText(hl, line))
	length := utf16Len(text.s)
	if t.maxLineLength > 0 && length >= t.maxLineLength {
		return &LineTokens{Tokens: []ThemedToken{t.set.rangeToToken(text.s, 0, 0, t.prefix)}, BracketIgnoredRanges: [][2]int{}}, nil
	}
	data, wide := lineRecords(hl, line, int(hl.m.ExportLiveLineTokCount(uint32(line))))
	n := len(data)
	if wide {
		n /= 2
	}
	out := &LineTokens{Tokens: []ThemedToken{}, BracketIgnoredRanges: [][2]int{}}
	start := 0
	for r := 0; r < n; r++ {
		end, id := recordAt(data, wide, r)
		if end <= start {
			continue
		}
		out.Tokens = append(out.Tokens, t.set.rangeToToken(text.slice(start, end), start, id, t.prefix))
		if standardTypes[id] != 0 {
			if k := len(out.BracketIgnoredRanges); k > 0 && out.BracketIgnoredRanges[k-1][1] >= start {
				out.BracketIgnoredRanges[k-1][1] = end
			} else {
				out.BracketIgnoredRanges = append(out.BracketIgnoredRanges, [2]int{start, end})
			}
		}
		start = end
	}
	// a freshly spliced line that deferred work has not reached yet still
	// has text to show unstyled
	if len(out.Tokens) == 0 && length > 0 {
		out.Tokens = append(out.Tokens, t.set.rangeToToken(text.s, 0, 0, t.prefix))
	}
	return out, nil
}

// ApplyEdits validates and applies edits to the current document. Without
// a RenderRange tokenization completes synchronously and Lines is empty.
// Calling ApplyEdits or Reset from this update's OnDeferTokenize fails.
func (t *LiveTokenizer) ApplyEdits(edits []TextEdit, opts *LiveUpdateOptions) (*LiveTokenizerUpdate, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	hl, err := t.live()
	if err != nil {
		return nil, err
	}
	if t.updating {
		return nil, errUpdating
	}
	var renderRange *LineRange
	if opts != nil {
		renderRange = opts.RenderRange
	}
	if err := checkRenderRange(renderRange); err != nil {
		return nil, err
	}
	batch, err := t.validate(hl, edits)
	if err != nil {
		return nil, err
	}
	previous := int(hl.m.ExportLiveLineCount())
	if len(batch) == 0 {
		if err := t.resume(); err != nil {
			return nil, err
		}
		return &LiveTokenizerUpdate{Revision: t.revision, PreviousLineCount: previous, LineCount: previous, LineChanges: []LiveLineChange{}, Lines: map[int][]HighlightedToken{}}, nil
	}
	// the native splice absorbs pending dirty ranges into this batch, and
	// runSlice restarts the background tail
	t.deferGeneration++
	t.paused = false
	t.updating = true
	defer func() { t.updating = false }()
	if err := t.stageEdits(hl, batch); err != nil {
		return nil, err
	}
	t.revision++
	lines, err := t.runSlice(hl, renderRange)
	if err != nil {
		return nil, err
	}
	return t.readUpdate(hl, previous, lines), nil
}

// Reset replaces the document in a fresh engine instance.
func (t *LiveTokenizer) Reset(code string, opts *LiveUpdateOptions) (*LiveTokenizerUpdate, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	old, err := t.live()
	if err != nil {
		return nil, err
	}
	if t.updating {
		return nil, errUpdating
	}
	var renderRange *LineRange
	if opts != nil {
		renderRange = opts.RenderRange
	}
	if err := checkRenderRange(renderRange); err != nil {
		return nil, err
	}
	// pending tokens describe the outgoing document; drop them
	t.deferGeneration++
	t.paused = false
	previous := int(old.m.ExportLiveLineCount())
	t.materializing = nil
	hl, err := createStaged(code, t.langID)
	if err != nil {
		return nil, err
	}
	t.hl = hl
	t.revision++
	t.updating = true
	lines, err := t.runSlice(hl, renderRange)
	t.updating = false
	if err != nil {
		return nil, err
	}
	count := int(hl.m.ExportLiveLineCount())
	return &LiveTokenizerUpdate{
		Revision:          t.revision,
		PreviousLineCount: previous,
		LineCount:         count,
		LineChanges:       []LiveLineChange{{0, previous, 0, count}},
		Lines:             lines,
	}, nil
}

func (t *LiveTokenizer) runSlice(hl *Highlighter, renderRange *LineRange) (map[int][]HighlightedToken, error) {
	if renderRange == nil {
		err := guard(func() { hl.m.ExportLiveRun(0x7fffffff, 0, 0) })
		return map[int][]HighlightedToken{}, err
	}
	generation := t.deferGeneration
	count := int(hl.m.ExportLiveLineCount())
	end := 0
	if renderRange.Start < count {
		end = min(renderRange.End, count)
	}
	lines, err := t.workSlice(hl, end, t.now().Add(time.Millisecond), renderRange)
	if generation == t.deferGeneration && t.hl == hl && !t.paused && t.pending(hl) {
		t.scheduleSlice(generation)
	}
	return lines, err
}

// scheduleSlice runs a background batch through the scheduler.
func (t *LiveTokenizer) scheduleSlice(generation int) {
	task := func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.deferSlice(generation)
	}
	if t.schedule != nil {
		t.schedule(task)
		return
	}
	go task()
}

func (t *LiveTokenizer) deferSlice(generation int) {
	if generation != t.deferGeneration || t.hl == nil || t.paused {
		return
	}
	hl := t.hl
	_, _ = t.workSlice(hl, int(hl.m.ExportLiveLineCount()), t.now().Add(time.Millisecond), nil)
	if generation == t.deferGeneration && t.hl == hl && !t.paused && t.pending(hl) {
		t.scheduleSlice(generation)
	}
}

// deliver calls OnDeferTokenize without the lock held.
func (t *LiveTokenizer) deliver(lines map[int][]HighlightedToken) {
	if t.onDeferTokenize == nil {
		return
	}
	t.mu.Unlock()
	defer t.mu.Lock()
	t.onDeferTokenize(lines)
}

// workSlice tokenizes and materializes lines before endLine until the
// deadline; a zero deadline has no limit.
func (t *LiveTokenizer) workSlice(hl *Highlighter, endLine int, deadline time.Time, renderRange *LineRange) (map[int][]HighlightedToken, error) {
	generation := t.deferGeneration
	lines := map[int][]HighlightedToken{}
	deferred := map[int][]HighlightedToken{}
	first := t.set.themes[0]
	unlimited := deadline.IsZero()
	expired := func() bool { return !unlimited && !t.now().Before(deadline) }
	inRange := func(line int) bool {
		return renderRange != nil && line >= renderRange.Start && line < renderRange.End
	}
	var err error
	for generation == t.deferGeneration && !expired() && t.hl == hl {
		current := t.materializing
		if current == nil {
			line := int(hl.m.ExportLiveStats(10))
			if hl.m.ExportLiveStats(9) == 0 || line >= endLine {
				break
			}
			work := uint32(4096)
			if unlimited {
				work = 0
			}
			if err = guard(func() { hl.m.ExportLiveRun(1, uint32(endLine), work) }); err != nil {
				break
			}
			if int(hl.m.ExportLiveStats(10)) == line {
				continue
			}
			if t.onDeferTokenize == nil && !inRange(line) {
				continue
			}
			current = &materializing{line: line}
			t.materializing = current
			if expired() {
				break
			}
		}
		line := current.line
		if line >= endLine {
			break
		}
		byteLength := int(hl.m.ExportLiveLineByteLen(uint32(line)))
		if current.byte < byteLength {
			ptr := int(hl.m.ExportLiveLineTextPtr(uint32(line)))
			to := min(byteLength, current.byte+65536)
			for to < byteLength && hl.m.Mem[ptr+to]&0xc0 == 0x80 {
				to++
			}
			current.text += string(hl.m.Mem[ptr+current.byte : ptr+to])
			current.byte = to
			if to < byteLength || expired() {
				continue
			}
		}
		count := int(hl.m.ExportLiveLineTokCount(uint32(line)))
		if t.maxLineLength > 0 && int(hl.m.ExportLiveLineLen(uint32(line))) >= t.maxLineLength {
			count = 0
		}
		data, wide := lineRecords(hl, line, count)
		text := newUTF16Text(current.text)
		stop := min(count, current.record+256)
		for ; current.record < stop; current.record++ {
			end, id := recordAt(data, wide, current.record)
			if end > current.start {
				fg := orDefault(styleColor(first.Styles[id]), first.Fg)
				current.tokens = append(current.tokens, HighlightedToken{current.start, fg, text.slice(current.start, end)})
				current.start = end
			}
		}
		if current.record < count {
			continue
		}
		if len(current.tokens) == 0 {
			fg := ""
			if current.text != "" {
				fg = first.Fg
			}
			current.tokens = append(current.tokens, HighlightedToken{0, fg, current.text})
		}
		if inRange(line) {
			lines[line] = current.tokens
		} else {
			deferred[line] = current.tokens
		}
		t.materializing = nil
		if renderRange == nil && len(deferred) >= 16 {
			ready := deferred
			deferred = map[int][]HighlightedToken{}
			t.deliver(ready)
		}
	}
	if len(deferred) > 0 {
		t.deliver(deferred)
	}
	return lines, err
}

func (t *LiveTokenizer) readUpdate(hl *Highlighter, previous int, lines map[int][]HighlightedToken) *LiveTokenizerUpdate {
	base := int(hl.m.ExportLiveChangesPtr())
	le := binary.LittleEndian
	count := int(le.Uint32(hl.m.Mem[base:]))
	changes := make([]LiveLineChange, count)
	for i := range changes {
		at := base + 4 + i*16
		changes[i] = LiveLineChange{
			int(le.Uint32(hl.m.Mem[at:])),
			int(le.Uint32(hl.m.Mem[at+4:])),
			int(le.Uint32(hl.m.Mem[at+8:])),
			int(le.Uint32(hl.m.Mem[at+12:])),
		}
	}
	return &LiveTokenizerUpdate{
		Revision:          t.revision,
		PreviousLineCount: previous,
		LineCount:         int(hl.m.ExportLiveLineCount()),
		LineChanges:       changes,
		Lines:             lines,
	}
}

// validate checks edit bounds, ordering, and overlaps before changing the
// document. It returns edits in ascending order without no-op replacements,
// merging edits that share a line.
func (t *LiveTokenizer) validate(hl *Highlighter, edits []TextEdit) ([]normalizedEdit, error) {
	lineCount := int(hl.m.ExportLiveLineCount())
	items := make([]normalizedEdit, 0, len(edits))
	for _, edit := range edits {
		start, end := edit.Range.Start, edit.Range.End
		for _, pos := range []Position{start, end} {
			if pos.Line < 0 || pos.Character < 0 {
				return nil, &TypeError{"positions must be non-negative integers"}
			}
			if pos.Line >= lineCount {
				return nil, &RangeError{fmt.Sprintf("line %d out of range", pos.Line)}
			}
			if pos.Character > int(hl.m.ExportLiveLineLen(uint32(pos.Line))) {
				return nil, &RangeError{fmt.Sprintf("character %d past the end of line %d", pos.Character, pos.Line)}
			}
		}
		if start.Line > end.Line || (start.Line == end.Line && start.Character > end.Character) {
			return nil, &RangeError{"range start is after its end"}
		}
		items = append(items, normalizedEdit{start.Line, start.Character, end.Line, end.Character, toLiveText(edit.NewText)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.sl != b.sl {
			return a.sl < b.sl
		}
		if a.sc != b.sc {
			return a.sc < b.sc
		}
		if a.el != b.el {
			return a.el < b.el
		}
		return a.ec < b.ec
	})
	for i := 1; i < len(items); i++ {
		prev, cur := items[i-1], items[i]
		if prev.el > cur.sl || (prev.el == cur.sl && prev.ec > cur.sc) {
			return nil, &RangeError{"edit ranges overlap"}
		}
		// two inserts at one position have no defined order
		if prev == (normalizedEdit{cur.sl, cur.sc, cur.el, cur.ec, prev.newText}) && prev.sl == prev.el && prev.sc == prev.ec {
			return nil, &RangeError{"edit ranges overlap"}
		}
	}
	lastLine := -1
	var lastText *utf16Text
	readLine := func(line int) *utf16Text {
		if line != lastLine {
			lastText = newUTF16Text(lineText(hl, line))
			lastLine = line
		}
		return lastText
	}
	var merged []normalizedEdit
	for _, e := range items {
		if t.isNoopEdit(hl, e, readLine) {
			continue
		}
		if k := len(merged); k > 0 && merged[k-1].el == e.sl {
			prev := &merged[k-1]
			prev.newText += readLine(e.sl).slice(prev.ec, e.sc) + e.newText
			prev.el = e.el
			prev.ec = e.ec
		} else {
			merged = append(merged, e)
		}
	}
	return merged, nil
}

// isNoopEdit reports whether the replacement equals the range's text.
func (t *LiveTokenizer) isNoopEdit(hl *Highlighter, e normalizedEdit, readLine func(int) *utf16Text) bool {
	newLen := utf16Len(e.newText)
	rangeLen := e.ec - e.sc
	for line := e.sl; line < e.el; line++ {
		rangeLen += int(hl.m.ExportLiveLineLen(uint32(line))) + int(hl.m.ExportLiveLineFlags(uint32(line))&3)
		if rangeLen > newLen {
			return false
		}
	}
	if rangeLen != newLen {
		return false
	}
	if rangeLen == 0 {
		return true
	}
	rest := e.newText
	for line := e.sl; line <= e.el; line++ {
		text := readLine(line)
		from, to := 0, utf16Len(text.s)
		if line == e.sl {
			from = e.sc
		}
		if line == e.el {
			to = e.ec
		}
		part := text.slice(from, to)
		if !strings.HasPrefix(rest, part) {
			return false
		}
		rest = rest[len(part):]
		if line < e.el {
			term := eol(hl.m.ExportLiveLineFlags(uint32(line)))
			if !strings.HasPrefix(rest, term) {
				return false
			}
			rest = rest[len(term):]
		}
	}
	return true
}

// stageEdits copies the batch into a staged block and splices natively.
func (t *LiveTokenizer) stageEdits(hl *Highlighter, batch []normalizedEdit) error {
	total := 4 + len(batch)*24
	for _, e := range batch {
		total += len(e.newText)
	}
	undelivered := uint32(0)
	if t.materializing != nil {
		undelivered = uint32(t.materializing.line + 1)
	}
	err := guard(func() {
		ptr := int(hl.m.ExportLiveStage(uint32(total)))
		le := binary.LittleEndian
		mem := hl.m.Mem
		textOff := 4 + len(batch)*24
		for i, e := range batch {
			at := ptr + 4 + i*24
			le.PutUint32(mem[at:], uint32(e.sl))
			le.PutUint32(mem[at+4:], uint32(e.sc))
			le.PutUint32(mem[at+8:], uint32(e.el))
			le.PutUint32(mem[at+12:], uint32(e.ec))
			le.PutUint32(mem[at+16:], uint32(textOff))
			le.PutUint32(mem[at+20:], uint32(len(e.newText)))
			copy(mem[ptr+textOff:], e.newText)
			textOff += len(e.newText)
		}
		le.PutUint32(mem[ptr:], uint32(len(batch)))
		hl.m.ExportLiveApplyEdits(uint32(ptr), undelivered)
	})
	t.materializing = nil
	return err
}
