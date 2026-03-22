package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"golang.org/x/oauth2"

	"orpheus/internal/config"
)

var spotifyEndpoint = oauth2.Endpoint{
	AuthURL:  "https://accounts.spotify.com/authorize",
	TokenURL: "https://accounts.spotify.com/api/token",
}

type AuthorizationSession struct {
	State     string
	Verifier  string
	AuthURL   string
	CreatedAt time.Time
}

type TokenStore interface {
	Load() (*oauth2.Token, error)
	Save(*oauth2.Token) error
}

type Manager struct {
	config *oauth2.Config
	store  TokenStore
}

func NewPKCEManager(cfg config.Config, store TokenStore) (*Manager, error) {
	if _, err := url.ParseRequestURI(cfg.RedirectURI); err != nil {
		return nil, fmt.Errorf("invalid spotify_redirect_uri: %w", err)
	}

	return &Manager{
		config: &oauth2.Config{
			ClientID:    cfg.SpotifyClientID,
			RedirectURL: cfg.RedirectURI,
			Scopes:      cfg.Scopes,
			Endpoint:    spotifyEndpoint,
		},
		store: store,
	}, nil
}

func (m *Manager) BeginAuth() (*AuthorizationSession, error) {
	state, err := newState()
	if err != nil {
		return nil, fmt.Errorf("generate oauth state: %w", err)
	}
	verifier, err := newVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate pkce verifier: %w", err)
	}
	challenge := challengeForVerifier(verifier)

	return &AuthorizationSession{
		State:    state,
		Verifier: verifier,
		AuthURL: m.config.AuthCodeURL(
			state,
			oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
			oauth2.SetAuthURLParam("code_challenge", challenge),
		),
		CreatedAt: time.Now(),
	}, nil
}

func (m *Manager) ExchangeCode(ctx context.Context, code, verifier string) (*oauth2.Token, error) {
	token, err := m.config.Exchange(
		ctx,
		code,
		oauth2.SetAuthURLParam("code_verifier", verifier),
	)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}
	return token, nil
}

func (m *Manager) TokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	return m.config.TokenSource(ctx, token)
}

func (m *Manager) LoadToken() (*oauth2.Token, error) {
	return m.store.Load()
}

func (m *Manager) SaveToken(token *oauth2.Token) error {
	return m.store.Save(token)
}

func (m *Manager) EnsureUsableToken(ctx context.Context) (*oauth2.Token, error) {
	token, err := m.store.Load()
	if err != nil {
		return nil, err
	}

	refreshed, err := m.config.TokenSource(ctx, token).Token()
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	if refreshed == nil {
		return nil, errors.New("token source returned nil token")
	}
	if refreshed.AccessToken != token.AccessToken {
		if err := m.store.Save(refreshed); err != nil {
			return nil, fmt.Errorf("persist refreshed token: %w", err)
		}
	}
	return refreshed, nil
}
