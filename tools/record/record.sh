#!/bin/sh
# Record the TypeScript suite's library calls into testdata/golden.
#
# Needs bun and the package's devDependencies (wabt, binaryen, shiki,
# tree-sitter-highlight) in a node_modules directory:
#
#   tools/record/record.sh /path/to/node_modules
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
node_modules=$1
work=$(mktemp -d)
python3 "$here/instrument.py" "$root/vendor/pierre-highlights" "$work/pkg" "$node_modules"
(cd "$work/pkg" && REC_OUT="$work/rec" bun test --preload ./test/_rec.ts --timeout 120000) || true
rm -f "$root"/testdata/golden/*.jsonl.gz
for f in "$work"/rec/*.jsonl; do
	gzip -9 -c "$f" > "$root/testdata/golden/$(basename "$f").gz"
done
cp "$work/rec/order.txt" "$root/testdata/golden/order.txt"
rm -rf "$work"
