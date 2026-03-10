package auth

import (
	"sync"

	"golang.org/x/oauth2"
)

type TokenChangeFunc func(newToken *oauth2.Token)

type NotifyingTokenSource struct {
	inner     oauth2.TokenSource
	onChange  TokenChangeFunc
	mu        sync.Mutex
	lastToken string
}

func NewNotifyingTokenSourceWithInitial(src oauth2.TokenSource, fn TokenChangeFunc, initialAccessToken string) *NotifyingTokenSource {
	return &NotifyingTokenSource{
		inner:     src,
		onChange:  fn,
		lastToken: initialAccessToken,
	}
}

func (n *NotifyingTokenSource) Token() (*oauth2.Token, error) {
	t, err := n.inner.Token()
	if err != nil {
		return nil, err
	}

	n.mu.Lock()
	changed := t.AccessToken != n.lastToken
	if changed {
		n.lastToken = t.AccessToken
	}
	n.mu.Unlock()

	if changed && n.onChange != nil {
		n.onChange(t)
	}

	return t, nil
}

var _ oauth2.TokenSource = (*NotifyingTokenSource)(nil)
