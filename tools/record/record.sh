#!/bin/sh
# Record the TypeScript suite's library calls into testdata/golden.
#
# The suite runs against the original TypeScript host in tools/record/ts,
# the WAT sources in internal/engine/wat, and the themes in themes/data,
# assembled into the layout of the @pierre/highlights package. It needs bun
# and the package's devDependencies (wabt, binaryen, shiki,
# tree-sitter-highlight) in a node_modules directory:
#
#   tools/record/record.sh /path/to/node_modules
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
node_modules=$1
work=$(mktemp -d)
pkg="$work/source"
mkdir -p "$pkg/themes"
cp -R "$here/ts/." "$pkg/"
cp -R "$root/internal/engine/wat" "$pkg/src"
cp "$root"/themes/data/*.json "$pkg/themes/"
python3 "$here/instrument.py" "$pkg" "$work/pkg" "$node_modules"
# theme_test.ts reads the Pierre theme package beside this one
mkdir -p "$work/theme/themes"
cp "$root"/themes/testdata/pierre-theme/*.json "$work/theme/themes/"
(cd "$work/pkg" && REC_OUT="$work/rec" bun test --preload ./test/_rec.ts --timeout 120000) || true
rm -f "$root"/testdata/golden/*.jsonl.gz
for f in "$work"/rec/*.jsonl; do
	gzip -6 -c "$f" > "$root/testdata/golden/$(basename "$f").gz"
done
cp "$work/rec/order.txt" "$root/testdata/golden/order.txt"
rm -rf "$work"
