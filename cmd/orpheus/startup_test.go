package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	forkspotify "github.com/elxgy/go-librespot"

	"orpheus/internal/tui"
)

func waitForChannel[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestStartPlayerSessionDoesNotBlockOnConnect(t *testing.T) {
	connectStarted := make(chan struct{})
	release := make(chan error, 1)
	connect := func(ctx context.Context, _ forkspotify.Logger, _ string) (playerSession, error) {
		close(connectStarted)
		select {
		case err := <-release:
			return playerSession{}, err
		case <-ctx.Done():
			return playerSession{}, ctx.Err()
		}
	}
	attach := func(context.Context, playerSession) (playerBackend, error) {
		t.Error("backend attach must wait for session connect")
		return playerBackend{}, errors.New("unexpected attach")
	}
	delivered := make(chan tea.Msg, 1)

	returned := make(chan struct{})
	go func() {
		startPlayerSession(context.Background(), nil, "", connect, attach, nil, func(msg tea.Msg) {
			delivered <- msg
		})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("starting the player session blocked on connect")
	}
	select {
	case <-connectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the session connector to run in the background")
	}

	connectErr := errors.New("login unavailable")
	release <- connectErr
	if got, want := waitForChannel(t, delivered, "session failure"), tui.PlayerBackendFailed(connectErr); !reflect.DeepEqual(got, want) {
		t.Fatalf("failure message = %#v, want %#v", got, want)
	}
}

func TestStartPlayerSessionDeliversSuccessAndCleanup(t *testing.T) {
	connect := func(_ context.Context, _ forkspotify.Logger, _ string) (playerSession, error) {
		return playerSession{}, nil
	}
	cleaned := atomic.Bool{}
	attach := func(context.Context, playerSession) (playerBackend, error) {
		return playerBackend{
			Cleanup: func() { cleaned.Store(true) },
		}, nil
	}
	registered := make(chan func(), 1)
	delivered := make(chan tea.Msg, 1)

	startPlayerSession(context.Background(), nil, "", connect, attach, func(cleanup func()) {
		registered <- cleanup
	}, func(msg tea.Msg) {
		delivered <- msg
	})

	cleanup := waitForChannel(t, registered, "backend cleanup")
	cleanup()
	if !cleaned.Load() {
		t.Fatal("expected the registered backend cleanup to run")
	}
	if got, want := waitForChannel(t, delivered, "backend ready"), tui.PlayerBackendReady(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("ready message = %#v, want %#v", got, want)
	}
}

func TestStartPlayerSessionClosesSessionOnAttachFailure(t *testing.T) {
	connect := func(_ context.Context, _ forkspotify.Logger, _ string) (playerSession, error) {
		return playerSession{}, nil
	}
	attachErr := errors.New("player unavailable")
	attach := func(context.Context, playerSession) (playerBackend, error) {
		return playerBackend{}, attachErr
	}
	registered := make(chan func(), 1)
	delivered := make(chan tea.Msg, 1)

	startPlayerSession(context.Background(), nil, "", connect, attach, func(func()) {
		registered <- func() {}
	}, func(msg tea.Msg) {
		delivered <- msg
	})

	if got, want := waitForChannel(t, delivered, "attach failure"), tui.PlayerBackendFailed(attachErr); !reflect.DeepEqual(got, want) {
		t.Fatalf("failure message = %#v, want %#v", got, want)
	}
	select {
	case <-registered:
		t.Fatal("a failed backend attach must not register cleanup")
	default:
	}
}

func TestBackendSupervisorShutdownCleansUpOnce(t *testing.T) {
	s := &backendSupervisor{}
	var calls atomic.Int32
	s.register(func() { calls.Add(1) })
	s.register(func() { calls.Add(10) })
	s.shutdown()
	s.shutdown()
	if calls.Load() != 10 {
		t.Fatalf("expected only the latest cleanup once, got %d calls", calls.Load())
	}
	s.register(func() { calls.Add(100) })
	if calls.Load() != 110 {
		t.Fatalf("expected post-shutdown cleanup to run immediately, got %d", calls.Load())
	}
}

func TestRejectedStoredCredentialsAreClearedWithoutLosingState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	state := `{"device_id":"keep-device","last_volume":42,"credentials":{"username":"old-user","data":"AQID"}}`
	if err := os.WriteFile(statePath, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(legacyPath, []byte(`{"username":"old-user","data":"AQID"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasStoredPlayerCredentials(dir) {
		t.Fatal("expected stored player credentials to be detected")
	}
	if err := clearStoredPlayerCredentials(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["credentials"]; ok {
		t.Fatal("credentials should be removed from state")
	}
	var deviceID string
	if err := json.Unmarshal(got["device_id"], &deviceID); err != nil || deviceID != "keep-device" {
		t.Fatalf("device ID was not preserved: %q, err=%v", deviceID, err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy credentials should be removed, stat err=%v", err)
	}
	if hasStoredPlayerCredentials(dir) {
		t.Fatal("cleared player credentials should no longer be detected")
	}
}

func TestIsStoredCredentialRejection(t *testing.T) {
	if !isStoredCredentialRejection(errors.New("failed authenticating with login5: INVALID_CREDENTIALS")) {
		t.Fatal("expected invalid credentials to trigger reauthentication")
	}
	if isStoredCredentialRejection(errors.New("connection refused")) {
		t.Fatal("network errors should not trigger credential deletion")
	}
}

func TestRequiresSpotifyReauthOnlyForRejectedRefreshTokens(t *testing.T) {
	for _, message := range []string{
		"oauth2: cannot fetch token: 400 Bad Request: invalid_grant",
		"token expired and refresh token is not set",
		"invalid_token",
	} {
		if !requiresSpotifyReauth(errors.New(message)) {
			t.Errorf("expected reauthorization for %q", message)
		}
	}
	for _, message := range []string{"connection refused", "request timed out", "429 rate limit"} {
		if requiresSpotifyReauth(errors.New(message)) {
			t.Errorf("transient failure %q must not trigger a new login", message)
		}
	}
}
