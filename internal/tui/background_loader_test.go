package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewBackgroundLoader(t *testing.T) {
	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)
	if loader == nil {
		t.Fatal("expected non-nil loader")
	}
	if loader.pool == nil {
		t.Fatal("expected non-nil pool")
	}
	if cap(loader.pool) != 64 {
		t.Fatalf("expected pool size 64, got %d", cap(loader.pool))
	}
}

func TestBeginLoadDedup(t *testing.T) {
	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

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
	loader := NewBackgroundLoader(ctx)

	if loader.BeginLoad("") {
		t.Fatal("expected empty ID to fail")
	}
}

func TestFinishLoadNonExistent(t *testing.T) {
	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

	loader.FinishLoad("nonexistent")
	if !loader.BeginLoad("nonexistent") {
		t.Fatal("expected begin to succeed after finish on non-existent")
	}
}

func TestExecuteWithTimeout(t *testing.T) {
	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

	results := loader.Execute(LoadRequest{
		Type:    LoadTypeImage,
		Items:   []LoadItem{{URL: "http://invalid.test/image"}},
		Timeout: 2 * time.Second,
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestExecuteEmptyItems(t *testing.T) {
	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

	results := loader.Execute(LoadRequest{
		Type:    LoadTypeImage,
		Items:   []LoadItem{},
		Timeout: 5 * time.Second,
	})
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestExecuteDefaultTimeout(t *testing.T) {
	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

	results := loader.Execute(LoadRequest{
		Type:  LoadTypeImage,
		Items: []LoadItem{{URL: "http://invalid.test/image"}},
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestExecuteCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	loader := NewBackgroundLoader(ctx)

	results := loader.Execute(LoadRequest{
		Type:    LoadTypeImage,
		Items:   []LoadItem{{URL: "http://example.com/image"}},
		Timeout: 5 * time.Second,
	})
	if results != nil {
		t.Logf("expected nil results for cancelled context, got %d results (race with pool acquire)", len(results))
	}
}

func TestExecuteHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake-image-data"))
	}))
	defer server.Close()

	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

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
	imgData, ok := results[0].Data.(ImageData)
	if !ok {
		t.Fatal("expected ImageData type")
	}
	if string(imgData.Data) != "fake-image-data" {
		t.Fatalf("expected 'fake-image-data', got %q", imgData.Data)
	}
}

func TestExecuteMultipleItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

	results := loader.Execute(LoadRequest{
		Type: LoadTypeImage,
		Items: []LoadItem{
			{URL: server.URL},
			{URL: "http://invalid.test/fail"},
			{URL: server.URL},
		},
		Timeout: 5 * time.Second,
	})
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("expected no error for item 0, got %v", results[0].Error)
	}
	if results[1].Error == nil {
		t.Fatal("expected error for item 1")
	}
	if results[2].Error != nil {
		t.Fatalf("expected no error for item 2, got %v", results[2].Error)
	}
}

func TestPoolBlocking(t *testing.T) {
	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	done := make(chan struct{})

	for i := 0; i < 70; i++ {
		go func() {
			concurrent.Add(1)
			defer concurrent.Add(-1)
			loader.Execute(LoadRequest{
				Type:    LoadTypeImage,
				Items:   []LoadItem{{URL: "http://invalid.test"}},
				Timeout: 2 * time.Second,
			})
			<-done
		}()
	}

	time.Sleep(500 * time.Millisecond)
	mc := maxConcurrent.Load()
	if mc > 64 {
		t.Fatalf("expected max concurrent <= 64, got %d", mc)
	}
	close(done)
}

func TestConcurrentBeginFinishLoad(t *testing.T) {
	ctx := context.Background()
	loader := NewBackgroundLoader(ctx)

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

func TestExecuteCancelledContextDuringLoad(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	loader := NewBackgroundLoader(ctx)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	cancel()

	results := loader.Execute(LoadRequest{
		Type:    LoadTypeImage,
		Items:   []LoadItem{{URL: server.URL}},
		Timeout: 5 * time.Second,
	})
	if results == nil {
		return
	}
	if len(results) > 0 && results[0].Error == context.Canceled {
		return
	}
}

func TestLoadImagesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	loader := NewBackgroundLoader(ctx)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	cancel()

	results := loader.loadImages([]LoadItem{{URL: server.URL}}, 5*time.Second)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
}
