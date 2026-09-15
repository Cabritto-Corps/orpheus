package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		RedirectURI:          "http://127.0.0.1:8989/callback",
		DeviceName:           "test",
		DeviceResolutionMode: "strict",
		TokenPath:            "/tmp/token.json",
		PollInterval:         1500 * time.Millisecond,
		Scopes:               []string{"streaming"},
	}
}

func TestValidateOK(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidateRejectsEmptyRedirectURI(t *testing.T) {
	cfg := validConfig()
	cfg.RedirectURI = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty redirect URI")
	}
}

func TestValidateRejectsEmptyDeviceName(t *testing.T) {
	cfg := validConfig()
	cfg.DeviceName = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty device name")
	}
}

func TestValidateRejectsInvalidResolutionMode(t *testing.T) {
	cfg := validConfig()
	cfg.DeviceResolutionMode = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid resolution mode")
	}
}

func TestValidateAcceptsRelaxedMode(t *testing.T) {
	cfg := validConfig()
	cfg.DeviceResolutionMode = "relaxed"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected relaxed mode to be valid, got: %v", err)
	}
}

func TestValidateRejectsEmptyTokenPath(t *testing.T) {
	cfg := validConfig()
	cfg.TokenPath = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty token path")
	}
}

func TestValidateRejectsZeroPollInterval(t *testing.T) {
	cfg := validConfig()
	cfg.PollInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero poll interval")
	}
}

func TestValidateRejectsEmptyScopes(t *testing.T) {
	cfg := validConfig()
	cfg.Scopes = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty scopes")
	}
}

func TestValidateMultipleErrors(t *testing.T) {
	cfg := validConfig()
	cfg.RedirectURI = ""
	cfg.DeviceName = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for multiple invalid fields")
	}
}

func TestValidateForAuthRequiresClientID(t *testing.T) {
	cfg := validConfig()
	if err := cfg.ValidateForAuth(); err == nil {
		t.Fatal("expected error for missing client ID")
	}
}

func TestValidateForAuthOK(t *testing.T) {
	cfg := validConfig()
	cfg.SpotifyClientID = "abc123"
	if err := cfg.ValidateForAuth(); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("EBT", "true")
	if !envBool("EBT", false) {
		t.Fatal("expected true")
	}

	t.Setenv("EBF", "false")
	if envBool("EBF", true) {
		t.Fatal("expected false")
	}

	if !envBool("EB_EMPTY_NEVER_SET_12345", true) {
		t.Fatal("expected fallback for unset var")
	}

	t.Setenv("EBBAD", "notabool")
	if envBool("EBBAD", false) {
		t.Fatal("expected fallback (false) on parse error")
	}
}

func TestEnvDuration(t *testing.T) {
	t.Setenv("TEST_DUR", "2s")
	if got := envDuration("TEST_DUR", time.Second); got != 2*time.Second {
		t.Fatalf("expected 2s, got %v", got)
	}
	t.Setenv("TEST_DUR", "")
	if got := envDuration("TEST_DUR", 500*time.Millisecond); got != 500*time.Millisecond {
		t.Fatalf("expected fallback, got %v", got)
	}
	t.Setenv("TEST_DUR", "invalid")
	if got := envDuration("TEST_DUR", time.Second); got != time.Second {
		t.Fatalf("expected fallback on parse error, got %v", got)
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV("a, b, c")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("unexpected: %v", got)
	}

	got = splitCSV(",,")
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}

	got = splitCSV("single")
	if len(got) != 1 || got[0] != "single" {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestLoadEnvFileFallsBackToConfigDir(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "orpheus")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(configDir, ".env")
	if err := os.WriteFile(envPath, []byte("ORPHEUS_TEST_FROM_CONFIG_DIR=from-config-env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORPHEUS_CONFIG_DIR", configDir)
	os.Unsetenv("ORPHEUS_TEST_FROM_CONFIG_DIR")

	emptyDir := t.TempDir()
	t.Chdir(emptyDir)

	loadEnvFile()

	if got := os.Getenv("ORPHEUS_TEST_FROM_CONFIG_DIR"); got != "from-config-env" {
		t.Fatalf("expected env var from config-dir .env, got %q", got)
	}
}

func TestLoadEnvFileCwdTakesPrecedence(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "orpheus")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".env"), []byte("ORPHEUS_PRECEDENCE_TEST=from-config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwdDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwdDir, ".env"), []byte("ORPHEUS_PRECEDENCE_TEST=from-cwd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORPHEUS_CONFIG_DIR", configDir)
	os.Unsetenv("ORPHEUS_PRECEDENCE_TEST")
	t.Chdir(cwdDir)

	loadEnvFile()

	if got := os.Getenv("ORPHEUS_PRECEDENCE_TEST"); got != "from-cwd" {
		t.Fatalf("expected cwd .env to take precedence, got %q", got)
	}
}

