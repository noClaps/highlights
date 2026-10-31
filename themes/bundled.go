// Bundled theme variables; one per file in themes/data.

package themes

import highlights "highlights"

// Bundled themes, named after their files: vitesse-dark.json is VitesseDark.
var (
	Andromeeda                        = load("andromeeda")
	AuroraX                           = load("aurora-x")
	AyuDark                           = load("ayu-dark")
	AyuLight                          = load("ayu-light")
	AyuMirage                         = load("ayu-mirage")
	CatppuccinFrappe                  = load("catppuccin-frappe")
	CatppuccinLatte                   = load("catppuccin-latte")
	CatppuccinMacchiato               = load("catppuccin-macchiato")
	CatppuccinMocha                   = load("catppuccin-mocha")
	DarkPlus                          = load("dark-plus")
	Dracula                           = load("dracula")
	DraculaSoft                       = load("dracula-soft")
	EverforestDark                    = load("everforest-dark")
	EverforestLight                   = load("everforest-light")
	GithubDark                        = load("github-dark")
	GithubDarkDefault                 = load("github-dark-default")
	GithubDarkDimmed                  = load("github-dark-dimmed")
	GithubDarkHighContrast            = load("github-dark-high-contrast")
	GithubLight                       = load("github-light")
	GithubLightDefault                = load("github-light-default")
	GithubLightHighContrast           = load("github-light-high-contrast")
	GruvboxDarkHard                   = load("gruvbox-dark-hard")
	GruvboxDarkMedium                 = load("gruvbox-dark-medium")
	GruvboxDarkSoft                   = load("gruvbox-dark-soft")
	GruvboxLightHard                  = load("gruvbox-light-hard")
	GruvboxLightMedium                = load("gruvbox-light-medium")
	GruvboxLightSoft                  = load("gruvbox-light-soft")
	Horizon                           = load("horizon")
	HorizonBright                     = load("horizon-bright")
	Houston                           = load("houston")
	KanagawaDragon                    = load("kanagawa-dragon")
	KanagawaLotus                     = load("kanagawa-lotus")
	KanagawaWave                      = load("kanagawa-wave")
	Laserwave                         = load("laserwave")
	LightPlus                         = load("light-plus")
	MaterialTheme                     = load("material-theme")
	MaterialThemeDarker               = load("material-theme-darker")
	MaterialThemeLighter              = load("material-theme-lighter")
	MaterialThemeOcean                = load("material-theme-ocean")
	MaterialThemePalenight            = load("material-theme-palenight")
	MinDark                           = load("min-dark")
	MinLight                          = load("min-light")
	Monokai                           = load("monokai")
	NightOwl                          = load("night-owl")
	NightOwlLight                     = load("night-owl-light")
	Nord                              = load("nord")
	OneDarkPro                        = load("one-dark-pro")
	OneLight                          = load("one-light")
	PierreDark                        = load("pierre-dark")
	PierreDarkProtanopiaDeuteranopia  = load("pierre-dark-protanopia-deuteranopia")
	PierreDarkSoft                    = load("pierre-dark-soft")
	PierreDarkTritanopia              = load("pierre-dark-tritanopia")
	PierreDarkVibrant                 = load("pierre-dark-vibrant")
	PierreLight                       = load("pierre-light")
	PierreLightProtanopiaDeuteranopia = load("pierre-light-protanopia-deuteranopia")
	PierreLightSoft                   = load("pierre-light-soft")
	PierreLightTritanopia             = load("pierre-light-tritanopia")
	PierreLightVibrant                = load("pierre-light-vibrant")
	Plastic                           = load("plastic")
	Poimandres                        = load("poimandres")
	Red                               = load("red")
	RosePine                          = load("rose-pine")
	RosePineDawn                      = load("rose-pine-dawn")
	RosePineMoon                      = load("rose-pine-moon")
	SlackDark                         = load("slack-dark")
	SlackOchin                        = load("slack-ochin")
	SnazzyLight                       = load("snazzy-light")
	SolarizedDark                     = load("solarized-dark")
	SolarizedLight                    = load("solarized-light")
	Synthwave84                       = load("synthwave-84")
	TokyoNight                        = load("tokyo-night")
	Vesper                            = load("vesper")
	VitesseBlack                      = load("vitesse-black")
	VitesseDark                       = load("vitesse-dark")
	VitesseLight                      = load("vitesse-light")
)

