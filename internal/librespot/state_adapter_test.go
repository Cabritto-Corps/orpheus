package librespot

import (
	"testing"

	golibrespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
)

func TestProvidedTracksToQueueEntriesUsesCache(t *testing.T) {
	p := &AppPlayer{}
	p.queueMetaCache = nil // let setCachedQueueMeta initialize it

	p.setCachedQueueMeta("7GhIk7Il098yCjg4BQjzvb", PlaybackStateQueueEntry{ID: "7GhIk7Il098yCjg4BQjzvb", Name: "Cached Track", Artist: "Cached Artist", DurationMS: 3000})

	tracks := []*connectpb.ProvidedTrack{{Uri: "spotify:track:7GhIk7Il098yCjg4BQjzvb"}}
	entries := providedTracksToQueueEntries(p, tracks)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "Cached Track" {
		t.Fatalf("expected cached name, got %s", entries[0].Name)
	}
}

func TestProvidedTracksToQueueEntriesUsesMetadata(t *testing.T) {
	p := &AppPlayer{}
	tracks := []*connectpb.ProvidedTrack{{
		Uri: "spotify:track:t2",
		Metadata: map[string]string{
			"title":       "Track Title",
			"artist_name": "Track Artist",
			"duration_ms": "180000",
		},
	}}
	entries := providedTracksToQueueEntries(p, tracks)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "Track Title" {
		t.Fatalf("expected Track Title, got %s", entries[0].Name)
	}
	if entries[0].Artist != "Track Artist" {
		t.Fatalf("expected Track Artist, got %s", entries[0].Artist)
	}
	if entries[0].DurationMS != 180000 {
		t.Fatalf("expected 180000, got %d", entries[0].DurationMS)
	}
}

func TestProvidedTracksToQueueEntriesFallback(t *testing.T) {
	p := &AppPlayer{}
	tracks := []*connectpb.ProvidedTrack{{Uri: "spotify:track:unknown"}}
	entries := providedTracksToQueueEntries(p, tracks)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "Unknown track" {
		t.Fatalf("expected fallback name, got %s", entries[0].Name)
	}
	if entries[0].Artist != "-" {
		t.Fatalf("expected fallback artist, got %s", entries[0].Artist)
	}
}

func TestProvidedTracksToQueueEntriesNil(t *testing.T) {
	p := &AppPlayer{}
	entries := providedTracksToQueueEntries(p, nil)
	if entries != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestProvidedTracksToQueueEntriesNormalizesID(t *testing.T) {
	p := &AppPlayer{}
	tracks := []*connectpb.ProvidedTrack{{Uri: "spotify:track:abc123"}}
	entries := providedTracksToQueueEntries(p, tracks)

	expected := golibrespot.NormalizeSpotifyId("spotify:track:abc123")
	if entries[0].ID != expected {
		t.Fatalf("expected normalized id %s, got %s", expected, entries[0].ID)
	}
}

func TestMetadataValue(t *testing.T) {
	meta := map[string]string{"title": "My Song", "name": "Alt Name"}
	got := metadataValue(meta, "title", "name")
	if got != "My Song" {
		t.Fatalf("expected first match 'title', got %s", got)
	}

	got = metadataValue(meta, "missing", "name")
	if got != "Alt Name" {
		t.Fatalf("expected fallback to 'name', got %s", got)
	}

	got = metadataValue(meta, "missing")
	if got != "" {
		t.Fatalf("expected empty, got %s", got)
	}
}

func TestMetadataDurationMS(t *testing.T) {
	meta := map[string]string{"duration_ms": "240000"}
	if got := metadataDurationMS(meta); got != 240000 {
		t.Fatalf("expected 240000, got %d", got)
	}

	meta = map[string]string{"duration": "180"}
	if got := metadataDurationMS(meta); got != 180000 {
		t.Fatalf("expected 180000 (seconds to ms), got %d", got)
	}

	meta = map[string]string{"other": "value"}
	if got := metadataDurationMS(meta); got != 0 {
		t.Fatalf("expected 0 for missing keys, got %d", got)
	}
}

func TestFallbackQueueLabel(t *testing.T) {
	if got := fallbackQueueLabel(); got != "Unknown track" {
		t.Fatalf("expected 'Unknown track', got %s", got)
	}
}
