package config

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
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
}

func LoadFromEnv() (Config, error) {
	if err := godotenv.Load(); err != nil {
		if _, statErr := os.Stat(".env"); statErr == nil {
			slog.Warn("failed to parse .env file", "error", err)
		}
	}

	cfg := Config{
		SpotifyClientID:      envAny("spotify_client_id", "SPOTIFY_CLIENT_ID"),
		RedirectURI:          envDefault("spotify_redirect_uri", "http://127.0.0.1:8989/callback"),
		Scopes:               splitCSV(envDefault("spotify_scopes", "streaming,user-read-playback-state,user-modify-playback-state,user-read-currently-playing,playlist-read-private,playlist-read-collaborative,user-library-read")),
		DeviceName:           envDefault("spotify_device_name", "orpheus"),
		DeviceResolutionMode: envDefault("orpheus_device_resolution_mode", "strict"),
		AllowActiveFallback:  envBool("orpheus_allow_active_fallback", false),
		TokenPath:            envDefault("orpheus_token_path", defaultTokenPath()),
		PollInterval:         envDuration("orpheus_poll_interval", 1500*time.Millisecond),
		NerdFonts:            envBool("orpheus_nerd_fonts", false),
		OnSongChange:         envDefault("orpheus_on_song_change", ""),
		LogFile:              envDefault("orpheus_log_file", defaultLogPath()),
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

func defaultTokenPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ".orpheus-token.json"
	}
	return home + "/.config/orpheus/token.json"
}

func defaultLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return home + "/.config/orpheus/orpheus.log"
}
