package librespot

import (
	"testing"

	golibrespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	metadatapb "github.com/elxgy/go-librespot/proto/spotify/metadata"
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
	if got := fallbackQueueLabel(false); got != "Unknown track" {
		t.Fatalf("expected 'Unknown track', got %s", got)
	}
	if got := fallbackQueueLabel(true); got != "Loading…" {
		t.Fatalf("expected resolving rows to show loading, got %s", got)
	}
}

func TestBuildPlaybackStateUpdateAlwaysSetsTrackID(t *testing.T) {
	p := &AppPlayer{
		runtime: &Runtime{Cfg: &Config{DeviceName: "test", VolumeSteps: 64}, DeviceId: "dev1"},
		state: &State{
			device: &connectpb.DeviceInfo{Volume: 32768},
			player: &connectpb.PlayerState{
				Track: &connectpb.ProvidedTrack{Uri: "spotify:track:7GhIk7Il098yCjg4BQjzvb"},
			},
		},
	}
	update := p.BuildPlaybackStateUpdate()
	if update == nil {
		t.Fatal("expected non-nil update")
	}
	expected := golibrespot.NormalizeSpotifyId("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	if update.TrackID != expected {
		t.Fatalf("expected TrackID %q, got %q", expected, update.TrackID)
	}
}

// Sparse metadata must degrade, never panic.
func TestNewApiResponseStatusTrackSparseMetadata(t *testing.T) {
	p := &AppPlayer{runtime: &Runtime{Cfg: DefaultConfig()}}
	track := p.newApiResponseStatusTrack(golibrespot.NewMediaFromTrack(&metadatapb.Track{
		Gid: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	}), 0)
	if track == nil {
		t.Fatal("sparse track must still produce a status track")
	}
	if track.Name != "" || track.AlbumName != "" || len(track.ArtistNames) != 0 {
		t.Fatalf("sparse track must degrade to empty, got %+v", track)
	}
	if track.AlbumCoverUrl != nil {
		t.Fatalf("no ProductInfo means no cover URL, got %q", *track.AlbumCoverUrl)
	}
	episode := p.newApiResponseStatusTrack(golibrespot.NewMediaFromEpisode(&metadatapb.Episode{
		Gid: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	}), 0)
	if episode == nil || episode.Name != "" || episode.AlbumName != "" {
		t.Fatalf("sparse episode must degrade to empty, got %+v", episode)
	}
}

func TestQueueEntryOriginFollowsIsQueued(t *testing.T) {
	p := &AppPlayer{}
	tracks := []*connectpb.ProvidedTrack{
		{Uri: "spotify:track:q1", Metadata: map[string]string{"is_queued": "true"}},
		{Uri: "spotify:track:c1", Metadata: map[string]string{"title": "Ctx"}},
	}
	entries := providedTracksToQueueEntries(p, tracks)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if !entries[0].Queued {
		t.Fatal("is_queued entry must be flagged manual")
	}
	if entries[1].Queued {
		t.Fatal("context entry must not be flagged manual")
	}
}

func TestQueueEntryOriginRefreshesOnCacheHit(t *testing.T) {
	// A cached name may ride while origin moves.
	p := &AppPlayer{}
	p.setCachedQueueMeta("spotify:track:x", PlaybackStateQueueEntry{ID: "spotify:track:x", Name: "X"})
	tracks := []*connectpb.ProvidedTrack{
		{Uri: "spotify:track:x", Metadata: map[string]string{"is_queued": "true"}},
	}
	entries := providedTracksToQueueEntries(p, tracks)
	if len(entries) != 1 || !entries[0].Queued || entries[0].Name != "X" {
		t.Fatalf("cache hit must refresh origin but keep the name, got %+v", entries)
	}
}

func TestBuildUpdateThreadsContextURI(t *testing.T) {
	p := newQueueEditTestPlayer(t, []string{"spotify:track:1111111111111111111111"})
	p.state.player.ContextUri = "spotify:playlist:testctx"
	u := p.buildPlaybackStateUpdate(true)
	if u == nil {
		t.Fatal("expected an update")
	}
	if u.ContextURI != "spotify:playlist:testctx" {
		t.Fatalf("ContextURI = %q, want the loaded context", u.ContextURI)
	}
	if len(u.Queue) != 1 || !u.Queue[0].Queued {
		t.Fatalf("queue head must be flagged manual, got %+v", u.Queue)
	}
}

func TestBuildPlaybackStateFallsBackToCachedMetaForCurrentTrack(t *testing.T) {
	p, _ := newQueueHeadSignalPlayer(t, []string{"spotify:track:7GhIk7Il098yCjg4BQjzvb"})
	p.state.player.Track = &connectpb.ProvidedTrack{Uri: "spotify:track:7GhIk7Il098yCjg4BQjzvb"}
	p.setCachedQueueMeta("7GhIk7Il098yCjg4BQjzvb", PlaybackStateQueueEntry{ID: "7GhIk7Il098yCjg4BQjzvb", Name: "Seeded Song", Artist: "Seeded Artist"})
	out := p.BuildPlaybackStateUpdate()
	if out.TrackName != "Seeded Song" || out.ArtistName != "Seeded Artist" {
		t.Fatalf("header must use cached metadata before the stream loads: %#v", out)
	}
}

func TestBuildPlaybackStatePrefersPageMetadataOverCache(t *testing.T) {
	p, _ := newQueueHeadSignalPlayer(t, []string{"spotify:track:7GhIk7Il098yCjg4BQjzvb"})
	p.state.player.Track = &connectpb.ProvidedTrack{Uri: "spotify:track:7GhIk7Il098yCjg4BQjzvb", Metadata: map[string]string{"title": "Page Song", "artist_name": "Page Artist"}}
	p.setCachedQueueMeta("7GhIk7Il098yCjg4BQjzvb", PlaybackStateQueueEntry{ID: "7GhIk7Il098yCjg4BQjzvb", Name: "Seeded Song", Artist: "Seeded Artist"})
	out := p.BuildPlaybackStateUpdate()
	if out.TrackName != "Page Song" || out.ArtistName != "Page Artist" {
		t.Fatalf("page metadata must win over cache: %#v", out)
	}
}

func TestQueueEntriesShowLoadingWhileResolving(t *testing.T) {
	p := newTestAppPlayer()
	all := []*connectpb.ProvidedTrack{{Uri: "spotify:track:7GhIk7Il098yCjg4BQjzvb"}}
	p.queueMetaPending.Store(true)
	if entries := providedTracksToQueueEntries(p, all); len(entries) != 1 || entries[0].Name != "Loading…" {
		t.Fatalf("resolving rows must show loading: %#v", entries)
	}
	p.queueMetaPending.Store(false)
	if entries := providedTracksToQueueEntries(p, all); len(entries) != 1 || entries[0].Name != "Unknown track" {
		t.Fatalf("settled rows must show unknown: %#v", entries)
	}
}
