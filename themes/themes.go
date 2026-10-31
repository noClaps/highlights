// Package themes provides the bundled Zed-compatible themes, the
// CSS-variable pass-through theme, and ToCSS.
package themes

import (
	"embed"
	"strings"

	highlights "highlights"
)

//go:embed data/*.json
var data embed.FS

func load(name string) *highlights.Theme {
	b, err := data.ReadFile("data/" + name + ".json")
	if err != nil {
		panic(err)
	}
	return highlights.MustParseTheme(b)
}

// Names returns the names of the bundled themes, sorted, as JSON accepts
// them.
func Names() []string {
	entries, err := data.ReadDir("data")
	if err != nil {
		panic(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names
}

// JSON returns a bundled theme's source file.
func JSON(name string) ([]byte, error) {
	return data.ReadFile("data/" + name + ".json")
}

// CSSVariables is a pass-through theme: every color resolves to a
// prefixed CSS variable.
var CSSVariables = &highlights.Theme{
	Name:         "CSS Variables",
	Appearance:   "dark",
	CSSVariables: &highlights.CSSVariables{All: true},
}

// ToCSS converts a theme to CSS custom-property declarations; "" uses the
// default prefix.
func ToCSS(theme *highlights.Theme, cssVariablePrefix string) string {
	return highlights.ToCSS(theme, cssVariablePrefix)
}
