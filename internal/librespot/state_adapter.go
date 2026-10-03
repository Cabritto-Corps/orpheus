package librespot

import (
	"strconv"
	"strings"

	golibrespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
)

const queueOverrideMaxTracks = 64

func (p *AppPlayer) BuildPlaybackStateUpdate() *PlaybackStateUpdate {
	return p.buildPlaybackStateUpdate(true)
}

func (p *AppPlayer) buildPlaybackStateUpdate(includeQueue bool) *PlaybackStateUpdate {
	if p.state == nil || p.state.player == nil {
		return nil
	}
	pos := golibrespot.TrackPosition(p.state.player, 0)
	vol := p.apiVolume()
	playing := p.state.player.IsPlaying && !p.state.player.IsPaused
	shuffle := p.state.player.Options != nil && p.state.player.Options.ShufflingContext
	out := &PlaybackStateUpdate{
		DeviceName:    p.runtime.Cfg.DeviceName,
		DeviceID:      p.runtime.DeviceId,
		Volume:        int(vol),
		Playing:       playing,
		ProgressMS:    int(pos),
		DurationMS:    0,
		ShuffleState:  shuffle,
		RepeatContext: p.state.player.Options != nil && p.state.player.Options.RepeatingContext,
		RepeatTrack:   p.state.player.Options != nil && p.state.player.Options.RepeatingTrack,
	}

	if includeQueue {
		out.QueueIncluded = true
		if p.state.tracks != nil {
			// Loaded-only read: a page fetch would stall the Run loop; the top-up timer extends pages off the emit path.
			upcoming := p.state.tracks.UpcomingTracksLoaded(queueOverrideMaxTracks)
			out.Queue = providedTracksToQueueEntries(p, upcoming)
			out.QueueHasMore = len(upcoming) >= queueOverrideMaxTracks
			if out.QueueHasMore {
				p.scheduleQueueTopUp()
			}
		} else {
			out.Queue = nil
			out.QueueHasMore = false
		}
	}

	if p.state.player.Track != nil {
		out.TrackID = golibrespot.NormalizeSpotifyId(p.state.player.Track.Uri)
	}
	out.ContextURI = strings.TrimSpace(p.state.player.ContextUri)
	if p.primaryStream == nil && p.state.player.Track != nil && p.state.player.Track.Metadata != nil {
		out.TrackName = metadataValue(p.state.player.Track.Metadata, "title", "name", "track_name")
		out.ArtistName = metadataValue(p.state.player.Track.Metadata, "artist_name", "artist", "artists", "show_name")
		out.AlbumName = metadataValue(p.state.player.Track.Metadata, "album_title", "album_name", "album")
	}
	prod := p.prodInfoSnapshot()
	if p.primaryStream != nil && prod != nil {
		durationMs := int64(p.primaryStream.Media.Duration())
		if durationMs > 0 && pos > durationMs {
			pos = durationMs
		}
		t := p.newApiResponseStatusTrack(p.primaryStream.Media, pos)
		out.TrackName = t.Name
		if len(t.ArtistNames) > 0 {
			out.ArtistName = t.ArtistNames[0]
			for i := 1; i < len(t.ArtistNames); i++ {
				out.ArtistName += ", " + t.ArtistNames[i]
			}
		}
		out.AlbumName = t.AlbumName
		out.DurationMS = int(durationMs)
		out.ProgressMS = int(t.Position)
		if t.AlbumCoverUrl != nil {
			out.AlbumImageURL = *t.AlbumCoverUrl
		}
	}
	if out.DurationMS <= 0 && p.state.player.Duration > 0 {
		out.DurationMS = int(p.state.player.Duration)
	}
	// A stale transfer can push the derived position below zero; clamp it.
	if out.ProgressMS < 0 {
		out.ProgressMS = 0
	}
	return out
}

func providedTracksToQueueEntries(p *AppPlayer, tracks []*connectpb.ProvidedTrack) []PlaybackStateQueueEntry {
	if len(tracks) == 0 {
		return nil
	}
	out := make([]PlaybackStateQueueEntry, 0, len(tracks))
	for _, t := range tracks {
		id := golibrespot.NormalizeSpotifyId(t.Uri)
		// Origin follows the fork's is_queued marker.
		e := PlaybackStateQueueEntry{ID: id, Queued: t.Metadata["is_queued"] == "true"}
		if cached := p.getCachedQueueMeta(id); cached != nil {
			e = *cached
			// Re-read origin: the same ID can move between regions.
			e.Queued = t.Metadata["is_queued"] == "true"
			out = append(out, e)
			continue
		}
		if t.Metadata != nil {
			e.Name = metadataValue(t.Metadata, "title", "name", "track_name", "entity_name", "track_title")
			e.Artist = metadataValue(t.Metadata, "artist_name", "artist", "artists", "show_name", "album_artist_name")
			e.DurationMS = metadataDurationMS(t.Metadata)
		}
		if e.Name == "" {
			e.Name = fallbackQueueLabel()
		}
		if e.Artist == "" {
			e.Artist = "-"
		}
		out = append(out, e)
	}
	return out
}

func metadataValue(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if val := strings.TrimSpace(metadata[key]); val != "" {
			return val
		}
	}
	return ""
}

func fallbackQueueLabel() string {
	return "Unknown track"
}

func metadataDurationMS(metadata map[string]string) int {
	for _, key := range []string{"duration_ms", "duration", "track_duration", "length"} {
		raw := strings.TrimSpace(metadata[key])
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			continue
		}
		if n < 2000 {
			n *= 1000
		}
		return n
	}
	return 0
}
