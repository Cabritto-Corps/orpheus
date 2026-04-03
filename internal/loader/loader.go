package loader

import (
	"context"
	"sync"
	"time"
)

type LoadType int

const (
	LoadTypeImage LoadType = iota
	LoadTypeContextImageURL
)

type LoadItem struct {
	URL  string
	Kind string
	ID   string
}

type LoadResult struct {
	Index int
	Data  interface{}
	Error error
}

type ImageData struct {
	Data []byte
}

type ImageURLData struct {
	URL string
}

type TrackMetadataData struct {
	ID         string
	Name       string
	Artist     string
	DurationMS int
}

type LoadRequest struct {
	Type    LoadType
	Items   []LoadItem
	Timeout time.Duration
}

type Executor func(ctx context.Context, req LoadRequest) []LoadResult

type BackgroundLoader struct {
	ctx      context.Context
	pool     chan struct{}
	mu       sync.Mutex
	inflight map[string]struct{}
	executor Executor
}

func New(ctx context.Context, poolSize int, exec Executor) *BackgroundLoader {
	return &BackgroundLoader{
		ctx:      ctx,
		pool:     make(chan struct{}, poolSize),
		inflight: make(map[string]struct{}),
		executor: exec,
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
	req.Timeout = timeout

	if l.executor == nil {
		return nil
	}
	return l.executor(l.ctx, req)
}
