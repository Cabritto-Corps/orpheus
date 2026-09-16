package tui

import (
	"context"
	"sync"
	"time"

	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

func NewTUIExecutor(ctx context.Context, catalog spotify.PlaylistCatalog) loader.Executor {
	if catalog == nil {
		return nil
	}
	return func(ctx context.Context, req loader.LoadRequest) []loader.LoadResult {
		switch req.Type {
		case loader.LoadTypeImage:
			return loadImages(ctx, req.Items, req.Timeout, httpImageProvider{}.Fetch)
		case loader.LoadTypeContextImageURL:
			return resolveContextImageURLs(ctx, catalog, req.Items, req.Timeout)
		}
		return nil
	}
}

func loadImages(ctx context.Context, items []loader.LoadItem, timeout time.Duration, fetch func(context.Context, string) ([]byte, error)) []loader.LoadResult {
	results := make([]loader.LoadResult, len(items))
	for i := range results {
		results[i].Index = i
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(idx int, url string) {
			defer wg.Done()
			// Blocking slot wait: holders release within one per-item
			// timeout, so the queue can only drain, never starve.
			sem <- struct{}{}
			defer func() { <-sem }()
			// Per-item deadline starts at slot acquisition: a slow queue
			// head must not cancel its siblings.
			ictx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			data, err := fetch(ictx, url)
			if err != nil {
				results[idx] = loader.LoadResult{Index: idx, Error: err}
			} else {
				results[idx] = loader.LoadResult{Index: idx, Data: loader.ImageData{Data: data}}
			}
		}(i, item.URL)
	}
	wg.Wait()
	return results
}

func resolveContextImageURLs(ctx context.Context, catalog spotify.PlaylistCatalog, items []loader.LoadItem, timeout time.Duration) []loader.LoadResult {
	results := make([]loader.LoadResult, len(items))
	for i := range results {
		results[i].Index = i
	}
	fctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var wg sync.WaitGroup
	for i, item := range items {
		select {
		case <-fctx.Done():
			results[i] = loader.LoadResult{Index: i, Error: fctx.Err()}
			continue
		default:
		}
		wg.Add(1)
		go func(idx int, kind, id string) {
			defer wg.Done()
			url, err := catalog.ResolveContextImageURL(fctx, kind, id)
			if err != nil {
				results[idx] = loader.LoadResult{Index: idx, Error: err}
			} else {
				results[idx] = loader.LoadResult{Index: idx, Data: loader.ImageURLData{URL: url}}
			}
		}(i, item.Kind, item.ID)
	}
	wg.Wait()
	return results
}
