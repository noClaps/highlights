# highlights (Go)

A Go port of
[`@pierre/highlights`](https://github.com/pierrecomputer/pierre/tree/main/packages/highlights)
0.3.0, a
syntax highlighter with 151 built-in languages and Zed-compatible themes. It
produces HTML or Shiki-compatible themed tokens from complete input, input
chunks, or document edits.

```go
import (
	"highlights"
	"highlights/themes"
)

html, err := highlights.CodeToHTML("console.log('Hello world!')", highlights.Options{
	Lang:         "js",
	ThemeOptions: highlights.ThemeOptions{Theme: themes.PierreDark},
})
```

## API

| TypeScript                     | Go                                                      |
| ------------------------------ | ------------------------------------------------------- |
| `codeToHtml`                   | `CodeToHTML`, `CodeToHTMLBytes`                         |
| `codeToTokens`                 | `CodeToTokens`, `CodeToTokensBytes`                     |
| `createHighlighter`            | `NewHighlighter` (one engine instance, not concurrent)  |
| `StreamTokenizer`              | `NewStreamTokenizer`: `Push`, `PushBytes`, `End`, `Close` |
| `LiveTokenizer`                | `NewLiveTokenizer`: `ApplyEdits`, `Reset`, `LineTokens`, `Close` |
| `isSupportedLanguage`          | `IsSupportedLanguage`, `LangID`, `Languages`            |
| `prepareTheme`, `toCSS`        | `PrepareTheme`, `ToCSS` (`themes.ToCSS`)                |
| Zed theme objects              | `Theme`, `ThemeStyle` (`Colors`, `Players`, `Syntax`), `ParseTheme` |
| `@pierre/highlights/themes`    | package `themes`: `themes.PierreDark`, `themes.ByName`  |

The package-level functions are safe for concurrent use; they pool engine
instances. Offsets in tokens and live-document positions are UTF-16 code
units, as in Shiki and the Language Server Protocol. Errors keep the
TypeScript messages and wrap a kind to test with `errors.Is`:
`ErrUnknownLanguage`, `ErrInvalidTheme`, `ErrInvalidArgument`,
`ErrOutOfRange`, `ErrStreamEnded`, or `ErrClosed`. See the package
documentation and examples (`go doc highlights`).

`LiveTokenizer` runs deferred work through `LiveTokenizerOptions.Schedule`
(a goroutine by default) and calls `OnDeferTokenize` without holding its
lock, so the callback may call any method.

## Command

`cmd/highlights` writes a file, or standard input, as highlighted HTML: the
same `<pre>` block with inline styles that `CodeToHTML` returns.

```sh
go install ./cmd/highlights
highlights main.go > main.html
git show HEAD:main.go | highlights -l go -t github-light
```

The language comes from the file name (`main.go`, `Dockerfile`), or from
`-l`; standard input and unknown names are plain text. `-t` picks a bundled
theme (`-list-themes`) or reads a theme JSON file.

## How the port is built

The lexers are written in WebAssembly Text, in `internal/engine/wat` (the
`src` directory of the original package; its
[architecture notes](https://github.com/pierrecomputer/pierre/blob/main/packages/highlights/ARCHITECTURE.md)
describe them). Rather than run Wasm, the Go port translates the WAT to Go:

- `internal/wat` ports the WAT preprocessor (`transformWat` in
  `scripts/build.ts`); its output is byte-identical to the TypeScript one.
- `internal/watgo` translates the expanded module to Go: each Wasm function
  becomes a method on `engine.Machine`, which owns the linear memory. The
  output reads like hand-written Go: WAT names and enum members become Go
  names, loops become labeled `for` loops, byte switches become `switch`
  statements with rune literals, tail branches become `if`/`else` arms,
  constant conditions fold away, and expressions print with Go precedence.
  Functions, parameters, locals and globals that only hold 0 or 1 are
  `bool`s, memory is read through `m.Mem[p]` and `encoding/binary`, selects
  become `if`/`else` assignments, else-if chains become `switch`es,
  variables are declared with `:=` where they are first assigned,
  temporaries are named after the call or parameter they belong to, vector
  operations are methods of `rt.V128`, and bit counts and rotations use
  `math/bits`.

  What "idiomatic" means here, and how it is checked:

  1. It passes the linters hand-written Go would: `tools/lint.sh` runs
     gofmt, `go vet`, staticcheck, revive and gocritic over the module, then
     again over a copy of the engine without its generated-code header,
     since linters skip generated files and staticcheck holds back its style
     checks on them.
  2. Control flow is structured: no `goto` (`TestGeneratedCodeStyle`).
  3. Conditions are Go bools, not 0/1 arithmetic: `TestGeneratedCodeStyle`
     walks the syntax tree and fails on any comparison of `rt.B2u(c)`, or of
     an and/or/xor with one as an operand, against 0 or 1. `rt.B2u` remains
     only where a condition is a number: bits packed into a word
     (`maps<<1 | rt.B2u(c == '{')`), an offset (`lhs + rt.B2u(c == 'm')`),
     or an integer that also holds other values, since Go has no
     bool-to-int conversion.
  4. The standard library and built-ins are used where they have the
     operation: `encoding/binary` for wide loads and stores, `math/bits` for
     bit counts and rotations, `copy` and `clear` for memory copies and zero
     fills (`TestGeneratedCodeStyle`). `internal/wasmrt` keeps what they
     lack: filling memory with a non-zero byte, memory growth, and the
     16-byte vector type `V128` with its operations as methods.
  5. Go has no conditional expression, so a choice is an `if`: there is no
     select helper left, and temporaries are named after what they hold
     (the callee, the parameter they are passed to, `result`, `cond`), not
     numbered.
- `internal/engine` is the generated engine (`go generate ./internal/engine`
  regenerates it and `tables_gen.go`).
- The root package ports the TypeScript host, whose original is in
  `tools/record/ts/lib`.

## Tests

`go test ./...` runs:

- `TestGolden`, which replays every test of the TypeScript suite (3,574
  tests in 156 files) as a Go subtest of the same name. Each recorded library
  call is repeated on the Go port and must give the same result, as must any
  engine memory a test reads directly. Files replay in suite order because
  the TypeScript helpers share some instances between files. See
  [`testdata/golden/README.md`](testdata/golden/README.md).

  The original TypeScript suite, host, and build script are kept in
  `tools/record/ts` so the recordings can be regenerated.
  `bundled themes: matches Shiki and Pierre catalogs` reads the Pierre theme
  package beside the original one; the recorder supplies it from
  `themes/testdata`, and `themes/catalog_test.go` ports its checks. A few
  dozen calls that pass values Go's types cannot express (a number as a
  theme, `Infinity` as a line) are skipped and counted in the test log.
- Direct ports of the preprocessor and engine tests from `wasm_test.ts`, the
  memory-capacity checks from `memory_test.ts`, and the linear-time checks
  from five language tests, plus unit tests of the runtime helpers. Checks
  of the Binaryen optimizer and of zero-copy JavaScript views have no Go
  counterpart.
- `lexer_test.go`, which pins the departures below.

## Departures from `@pierre/highlights`

The port matches the original's output except where a lexer bug was fixed:

- Python highlights `as` as a keyword (`import numpy as np`,
  `with open(p) as f`, `except E as e`); the original colored it as a name.

A fix changes the output of recorded tests, so their recordings are
rewritten from this port's results. `GOLDEN_UPDATE` names the subtests, comma
separated, as `<file>/<test name>`:

```sh
GOLDEN_UPDATE="conformance_test/python: malformed runs leave no state behind" go test -run 'TestGolden$' .
```

Only the values that differ are rewritten, and only those lines of the
recording change. The whole suite must run, since files share instances.
Before the `as` fix was re-recorded, every value in the two affected tests
was compared between the engine before and after the change: the only
differences were `as` becoming a keyword, in five HTML outputs and the eight
memory snapshots that hold them.

## License

Apache-2.0, as the original package. See [`LICENSE.md`](LICENSE.md) and the
third-party theme notices in [`NOTICE.md`](NOTICE.md).
