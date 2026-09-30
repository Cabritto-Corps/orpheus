package config

import (
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

type AppSettings struct {
	Crossfade  *CrossfadeSettings  `json:"crossfade,omitempty"`
	AudioCache *AudioCacheSettings `json:"audio_cache,omitempty"`
	Images     *ImageStyleSettings `json:"images,omitempty"`
}

const (
	ImageStyleRendered  = "rendered"
	ImageStylePixelated = "pixelated"
)

type ImageStyleSettings struct {
	Style *string `json:"style,omitempty"`
}

func NormalizeImageStyle(style string) string {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case ImageStyleRendered:
		return ImageStyleRendered
	case ImageStylePixelated:
		return ImageStylePixelated
	default:
		return ""
	}
}

func ExplicitImageStyle(path string) (string, bool) {
	settings := LoadAppSettings(path)
	if settings.Images == nil || settings.Images.Style == nil {
		return "", false
	}
	style := NormalizeImageStyle(*settings.Images.Style)
	return style, style != ""
}

type CrossfadeSettings struct {
	Enabled *bool    `json:"enabled,omitempty"`
	Seconds *float64 `json:"seconds,omitempty"`
}

type AudioCacheSettings struct {
	Enabled *bool  `json:"enabled,omitempty"`
	SizeMB  *int64 `json:"size_mb,omitempty"`
}

func defaultSettingsPath() string {
	dir, err := DefaultConfigDir()
	if err != nil || dir == "" {
		return "config.json"
	}
	return filepath.Join(dir, "config.json")
}

func ApplyAppSettings(cfg *Config, settings AppSettings) {
	if s := settings.Crossfade; s != nil {
		if s.Enabled != nil {
			cfg.Crossfade = *s.Enabled
		}
		if s.Seconds != nil {
			cfg.CrossfadeSeconds = *s.Seconds
		}
	}
	if s := settings.AudioCache; s != nil {
		if s.Enabled != nil {
			cfg.AudioCacheEnabled = *s.Enabled
		}
		if s.SizeMB != nil {
			cfg.AudioCacheSizeMB = *s.SizeMB
		}
	}
	if s := settings.Images; s != nil && s.Style != nil {
		if style := NormalizeImageStyle(*s.Style); style != "" {
			cfg.ImageStyle = style
		}
	}
}

func LoadAppSettings(path string) AppSettings {
	var settings AppSettings
	if path == "" {
		return settings
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed reading config file", "path", path, "error", err)
		}
		return settings
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		slog.Warn("malformed config file, using environment values", "path", path, "error", err)
	}
	return settings
}

// SaveAppSettings merges atomically: unmanaged keys survive, managed sections win.
func SaveAppSettings(path string, settings AppSettings) error {
	if path == "" {
		return os.ErrInvalid
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	merged := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &merged); err != nil || merged == nil {
			merged = map[string]any{}
		}
	}
	ours, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	var overlay map[string]any
	if err := json.Unmarshal(ours, &overlay); err != nil {
		return err
	}
	maps.Copy(merged, overlay)
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
