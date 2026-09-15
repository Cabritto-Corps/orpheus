package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpsertEnvFilePreservesCommentsAndOtherLines(t *testing.T) {
	path := writeEnvFile(t, ""+
		"# my spotify dashboard creds\n"+
		"SPOTIFY_CLIENT_ID=abc123\n"+
		"\n"+
		"# player settings\n"+
		"orpheus_poll_interval=1500ms\n")

	err := UpsertEnvFile(path, map[string]string{"orpheus_crossfade": "true"})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "SPOTIFY_CLIENT_ID=abc123") {
		t.Fatalf("client id line was altered:\n%s", got)
	}
	if !strings.Contains(got, "# player settings") {
		t.Fatalf("comment lost:\n%s", got)
	}
	if !strings.Contains(got, "orpheus_crossfade=true") {
		t.Fatalf("key not written:\n%s", got)
	}
	if strings.Contains(got, "orpheus_crossfade ") {
		t.Fatalf("wrong key written:\n%s", got)
	}
}

func TestUpsertEnvFileReplacesExistingKeyInPlace(t *testing.T) {
	path := writeEnvFile(t, "A=1\norpheus_crossfade=false\nB=2\n")

	if err := UpsertEnvFile(path, map[string]string{"orpheus_crossfade": "true"}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Contains(got, "orpheus_crossfade=false") {
		t.Fatalf("old value still present:\n%s", got)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if lines[1] != "orpheus_crossfade=true" {
		t.Fatalf("expected in-place replacement at line 2, got %q", lines[1])
	}
	if len(lines) != 3 || lines[0] != "A=1" || lines[2] != "B=2" {
		t.Fatalf("other lines moved: %q", lines)
	}
}

func TestUpsertEnvFileAppendsMissingKey(t *testing.T) {
	path := writeEnvFile(t, "SPOTIFY_CLIENT_ID=abc\n# tail comment\n")

	if err := UpsertEnvFile(path, map[string]string{"orpheus_crossfade": "true", "orpheus_crossfade_seconds": "3"}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "# tail comment\n") {
		t.Fatalf("comment not preserved before appended keys:\n%s", got)
	}
	if !strings.Contains(got, "orpheus_crossfade=true") || !strings.Contains(got, "orpheus_crossfade_seconds=3") {
		t.Fatalf("keys not appended:\n%s", got)
	}
}

func TestUpsertEnvFileCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")

	if err := UpsertEnvFile(path, map[string]string{"orpheus_crossfade": "true"}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if got := string(data); got != "orpheus_crossfade=true\n" {
		t.Fatalf("unexpected new file content: %q", got)
	}
}

func TestUpsertEnvFileMultipleKeysOneWrite(t *testing.T) {
	path := writeEnvFile(t, "orpheus_crossfade=false\norpheus_crossfade_seconds=1\nkeep=yes\n")

	if err := UpsertEnvFile(path, map[string]string{"orpheus_crossfade": "true", "orpheus_crossfade_seconds": "5"}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Contains(got, "=false") || strings.Contains(got, "=1\n") {
		t.Fatalf("stale values remain:\n%s", got)
	}
	if !strings.Contains(got, "orpheus_crossfade=true") || !strings.Contains(got, "orpheus_crossfade_seconds=5") || !strings.Contains(got, "keep=yes") {
		t.Fatalf("unexpected content:\n%s", got)
	}
}
