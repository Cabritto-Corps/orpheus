package librespot

import (
	"testing"

	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
)

func TestDerivedQueueCacheIsolationAndInvalidation(t *testing.T) {
	p := &AppPlayer{}
	entries := []PlaybackStateQueueEntry{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
	}

	p.setDerivedQueueCache("k", entries, true)
	entries[0].ID = "mutated"

	cached, hasMore, ok := p.getDerivedQueueCache("k")
	if !ok || !hasMore {
		t.Fatalf("expected derived cache hit with hasMore=true, ok=%t hasMore=%t", ok, hasMore)
	}
	if cached[0].ID != "a" {
		t.Fatalf("expected cache to keep internal copy, got first id %q", cached[0].ID)
	}

	cached[1].ID = "changed"
	cachedAgain, _, ok := p.getDerivedQueueCache("k")
	if !ok {
		t.Fatal("expected second derived cache hit")
	}
	if cachedAgain[1].ID != "b" {
		t.Fatalf("expected cache reads to return copies, got %q", cachedAgain[1].ID)
	}

	p.invalidateDerivedQueueCache()
	if _, _, ok := p.getDerivedQueueCache("k"); ok {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestSetCachedQueueMetaInvalidatesDerivedQueueCache(t *testing.T) {
	p := &AppPlayer{}
	p.setDerivedQueueCache("k", []PlaybackStateQueueEntry{{ID: "a"}}, false)

	p.setCachedQueueMeta("track-1", PlaybackStateQueueEntry{
		ID:         "track-1",
		Name:       "Track 1",
		Artist:     "Artist 1",
		DurationMS: 1234,
	})

	if _, _, ok := p.getDerivedQueueCache("k"); ok {
		t.Fatal("expected derived cache to invalidate when queue metadata updates")
	}
	got := p.getCachedQueueMeta("track-1")
	if got == nil || got.Name != "Track 1" {
		t.Fatalf("expected queue metadata cache update, got %+v", got)
	}
}

func TestInvalidateQueueDerivationResetBehavior(t *testing.T) {
	p := &AppPlayer{}
	p.markPlayedTrack("spotify:track:abc")
	p.setDerivedQueueCache("k", []PlaybackStateQueueEntry{{ID: "a"}}, false)

	p.invalidateQueueDerivation(false)
	if p.playedTrackCount() != 1 {
		t.Fatalf("expected played-track set to be preserved when reset=false, got %d", p.playedTrackCount())
	}
	if _, _, ok := p.getDerivedQueueCache("k"); ok {
		t.Fatal("expected derived queue cache invalidated")
	}

	p.setDerivedQueueCache("k2", []PlaybackStateQueueEntry{{ID: "b"}}, false)
	p.invalidateQueueDerivation(true)
	if p.playedTrackCount() != 0 {
		t.Fatalf("expected played-track set reset when requested, got %d", p.playedTrackCount())
	}
	if _, _, ok := p.getDerivedQueueCache("k2"); ok {
		t.Fatal("expected derived queue cache invalidated after reset=true")
	}
}

func TestSeedPlayedTrackSetFromPlaybackWindow(t *testing.T) {
	p := &AppPlayer{
		state: &State{
			player: &connectpb.PlayerState{
				PrevTracks: []*connectpb.ProvidedTrack{
					{Uri: "spotify:track:prev1"},
					{Uri: "spotify:track:prev2"},
				},
				Track: &connectpb.ProvidedTrack{Uri: "spotify:track:curr"},
			},
		},
	}
	p.seedPlayedTrackSetFromPlaybackWindow()
	if !p.isPlayedTrack("spotify:track:prev1") || !p.isPlayedTrack("spotify:track:prev2") || !p.isPlayedTrack("spotify:track:curr") {
		t.Fatal("expected playback window tracks to be seeded as played")
	}
}
