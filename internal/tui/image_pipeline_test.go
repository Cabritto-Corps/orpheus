package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	list "github.com/charmbracelet/bubbles/list"

	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

func TestLoadLibraryCoversDrainsBatchSized(t *testing.T) {
	m := NewLoaderModel()
	playlistItems := make([]list.Item, 0, 30)
	for i := range 30 {
		playlistItems = append(playlistItems, playlistItem{summary: spotify.PlaylistSummary{
			ID:       fmt.Sprintf("p%d", i),
			Name:     fmt.Sprintf("pl%d", i),
			ImageURL: fmt.Sprintf("lib-u%d", i),
		}})
	}
	m.browse.playlistList.SetItems(playlistItems)

	m.loadLibraryCoversCmd(30)

	inflightCount := 0
	m.ui.imgs.mu.RLock()
	inflightCount = len(m.ui.imgs.inflight)
	queued := len(m.ui.cover.queue)
	m.ui.imgs.mu.RUnlock()

	if inflightCount != coverQueueDrainBatch {
		t.Fatalf("expected first library drain to cap at %d, got %d", coverQueueDrainBatch, inflightCount)
	}
	if queued != 30-coverQueueDrainBatch {
		t.Fatalf("expected %d URLs to remain queued for the chained drain, got %d", 30-coverQueueDrainBatch, queued)
	}
}

func TestLoadImagesPerItemDeadlineIsolatesSiblings(t *testing.T) {
	fetch := func(ctx context.Context, url string) ([]byte, error) {
		if strings.HasPrefix(url, "slow") {
			select {
			case <-time.After(600 * time.Millisecond):
				return []byte{1}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		select {
		case <-time.After(20 * time.Millisecond):
			return []byte{2}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	items := make([]loader.LoadItem, 12)
	for i := range items {
		if i < 8 {
			items[i] = loader.LoadItem{URL: fmt.Sprintf("slow%d", i)}
		} else {
			items[i] = loader.LoadItem{URL: fmt.Sprintf("fast%d", i)}
		}
	}

	results := loadImages(context.Background(), items, 250*time.Millisecond, fetch)
	for i, r := range results {
		if i < 8 {
			if r.Error == nil {
				t.Fatalf("slow item %d should hit its own deadline", i)
			}
			continue
		}
		if r.Error != nil {
			t.Fatalf("fast item %d must not be cancelled by slow siblings: %v", i, r.Error)
		}
	}
}

func TestBatchImageFailureUsesRetryLadder(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "one", ImageURL: "u1"}},
	})

	nextModel, cmd := m.handleImagesBatchLoadedMsg(imagesBatchLoadedMsg{
		results: []imageLoadedMsg{{url: "u1", err: errors.New("transient")}},
	})
	got := nextModel.(model)
	if cmd == nil {
		t.Fatal("expected a retry tick for a batch failure")
	}
	if _, stamped := got.ui.imgs.failedAt["u1"]; stamped {
		t.Fatal("expected batch failure to retry before marking failed")
	}
	if _, ok := got.ui.cover.imageRetryToken["u1"]; !ok {
		t.Fatal("expected retry token for batch failure")
	}

	next, _ := got.handleImagesBatchLoadedMsg(imagesBatchLoadedMsg{
		results: []imageLoadedMsg{{url: "u1", err: errors.New("transient")}},
	})
	got = next.(model)
	got.ui.cover.imageRetryCount["u1"] = imageLoadRetryMax
	next, _ = got.handleImagesBatchLoadedMsg(imagesBatchLoadedMsg{
		results: []imageLoadedMsg{{url: "u1", err: errors.New("transient")}},
	})
	got = next.(model)
	if _, stamped := got.ui.imgs.failedAt["u1"]; !stamped {
		t.Fatal("expected failed stamp after retries exhausted")
	}
}

func TestKittyEncodeDownscalesToBudget(t *testing.T) {
	cases := []struct {
		w, h, wantW, wantH int
	}{
		{640, 640, 512, 512},
		{640, 480, 512, 384},
		{480, 640, 384, 512},
		{300, 300, 300, 300},
	}
	for _, tc := range cases {
		src := image.NewRGBA(image.Rect(0, 0, tc.wantW*0+tc.w, tc.h))
		encoded, err := encodeImageAsPNGBase64(src)
		if err != nil {
			t.Fatalf("encode %dx%d: %v", tc.w, tc.h, err)
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("base64 %dx%d: %v", tc.w, tc.h, err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("png config %dx%d: %v", tc.w, tc.h, err)
		}
		if cfg.Width != tc.wantW || cfg.Height != tc.wantH {
			t.Fatalf("encode %dx%d: got %dx%d, want %dx%d", tc.w, tc.h, cfg.Width, cfg.Height, tc.wantW, tc.wantH)
		}
	}
}

func TestSetProtocolInvalidatesAndRespectsOverride(t *testing.T) {
	c := newImgCache()
	c.setProtocol(imageProtocolKitty)
	c.encoded["u1"] = "enc"
	c.covers.Set(coverKey{url: "u1", cols: 10, rows: 10}, "rendered")
	c.kittyVisible = true
	c.lastKittyURL = "u1"

	c.setProtocol(imageProtocolNone)
	if c.protocol != imageProtocolNone {
		t.Fatal("expected protocol switch")
	}
	if _, ok := c.covers.Get(coverKey{url: "u1", cols: 10, rows: 10}); ok {
		t.Fatal("expected rendered covers invalidated on protocol switch")
	}
	if c.kittyVisible {
		t.Fatal("expected overlay state reset on protocol switch")
	}
	if !c.kittyForceRedraw {
		t.Fatal("expected forced redraw on protocol switch")
	}

	c.protocolExplicit = true
	c.setProtocol(imageProtocolKitty)
	if c.protocol != imageProtocolNone {
		t.Fatal("explicit ORPHEUS_IMAGE_PROTOCOL override must not be auto-switched")
	}
}

func TestCoverQueuePruneExcept(t *testing.T) {
	c := newCoverManager()
	for _, u := range []string{"a", "b", "c", "d"} {
		c.enqueueURL(u)
	}
	c.pruneExcept(map[string]struct{}{"b": {}, "d": {}})
	if len(c.queue) != 2 {
		t.Fatalf("expected 2 queued after prune, got %d", len(c.queue))
	}
	for _, u := range c.queue {
		if u != "b" && u != "d" {
			t.Fatalf("unexpected queued url %q", u)
		}
	}
	if len(c.queued) != 2 {
		t.Fatalf("expected queued map pruned too, got %d", len(c.queued))
	}
}
