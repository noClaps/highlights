package themes_test

import (
	"fmt"
	"strings"

	"highlights/themes"
)

func ExampleToCSS() {
	css := themes.ToCSS(themes.PierreDark, "")
	fmt.Println(strings.Split(css, ";")[0])
	// Output: --hls-background: #0a0a0a
}
