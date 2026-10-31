#!/bin/sh
# Lints the module, then the generated engine as if it were hand-written:
# linters skip files marked generated, so gocritic and revive see a copy of
# the engine without that header.
set -eu
cd "$(dirname "$0")/.."
# gocritic's sloppyTypeAssert and caseOrder report every assertion from an
# interface and every type switch case as wrong when it lacks type
# information, as it does under this Go release.
critic="go run github.com/go-critic/go-critic/cmd/gocritic@latest check -disable sloppyTypeAssert,caseOrder"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
	echo "not gofmt-ed: $unformatted" >&2
	exit 1
fi
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go run github.com/mgechev/revive@latest -set_exit_status ./...
$critic ./...

copy=$(mktemp -d)
trap 'rm -rf "$copy"' EXIT
mkdir -p "$copy/internal"
cp go.mod "$copy/"
cp -R internal/engine internal/wasmrt "$copy/internal/"
rm "$copy/internal/engine/engine_test.go"
sed '/^\/\/ Code generated .* DO NOT EDIT\.$/d' internal/engine/engine_gen.go >"$copy/internal/engine/engine_gen.go"
(
	cd "$copy"
	go vet ./internal/engine
	go run honnef.co/go/tools/cmd/staticcheck@latest ./internal/engine
	go run github.com/mgechev/revive@latest -set_exit_status ./internal/engine
	$critic ./internal/engine
)
echo "lint: ok"
