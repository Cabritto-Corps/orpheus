package config

import (
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
