package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

func WaitForCallback(ctx context.Context, redirectURI, expectedState string) (string, error) {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return "", fmt.Errorf("parse redirect URI: %w", err)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}

	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)
	var sendOnce sync.Once
	send := func(code string, err error) {
		sendOnce.Do(func() { resultCh <- callbackResult{code: code, err: err} })
	}

	mux := http.NewServeMux()
	mux.HandleFunc(parsed.Path, func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state != expectedState {
			http.Error(w, "invalid oauth state", http.StatusBadRequest)
			send("", errors.New("invalid oauth state"))
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			send("", errors.New("missing authorization code"))
			return
		}
		_, _ = w.Write([]byte("Authentication successful. You can close this tab."))
		send(code, nil)
	})

	server := &http.Server{
		Addr:    parsed.Host,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			send("", err)
		}
	}()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-resultCh:
		return result.code, result.err
	}
}
