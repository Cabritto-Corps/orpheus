package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	forkspotify "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/session"
	"github.com/elxgy/go-librespot/sessionconfig"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
	"orpheus/internal/tui"
)

type playerSession struct {
	Session  *session.Session
	AppState *forkspotify.AppState
}

func (s playerSession) Close() {
	if s.Session != nil {
		s.Session.Close()
	}
}

type playerBackend struct {
	Cleanup func()
	Catalog spotify.PlaylistCatalog
}

type playerSessionConnector func(ctx context.Context, logger forkspotify.Logger, configDir string) (playerSession, error)
type playerBackendConnector func(ctx context.Context, sess playerSession) (playerBackend, error)

func connectPlayerSession(ctx context.Context, logger forkspotify.Logger, configDir string) (playerSession, error) {
	hadStoredCredentials := hasStoredPlayerCredentials(configDir)
	connect := func() (playerSession, error) {
		sess, appState, err := sessionconfig.NewSessionFromConfigDir(ctx, logger, sessionconfig.Options{
			ConfigDir:    configDir,
			CallbackPort: 8080,
			DeviceType:   "computer",
		})
		if err != nil {
			return playerSession{}, fmt.Errorf("could not sign in to Spotify: %w\ncheck your network or a Spotify outage (status.spotify.com)", err)
		}
		return playerSession{Session: sess, AppState: appState}, nil
	}
	connected, err := connect()
	if err == nil || !hadStoredCredentials || !isStoredCredentialRejection(err) {
		return connected, err
	}
	if clearErr := clearStoredPlayerCredentials(configDir); clearErr != nil {
		return playerSession{}, fmt.Errorf("stored player credentials were rejected and could not be cleared: %w (clear credentials: %v)", err, clearErr)
	}
	logger.Warnf("stored Spotify playback credentials were rejected; retrying sign-in")
	return connect()
}

func hasStoredPlayerCredentials(configDir string) bool {
	var state struct {
		Credentials struct {
			Username string `json:"username"`
			Data     []byte `json:"data"`
		} `json:"credentials"`
	}
	data, err := os.ReadFile(filepath.Join(configDir, "state.json"))
	if err == nil && json.Unmarshal(data, &state) == nil && state.Credentials.Username != "" && len(state.Credentials.Data) > 0 {
		return true
	}
	var legacy struct {
		Username string `json:"username"`
		Data     []byte `json:"data"`
	}
	data, err = os.ReadFile(filepath.Join(configDir, "credentials.json"))
	return err == nil && json.Unmarshal(data, &legacy) == nil && legacy.Username != "" && len(legacy.Data) > 0
}

func isStoredCredentialRejection(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToUpper(err.Error())
	return strings.Contains(message, "INVALID_CREDENTIALS") ||
		strings.Contains(message, "FAILED AUTHENTICATING ACCESSPOINT WITH STORED CREDENTIALS")
}

func clearStoredPlayerCredentials(configDir string) error {
	statePath := filepath.Join(configDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err == nil {
		var state map[string]json.RawMessage
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode player state: %w", err)
		}
		delete(state, "credentials")
		clean, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("encode player state: %w", err)
		}
		info, err := os.Stat(statePath)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(configDir, "state.json.*.tmp")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if err := tmp.Chmod(info.Mode().Perm()); err != nil {
			_ = tmp.Close()
			return err
		}
		if _, err := tmp.Write(append(clean, '\n')); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmpName, statePath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	legacyPath := filepath.Join(configDir, "credentials.json")
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func startPlayerSession(ctx context.Context, logger forkspotify.Logger, configDir string, connect playerSessionConnector, backend playerBackendConnector, started func(func()), deliver func(tea.Msg)) {
	go func() {
		sess, err := connect(ctx, logger, configDir)
		if err != nil {
			deliver(tui.PlayerBackendFailed(err))
			return
		}
		attachment, err := backend(ctx, sess)
		if err != nil {
			sess.Close()
			deliver(tui.PlayerBackendFailed(err))
			return
		}
		if attachment.Cleanup != nil && started != nil {
			started(attachment.Cleanup)
		}
		deliver(tui.PlayerBackendReady(attachment.Catalog))
	}()
}

func attachPlayerBackend(ctx context.Context, logger forkspotify.Logger, cfg *librespot.Config, playbackStateCh chan<- *librespot.PlaybackStateUpdate, tuiCmdCh <-chan librespot.TUICommand, sess playerSession, baseCatalog spotify.PlaylistCatalog) (playerBackend, error) {
	runtime, err := librespot.NewRuntime(cfg, sess.AppState, logger, playbackStateCh)
	if err != nil {
		return playerBackend{}, err
	}
	appPlayer, err := librespot.NewAppPlayer(ctx, runtime, sess.Session)
	if err != nil {
		return playerBackend{}, err
	}
	go appPlayer.Run(ctx, tuiCmdCh)

	var upgrade spotify.PlaylistCatalog
	if baseCatalog == nil {
		upgrade = librespot.NewPlaylistCatalog(sess.Session)
	}
	return playerBackend{
		Cleanup: func() {
			appPlayer.Close()
			sess.Close()
		},
		Catalog: upgrade,
	}, nil
}

type backendSupervisor struct {
	mu      sync.Mutex
	stopped bool
	cleanup func()
}

func (s *backendSupervisor) register(cleanup func()) {
	if cleanup == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		cleanup()
		return
	}
	s.cleanup = cleanup
}

func (s *backendSupervisor) shutdown() {
	s.mu.Lock()
	cleanup := s.cleanup
	s.cleanup = nil
	s.stopped = true
	s.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}
