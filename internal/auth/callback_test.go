package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestWaitForCallbackIgnoresInvalidAndIncompleteRequests(t *testing.T) {
	port := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan struct {
		code string
		err  error
	}, 1)
	go func() {
		code, err := WaitForCallback(ctx, fmt.Sprintf("http://127.0.0.1:%d/callback", port), "expected-state")
		result <- struct {
			code string
			err  error
		}{code, err}
	}()

	client := &http.Client{Timeout: 250 * time.Millisecond}
	doRequest := func(query string) int {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?%s", port, query))
			if err == nil {
				_ = resp.Body.Close()
				return resp.StatusCode
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("callback server did not start")
		return 0
	}

	if status := doRequest("state=wrong&code=attacker"); status != http.StatusBadRequest {
		t.Fatalf("wrong state status = %d, want 400", status)
	}
	if status := doRequest("state=expected-state"); status != http.StatusBadRequest {
		t.Fatalf("missing code status = %d, want 400", status)
	}
	if status := doRequest("state=expected-state&code=valid-code"); status != http.StatusOK {
		t.Fatalf("valid callback status = %d, want 200", status)
	}

	select {
	case got := <-result:
		if got.err != nil || got.code != "valid-code" {
			t.Fatalf("callback result = (%q, %v), want (valid-code, nil)", got.code, got.err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for valid callback")
	}
}

func TestWaitForCallbackReturnsProviderDenial(t *testing.T) {
	port := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := WaitForCallback(ctx, fmt.Sprintf("http://127.0.0.1:%d/callback", port), "expected-state")
		result <- err
	}()

	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(time.Second)
	var status int
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?state=expected-state&error=access_denied", port))
		if err == nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != http.StatusBadRequest {
		t.Fatalf("provider denial status = %d, want 400", status)
	}
	select {
	case err := <-result:
		if err == nil || err.Error() != "spotify authorization failed: access_denied" {
			t.Fatalf("provider denial error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for provider denial")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
