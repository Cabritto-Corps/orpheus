package librespot

import (
	"os"
	"path/filepath"
	"testing"

	devicespb "github.com/elxgy/go-librespot/proto/spotify/connectstate/devices"
)

func TestParseDeviceTypeDefault(t *testing.T) {
	dt, err := parseDeviceType("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dt != devicespb.DeviceType_COMPUTER {
		t.Fatalf("expected COMPUTER for empty, got %v", dt)
	}
}

func TestParseDeviceTypeValid(t *testing.T) {
	dt, err := parseDeviceType("speaker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dt != devicespb.DeviceType_SPEAKER {
		t.Fatalf("expected SPEAKER, got %v", dt)
	}
}

func TestParseDeviceTypeCaseInsensitive(t *testing.T) {
	dt, err := parseDeviceType("Computer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dt != devicespb.DeviceType_COMPUTER {
		t.Fatalf("expected COMPUTER, got %v", dt)
	}
}

func TestParseDeviceTypeInvalid(t *testing.T) {
	_, err := parseDeviceType("toaster")
	if err == nil {
		t.Fatal("expected error for invalid device type")
	}
}

func TestEnsureConfigDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "orpheus", "sub")
	if err := EnsureConfigDir(sub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(sub); err != nil {
		t.Fatalf("directory not created: %v", err)
	}
}

func TestEnsureConfigDirEmpty(t *testing.T) {
	if err := EnsureConfigDir(""); err == nil {
		t.Fatal("expected error for empty config dir")
	}
}
