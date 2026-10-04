package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/auth"
	"orpheus/internal/config"
)

// RunAuthLogin displays the standalone Spotify Web API sign-in modal. It does
// not start the player or the regular Orpheus application.
func RunAuthLogin(ctx context.Context, cfg config.Config) (bool, error) {
	if strings.TrimSpace(cfg.SpotifyClientID) == "" {
		setup := newModel(ctx, nil, cfg, nil, nil, nil)
		setup.ui.width, setup.ui.height = 80, 24
		setup.ui.authLoginOnly = true
		setup.ui.clientIDSetupOpen = true
		final, err := tea.NewProgram(setup).Run()
		if err != nil {
			return false, err
		}
		result, ok := final.(model)
		if !ok || !result.ui.clientIDComplete {
			return false, nil
		}
		cfg.SpotifyClientID = result.ui.config.SpotifyClientID
	}

	manager, err := auth.NewPKCEManager(cfg, auth.NewFileTokenStore(cfg.TokenPath))
	if err != nil {
		return false, err
	}
	session, err := manager.BeginAuth()
	if err != nil {
		return false, err
	}
	loginCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	login := newModel(loginCtx, nil, cfg, nil, nil, nil)
	login.ui.width, login.ui.height = 80, 24
	login.ui.authLoginOnly = true
	login.ui.authLoginURL = session.AuthURL
	login.ui.authLoginOpen = true
	program := tea.NewProgram(login)
	go func() {
		callbackCtx, callbackCancel := context.WithTimeout(loginCtx, 3*time.Minute)
		defer callbackCancel()
		code, callbackErr := auth.WaitForCallback(callbackCtx, cfg.RedirectURI, session.State)
		if callbackErr == nil {
			exchangeCtx, exchangeCancel := context.WithTimeout(loginCtx, 30*time.Second)
			token, exchangeErr := manager.ExchangeCode(exchangeCtx, code, session.Verifier)
			exchangeCancel()
			callbackErr = exchangeErr
			if callbackErr == nil {
				callbackErr = manager.SaveToken(token)
			}
		}
		program.Send(authLoginCompleteMsg{err: callbackErr})
	}()
	final, err := program.Run()
	if err != nil {
		return false, err
	}
	result, ok := final.(model)
	if !ok {
		return false, nil
	}
	if result.ui.authLoginSucceeded {
		return true, nil
	}
	if result.ui.authLoginFinished {
		if result.ui.authLoginError == "" {
			return false, errors.New("spotify sign-in failed")
		}
		return false, errors.New(result.ui.authLoginError)
	}
	return false, nil
}
