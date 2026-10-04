package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
)

func TestAuthLoginRequiredOpensModalWithoutRenderingURL(t *testing.T) {
	const loginURL = "https://accounts.spotify.com/authorize?client_id=private&state=private"
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.width = 100
	m.ui.height = 30

	next, _ := m.Update(AuthLoginRequired(loginURL))
	got := next.(model)
	if !got.ui.authLoginOpen || got.ui.authLoginURL != loginURL {
		t.Fatalf("auth dialog state not set: open=%v url=%q", got.ui.authLoginOpen, got.ui.authLoginURL)
	}
	view := got.View().Content
	for _, want := range []string{"Spotify playback sign-in required", "separate from the Web API login", "Authorize Orpheus playback", "Open login page", "Copy login link"} {
		if !strings.Contains(view, want) {
			t.Errorf("auth dialog must show %q", want)
		}
	}
	if strings.Contains(view, loginURL) || strings.Contains(view, "client_id=private") {
		t.Fatal("authorization URL must stay hidden from the TUI")
	}
}

func TestAuthLoginDialogCanBeDismissedAndReopened(t *testing.T) {
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.authLoginURL = "https://accounts.spotify.com/authorize?state=private"
	m.ui.authLoginOpen = true

	next, _ := m.handleAuthLoginKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := next.(model)
	if got.ui.authLoginOpen || got.ui.authLoginURL == "" {
		t.Fatal("closing dialog should hide it but keep the pending login URL")
	}

	next, _ = got.handleKey(tea.KeyPressMsg{Code: 'l', Text: "l"})
	got = next.(model)
	if !got.ui.authLoginOpen {
		t.Fatal("pressing L should reopen the pending login dialog")
	}
}

func TestClientIDOnboardingAppearsWhenNotConfigured(t *testing.T) {
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.width, m.ui.height = 100, 30
	m.ui.clientIDSetupOpen = true
	if m.modalKind() != modalClientIDSetup {
		t.Fatal("expected Client ID onboarding on first run")
	}
	view := m.View().Content
	for _, want := range []string{"Spotify Web API setup", "Redirect URI", "Web API", "Web Playback SDK", "User Management", "Spotify account email", "setup guide", "Save Client ID"} {
		if !strings.Contains(view, want) {
			t.Errorf("onboarding must show %q", want)
		}
	}
}

func TestClientIDGuideShortcutWorksBeforeEditing(t *testing.T) {
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.clientIDSetupOpen = true
	m.ui.clientIDInput = ""
	next, cmd := m.handleClientIDSetupKey(tea.KeyPressMsg{Code: 't', Text: "t"})
	got := next.(model)
	if cmd == nil {
		t.Fatal("expected tutorial shortcut to launch the guide")
	}
	if got.ui.clientIDEditing || got.ui.clientIDInput != "" {
		t.Fatal("guide shortcut must not enter or modify the Client ID field")
	}
}

func TestClientIDInputPromptChangesWhenEditingStarts(t *testing.T) {
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.width, m.ui.height = 100, 30
	m.ui.clientIDSetupOpen = true
	next, _ := m.handleClientIDSetupKey(tea.KeyPressMsg{Code: 'i', Text: "i"})
	got := next.(model)
	if !got.ui.clientIDEditing || !strings.Contains(got.clientIDSetupModalView(), "Type or paste your Client ID here") {
		t.Fatal("editing mode should prompt the user to type or paste their Client ID")
	}
}

func TestClientIDSavedShowsSignInContinuation(t *testing.T) {
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.width, m.ui.height = 100, 30
	m.ui.clientIDSetupOpen = true
	m.ui.clientIDInput = "client-id"
	next, _ := m.Update(clientIDSavedMsg{})
	got := next.(model)
	if !got.ui.clientIDComplete || got.ui.config.SpotifyClientID != "client-id" {
		t.Fatal("expected onboarding to complete after save")
	}
	if !strings.Contains(got.View().Content, "Continuing to Spotify sign-in") {
		t.Fatal("expected sign-in continuation guidance after saving Client ID")
	}
}

func TestClientIDOnboardingAcceptsTerminalPaste(t *testing.T) {
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.clientIDSetupOpen = true
	m.ui.clientIDEditing = true
	next, _ := m.Update(tea.PasteMsg{Content: "  abcd-1234\r\n"})
	got := next.(model)
	if got.ui.clientIDInput != "abcd-1234" {
		t.Fatalf("expected cleaned pasted Client ID, got %q", got.ui.clientIDInput)
	}
}

func TestStandaloneAuthLoginSuccessRequestsExit(t *testing.T) {
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.authLoginOnly = true
	next, cmd := m.Update(authLoginCompleteMsg{})
	got := next.(model)
	if !got.ui.authLoginSucceeded || cmd == nil {
		t.Fatal("successful standalone login should finish and close its TUI")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("successful standalone login should quit the modal program")
	}
}

func TestStandaloneAuthLoginFailureRequestsExitAndPreservesError(t *testing.T) {
	m := newModel(context.Background(), nil, config.Config{}, nil, nil, nil)
	m.ui.authLoginOnly = true
	wantErr := errors.New("authorization denied")
	next, cmd := m.Update(authLoginCompleteMsg{err: wantErr})
	got := next.(model)
	if got.ui.authLoginSucceeded || got.ui.authLoginError != wantErr.Error() {
		t.Fatalf("failure state not preserved: succeeded=%v err=%q", got.ui.authLoginSucceeded, got.ui.authLoginError)
	}
	if cmd == nil {
		t.Fatal("failed standalone login should close so the error reaches the CLI")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("failed standalone login should quit the modal program")
	}
}
