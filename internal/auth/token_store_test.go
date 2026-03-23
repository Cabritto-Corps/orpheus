package auth

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewFileTokenStore(path)

	token := &oauth2.Token{
		AccessToken:  "access123",
		RefreshToken: "refresh456",
		TokenType:    "Bearer",
	}

	if err := store.Save(token); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.AccessToken != "access123" {
		t.Fatalf("expected access123, got %s", loaded.AccessToken)
	}
	if loaded.RefreshToken != "refresh456" {
		t.Fatalf("expected refresh456, got %s", loaded.RefreshToken)
	}
}

func TestLoadMissingFile(t *testing.T) {
	store := NewFileTokenStore("/tmp/nonexistent-token-test-12345.json")
	_, err := store.Load()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(path, []byte("not json"), 0o600)

	store := NewFileTokenStore(path)
	_, err := store.Load()
	if err == nil {
		t.Fatal("expected error for corrupted file")
	}
}

func TestSaveNilToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewFileTokenStore(path)

	if err := store.Save(nil); err == nil {
		t.Fatal("expected error for nil token")
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "nested", "token.json")
	store := NewFileTokenStore(path)

	token := &oauth2.Token{AccessToken: "test"}
	if err := store.Save(token); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestSaveUsesAtomicRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewFileTokenStore(path)

	_ = store.Save(&oauth2.Token{AccessToken: "v1"})

	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Fatal("tmp file should be removed after rename")
	}
}

func TestSaveFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	store := NewFileTokenStore(path)

	_ = store.Save(&oauth2.Token{AccessToken: "secret"})
	info, _ := os.Stat(path)
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Fatalf("expected 0600, got %o", perm)
	}
}
