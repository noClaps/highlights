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
  call is repeated on the Go port and must give the same result. See
  [`testdata/golden/README.md`](testdata/golden/README.md).
- Direct ports of the preprocessor and engine tests from `wasm_test.ts`, and
  unit tests of the runtime helpers.
