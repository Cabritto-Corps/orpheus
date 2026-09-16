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
}

var themePresets = map[string]themeColors{
	"default": {
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
	},
	"minimal": {
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
	},
	"high_contrast": {
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
	},
}

var validThemeColors = map[string]bool{
	"blue": true, "blue_light": true, "off_white": true, "gray": true,
	"muted_blue": true, "dim_blue": true, "divider": true, "error": true,
	"scrim": true, "selection_fg": true, "selection_bg": true,
}

// LoadTheme resolves the configured preset plus optional per-color overrides
// from a theme.json file. Unknown or invalid input warns and falls back, so
// the result is always renderable.
func LoadTheme(preset, path string) themeColors {
	colors := themePreset(preset)

	if path == "" {
		return colors
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed reading theme file, using preset", "path", path, "error", err)
		}
		return colors
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Warn("malformed theme file, using preset", "path", path, "error", err)
		return colors
	}
	if p, ok := raw["preset"].(string); ok {
		colors = themePreset(p)
		delete(raw, "preset")
	}
	applyThemeOverrides(&colors, raw)
	return colors
}

// SaveThemePreset persists the chosen preset as a marker in theme.json.
// Per-color overrides present in the file are dropped: they would otherwise
// fight the preset. The write is atomic.
func SaveThemePreset(path, preset string) error {
	if path == "" {
		return fmt.Errorf("no theme file path configured")
	}
	name := themePresetName(preset)
	body, err := json.MarshalIndent(map[string]string{"preset": name}, "", "  ")
	if err != nil {
		return err
	}
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
		}
	}
}

func themePreset(name string) themeColors {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "default":
		return themePresets["default"]
	case "minimal":
		return themePresets["minimal"]
	case "high_contrast", "high-contrast":
		return themePresets["high_contrast"]
	default:
		slog.Warn("unknown theme preset, using default theme", "theme", name)
		return themePresets["default"]
	}
}

// themePresetName normalizes a preset name for round-tripping.
func themePresetName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "minimal":
		return "minimal"
	case "high_contrast", "high-contrast":
		return "high_contrast"
	default:
		return "default"
	}
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
