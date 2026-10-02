package main

import (
	"context"
	"fmt"
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
