package tui

import (
	"context"
	"sync"
	"time"
)

type LoadType int

const (
	LoadTypeImage LoadType = iota
)

type LoadItem struct {
	URL string
}

type LoadResult struct {
	Index int
	Data  interface{}
	Error error
}

type ImageData struct {
	Data []byte
}

type LoadRequest struct {
	Type    LoadType
	Items   []LoadItem
	Timeout time.Duration
}

type BackgroundLoader struct {
	ctx      context.Context
	pool     chan struct{}
	mu       sync.Mutex
	inflight map[string]struct{}
}

func NewBackgroundLoader(ctx context.Context) *BackgroundLoader {
	return &BackgroundLoader{
		ctx:      ctx,
		pool:     make(chan struct{}, 64),
		inflight: make(map[string]struct{}),
	}
}

func (l *BackgroundLoader) BeginLoad(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" {
		return false
	}
	if _, ok := l.inflight[id]; ok {
		return false
	}
	l.inflight[id] = struct{}{}
	return true
}

func (l *BackgroundLoader) FinishLoad(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.inflight, id)
}

func (l *BackgroundLoader) Execute(req LoadRequest) []LoadResult {
	select {
	case <-l.ctx.Done():
		return nil
	case l.pool <- struct{}{}:
		defer func() { <-l.pool }()
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	switch req.Type {
	case LoadTypeImage:
		return l.loadImages(req.Items, timeout)
	}
	return nil
}

func (l *BackgroundLoader) loadImages(items []LoadItem, timeout time.Duration) []LoadResult {
	results := make([]LoadResult, len(items))
	for i, item := range items {
		select {
		case <-l.ctx.Done():
			results[i] = LoadResult{Index: i, Error: l.ctx.Err()}
			return results
		default:
		}
		ctx, cancel := context.WithTimeout(l.ctx, timeout)
		data, err := httpImageProvider{}.Fetch(ctx, item.URL)
		cancel()
		if err != nil {
			results[i] = LoadResult{Index: i, Error: err}
		} else {
			results[i] = LoadResult{Index: i, Data: ImageData{Data: data}}
		}
	}
	return results
}
