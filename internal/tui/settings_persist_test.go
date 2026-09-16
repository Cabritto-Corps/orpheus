package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistEnvFallsBackToConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORPHEUS_CONFIG_DIR", dir)

	m, _, _, _ := newSettingsTestModel(t)
	m.ui.settings.envPath = ""

	m.persistEnv(map[string]string{"orpheus_crossfade": "true"})
	if m.ui.settings.saveErr != "" {
		t.Fatalf("save must succeed via the config-dir fallback, got %q", m.ui.settings.saveErr)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("config-dir .env not created: %v", err)
	}
	if !strings.Contains(string(data), "orpheus_crossfade=true") {
		t.Fatalf("value not persisted:\n%s", data)
	}
	if m.ui.settings.envPath != filepath.Join(dir, ".env") {
		t.Fatalf("resolved path not remembered, envPath = %q", m.ui.settings.envPath)
	}
}

func TestPersistEnvSurfacesFailure(t *testing.T) {
	m, _, _, envPath := newSettingsTestModel(t)
	m.ui.settings.envPath = filepath.Join(envPath, "missing-dir", ".env")

	m.persistEnv(map[string]string{"orpheus_crossfade": "true"})
	if m.ui.settings.saveErr == "" {
		t.Fatal("save failure must be surfaced in the settings modal")
	}
}

func TestSettingsRootShowsSaveError(t *testing.T) {
	m, _, _, envPath := newSettingsTestModel(t)
	m.ui.width = 100
	m.ui.height = 40
	m.ui.settings.envPath = filepath.Join(envPath, "missing-dir", ".env")
	m.persistEnv(map[string]string{"orpheus_crossfade": "true"})

	out := m.settingsModalView()
	if !strings.Contains(out, "save failed") {
		t.Fatalf("save error not rendered in the settings modal:\n%s", out)
	}
}

func TestCrossfadeSaveAndReloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORPHEUS_CONFIG_DIR", dir)
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("SPOTIFY_CLIENT_ID=keepme\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, _, _, _ := newSettingsTestModel(t)
	m.ui.settings.envPath = envPath

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

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "orpheus_crossfade=true") ||
		!strings.Contains(string(data), "SPOTIFY_CLIENT_ID=keepme") {
		t.Fatalf("round-trip lost data:\n%s", data)
	}
}
