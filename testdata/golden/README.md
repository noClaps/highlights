# Golden recordings of the TypeScript test suite

Each `<file>.jsonl.gz` holds one line per test of
`vendor/pierre-highlights/test/<file>.ts`, in suite order. A test line lists
every library call the test made: the arguments, the result or error, and
any memory the test wrote or read directly. `TestGolden` (in
`golden_test.go`) replays each test as a Go subtest of the same name and
requires each result to match the recording. Results longer than 64
characters are stored as a truncated SHA-256 of their canonical JSON; set
`REC_FULL=1` when recording to keep them in full for debugging.

Regenerate with `tools/record/record.sh /path/to/node_modules` after
changing the vendored package. The recorder instruments a temporary copy of
the package; the vendored sources are not modified.
