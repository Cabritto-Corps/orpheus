package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"orpheus/internal/auth"
	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
	"orpheus/internal/tui"

	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

const (
	tokenRefreshTimeout = 30 * time.Second
	oauthHTTPTimeout    = 20 * time.Second
)

func main() {
	if len(os.Args) < 2 || os.Args[1] == "librespot" {
		if err := runLibrespotTUI(); err != nil {
			slog.Error("tui failed", "error", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		slog.Error("startup configuration failed", "error", err)
		os.Exit(1)
	}
	authManager, err := auth.NewPKCEManager(cfg, auth.NewFileTokenStore(cfg.TokenPath))
	if err != nil {
		slog.Error("oauth setup failed", "error", err)
		os.Exit(1)
	}

	if len(os.Args) >= 3 && os.Args[1] == "auth" && os.Args[2] == "login" {
		runAuthLogin(authManager, cfg)
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "auth" && (len(os.Args) < 3 || os.Args[2] != "login") {
		slog.Error("unknown auth command", "usage", "orpheus auth login")
		os.Exit(1)
	}
	rootCtx, rootCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer rootCancel()
	oauthCtx := context.WithValue(rootCtx, oauth2.HTTPClient, oauthHTTPClient())

	token, err := ensureUsableTokenWithRetry(oauthCtx, authManager)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			session, sessionErr := authManager.BeginAuth()
			if sessionErr != nil {
				slog.Error("failed to create oauth session", "error", sessionErr)
				os.Exit(1)
			}
			slog.Error("oauth token missing; run login flow", "next_step", "orpheus auth login", "authorization_url", session.AuthURL)
			os.Exit(1)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Error("oauth token refresh timed out", "error", err, "timeout", tokenRefreshTimeout, "next_step", "check network/connectivity and rerun")
			os.Exit(1)
		}
		slog.Error("failed to refresh oauth token; re-auth may be required", "error", err, "next_step", "orpheus auth login")
		os.Exit(1)
	}

	if len(os.Args) >= 2 && os.Args[1] == "check" {
		runCheck(oauthCtx, authManager, token, cfg)
		return
	}
}

func oauthHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{Timeout: oauthHTTPTimeout}
	}
	clone := transport.Clone()
	clone.ResponseHeaderTimeout = oauthHTTPTimeout
	return &http.Client{
		Timeout:   oauthHTTPTimeout,
		Transport: clone,
	}
}

func ensureUsableTokenWithRetry(ctx context.Context, authManager *auth.Manager) (*oauth2.Token, error) {
	const maxAttempts = 2
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptTimeout := tokenRefreshTimeout + time.Duration(attempt)*15*time.Second
		tokenCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		token, err := authManager.EnsureUsableToken(tokenCtx)
		cancel()
		if err == nil {
			return token, nil
		}
		lastErr = err
		if !isRetryableTokenRefreshError(err) || attempt+1 >= maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func isRetryableTokenRefreshError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "tempor")
}

