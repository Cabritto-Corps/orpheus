package tui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type themeColors struct {
	Blue        string `json:"blue"`
	BlueLight   string `json:"blue_light"`
	OffWhite    string `json:"off_white"`
	Gray        string `json:"gray"`
	MutedBlue   string `json:"muted_blue"`
	DimBlue     string `json:"dim_blue"`
	Divider     string `json:"divider"`
	Error       string `json:"error"`
	Scrim       string `json:"scrim"`
	SelectionFg string `json:"selection_fg"`
	SelectionBg string `json:"selection_bg"`
	Page        string `json:"page"`
	Panel       string `json:"panel"`
}

// themeGlyphs picks the character families a theme can swap without
// touching the palette: box border family, the now-playing marker, the
// transport play/pause pair, the spinner animation and the progress bar
// charset.
type themeGlyphs struct {
	Border     string `json:"border"`
	NowPlaying string `json:"now_playing"`
	PlayPause  string `json:"play_pause"`
	Spinner    string `json:"spinner"`
	Bar        string `json:"bar"`
}

// themeTypography is the text-attribute layer: weight for title lines,
// italics for description lines.
type themeTypography struct {
	BoldTitles  bool `json:"bold_titles"`
	ItalicDescs bool `json:"italic_descriptions"`
}

// themeBackgrounds picks how the frame's background zones are painted:
// "divided" lifts the header band over the shared page tone; "solid" uses
// one uniform color for the whole frame.
type themeBackgrounds struct {
	Style string `json:"style"`
}

// themeCover styles the cover-art cell: a themed frame drawn around the
// art (none keeps the bare look).
type themeCover struct {
	Frame string `json:"frame"`
}

// themeState is one fully-resolved theme: palette plus the glyph,
// typography and cover layers.
type themeState struct {
	colors      themeColors
	glyphs      themeGlyphs
	typography  themeTypography
	cover       themeCover
	backgrounds themeBackgrounds
}

// themeEntry is one shipped preset in the registry.
type themeEntry struct {
	name   string
	colors themeColors
}