func TestDefaultConfigDirRespectsEnv(t *testing.T) {
	t.Setenv("ORPHEUS_CONFIG_DIR", "/custom/orpheus")
	got, err := DefaultConfigDir()
	if err != nil || got != "/custom/orpheus" {
		t.Fatalf("expected /custom/orpheus, got %q err=%v", got, err)
	}
}

func TestAudioCacheEnvParsing(t *testing.T) {
	t.Setenv("orpheus_audio_cache_enabled", "true")
	t.Setenv("orpheus_audio_cache_size_mb", "2048")
	t.Setenv("orpheus_audio_cache_dir", "/tmp/orpheus-cache-test")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if !cfg.AudioCacheEnabled {
		t.Fatal("expected audio cache enabled")
	}
	if cfg.AudioCacheSizeMB != 2048 {
		t.Fatalf("AudioCacheSizeMB = %d, want 2048", cfg.AudioCacheSizeMB)
	}
	if cfg.AudioCacheDir != "/tmp/orpheus-cache-test" {
		t.Fatalf("AudioCacheDir = %q", cfg.AudioCacheDir)
	}
}

func TestAudioCacheEnvDefaults(t *testing.T) {
	os.Unsetenv("orpheus_audio_cache_enabled")
	os.Unsetenv("orpheus_audio_cache_size_mb")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.AudioCacheEnabled {
		t.Fatal("audio cache must default to disabled")
	}
	if cfg.AudioCacheSizeMB != 1024 {
		t.Fatalf("AudioCacheSizeMB default = %d, want 1024", cfg.AudioCacheSizeMB)
	}
}

func TestAudioCacheEnvMalformedFallsBack(t *testing.T) {
	t.Setenv("orpheus_audio_cache_enabled", "true")
	t.Setenv("orpheus_audio_cache_size_mb", "lots")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.AudioCacheSizeMB != 1024 {
		t.Fatalf("malformed size should fall back to default, got %d", cfg.AudioCacheSizeMB)
	}
}

func TestCrossfadeEnvParsing(t *testing.T) {
	t.Setenv("orpheus_crossfade", "true")
	t.Setenv("orpheus_crossfade_seconds", "6.5")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if !cfg.Crossfade {
		t.Fatal("expected crossfade enabled")
	}
	if cfg.CrossfadeSeconds != 6.5 {
		t.Fatalf("CrossfadeSeconds = %v, want 6.5", cfg.CrossfadeSeconds)
	}
}

func TestCrossfadeEnvDefaults(t *testing.T) {
	os.Unsetenv("orpheus_crossfade")
	os.Unsetenv("orpheus_crossfade_seconds")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.Crossfade {
		t.Fatal("crossfade must default to disabled")
	}
	if cfg.CrossfadeSeconds != 0 {
		t.Fatalf("CrossfadeSeconds default = %v, want 0", cfg.CrossfadeSeconds)
	}
}

func TestCrossfadeEnvMalformedFallsBack(t *testing.T) {
	t.Setenv("orpheus_crossfade", "true")
	t.Setenv("orpheus_crossfade_seconds", "lots")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.CrossfadeSeconds != 0 {
		t.Fatalf("malformed CrossfadeSeconds = %v, want 0", cfg.CrossfadeSeconds)
	}
}

func TestCrossfadeNegativeSecondsRejected(t *testing.T) {
	t.Setenv("orpheus_crossfade", "true")
	t.Setenv("orpheus_crossfade_seconds", "-2")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.CrossfadeSeconds != 0 {
		t.Fatalf("negative CrossfadeSeconds = %v, want 0", cfg.CrossfadeSeconds)
	}
}
