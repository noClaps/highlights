// Package kwtest is the Go translation of the keyword-table test module in
// vendor/pierre-highlights/test/wasm_test.ts.
package kwtest

//go:generate go run ../../cmd/genengine -src module.wat -out . -pkg kwtest
