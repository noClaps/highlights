# Golden recordings of the TypeScript test suite

Each `<file>.jsonl.gz` holds one line per test of
`tools/record/ts/test/<file>.ts`, in suite order. A test line lists
every library call the test made: the arguments, the result or error, and
any memory the test wrote or read directly. `TestGolden` (in
`golden_test.go`) replays each test as a Go subtest of the same name and
requires each result to match the recording. Results longer than 64
characters are stored as a truncated SHA-256 of their canonical JSON; set
`REC_FULL=1` when recording to keep them in full for debugging.

`order.txt` lists the files in the order the suite ran them; the replay
follows it.

Regenerate with `tools/record/record.sh /path/to/node_modules` after
changing the WAT sources or the TypeScript suite. The recorder assembles and instruments a
temporary copy of the package; the sources in the tree are not modified.
Live-tokenizer recordings include clock readings, so they differ between
runs even when every result is the same.