// themeRegistry is the single source of truth for shipped presets: order
// drives the picker, and the lookup helpers below derive from it, so adding
// a theme is one entry here and nothing else.
var themeRegistry = []themeEntry{
	{"default", themeColors{
		Blue:      "#4A90D9",
		BlueLight: "#7AB8E6",
		OffWhite:  "#C8CDD4",
		Gray:      "#808897",
		MutedBlue: "#5B7A9E",
		DimBlue:   "#728FB0",
		Divider:   "#2A3A4A",
		Error:     "#FF5757",

		Scrim:       "#0D1018",
		SelectionFg: "#C8CDD4",
		SelectionBg: "#2E4A66",
		Page:        "#0A0D12",
	}},
	{"minimal", themeColors{
		Blue:      "7",
		BlueLight: "15",
		OffWhite:  "7",
		Gray:      "8",
		MutedBlue: "8",
		DimBlue:   "0",
		Divider:   "8",
		Error:     "1",

		Scrim:       "0",
		SelectionFg: "0",
		SelectionBg: "7",
		Page:        "0",
	}},
	{"high_contrast", themeColors{
		Blue:      "#00FF87",
		BlueLight: "#7CFFC4",
		OffWhite:  "#FFFFFF",
		Gray:      "#AAAAAA",
		MutedBlue: "#B8B8B8",
		DimBlue:   "#909090",
		Divider:   "#666666",
		Error:     "#FF3B3B",

		Scrim:       "#101010",
		SelectionFg: "#000000",
		SelectionBg: "#00FF87",
		Page:        "#000000",
	}},
	{"catppuccin", themeColors{
		Blue:      "#89B4FA",
		BlueLight: "#CBA6F7",
		OffWhite:  "#CDD6F4",
		Gray:      "#7F849C",
		MutedBlue: "#7F849C",
		DimBlue:   "#45475A",
		Divider:   "#313244",
		Error:     "#F38BA8",

		Scrim:       "#181825",
		SelectionFg: "#CDD6F4",
		SelectionBg: "#45475A",
		Page:        "#11111B",
	}},
	{"tokyo_night", themeColors{
		Blue:      "#7AA2F7",
		BlueLight: "#7DCFFF",
		OffWhite:  "#C0CAF5",
		Gray:      "#565F89",
		MutedBlue: "#565F89",
		DimBlue:   "#2E3C64",
		Divider:   "#1D202F",
		Error:     "#DB4B4B",

		Scrim:       "#16161E",
		SelectionFg: "#C0CAF5",
		SelectionBg: "#2E3C64",
		Page:        "#0F1017",
	}},
	{"gruvbox", themeColors{
		Blue:      "#FE8019",
		BlueLight: "#8EC07C",
		OffWhite:  "#EBDBB2",
		Gray:      "#928374",
		MutedBlue: "#928374",
		DimBlue:   "#504945",
		Divider:   "#3C3836",
		Error:     "#FB4934",

		Scrim:       "#141414",
		SelectionFg: "#EBDBB2",
		SelectionBg: "#504945",
		Page:        "#0F0F0D",
	}},
	{"nord", themeColors{
		Blue:      "#88C0D0",
		BlueLight: "#81A1C1",
		OffWhite:  "#ECEFF4",
		Gray:      "#4C566A",
		MutedBlue: "#4C566A",
		DimBlue:   "#434C5E",
		Divider:   "#3B4252",
		Error:     "#BF616A",

		Scrim:       "#242933",
		SelectionFg: "#ECEFF4",
		SelectionBg: "#434C5E",
		Page:        "#20242F",
	}},
	{"dracula", themeColors{
		Blue:      "#BD93F9",
		BlueLight: "#8BE9FD",
		OffWhite:  "#F8F8F2",
		Gray:      "#6272A4",
		MutedBlue: "#6272A4",
		DimBlue:   "#44475A",
		Divider:   "#44475A",
		Error:     "#FF5555",

		Scrim:       "#16171F",
		SelectionFg: "#F8F8F2",
		SelectionBg: "#44475A",
		Page:        "#0E0F16",
	}},
	{"solarized_dark", themeColors{
		Blue:      "#268BD2",
		BlueLight: "#2AA198",
		OffWhite:  "#839496",
		Gray:      "#586E75",
		MutedBlue: "#586E75",
		DimBlue:   "#073642",
		Divider:   "#586E75",
		Error:     "#DC322F",

		Scrim: "#001219",
		// Official highlight pairing is base02:base1 — brighter than the
		// body-text base0, which misses 4.5:1 on the selection bg.
		SelectionFg: "#93A1A1",
		SelectionBg: "#073642",
		Page:        "#000B10",
	}},
	{"rose_pine", themeColors{
		Blue:      "#C4A7E7",
		BlueLight: "#9CCFD8",
		OffWhite:  "#E0DEF4",
		Gray:      "#6E6A86",
		MutedBlue: "#6E6A86",
		DimBlue:   "#403D52",
		Divider:   "#524F67",
		Error:     "#EB6F92",

		Scrim:       "#0F0C15",
		SelectionFg: "#E0DEF4",
		SelectionBg: "#403D52",
		Page:        "#0C0A11",
	}},
	{"kanagawa", themeColors{
		Blue:      "#7E9CD8",
		BlueLight: "#7AA89F",
		OffWhite:  "#DCD7BA",
		Gray:      "#727169",
		MutedBlue: "#727169",
		DimBlue:   "#2D4F67",
		Divider:   "#2A2A37",
		Error:     "#E82424",

		Scrim:       "#16161D",
		SelectionFg: "#DCD7BA",
		SelectionBg: "#2D4F67",
		Page:        "#0E0E14",
	}},
}

// themeGlyphSets are the curated value sets for every glyph option; the
// registry entries below are the display names used by both the JSON
// schema and the settings cycles.
var (
	glyphBorderChoices     = []string{"rounded", "thick", "double", "ascii"}
	glyphNowPlayingChoices = []string{"note", "dot", "play", "arrow", "plain"}
	glyphPlayPauseChoices  = []string{"modern", "bold", "thin", "ascii"}
	glyphSpinnerChoices    = []string{"minidot", "dot", "line", "points", "meter", "pulse"}
	glyphBarChoices        = []string{"block", "line"}
)

var defaultGlyphs = themeGlyphs{Border: "rounded", NowPlaying: "note", PlayPause: "modern", Spinner: "minidot", Bar: "block"}
var defaultTypography = themeTypography{}
var defaultCover = themeCover{Frame: "none"}
var defaultBackgrounds = themeBackgrounds{Style: "divided"}

var validGlyphBorder = stringSet(glyphBorderChoices)
var validGlyphNowPlaying = stringSet(glyphNowPlayingChoices)
var validGlyphPlayPause = stringSet(glyphPlayPauseChoices)
var validGlyphSpinner = stringSet(glyphSpinnerChoices)
var validGlyphBar = stringSet(glyphBarChoices)
var validCoverFrame = stringSet([]string{"none", "rounded", "thick"})
var validBackgroundStyle = stringSet([]string{"divided", "solid"})

var validThemeColors = map[string]bool{
	"blue": true, "blue_light": true, "off_white": true, "gray": true,
	"muted_blue": true, "dim_blue": true, "divider": true, "error": true,
	"scrim": true, "selection_fg": true, "selection_bg": true,
	"page": true, "panel": true,
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, v := range values {
		out[v] = true
	}
	return out
}

