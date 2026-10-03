package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeInitialConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readSavedConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("config malformed after save:\n%s", data)
	}
	return saved
}

func TestSaveAppSettingsPreservesUnknownKeys(t *testing.T) {
	path := writeInitialConfig(t, `{"crossfade":{"enabled":false,"seconds":2},"custom_section":{"keep":true}}
`)
	enabled := true
	seconds := 5.0
	if err := SaveAppSettings(path, AppSettings{
		Crossfade: &CrossfadeSettings{Enabled: &enabled, Seconds: &seconds},
	}); err != nil {
		t.Fatal(err)
	}

	saved := readSavedConfig(t, path)
	if _, ok := saved["custom_section"]; !ok {
		t.Fatal("unknown top-level key was dropped on save")
	}
	cf, ok := saved["crossfade"].(map[string]any)
	if !ok {
		t.Fatal("crossfade section missing after save")
	}
	if cf["enabled"] != true {
		t.Fatalf("crossfade enabled = %v, want true", cf["enabled"])
	}
	if cf["seconds"] != 5.0 {
		t.Fatalf("crossfade seconds = %v, want 5", cf["seconds"])
	}
}

func TestSaveAppSettingsRecoversFromNullFile(t *testing.T) {
	path := writeInitialConfig(t, `null`)
	cacheEnabled := true
	sizeMB := int64(512)
	if err := SaveAppSettings(path, AppSettings{
		AudioCache: &AudioCacheSettings{Enabled: &cacheEnabled, SizeMB: &sizeMB},
	}); err != nil {
		t.Fatal(err)
	}

	saved := readSavedConfig(t, path)
	ac, ok := saved["audio_cache"].(map[string]any)
	if !ok {
		t.Fatal("audio_cache section missing after saving over a null file")
	}
	if ac["enabled"] != true {
		t.Fatalf("audio_cache enabled = %v, want true", ac["enabled"])
	}
}

func TestSaveAppSettingsOverMalformedFile(t *testing.T) {
	path := writeInitialConfig(t, `{not json`)
	enabled := false
	if err := SaveAppSettings(path, AppSettings{
		Crossfade: &CrossfadeSettings{Enabled: &enabled},
	}); err != nil {
		t.Fatal(err)
	}

	saved := readSavedConfig(t, path)
	if _, ok := saved["crossfade"]; !ok {
		t.Fatal("managed section missing after saving over a malformed file")
	}
}

func TestAppSettingsImageStyleOverlay(t *testing.T) {
	pixelated := " PIXELATED "
	cfg := Config{}
	ApplyAppSettings(&cfg, AppSettings{})
	if cfg.ImageStyle != "" {
		t.Fatalf("unset images section must leave ImageStyle empty, got %q", cfg.ImageStyle)
	}
	ApplyAppSettings(&cfg, AppSettings{Images: &ImageStyleSettings{Style: &pixelated}})
	if cfg.ImageStyle != ImageStylePixelated {
		t.Fatalf("ImageStyle = %q, want %q", cfg.ImageStyle, ImageStylePixelated)
	}
	none := "none"
	unknown := "oil-painting"
	ApplyAppSettings(&cfg, AppSettings{Images: &ImageStyleSettings{Style: &none}})
	ApplyAppSettings(&cfg, AppSettings{Images: &ImageStyleSettings{Style: &unknown}})
	if cfg.ImageStyle != ImageStylePixelated {
		t.Fatalf("env-only and unknown styles must not overwrite ImageStyle, got %q", cfg.ImageStyle)
	}
}

func TestApplyAppSettingsValidatesNumerics(t *testing.T) {
	configWarnings = nil
	cfg := Config{AudioCacheSizeMB: 1024, CrossfadeSeconds: 3}
	zero := int64(0)
	neg := int64(-512)
	negSecs := -2.5
	ApplyAppSettings(&cfg, AppSettings{AudioCache: &AudioCacheSettings{SizeMB: &zero}})
	ApplyAppSettings(&cfg, AppSettings{AudioCache: &AudioCacheSettings{SizeMB: &neg}})
	ApplyAppSettings(&cfg, AppSettings{Crossfade: &CrossfadeSettings{Seconds: &negSecs}})
	if cfg.AudioCacheSizeMB != 1024 {
		t.Fatalf("AudioCacheSizeMB = %d, want fallback 1024", cfg.AudioCacheSizeMB)
	}
	if cfg.CrossfadeSeconds != 3 {
		t.Fatalf("CrossfadeSeconds = %v, want fallback 3", cfg.CrossfadeSeconds)
	}
	if len(Warnings()) != 3 {
		t.Fatalf("expected 3 config warnings, got %q", Warnings())
	}

	configWarnings = nil
	good := int64(512)
	goodSecs := 5.0
	ApplyAppSettings(&cfg, AppSettings{
		AudioCache: &AudioCacheSettings{SizeMB: &good},
		Crossfade:  &CrossfadeSettings{Seconds: &goodSecs},
	})
	if cfg.AudioCacheSizeMB != 512 || cfg.CrossfadeSeconds != 5 {
		t.Fatalf("valid overlay not applied: %+v", cfg)
	}
	if len(Warnings()) != 0 {
		t.Fatalf("valid overlay must warn nothing, got %q", Warnings())
	}
}

func TestExplicitImageStyle(t *testing.T) {
	path := writeInitialConfig(t, `{"images":{"style":"rendered"}}
`)
	style, explicit := ExplicitImageStyle(path)
	if !explicit || style != ImageStyleRendered {
		t.Fatalf("explicit style = (%q, %v), want (%q, true)", style, explicit, ImageStyleRendered)
	}
	missing := writeInitialConfig(t, `{"crossfade":{"enabled":true}}
`)
	if style, explicit := ExplicitImageStyle(missing); explicit || style != "" {
		t.Fatalf("omitted images section must be unset, got (%q, %v)", style, explicit)
	}
	unknown := writeInitialConfig(t, `{"images":{"style":"oil-painting"}}
`)
	if style, explicit := ExplicitImageStyle(unknown); explicit || style != "" {
		t.Fatalf("unknown images style must be unset, got (%q, %v)", style, explicit)
	}
}

func TestLoadFromEnvImageStylePrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORPHEUS_CONFIG_DIR", dir)
	t.Setenv("SPOTIFY_CLIENT_ID", "test-client")
	t.Setenv("ORPHEUS_IMAGE_PROTOCOL", "none")
	settingsPath := filepath.Join(dir, "config.json")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImageStyle != "" {
		t.Fatalf("unset config.json must leave ImageStyle empty for ORPHEUS_IMAGE_PROTOCOL, got %q", cfg.ImageStyle)
	}

	pixelated := ImageStylePixelated
	if err := SaveAppSettings(settingsPath, AppSettings{Images: &ImageStyleSettings{Style: &pixelated}}); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImageStyle != ImageStylePixelated {
		t.Fatalf("explicit config.json style must win, got %q", cfg.ImageStyle)
	}
}
