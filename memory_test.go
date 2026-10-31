package highlights

// Ports of tools/record/ts/test/memory_test.ts. The golden replay
// checks the outputs of these tests; this file checks memory capacity,
// which the replay cannot see.

import (
	"os"
	"strings"
	"testing"
)

func pierreDarkForTest(t *testing.T) *Theme {
	t.Helper()
	data, err := os.ReadFile("themes/data/pierre-dark.json")
	if err != nil {
		t.Fatal(err)
	}
	return MustParseTheme(data)
}

func TestLargePlainHTMLReservesCapacityCloseToInputPlusOutput(t *testing.T) {
	opts := Options{Lang: "plain", ThemeOptions: ThemeOptions{Theme: pierreDarkForTest(t)}}
	h := NewHighlighter()
	input := strings.Repeat("x", 5*1024*1024)
	out, err := h.CodeToHTML(input, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, input) {
		t.Fatal("output lost the input")
	}
	if got, limit := len(h.m.Mem), len(input)+len(out)+512*1024; got >= limit {
		t.Fatalf("memory %d bytes, want below %d", got, limit)
	}
	tiny, err := h.CodeToHTML("tiny", opts)
	if err != nil || !strings.Contains(tiny, ">tiny<") {
		t.Fatalf("tiny: %q %v", tiny, err)
	}
}

// Pooled instances above 8 MiB are dropped, so a small call after a large
// one does not keep the large memory.
func TestSharedHTMLRecyclesLargeCapacityForSmallInputs(t *testing.T) {
	opts := Options{Lang: "plain", ThemeOptions: ThemeOptions{Theme: pierreDarkForTest(t)}}
	for _, large := range []func() error{
		func() error { _, err := CodeToHTML(strings.Repeat("x", 5*1024*1024), opts); return err },
		func() error { _, err := CodeToTokens(strings.Repeat("x", 9*1024*1024), opts); return err },
	} {
		if err := large(); err != nil {
			t.Fatal(err)
		}
		h := getShared()
		if pages := h.Pages(); pages*pageSize > 1024*1024 {
			t.Fatalf("pooled instance holds %d pages", pages)
		}
		putShared(h)
		out, err := CodeToHTML("tiny", opts)
		if err != nil || !strings.Contains(out, ">tiny<") {
			t.Fatalf("tiny: %q %v", out, err)
		}
	}
}
