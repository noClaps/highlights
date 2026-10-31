# highlights (Go)

A Go port of [`@pierre/highlights`](vendor/pierre-highlights/README.md), a
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
| `StreamTokenizer`              | `NewStreamTokenizer`: `PushCode`, `PushBytes`, `End`    |
| `LiveTokenizer`                | `NewLiveTokenizer`: `ApplyEdits`, `Reset`, `LineTokens` |
| `isSupportedLanguage`          | `IsSupportedLanguage`, `LangID`, `Languages`            |
| `prepareTheme`, `toCSS`        | `PrepareTheme`, `ToCSS` (`themes.ToCSS`)                |
| `@pierre/highlights/themes`    | package `themes`: `themes.PierreDark`, `themes.ByName`  |

The package-level functions are safe for concurrent use; they pool engine
instances. Offsets in tokens and live-document positions are UTF-16 code
units, as in Shiki and the Language Server Protocol. Errors that the
TypeScript package throws are returned as `*TypeError`, `*RangeError`, or
plain errors with the same messages.

`LiveTokenizer` runs deferred work through `LiveTokenizerOptions.Schedule`
(a goroutine by default) and calls `OnDeferTokenize` without holding its
lock, so the callback may call any method.

## How the port is built

The lexers are written in WebAssembly Text in the vendored package. Rather
than run Wasm, the Go port translates the WAT to Go:

- `internal/wat` ports the WAT preprocessor (`transformWat` in
  `scripts/build.ts`); its output is byte-identical to the TypeScript one.
- `internal/watgo` translates the expanded module to Go: each Wasm function
  becomes a method on `engine.Machine`, which owns the linear memory.
  `internal/wasmrt` holds the runtime helpers, including 128-bit vectors.
- `internal/engine` is the generated engine (`go generate ./internal/engine`
  regenerates it and `tables_gen.go`).
- The root package ports the TypeScript host in `lib/`.

## Tests

`go test ./...` runs:

- `TestGolden`, which replays every test of the TypeScript suite (3,574
  tests in 156 files) as a Go subtest of the same name. Each recorded library
  call is repeated on the Go port and must give the same result, as must any
  engine memory a test reads directly. Files replay in suite order because
  the TypeScript helpers share some instances between files. See
  [`testdata/golden/README.md`](testdata/golden/README.md).

  One test is skipped: `bundled themes: matches Shiki and Pierre catalogs`
  reads a sibling directory of the original monorepo and fails there too. A
  few dozen calls that pass values Go's types cannot express (a number as a
  theme, `Infinity` as a line) are skipped and counted in the test log.
- Direct ports of the preprocessor and engine tests from `wasm_test.ts`, the
  memory-capacity checks from `memory_test.ts`, and the linear-time checks
  from five language tests, plus unit tests of the runtime helpers. Checks
  of the Binaryen optimizer and of zero-copy JavaScript views have no Go
  counterpart.
