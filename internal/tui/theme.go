package tui

import (
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

type themeColors struct {
	Blue      string `json:"blue"`
	BlueLight string `json:"blue_light"`
	OffWhite  string `json:"off_white"`
	Gray      string `json:"gray"`
	MutedBlue string `json:"muted_blue"`
	DimBlue   string `json:"dim_blue"`
	Divider   string `json:"divider"`
	Error     string `json:"error"`
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
	},
	"minimal": {
		Blue:      "",
		BlueLight: "8",
		OffWhite:  "",
		Gray:      "8",
		MutedBlue: "8",
		DimBlue:   "8",
		Divider:   "8",
		Error:     "1",
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
	},
}

var validThemeColors = map[string]bool{
	"blue": true, "blue_light": true, "off_white": true, "gray": true,
	"muted_blue": true, "dim_blue": true, "divider": true, "error": true,
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
	applyThemeOverrides(&colors, raw)
	return colors
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
		slog.Warn("unknown orpheus_theme, using default theme", "theme", name)
		return themePresets["default"]
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
