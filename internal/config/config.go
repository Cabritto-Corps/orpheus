package config

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	SpotifyClientID      string
	RedirectURI          string
	Scopes               []string
	DeviceName           string
	DeviceResolutionMode string
	AllowActiveFallback  bool
	TokenPath            string
	PollInterval         time.Duration
	NerdFonts            bool
	OnSongChange         string
	LogFile              string
	AudioCacheEnabled    bool
	AudioCacheSizeMB     int64
	AudioCacheDir        string
}

func LoadFromEnv() (Config, error) {
	loadEnvFile()

	cfg := Config{
		SpotifyClientID:      envAny("spotify_client_id", "SPOTIFY_CLIENT_ID"),
		RedirectURI:          envDefault("spotify_redirect_uri", "http://127.0.0.1:8989/callback"),
		Scopes:               splitCSV(envDefault("spotify_scopes", "streaming,user-read-playback-state,user-modify-playback-state,user-read-currently-playing,playlist-read-private,playlist-read-collaborative,user-library-read")),
		DeviceName:           envDefault("spotify_device_name", "orpheus"),
		DeviceResolutionMode: envDefault("orpheus_device_resolution_mode", "strict"),
		AllowActiveFallback:  envBool("orpheus_allow_active_fallback", false),
		TokenPath:            envDefault("orpheus_token_path", defaultTokenPath()),
		PollInterval:         envDuration("orpheus_poll_interval", 1500*time.Millisecond),
		NerdFonts:            resolveNerdFonts(os.Getenv("orpheus_nerd_fonts")),
		OnSongChange:         envDefault("orpheus_on_song_change", ""),
		LogFile:              envDefault("orpheus_log_file", defaultLogPath()),
		AudioCacheEnabled:    envBool("orpheus_audio_cache_enabled", false),
		AudioCacheSizeMB:     envInt64("orpheus_audio_cache_size_mb", 1024),
		AudioCacheDir:        envDefault("orpheus_audio_cache_dir", ""),
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) ValidateForAuth() error {
	if c.SpotifyClientID == "" {
		return errors.New("spotify_client_id / SPOTIFY_CLIENT_ID is not set\n" +
			"Register a free Spotify app at https://developer.spotify.com/dashboard,\n" +
			"add http://127.0.0.1:8989/callback as a redirect URI, then set the env var")
	}
	return nil
}

func (c Config) Validate() error {
	var errs []error
	if c.RedirectURI == "" {
		errs = append(errs, errors.New("spotify_redirect_uri must not be empty"))
	}
	if c.DeviceName == "" {
		errs = append(errs, errors.New("spotify_device_name must not be empty"))
	}
	if c.DeviceResolutionMode != "strict" && c.DeviceResolutionMode != "relaxed" {
		errs = append(errs, errors.New("orpheus_device_resolution_mode must be strict or relaxed"))
	}
	if c.TokenPath == "" {
		errs = append(errs, errors.New("orpheus_token_path must not be empty"))
	}
	if c.PollInterval <= 0 {
		errs = append(errs, errors.New("orpheus_poll_interval must be > 0"))
	}
	if len(c.Scopes) == 0 {
		errs = append(errs, errors.New("spotify_scopes must define at least one scope"))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func envAny(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		slog.Warn("invalid boolean value, using default", "key", key, "value", raw, "default", fallback)
		return fallback
	}
	return v
}

// resolveNerdFonts honors explicit true/false and falls back to auto-detection
// ("auto" or unset): Nerd Font glyphs can only render if a Nerd Font family is
// installed and selectable by the terminal, so the fontconfig list is the best
// available signal.
func resolveNerdFonts(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return nerdFontsInstalled()
	case "auto":
		return nerdFontsInstalled()
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		slog.Warn("invalid orpheus_nerd_fonts value, auto-detecting", "value", raw)
		return nerdFontsInstalled()
	}
}

var nerdFontsInstalled = sync.OnceValue(detectNerdFontsInstalled)

func detectNerdFontsInstalled() bool {
	out, err := exec.Command("fc-list", ":", "family").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "nerd font")
}

func envInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		slog.Warn("invalid integer value, using default", "key", key, "value", raw, "default", fallback)
		return fallback
	}
	return v
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("invalid duration value, using default", "key", key, "value", raw, "default", fallback)
		return fallback
	}
	return v
}

func splitCSV(input string) []string {
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

func DefaultConfigDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("ORPHEUS_CONFIG_DIR")); v != "" {
		return v, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "orpheus"), nil
}

func loadEnvFile() {
	if _, err := os.Stat(".env"); err == nil {
		if loadErr := godotenv.Load(); loadErr != nil {
			slog.Warn("failed to parse .env file", "error", loadErr)
		}
		return
	}
	dir, err := DefaultConfigDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, ".env")
	if _, err := os.Stat(path); err == nil {
		if loadErr := godotenv.Load(path); loadErr != nil {
			slog.Warn("failed to parse .env file", "path", path, "error", loadErr)
		}
	}
}

func defaultTokenPath() string {
	dir, err := DefaultConfigDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ".orpheus-token.json"
	}
	return filepath.Join(dir, "token.json")
}

func defaultLogPath() string {
	dir, err := DefaultConfigDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, "orpheus.log")
}
