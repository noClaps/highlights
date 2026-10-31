// Package engine is the Go translation of src/highlights.wat. The lexers,
// emitter, and live document store run on a Machine that owns the module's
// linear memory.
package engine

//go:generate go run ../cmd/genengine -src ../../vendor/pierre-highlights/src/highlights.wat -out . -pkg engine
