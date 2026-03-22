package librespot

import "testing"

func TestSetCachedQueueMetaAndGetCachedQueueMeta(t *testing.T) {
	p := &AppPlayer{}

	p.setCachedQueueMeta("track-1", PlaybackStateQueueEntry{
		ID:         "track-1",
		Name:       "Track 1",
		Artist:     "Artist 1",
		DurationMS: 1234,
	})

	got := p.getCachedQueueMeta("track-1")
	if got == nil || got.Name != "Track 1" || got.Artist != "Artist 1" {
		t.Fatalf("expected cached entry, got %+v", got)
	}

	missing := p.getCachedQueueMeta("nonexistent")
	if missing != nil {
		t.Fatalf("expected nil for missing key, got %+v", missing)
	}
}
