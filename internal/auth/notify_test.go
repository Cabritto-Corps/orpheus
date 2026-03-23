package auth

import (
	"fmt"
	"testing"

	"golang.org/x/oauth2"
)

type mockTokenSource struct {
	token *oauth2.Token
	err   error
}

func (m *mockTokenSource) Token() (*oauth2.Token, error) {
	return m.token, m.err
}

func TestNotifyingCallsOnChange(t *testing.T) {
	called := false
	var got *oauth2.Token

	src := &mockTokenSource{token: &oauth2.Token{AccessToken: "new-token"}}
	nts := NewNotifyingTokenSourceWithInitial(src, func(tk *oauth2.Token) {
		called = true
		got = tk
	}, "old-token")

	result, err := nts.Token()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected onChange to be called")
	}
	if got.AccessToken != "new-token" {
		t.Fatalf("expected new-token, got %s", got.AccessToken)
	}
	if result.AccessToken != "new-token" {
		t.Fatalf("expected new-token, got %s", result.AccessToken)
	}
}

func TestNotifyingSkipsWhenUnchanged(t *testing.T) {
	called := false

	src := &mockTokenSource{token: &oauth2.Token{AccessToken: "same"}}
	nts := NewNotifyingTokenSourceWithInitial(src, func(tk *oauth2.Token) {
		called = true
	}, "same")

	_, err := nts.Token()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("expected onChange NOT to be called when token unchanged")
	}
}

func TestNotifyingNilOnChange(t *testing.T) {
	src := &mockTokenSource{token: &oauth2.Token{AccessToken: "new"}}
	nts := NewNotifyingTokenSourceWithInitial(src, nil, "old")

	_, err := nts.Token()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNotifyingPropagatesError(t *testing.T) {
	src := &mockTokenSource{err: fmt.Errorf("token source failed")}
	nts := NewNotifyingTokenSourceWithInitial(src, nil, "")

	_, err := nts.Token()
	if err == nil {
		t.Fatal("expected error from inner token source")
	}
}