// LoadTheme resolves the configured preset plus optional per-color and
// section overrides from a theme.json file. Unknown or invalid input warns
// and falls back, so the result is always renderable.
func LoadTheme(preset, path string) themeState {
	st := themePresetState(preset)

	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed reading theme file, using preset", "path", path, "error", err)
		}
		return st
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Warn("malformed theme file, using preset", "path", path, "error", err)
		return st
	}
	if p, ok := raw["preset"].(string); ok {
		st = themePresetState(p)
		delete(raw, "preset")
	}
	applyThemeStateOverrides(&st.glyphs, &st.typography, &st.cover, &st.backgrounds, raw)
	applyThemeOverrides(&st.colors, colorOverridesOnly(raw))
	return st
}

// colorOverridesOnly strips the structured sections (and the preset marker)
// from a raw theme.json map, leaving only the flat per-color overrides.
func colorOverridesOnly(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		switch k {
		case "glyphs", "typography", "cover":
		default:
			out[k] = v
		}
	}
	return out
}

// applyGlyphOverrides merges a raw glyphs map into g, warning and keeping
// the current value on unknown or wrongly-typed entries.
func applyGlyphOverrides(g *themeGlyphs, raw map[string]any) {
	applyStringField(raw, "border", validGlyphBorder, &g.Border, "glyphs.border")
	applyStringField(raw, "now_playing", validGlyphNowPlaying, &g.NowPlaying, "glyphs.now_playing")
	applyStringField(raw, "play_pause", validGlyphPlayPause, &g.PlayPause, "glyphs.play_pause")
	applyStringField(raw, "spinner", validGlyphSpinner, &g.Spinner, "glyphs.spinner")
	applyStringField(raw, "bar", validGlyphBar, &g.Bar, "glyphs.bar")
}

func applyStringField(raw map[string]any, key string, valid map[string]bool, dst *string, where string) {
	v, ok := raw[key]
	if !ok {
		return
	}
	s, ok := v.(string)
	if !ok || !valid[s] {
		slog.Warn("invalid theme option, keeping current value", "where", where, "value", v)
		return
	}
	*dst = s
}

// applyThemeStateOverrides merges the structured sections of a raw
// theme.json into glyph/typography/cover/backgrounds values.
func applyThemeStateOverrides(g *themeGlyphs, typ *themeTypography, cover *themeCover, bg *themeBackgrounds, raw map[string]any) {
	if gm, ok := raw["glyphs"].(map[string]any); ok {
		applyGlyphOverrides(g, gm)
	}
	if tm, ok := raw["typography"].(map[string]any); ok {
		if v, ok := tm["bold_titles"].(bool); ok {
			typ.BoldTitles = v
		}
		if v, ok := tm["italic_descriptions"].(bool); ok {
			typ.ItalicDescs = v
		}
	}
	if cm, ok := raw["cover"].(map[string]any); ok {
		applyStringField(cm, "frame", validCoverFrame, &cover.Frame, "cover.frame")
	}
	if bm, ok := raw["backgrounds"].(map[string]any); ok {
		applyStringField(bm, "style", validBackgroundStyle, &bg.Style, "backgrounds.style")
	}
}

// loadThemeOverrides reads the valid per-color overrides stored in a
// theme.json, so previews can resolve preset + user tweaks.
func loadThemeOverrides(path string) map[string]any {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	delete(raw, "preset")
	return raw
}

// resolveThemeColors combines a preset with stored per-color overrides.
func resolveThemeColors(preset string, overrides map[string]any) themeColors {
	colors := themePreset(preset)
	applyThemeOverrides(&colors, colorOverridesOnly(overrides))
	return colors
}

// resolveThemeState combines a preset with stored per-color and section
// overrides — the preview path for the options editor.
func resolveThemeState(preset string, overrides map[string]any) themeState {
	st := themePresetState(preset)
	applyThemeStateOverrides(&st.glyphs, &st.typography, &st.cover, &st.backgrounds, overrides)
	applyThemeOverrides(&st.colors, colorOverridesOnly(overrides))
	return st
}

// themePresetState resolves a preset into all layers.
func themePresetState(name string) themeState {
	return themeState{colors: themePreset(name), glyphs: defaultGlyphs, typography: defaultTypography, cover: defaultCover, backgrounds: defaultBackgrounds}
}

