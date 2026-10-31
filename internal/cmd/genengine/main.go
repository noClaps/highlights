// Command genengine translates a WAT module into a Go package.
//
//	genengine -src vendor/pierre-highlights/src/highlights.wat -out internal/engine -pkg engine
package main

import (
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"

	"highlights/internal/wat"
	"highlights/internal/watgo"
)

func main() {
	src := flag.String("src", "", "WAT module to translate")
	out := flag.String("out", "", "output directory")
	pkg := flag.String("pkg", "engine", "package name")
	flag.Parse()
	if err := run(*src, *out, *pkg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(src, out, pkg string) error {
	transformed, err := wat.TransformFile(src)
	if err != nil {
		return err
	}
	mod, err := watgo.Parse(transformed.Code)
	if err != nil {
		return err
	}
	code, mem, err := watgo.Generate(mod, watgo.Options{Package: pkg, MemFile: pkg + "_mem.bin"})
	if err != nil {
		return err
	}
	formatted, ferr := format.Source([]byte(code))
	if ferr != nil {
		_ = os.WriteFile(filepath.Join(out, pkg+"_gen.go.broken"), []byte(code), 0o644)
		return fmt.Errorf("format: %w", ferr)
	}
	if err := os.WriteFile(filepath.Join(out, pkg+"_gen.go"), formatted, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, pkg+"_mem.bin"), mem, 0o644)
}
