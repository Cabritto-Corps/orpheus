package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orpheus/internal/config"
)

func TestSaveAppSettingsWritesConfigJSON(t *testing.T) {
	m, _, _, configPath := newSettingsTestModel(t)
	m.ui.settings.crossfadeEnabled = true
	m.ui.settings.crossfadeSeconds = 5

	m.saveAppSettings()
	if m.ui.settings.saveErr != "" {
		t.Fatalf("save must succeed, got %q", m.ui.settings.saveErr)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.json not written: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("malformed config.json: %v", err)
	}
	cf, ok := raw["crossfade"].(map[string]any)
	if !ok || cf["enabled"] != true || cf["seconds"] != float64(5) {
		t.Fatalf("crossfade section wrong: %v", raw)
	}
}

func TestSaveAppSettingsSurfacesFailure(t *testing.T) {
	m, _, _, configPath := newSettingsTestModel(t)
	// A file in place of the target directory makes MkdirAll fail for real.
	if err := os.WriteFile(filepath.Join(filepath.Dir(configPath), "notadir"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.ui.settings.configPath = filepath.Join(filepath.Dir(configPath), "notadir", "config.json")

	m.saveAppSettings()
	if m.ui.settings.saveErr == "" {
		t.Fatal("save failure must be surfaced in the settings modal")
	}
}

func TestSettingsRootShowsSaveError(t *testing.T) {
	m, _, _, configPath := newSettingsTestModel(t)
	m.ui.width = 100
	m.ui.height = 40
	if err := os.WriteFile(filepath.Join(filepath.Dir(configPath), "notadir"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.ui.settings.configPath = filepath.Join(filepath.Dir(configPath), "notadir", "config.json")
	m.saveAppSettings()

	out := m.settingsModalView()
	if !strings.Contains(out, "save failed") {
		t.Fatalf("save error not rendered in the settings modal:\n%s", out)
	}
}

func TestCrossfadeSaveAndReloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORPHEUS_CONFIG_DIR", dir)
	t.Setenv("SPOTIFY_CLIENT_ID", "test-client")
	t.Setenv("ORPHEUS_KEYS_FILE", filepath.Join(dir, "keys.json"))
	t.Setenv("ORPHEUS_TOKEN_PATH", filepath.Join(dir, "token.json"))
	t.Setenv("ORPHEUS_THEME_FILE", filepath.Join(dir, "theme.json"))
	t.Setenv("ORPHEUS_LOG_FILE", filepath.Join(dir, "orpheus.log"))

	m, _, _, _ := newSettingsTestModel(t)
	m.ui.settings.configPath = filepath.Join(dir, "config.json")

	next := openSettingsForTest(m)
	// crossfade row is index 3 in the five-row root
	next = send(next, teaDown())
	next = send(next, teaDown())
	next = send(next, teaDown())
	if next.ui.settings.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", next.ui.settings.cursor)
	}
	next = sendEnter(next)
	if next.ui.settings.saveErr != "" {
		t.Fatalf("save failed: %q", next.ui.settings.saveErr)
	}

	// reload: the loader overlays config.json onto the env-derived values
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Crossfade || cfg.CrossfadeSeconds != 3 {
		t.Fatalf("reload lost the settings: crossfade=%v seconds=%v", cfg.Crossfade, cfg.CrossfadeSeconds)
	}
}