// SaveThemeOptions persists the full resolved state: the preset marker,
// per-color deltas against the preset, and the glyph/typography/cover
// sections. Deltas only, so a saved file keeps tracking its preset for
// everything the user left untouched. The write is atomic.
func SaveThemeOptions(path, preset string, state themeState) error {
	if path == "" {
		return fmt.Errorf("no theme file path configured")
	}
	name := themePresetName(preset)
	base := themePreset(name)
	body := map[string]any{"preset": name}
	if state.colors.Page != "" && state.colors.Page != base.Page {
		body["page"] = state.colors.Page
	}
	if state.colors.Panel != "" && state.colors.Panel != base.Panel {
		body["panel"] = state.colors.Panel
	}
	for _, c := range []struct {
		preset, current string
		json            string
	}{
		{base.Blue, state.colors.Blue, "blue"},
		{base.BlueLight, state.colors.BlueLight, "blue_light"},
		{base.OffWhite, state.colors.OffWhite, "off_white"},
		{base.Gray, state.colors.Gray, "gray"},
		{base.MutedBlue, state.colors.MutedBlue, "muted_blue"},
		{base.DimBlue, state.colors.DimBlue, "dim_blue"},
		{base.Divider, state.colors.Divider, "divider"},
		{base.Error, state.colors.Error, "error"},
		{base.Scrim, state.colors.Scrim, "scrim"},
		{base.SelectionFg, state.colors.SelectionFg, "selection_fg"},
		{base.SelectionBg, state.colors.SelectionBg, "selection_bg"},
	} {
		if c.current != "" && c.current != c.preset {
			body[c.json] = c.current
		}
	}
	if state.glyphs != defaultGlyphs {
		body["glyphs"] = state.glyphs
	}
	if state.typography != defaultTypography {
		body["typography"] = state.typography
	}
	if state.cover != defaultCover {
		body["cover"] = state.cover
	}
	if state.backgrounds != defaultBackgrounds {
		body["backgrounds"] = state.backgrounds
	}
	marshaled, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	return writeThemeFile(path, marshaled)
}

// SaveThemePreset persists the chosen preset as a marker in theme.json.
// Per-color overrides already in the file are preserved and keep applying
// on top of the preset. The write is atomic.
func SaveThemePreset(path, preset string) error {
	if path == "" {
		return fmt.Errorf("no theme file path configured")
	}
	name := themePresetName(preset)
	body := map[string]any{"preset": name}
	for k, v := range loadThemeOverrides(path) {
		body[k] = v
	}
	marshaled, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	return writeThemeFile(path, marshaled)
}

func writeThemeFile(path string, body []byte) error {
	body = append(body, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func applyThemeOverrides(colors *themeColors, raw map[string]any) {
	for name, v := range raw {
		if !validThemeColors[name] {
			slog.Warn("unknown color in theme file, ignoring", "color", name)
			continue
		}
		s, ok := v.(string)
		if !ok {
			slog.Warn("non-string color in theme file, ignoring", "color", name)
			continue
		}
		if !validColorValue(s) {
			slog.Warn("invalid color value, keeping preset color", "color", name, "value", s)
			continue
		}
		switch name {
		case "blue":
			colors.Blue = s
		case "blue_light":
			colors.BlueLight = s
		case "off_white":
			colors.OffWhite = s
		case "gray":
			colors.Gray = s
		case "muted_blue":
			colors.MutedBlue = s
		case "dim_blue":
			colors.DimBlue = s
		case "divider":
			colors.Divider = s
		case "error":
			colors.Error = s
		case "scrim":
			colors.Scrim = s
		case "selection_fg":
			colors.SelectionFg = s
		case "selection_bg":
			colors.SelectionBg = s
		case "page":
			colors.Page = s
		case "panel":
			colors.Panel = s
		}
	}
}

func themeRegistryNames() []string {
	names := make([]string, 0, len(themeRegistry))
	for _, entry := range themeRegistry {
		names = append(names, entry.name)
	}
	return names
}

func themePreset(name string) themeColors {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	for _, entry := range themeRegistry {
		if entry.name == name {
			return entry.colors
		}
	}
	if name != "" {
		slog.Warn("unknown theme preset, using default theme", "theme", name)
	}
	return themeRegistry[0].colors
}

// themePresetName normalizes a preset name for round-tripping; unknown
// names resolve to the default preset so saves stay renderable.
func themePresetName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	for _, entry := range themeRegistry {
		if entry.name == name {
			return entry.name
		}
	}
	return themeRegistry[0].name
}

func validColorValue(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	if isANSIColorName(s) {
		return true
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n >= 0 && n <= 15
	}
	return validHexColor(s)
}

func isANSIColorName(s string) bool {
	switch strings.ToLower(s) {
	case "black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
		"bright_black", "bright_red", "bright_green", "bright_yellow",
		"bright_blue", "bright_magenta", "bright_cyan", "bright_white":
		return true
	}
	return false
}

func validHexColor(s string) bool {
	hex := strings.TrimPrefix(s, "#")
	if len(hex) != 3 && len(hex) != 6 {
		return false
	}
	for _, c := range hex {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
