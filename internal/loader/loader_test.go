package loader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	ctx := context.Background()
	loader := New(ctx, 64, nil)
	if loader == nil {
		t.Fatal("expected non-nil loader")
	}
	if cap(loader.pool) != 64 {
		t.Fatalf("expected pool size 64, got %d", cap(loader.pool))
	}
}

func TestBeginLoadDedup(t *testing.T) {
	ctx := context.Background()
	loader := New(ctx, 64, nil)

	if !loader.BeginLoad("url1") {
		t.Fatal("expected first begin to succeed")
	}
	if loader.BeginLoad("url1") {
		t.Fatal("expected duplicate begin to fail")
	}

	loader.FinishLoad("url1")
	if !loader.BeginLoad("url1") {
		t.Fatal("expected begin after finish to succeed")
	}
}

func TestBeginLoadEmptyID(t *testing.T) {
	ctx := context.Background()
	loader := New(ctx, 64, nil)

	if loader.BeginLoad("") {
		t.Fatal("expected empty ID to fail")
	}
}

func TestFinishLoadNonExistent(t *testing.T) {
	ctx := context.Background()
	loader := New(ctx, 64, nil)

	loader.FinishLoad("nonexistent")
	if !loader.BeginLoad("nonexistent") {
		t.Fatal("expected begin to succeed after finish on non-existent")
	}
}

func TestExecuteWithNilExecutor(t *testing.T) {
	ctx := context.Background()
	loader := New(ctx, 64, nil)

	results := loader.Execute(LoadRequest{
		Type:    LoadTypeImage,
		Items:   []LoadItem{{URL: "http://example.com"}},
		Timeout: 2 * time.Second,
	})
	if results != nil {
		t.Fatalf("expected nil results with nil executor, got %d results", len(results))
	}
}

func TestExecuteWithExecutor(t *testing.T) {
	ctx := context.Background()
	exec := func(ctx context.Context, req LoadRequest) []LoadResult {
		results := make([]LoadResult, len(req.Items))
		for i, item := range req.Items {
			results[i] = LoadResult{Index: i, Data: item.URL}
		}
		return results
	}
	loader := New(ctx, 64, exec)

	results := loader.Execute(LoadRequest{
		Type:    LoadTypeImage,
		Items:   []LoadItem{{URL: "http://example.com/1"}, {URL: "http://example.com/2"}},
		Timeout: 5 * time.Second,
	})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Data != "http://example.com/1" {
		t.Fatalf("expected first URL, got %v", results[0].Data)
	}
}

func TestExecuteCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	loader := New(ctx, 64, nil)

	results := loader.Execute(LoadRequest{
		Type:    LoadTypeImage,
		Items:   []LoadItem{{URL: "http://example.com"}},
		Timeout: 5 * time.Second,
	})
	if results != nil {
		t.Fatalf("expected nil results for cancelled context, got %d results", len(results))
	}
}

func TestExecuteDefaultTimeout(t *testing.T) {
	ctx := context.Background()
	var gotTimeout time.Duration
	exec := func(ctx context.Context, req LoadRequest) []LoadResult {
		gotTimeout = req.Timeout
		return nil
	}
	loader := New(ctx, 64, exec)

	loader.Execute(LoadRequest{
		Type:  LoadTypeImage,
		Items: []LoadItem{{URL: "http://example.com"}},
	})
	if gotTimeout != 10*time.Second {
		t.Fatalf("expected default timeout 10s, got %v", gotTimeout)
	}
}

func TestPoolBlocking(t *testing.T) {
	ctx := context.Background()
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	done := make(chan struct{})

	exec := func(ctx context.Context, req LoadRequest) []LoadResult {
		c := concurrent.Add(1)
		defer concurrent.Add(-1)
		if c > maxConcurrent.Load() {
			maxConcurrent.Store(c)
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}

	loader := New(ctx, 8, exec)

	for i := 0; i < 12; i++ {
		go func() {
			loader.Execute(LoadRequest{
				Type:    LoadTypeImage,
				Items:   []LoadItem{{URL: "http://example.com"}},
				Timeout: 2 * time.Second,
			})
			<-done
		}()
	}

	time.Sleep(200 * time.Millisecond)
	mc := maxConcurrent.Load()
	if mc > 8 {
		t.Fatalf("expected max concurrent <= 8, got %d", mc)
	}
	close(done)
}

func TestConcurrentBeginFinishLoad(t *testing.T) {
	ctx := context.Background()
	loader := New(ctx, 64, nil)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := "key"
			if loader.BeginLoad(key) {
				loader.FinishLoad(key)
			}
		}(i)
	}
	wg.Wait()
}

func TestExecuteHappyPathWithHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test-data"))
	}))
	defer server.Close()

	ctx := context.Background()
	exec := func(ctx context.Context, req LoadRequest) []LoadResult {
		results := make([]LoadResult, len(req.Items))
		for i, item := range req.Items {
			resp, err := http.Get(item.URL)
			if err != nil {
				results[i] = LoadResult{Index: i, Error: err}
				continue
			}
			buf := make([]byte, 1024)
			n, _ := resp.Body.Read(buf)
			_ = resp.Body.Close()
			results[i] = LoadResult{Index: i, Data: string(buf[:n])}
		}
		return results
	}
	loader := New(ctx, 64, exec)

	results := loader.Execute(LoadRequest{
		Type:    LoadTypeImage,
		Items:   []LoadItem{{URL: server.URL}},
		Timeout: 5 * time.Second,
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("expected no error, got %v", results[0].Error)
	}
	if results[0].Data != "test-data" {
		t.Fatalf("expected 'test-data', got %q", results[0].Data)
	}
}
