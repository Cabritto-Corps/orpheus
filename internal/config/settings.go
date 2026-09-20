package config

import (
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
)

// AppSettings is what the settings UI manages: the sections it writes live
// in config.json instead of .env, which stays a user-authored bootstrap
// file (normally just the Spotify client id).
type AppSettings struct {
	Crossfade  *CrossfadeSettings  `json:"crossfade,omitempty"`
	AudioCache *AudioCacheSettings `json:"audio_cache,omitempty"`
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

// ApplyAppSettings overlays the settings file onto the env-derived config:
// a value present in config.json wins over the .env/env copy, because the
// settings UI writes there. Anything the file omits keeps falling back to
// the environment and the defaults, so hand-edited files stay short.
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
}

// LoadAppSettings reads config.json, warning and starting empty on
// malformed input so the env values keep working.
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

// SaveAppSettings persists the managed sections atomically, merged over
// whatever the file already contains: keys outside the two managed sections
// survive a save instead of being silently dropped, and the managed
// sections always win. A malformed existing file starts from a clean
// object rather than failing the save.
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
			// Malformed or null: start from a clean object; the managed
			// sections still win.
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
