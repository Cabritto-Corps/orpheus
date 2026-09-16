package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func loadThemeColors(preset, path string) themeColors {
	st, _ := LoadTheme(preset, path)
	return st.colors
}

func TestDefaultPresetMatchesOriginalColors(t *testing.T) {
	want := map[string]string{
		"Blue": "#4A90D9", "BlueLight": "#7AB8E6", "OffWhite": "#C8CDD4",
		"Gray": "#808897", "MutedBlue": "#5B7A9E", "DimBlue": "#728FB0",
		"Divider": "#2A3A4A", "Error": "#FF5757",
	}
	c := themePreset("default")
	if c.Blue != want["Blue"] || c.BlueLight != want["BlueLight"] ||
		c.OffWhite != want["OffWhite"] || c.Gray != want["Gray"] ||
		c.MutedBlue != want["MutedBlue"] || c.DimBlue != want["DimBlue"] ||
		c.Divider != want["Divider"] || c.Error != want["Error"] {
		t.Fatalf("default preset drifted from the original palette: %+v", c)
	}
}

func TestLoadThemeDefaultIsPristine(t *testing.T) {
	colors := loadThemeColors("default", filepath.Join(t.TempDir(), "missing.json"))
	if colors.Blue != "#4A90D9" || colors.Error != "#FF5757" {
		t.Fatalf("missing theme file must yield pristine default, got %+v", colors)
	}
}

func TestThemeJSONOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	content := `{"blue": "#FF0000", "error": "bright_white"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	colors := loadThemeColors("default", path)
	if colors.Blue != "#FF0000" {
		t.Fatalf("blue override not applied: %q", colors.Blue)
	}
	if colors.Error != "bright_white" {
		t.Fatalf("error override not applied: %q", colors.Error)
	}
	if colors.Gray != "#808897" {
		t.Fatalf("unmentioned colors must keep preset value, gray = %q", colors.Gray)
	}
}

func TestThemeJSONInvalidValueKeepsPreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(path, []byte(`{"blue": "#XYZ", "gray": 5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	colors := loadThemeColors("default", path)
	if colors.Blue != "#4A90D9" {
		t.Fatalf("invalid hex must keep preset blue, got %q", colors.Blue)
	}
	if colors.Gray != "#808897" {
		t.Fatalf("non-string color must be ignored, gray = %q", colors.Gray)
	}
}

func TestThemeJSONUnknownColorIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(path, []byte(`{"purple": "#F0F", "blue": "#00FF00"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	colors := loadThemeColors("default", path)
	if colors.Blue != "#00FF00" {
		t.Fatalf("valid override must apply, blue = %q", colors.Blue)
	}
}

func TestThemeJSONMalformedFallsBackToPreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	colors := loadThemeColors("high_contrast", path)
	if colors.Blue != "#00FF87" {
		t.Fatalf("malformed file must fall back to preset, blue = %q", colors.Blue)
	}
}

func TestUnknownPresetFallsBackToDefault(t *testing.T) {
	colors := loadThemeColors("neon_dreams", "")
	if colors.Blue != "#4A90D9" {
		t.Fatalf("unknown preset must fall back to default, blue = %q", colors.Blue)
	}
}

func TestMinimalPresetUsesANSIAndNoColor(t *testing.T) {
	colors := loadThemeColors("minimal", "")
	// D5: minimal must keep roles distinguishable — accent (bold 7), bright
	// (15), dim (8) and selection inverted (black on 7) — not one flat "".
	if colors.Blue == colors.OffWhite && colors.Blue == colors.Gray {
		t.Fatal("minimal preset must keep at least three distinguishable roles")
	}
	if colors.Gray != "8" {
		t.Fatalf("minimal gray should be ANSI 8, got %q", colors.Gray)
	}
}

func TestApplyThemeAppliesAndIsIdempotent(t *testing.T) {
	colors := loadThemeColors("minimal", "")
	state := themeState{colors: colors}
	applyTheme(state)
	if colorBlue != lipgloss.Color("7") {
		t.Fatalf("minimal blue not applied, got %v", colorBlue)
	}
	first := colorGray
	applyTheme(state)
	if colorGray != first {
		t.Fatal("applyTheme is not idempotent")
	}

	applyTheme(themePresetState("default"))
	if c := lipgloss.Color("#4A90D9"); colorBlue != c {
		t.Fatalf("default restore drifted, colorBlue = %v", colorBlue)
	}
	if colorGray == first {
		t.Fatal("restoring default theme produced no color change")
	}
}

func TestSaveThemeOptionsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	state := themeState{
		colors:      themeColors{Blue: "#7AA2F7", Page: "#101216"},
		glyphs:      defaultGlyphs,
		typography:  defaultTypography,
		cover:       themeCover{Frame: "rounded"},
		backgrounds: themeBackgrounds{Style: "solid"},
	}
	if err := SaveThemeOptions(path, "default", state); err != nil {
		t.Fatal(err)
	}
	loaded, name := LoadTheme("default", path)
	if name != "default" {
		t.Fatalf("preset name = %q, want default", name)
	}
	if loaded.colors.Blue != "#7AA2F7" {
		t.Fatalf("blue delta not reloaded: %q", loaded.colors.Blue)
	}
	if loaded.colors.Gray != "#808897" {
		t.Fatalf("untouched color must follow the preset: %q", loaded.colors.Gray)
	}
	if loaded.colors.Page != "#101216" {
		t.Fatalf("page override not reloaded: %q", loaded.colors.Page)
	}
	if loaded.cover.Frame != "rounded" {
		t.Fatalf("cover frame not reloaded: %q", loaded.cover.Frame)
	}
	if loaded.backgrounds.Style != "solid" {
		t.Fatalf("backgrounds style not reloaded: %q", loaded.backgrounds.Style)
	}
	if loaded.glyphs != defaultGlyphs {
		t.Fatalf("default glyphs must not be written to the file: %+v", loaded.glyphs)
	}
}

func TestSaveThemeOptionsDeltasOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	state := themePresetState("default")
	if err := SaveThemeOptions(path, "default", state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"glyphs", "typography", "cover", "backgrounds", "blue", "gray", "page"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("untouched field %q must not be persisted", key)
		}
	}
	if raw["preset"] != "default" {
		t.Fatalf("preset marker missing: %v", raw)
	}
}

func TestValidColorValue(t *testing.T) {
	valid := []string{"", "#fff", "#4A90D9", "8", "15", "red", "BRIGHT_WHITE"}
	for _, v := range valid {
		if !validColorValue(v) {
			t.Errorf("validColorValue(%q) = false, want true", v)
		}
	}
	invalid := []string{"#12", "#12345", "#GGG", "16", "-1", "chucknorris"}
	for _, s := range invalid {
		if validColorValue(s) {
			t.Errorf("validColorValue(%q) = true, want false", s)
		}
	}
}
