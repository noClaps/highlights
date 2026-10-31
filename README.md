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
  Functions, locals and globals that only hold 0 or 1 are `bool`s, bytes are
  read as `m.Mem[p]`, selects become `if`/`else` assignments, else-if chains
  become `switch`es, variables are declared with `:=` where they are first
  assigned, temporaries are named after the call or parameter they belong
  to, and bit counts use `math/bits`.

  The engine is held to the linters hand-written code would be:
  `tools/lint.sh` runs gofmt, `go vet`, staticcheck, revive and gocritic over
  the module, then again over a copy of the engine without its generated-code
  header, since linters skip generated files. `TestGeneratedCodeStyle` in
  `internal/engine` adds checks the linters lack, such as no `goto`. The
  engine is still a translation: branchless arithmetic on conditions
  (`n |= rt.B2u(c != ' ')`), wide loads through `rt.Ld32` and the like, and
  SIMD through `rt.V128` remain.
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

## License

Apache-2.0, as the original package. See [`LICENSE.md`](LICENSE.md) and the
third-party theme notices in [`NOTICE.md`](NOTICE.md).
