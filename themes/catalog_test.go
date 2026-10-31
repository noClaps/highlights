package themes_test

// Port of "bundled themes: matches Shiki and Pierre catalogs and metadata"
// in vendor/pierre-highlights/test/theme_test.ts. The catalogs it reads
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
		style := theme.Style
		syntax, _ := style.Value("syntax").(*highlights.Object)
		if syntax.Len() == 0 {
			t.Errorf("%s: no syntax scopes", id)
		}
		if !isColor(highlights.ThemeBackground(style)) || !isColor(highlights.ThemeForeground(style)) {
			t.Errorf("%s: background or foreground is not a color", id)
		}
		for _, key := range style.Keys() {
			if s, ok := style.Value(key).(string); ok && !isColor(s) {
				t.Errorf("%s: %s = %q", id, key, s)
			}
		}
		if players, ok := style.Value("players").([]any); ok {
			for _, p := range players {
				player := p.(*highlights.Object)
				for _, key := range player.Keys() {
					if !isColor(player.Value(key)) {
						t.Errorf("%s: player %s", id, key)
					}
				}
			}
		}
		for _, scope := range syntax.Keys() {
			switch settings := syntax.Value(scope).(type) {
			case string:
				if !isColor(settings) {
					t.Errorf("%s: %s", id, scope)
				}
			case *highlights.Object:
				if c, ok := settings.Get("color"); ok && !isColor(c) {
					t.Errorf("%s: %s color", id, scope)
				}
				if fs, ok := settings.Get("font_style"); ok && fs != "normal" && fs != "italic" {
					t.Errorf("%s: %s font_style %v", id, scope, fs)
				}
				if fw, ok := settings.Get("font_weight"); ok {
					w, isNum := fw.(float64)
					if !isNum || w < 100 || w > 900 || int(w)%100 != 0 || w != float64(int(w)) {
						t.Errorf("%s: %s font_weight %v", id, scope, fw)
					}
				}
			}
		}
		for _, scope := range highlights.TokenTypes[1 : len(highlights.TokenTypes)-2] {
			var color any = highlights.ResolveThemeStyle(style, scope).Color
			if color == "" {
				color = nil
				if scope == "text.jsx" {
					color = highlights.ThemeForeground(style)
				}
			}
			if !isColor(color) {
				t.Errorf("%s: missing %s", id, scope)
			}
		}
		background := highlights.ResolveThemeStyle(style, "background").Color
		for _, pair := range [][2]string{{"created", "green"}, {"deleted", "red"}, {"modified", "blue"}} {
			color := style.Value(pair[0])
			if color == nil {
				color = style.Value("terminal.ansi." + pair[1])
			}
			if !isColor(color) {
				t.Errorf("%s: %s", id, pair[0])
				continue
			}
			if highlights.ResolveThemeStyle(highlights.NewObject("foreground", color), "foreground").Color == background {
				t.Errorf("%s: invisible %s", id, pair[0])
			}
		}
	}
	for name, colors := range pierreColors {
		style := themes.ByName[name].Style
		if highlights.ThemeBackground(style) != colors["editor.background"] {
			t.Errorf("%s: background %v, want %s", name, highlights.ThemeBackground(style), colors["editor.background"])
		}
		if highlights.ThemeForeground(style) != colors["editor.foreground"] {
			t.Errorf("%s: foreground %v, want %s", name, highlights.ThemeForeground(style), colors["editor.foreground"])
		}
	}
}

// exported is the source that declares the bundled theme variables.
var exported = func() string {
	b, _ := os.ReadFile("bundled.go")
	return string(b)
}()
