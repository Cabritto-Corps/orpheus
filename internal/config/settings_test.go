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
