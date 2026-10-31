package themes_test

// Port of "bundled themes: matches Shiki and Pierre catalogs and metadata"
// in tools/record/ts/test/theme_test.ts. The catalogs it reads
// from outside the package are in testdata.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	highlights "highlights"
	"highlights/themes"
)

type themeInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Type        string `json:"type"`
}

func catalog(t *testing.T) ([]themeInfo, map[string]map[string]string) {
	t.Helper()
	data, err := os.ReadFile("testdata/shiki-themes.json")
	if err != nil {
		t.Fatal(err)
	}
	var shiki struct{ Themes []themeInfo }
	if err := json.Unmarshal(data, &shiki); err != nil {
		t.Fatal(err)
	}
	info := shiki.Themes
	pierreColors := map[string]map[string]string{}
	files, _ := filepath.Glob("testdata/pierre-theme/*.json")
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Name        string            `json:"name"`
			DisplayName string            `json:"displayName"`
			Type        string            `json:"type"`
			Colors      map[string]string `json:"colors"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		info = append(info, themeInfo{p.Name, p.DisplayName, p.Type})
		pierreColors[p.Name] = p.Colors
	}
	if len(files) != 10 {
		t.Fatalf("found %d Pierre themes", len(files))
	}
	return info, pierreColors
}

var camelRe = regexp.MustCompile(`-([a-z\d])`)

func camel(name string) string {
	s := camelRe.ReplaceAllStringFunc(name, func(m string) string { return strings.ToUpper(m[1:]) })
	return strings.ToUpper(s[:1]) + s[1:]
}

func TestBundledThemesMatchShikiAndPierreCatalogsAndMetadata(t *testing.T) {
	info, pierreColors := catalog(t)
	var names []string
	for _, i := range info {
		names = append(names, i.ID)
	}
	slices.Sort(names)

	files, _ := filepath.Glob("data/*.json")
	var bundled []string
	for _, f := range files {
		bundled = append(bundled, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	slices.Sort(bundled)
	if !slices.Equal(bundled, names) {
		t.Fatalf("theme files %v\ncatalog %v", bundled, names)
	}
	var keys []string
	for k := range themes.ByName {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, names) {
		t.Fatalf("ByName %v\ncatalog %v", keys, names)
	}

	isColor := highlights.IsThemeColor
	for _, i := range info {
		theme := themes.ByName[i.ID]
		id := i.ID
		if want := camel(id); !strings.Contains(exported, "\t"+want+" ") {
			t.Errorf("%s: no exported variable %s", id, want)
		}
		if theme.Name != i.DisplayName || theme.Appearance != i.Type {
			t.Errorf("%s: name %q appearance %q, want %q %q", id, theme.Name, theme.Appearance, i.DisplayName, i.Type)
		}
		style := &theme.Style
		if len(style.Syntax) == 0 {
			t.Errorf("%s: no syntax scopes", id)
		}
		if !isColor(style.Background()) || !isColor(style.Foreground()) {
			t.Errorf("%s: background or foreground is not a color", id)
		}
		for key, c := range style.Colors {
			if !isColor(c) {
				t.Errorf("%s: %s = %q", id, key, c)
			}
		}
		for _, p := range style.Players {
			for key, c := range map[string]string{"cursor": p.Cursor, "selection": p.Selection, "background": p.Background} {
				if c != "" && !isColor(c) {
					t.Errorf("%s: player %s", id, key)
				}
			}
		}
		for _, r := range style.Syntax {
			if r.Color != "" && !isColor(r.Color) {
				t.Errorf("%s: %s color", id, r.Scope)
			}
			if r.FontStyle != "" && r.FontStyle != "normal" && r.FontStyle != "italic" {
				t.Errorf("%s: %s font_style %q", id, r.Scope, r.FontStyle)
			}
			if w := r.FontWeight; w != 0 && (w < 100 || w > 900 || w%100 != 0) {
				t.Errorf("%s: %s font_weight %d", id, r.Scope, w)
			}
		}
		for _, scope := range highlights.TokenTypes[1 : len(highlights.TokenTypes)-2] {
			color := style.Resolve(scope).Color
			if color == "" && scope == "text.jsx" {
				color = style.Foreground()
			}
			if !isColor(color) {
				t.Errorf("%s: missing %s", id, scope)
			}
		}
		background := style.Resolve("background").Color
		for _, pair := range [][2]string{{"created", "green"}, {"deleted", "red"}, {"modified", "blue"}} {
			color, ok := style.Colors[pair[0]]
			if !ok {
				color = style.Colors["terminal.ansi."+pair[1]]
			}
			if !isColor(color) {
				t.Errorf("%s: %s", id, pair[0])
				continue
			}
			visible := highlights.ThemeStyle{Colors: map[string]string{"foreground": color}}
			if visible.Resolve("foreground").Color == background {
				t.Errorf("%s: invisible %s", id, pair[0])
			}
		}
	}
	for name, colors := range pierreColors {
		style := &themes.ByName[name].Style
		if style.Background() != colors["editor.background"] {
			t.Errorf("%s: background %v, want %s", name, style.Background(), colors["editor.background"])
		}
		if style.Foreground() != colors["editor.foreground"] {
			t.Errorf("%s: foreground %v, want %s", name, style.Foreground(), colors["editor.foreground"])
		}
	}
}

// exported is the source that declares the bundled theme variables.
var exported = func() string {
	b, _ := os.ReadFile("bundled.go")
	return string(b)
}()
