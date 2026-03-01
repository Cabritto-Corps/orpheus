package librespot

import (
	"context"
	"strconv"
	"strings"
	"time"

	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
)

const queueOverrideMaxTracks = 500

func (p *AppPlayer) BuildPlaybackStateUpdate() *PlaybackStateUpdate {
	if p.state == nil || p.state.player == nil {
		return nil
	}
	pos := p.state.trackPosition()
	vol := p.apiVolume()
	playing := p.state.player.IsPlaying && !p.state.player.IsPaused
	shuffle := false
	if p.state.player.Options != nil && p.state.player.Options.ShufflingContext {
		shuffle = true
	}
	out := &PlaybackStateUpdate{
		DeviceName:   p.runtime.Cfg.DeviceName,
		DeviceID:     p.runtime.DeviceId,
		Volume:       int(vol),
		Playing:      playing,
		ProgressMS:   int(pos),
		DurationMS:   0,
		ShuffleState: shuffle,
		Queue:        providedTracksToQueueEntries(p, p.state.player.NextTracks),
	}
	if derivedQueue, hasMore, ok := p.derivedQueueFromTrackList(); ok {
		out.Queue = derivedQueue
		out.QueueHasMore = hasMore
	}
	if p.state.player.Track != nil {
		out.TrackID = normalizeSpotifyID(p.state.player.Track.Uri)
		if p.state.player.Track.Metadata != nil {
			out.TrackName = metadataValue(p.state.player.Track.Metadata, "title", "name", "track_name")
			out.ArtistName = metadataValue(p.state.player.Track.Metadata, "artist_name", "artist", "artists", "show_name")
			out.AlbumName = metadataValue(p.state.player.Track.Metadata, "album_title", "album_name", "album")
		}
	}
	if p.primaryStream != nil && p.prodInfo != nil {
		t := p.newApiResponseStatusTrack(p.primaryStream.Media, pos)
		out.TrackName = t.Name
		if len(t.ArtistNames) > 0 {
			out.ArtistName = t.ArtistNames[0]
			for i := 1; i < len(t.ArtistNames); i++ {
				out.ArtistName += ", " + t.ArtistNames[i]
			}
		}
		out.AlbumName = t.AlbumName
		out.DurationMS = t.Duration
		out.ProgressMS = int(t.Position)
		if t.AlbumCoverUrl != nil {
			out.AlbumImageURL = *t.AlbumCoverUrl
		}
	}
	if out.DurationMS <= 0 && p.state.player.Duration > 0 {
		out.DurationMS = int(p.state.player.Duration)
	}
	return out
}

func (p *AppPlayer) derivedQueueFromTrackList() ([]PlaybackStateQueueEntry, bool, bool) {
	if p.state == nil || p.state.tracks == nil || p.state.player == nil || p.state.player.Track == nil {
		return nil, false, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	all := p.state.tracks.AllTracks(ctx)
	if len(all) == 0 {
		return nil, false, false
	}

	currentUID := strings.TrimSpace(p.state.player.Track.Uid)
	currentID := normalizeSpotifyID(p.state.player.Track.Uri)
	currentIdx := -1
	if currentUID != "" {
		for i, t := range all {
			if strings.TrimSpace(t.Uid) == currentUID {
				currentIdx = i
				break
			}
		}
	}
	if currentIdx < 0 {
		for i, t := range all {
			if normalizeSpotifyID(t.Uri) == currentID {
				currentIdx = i
				break
			}
		}
	}
	if currentIdx < 0 {
		return nil, false, false
	}

	next := all[currentIdx+1:]
	hasMore := false
	if len(next) > queueOverrideMaxTracks {
		next = next[:queueOverrideMaxTracks]
		hasMore = true
	}
	return providedTracksToQueueEntries(p, next), hasMore, true
}

func providedTracksToQueueEntries(p *AppPlayer, tracks []*connectpb.ProvidedTrack) []PlaybackStateQueueEntry {
	if len(tracks) == 0 {
		return nil
	}
	out := make([]PlaybackStateQueueEntry, 0, len(tracks))
	for _, t := range tracks {
		id := normalizeSpotifyID(t.Uri)
		e := PlaybackStateQueueEntry{ID: id}
		if cached := p.getCachedQueueMeta(id); cached != nil {
			e = *cached
			out = append(out, e)
			continue
		}
		if t.Metadata != nil {
			e.Name = metadataValue(t.Metadata, "title", "name", "track_name", "entity_name", "track_title")
			e.Artist = metadataValue(t.Metadata, "artist_name", "artist", "artists", "show_name", "album_artist_name")
			e.DurationMS = metadataDurationMS(t.Metadata)
		}
		if e.Name == "" {
			e.Name = fallbackQueueLabel(id)
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

func normalizeSpotifyID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "spotify:") {
		parts := strings.Split(raw, ":")
		if len(parts) >= 3 {
			return parts[len(parts)-1]
		}
	}
	return raw
}

func fallbackQueueLabel(_ string) string {
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