func runAuthLogin(authManager *auth.Manager, cfg config.Config) {
	session, err := authManager.BeginAuth()
	if err != nil {
		slog.Error("failed to create oauth session", "error", err)
		os.Exit(1)
	}

	slog.Info("open this URL to authenticate", "authorization_url", session.AuthURL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	code, err := auth.WaitForCallback(ctx, cfg.RedirectURI, session.State)
	if err != nil {
		slog.Error("oauth callback failed", "error", err)
		os.Exit(1)
	}

	exchangeCtx, exchangeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer exchangeCancel()
	token, err := authManager.ExchangeCode(exchangeCtx, code, session.Verifier)
	if err != nil {
		slog.Error("oauth code exchange failed", "error", err)
		os.Exit(1)
	}
	if err := authManager.SaveToken(token); err != nil {
		slog.Error("failed to persist oauth token", "error", err)
		os.Exit(1)
	}
	slog.Info("oauth login successful", "token_path", cfg.TokenPath)
}

func runCheck(ctx context.Context, authManager *auth.Manager, token *oauth2.Token, cfg config.Config) {
	checkCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	baseTokenSource := authManager.TokenSource(ctx, token)
	notifyingSource := auth.NewNotifyingTokenSourceWithInitial(
		baseTokenSource,
		func(newToken *oauth2.Token) {
			_ = authManager.SaveToken(newToken)
		},
		token.AccessToken,
	)
	spotifyClient := spotify.NewClient(checkCtx, notifyingSource)
	svc := spotify.NewService(spotifyClient, spotify.Options{
		Mode:                spotify.DeviceMode(cfg.DeviceResolutionMode),
		AllowActiveFallback: cfg.AllowActiveFallback,
		ItemsHTTPClient:     spotify.NewItemsHTTPClient(notifyingSource),
	})

	report := func(name string, err error) {
		if err != nil {
			status, ok := spotify.HTTPStatusFromError(err)
			if ok {
				fmt.Fprintf(os.Stderr, "  %s: FAIL HTTP %d — %s\n", name, status, err.Error())
			} else {
				fmt.Fprintf(os.Stderr, "  %s: FAIL — %s\n", name, err.Error())
			}
			return
		}
		fmt.Fprintf(os.Stderr, "  %s: ok\n", name)
	}

	fmt.Fprintf(os.Stderr, "check: auth token loaded, scopes include playlist-read-private and playlist-read-collaborative\n")
	fmt.Fprintf(os.Stderr, "check: running API requests (same order as TUI)...\n\n")

	userID, err := svc.CurrentUserID(checkCtx)
	report("CurrentUser", err)
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "  current user id: %s\n\n", userID)

	playlistPage, err := svc.ListUserPlaylistsPage(checkCtx, 0, 5)
	report("CurrentUsersPlaylists(limit=5)", err)
	if err != nil {
		return
	}
	if len(playlistPage.Items) == 0 {
		fmt.Fprintf(os.Stderr, "  no playlists to test; create or follow a playlist and run check again\n")
		return
	}
	firstPl := playlistPage.Items[0]
	fmt.Fprintf(os.Stderr, "  playlists: %d (testing first: %q id=%s owner=%s)\n\n", len(playlistPage.Items), firstPl.Name, firstPl.ID, firstPl.OwnerID)

	tracksPage, err := svc.ListPlaylistTrackIDsPage(checkCtx, firstPl.ID, 0, 1)
	report("GetPlaylistItems(playlist, limit=1)", err)
	if err != nil {
		code, _ := spotify.HTTPStatusFromError(err)
		if code == 403 {
			if firstPl.OwnerID == userID {
				fmt.Fprintf(os.Stderr, "  (403 on owned playlist: token may lack playlist-read-private scope — run orpheus auth login to re-grant)\n")
			} else {
				fmt.Fprintf(os.Stderr, "  (403 = not owner/collaborator for this playlist; playback still works, TUI treats this as optional)\n")
			}
		}
		fmt.Fprintf(os.Stderr, "\n")
	} else {
		fmt.Fprintf(os.Stderr, "  track count first page: %d\n\n", len(tracksPage.TrackIDs))
	}

	status, err := svc.Status(checkCtx)
	report("PlayerState (Status)", err)
	if err != nil {
		if errors.Is(err, spotify.ErrNoActiveTrack) {
			fmt.Fprintf(os.Stderr, "  (no active track is normal if nothing is playing)\n")
		}
		fmt.Fprintf(os.Stderr, "\n")
	} else if status != nil {
		fmt.Fprintf(os.Stderr, "  device: %s playing: %t\n\n", status.DeviceName, status.Playing)
	}

	queue, err := svc.GetQueue(checkCtx)
	report("GetQueue", err)
	if err != nil {
		statusCode, _ := spotify.HTTPStatusFromError(err)
		if statusCode == 403 {
			fmt.Fprintf(os.Stderr, "  (403 = queue not available for this device/account; optional in TUI)\n")
		}
		fmt.Fprintf(os.Stderr, "\n")
	} else {
		fmt.Fprintf(os.Stderr, "  queue length: %d\n\n", len(queue))
	}

	fmt.Fprintf(os.Stderr, "check: done\n")
}

func runLibrespotTUI() error {
	configDir := os.Getenv("ORPHEUS_CONFIG_DIR")
	if configDir == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("config dir: %w", err)
		}
		configDir = filepath.Join(dir, "orpheus")
	}

	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("config dir: %w", err)
	}
	logPath := os.Getenv("ORPHEUS_LOG_FILE")
	if logPath == "" {
		logPath = filepath.Join(configDir, "orpheus.log")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Sync()
	defer logFile.Close()
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log := logrus.New()
	log.SetLevel(logrus.InfoLevel)
	log.SetOutput(logFile)
	log.SetFormatter(&logrus.TextFormatter{DisableColors: true})
	logger := &librespot.LogrusAdapter{Log: logrus.NewEntry(log)}
	log.SetReportCaller(false)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	sess, appState, err := librespot.NewSession(ctx, logger, librespot.SessionOptions{
		ConfigDir:    configDir,
		CallbackPort: 8080,
		DeviceType:   "computer",
	})
	if err != nil {
		return err
	}
	defer sess.Close()

	librespotCfg := librespot.DefaultConfig()
	librespotCfg.DeviceName = "orpheus"

	playbackStateCh := make(chan *librespot.PlaybackStateUpdate, 32)
	runtime, err := librespot.NewRuntime(librespotCfg, appState, logger, nil, playbackStateCh)
	if err != nil {
		return err
	}

	appPlayer, err := librespot.NewAppPlayer(ctx, runtime, sess)
	if err != nil {
		return err
	}
	defer appPlayer.Close()

	tuiCmdCh := make(chan librespot.TUICommand, 8)
	go appPlayer.Run(ctx, tuiCmdCh)

	tuiCfg := config.Config{
		DeviceName:   librespotCfg.DeviceName,
		PollInterval: 2 * time.Second,
		NerdFonts:    false,
	}

	var catalog spotify.PlaylistCatalog
	if cfg, cfgErr := config.LoadFromEnv(); cfgErr == nil {
		if authMgr, authErr := auth.NewPKCEManager(cfg, auth.NewFileTokenStore(cfg.TokenPath)); authErr == nil {
			if token, tokenErr := authMgr.LoadToken(); tokenErr == nil && token != nil {
				oauthCtx := context.WithValue(ctx, oauth2.HTTPClient, oauthHTTPClient())
				ts := authMgr.TokenSource(oauthCtx, token)
				spotifyClient := spotify.NewClient(oauthCtx, ts)
				catalog = spotify.NewService(spotifyClient, spotify.Options{})
			}
		}
	}
	if catalog == nil {
		catalog = librespot.NewPlaylistCatalog(sess)
	}
	err = tui.Run(ctx, catalog, nil, tuiCfg, tuiCmdCh, playbackStateCh)
	cancel()
	if err != nil {
		return err
	}
	return nil
}