// ByName maps each bundled theme file name to its theme.
var ByName = map[string]*highlights.Theme{
	"andromeeda":                           Andromeeda,
	"aurora-x":                             AuroraX,
	"ayu-dark":                             AyuDark,
	"ayu-light":                            AyuLight,
	"ayu-mirage":                           AyuMirage,
	"catppuccin-frappe":                    CatppuccinFrappe,
	"catppuccin-latte":                     CatppuccinLatte,
	"catppuccin-macchiato":                 CatppuccinMacchiato,
	"catppuccin-mocha":                     CatppuccinMocha,
	"dark-plus":                            DarkPlus,
	"dracula":                              Dracula,
	"dracula-soft":                         DraculaSoft,
	"everforest-dark":                      EverforestDark,
	"everforest-light":                     EverforestLight,
	"github-dark":                          GithubDark,
	"github-dark-default":                  GithubDarkDefault,
	"github-dark-dimmed":                   GithubDarkDimmed,
	"github-dark-high-contrast":            GithubDarkHighContrast,
	"github-light":                         GithubLight,
	"github-light-default":                 GithubLightDefault,
	"github-light-high-contrast":           GithubLightHighContrast,
	"gruvbox-dark-hard":                    GruvboxDarkHard,
	"gruvbox-dark-medium":                  GruvboxDarkMedium,
	"gruvbox-dark-soft":                    GruvboxDarkSoft,
	"gruvbox-light-hard":                   GruvboxLightHard,
	"gruvbox-light-medium":                 GruvboxLightMedium,
	"gruvbox-light-soft":                   GruvboxLightSoft,
	"horizon":                              Horizon,
	"horizon-bright":                       HorizonBright,
	"houston":                              Houston,
	"kanagawa-dragon":                      KanagawaDragon,
	"kanagawa-lotus":                       KanagawaLotus,
	"kanagawa-wave":                        KanagawaWave,
	"laserwave":                            Laserwave,
	"light-plus":                           LightPlus,
	"material-theme":                       MaterialTheme,
	"material-theme-darker":                MaterialThemeDarker,
	"material-theme-lighter":               MaterialThemeLighter,
	"material-theme-ocean":                 MaterialThemeOcean,
	"material-theme-palenight":             MaterialThemePalenight,
	"min-dark":                             MinDark,
	"min-light":                            MinLight,
	"monokai":                              Monokai,
	"night-owl":                            NightOwl,
	"night-owl-light":                      NightOwlLight,
	"nord":                                 Nord,
	"one-dark-pro":                         OneDarkPro,
	"one-light":                            OneLight,
	"pierre-dark":                          PierreDark,
	"pierre-dark-protanopia-deuteranopia":  PierreDarkProtanopiaDeuteranopia,
	"pierre-dark-soft":                     PierreDarkSoft,
	"pierre-dark-tritanopia":               PierreDarkTritanopia,
	"pierre-dark-vibrant":                  PierreDarkVibrant,
	"pierre-light":                         PierreLight,
	"pierre-light-protanopia-deuteranopia": PierreLightProtanopiaDeuteranopia,
	"pierre-light-soft":                    PierreLightSoft,
	"pierre-light-tritanopia":              PierreLightTritanopia,
	"pierre-light-vibrant":                 PierreLightVibrant,
	"plastic":                              Plastic,
	"poimandres":                           Poimandres,
	"red":                                  Red,
	"rose-pine":                            RosePine,
	"rose-pine-dawn":                       RosePineDawn,
	"rose-pine-moon":                       RosePineMoon,
	"slack-dark":                           SlackDark,
	"slack-ochin":                          SlackOchin,
	"snazzy-light":                         SnazzyLight,
	"solarized-dark":                       SolarizedDark,
	"solarized-light":                      SolarizedLight,
	"synthwave-84":                         Synthwave84,
	"tokyo-night":                          TokyoNight,
	"vesper":                               Vesper,
	"vitesse-black":                        VitesseBlack,
	"vitesse-dark":                         VitesseDark,
	"vitesse-light":                        VitesseLight,
}
