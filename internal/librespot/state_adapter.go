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
		DeviceName:    p.runtime.Cfg.DeviceName,
		DeviceID:      p.runtime.DeviceId,
		Volume:        int(vol),
		Playing:       playing,
		ProgressMS:    int(pos),
		DurationMS:    0,
		ShuffleState:  shuffle,
		RepeatContext: p.state.player.Options != nil && p.state.player.Options.RepeatingContext,
		RepeatTrack:   p.state.player.Options != nil && p.state.player.Options.RepeatingTrack,
		Queue:         providedTracksToQueueEntries(p, p.state.player.NextTracks),
	}
	deriveKey := p.currentDerivedQueueKey(shuffle)
	if cachedQueue, hasMore, ok := p.getDerivedQueueCache(deriveKey); ok {
		out.Queue = cachedQueue
		out.QueueHasMore = hasMore
	} else {
		var derivedQueue []PlaybackStateQueueEntry
		var hasMore bool
		var ok bool
		if shuffle {
			derivedQueue, hasMore, ok = p.derivedQueueFromShuffledTrackList()
		} else {
			derivedQueue, hasMore, ok = p.derivedQueueFromTrackList()
		}
		if ok {
			out.Queue = derivedQueue
			out.QueueHasMore = hasMore
			p.setDerivedQueueCache(deriveKey, derivedQueue, hasMore)
		}
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

	ctx, cancel := context.WithTimeout(p.ownerContext(), 8*time.Second)
	defer cancel()

	all := p.state.tracks.AllTracks(ctx)
	if len(all) == 0 {
		return nil, false, false
	}

	currentUID := strings.TrimSpace(p.state.player.Track.Uid)
	currentID := normalizeSpotifyID(p.state.player.Track.Uri)
	currentIdx := -1
	if p.state.player.Index != nil {
		if idx := int(p.state.player.Index.GetTrack()); idx >= 0 && idx < len(all) {
			currentIdx = idx
		}
	}
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

	repeatContext := p.state.player.Options != nil && p.state.player.Options.RepeatingContext
	ordered, hasMore := orderedQueueFromCurrent(all, currentIdx, queueOverrideMaxTracks, repeatContext)
	return providedTracksToQueueEntries(p, ordered), hasMore, true
}

func orderedQueueFromCurrent(all []*connectpb.ProvidedTrack, currentIdx int, maxTracks int, wrap bool) ([]*connectpb.ProvidedTrack, bool) {
	if len(all) == 0 || currentIdx < 0 || currentIdx >= len(all) {
		return nil, false
	}
	ordered := make([]*connectpb.ProvidedTrack, 0, len(all))
	ordered = append(ordered, all[currentIdx:]...)
	if wrap && currentIdx > 0 {
		ordered = append(ordered, all[:currentIdx]...)
	}
	hasMore := false
	if maxTracks > 0 && len(ordered) > maxTracks {
		ordered = ordered[:maxTracks]
		hasMore = true
	}
	return ordered, hasMore
}

func trackQueueKey(uid, uri string) string {
	uid = strings.TrimSpace(uid)
	if uid != "" {
		return "uid:" + uid
	}
	id := normalizeSpotifyID(uri)
	if id == "" {
		return ""
	}
	return "id:" + id
}

func (p *AppPlayer) derivedQueueFromShuffledTrackList() ([]PlaybackStateQueueEntry, bool, bool) {
	if p.state == nil || p.state.player == nil {
		return nil, false, false
	}

	outTracks := make([]*connectpb.ProvidedTrack, 0, queueOverrideMaxTracks)
	seen := make(map[string]struct{}, queueOverrideMaxTracks)
	markSeen := func(uid, uri string) {
		if key := trackQueueKey(uid, uri); key != "" {
			seen[key] = struct{}{}
		}
	}

	for _, t := range p.state.player.PrevTracks {
		if t == nil {
			continue
		}
		markSeen(t.Uid, t.Uri)
	}
	if p.state.player.Track != nil {
		markSeen(p.state.player.Track.Uid, p.state.player.Track.Uri)
	}

	for _, t := range p.state.player.NextTracks {
		if t == nil {
			continue
		}
		if key := trackQueueKey(t.Uid, t.Uri); key != "" {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		outTracks = append(outTracks, t)
		if len(outTracks) >= queueOverrideMaxTracks {
			return providedTracksToQueueEntries(p, outTracks), true, true
		}
	}

	if p.state.tracks == nil {
		return providedTracksToQueueEntries(p, outTracks), false, true
	}

	ctx, cancel := context.WithTimeout(p.ownerContext(), 8*time.Second)
	defer cancel()
	all := p.state.tracks.AllTracks(ctx)
	if len(all) == 0 {
		return providedTracksToQueueEntries(p, outTracks), false, true
	}

	hasMore := false
	for _, t := range all {
		if t == nil {
			continue
		}
		if key := trackQueueKey(t.Uid, t.Uri); key != "" {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		if len(outTracks) >= queueOverrideMaxTracks {
			hasMore = true
			break
		}
		outTracks = append(outTracks, t)
	}
	return providedTracksToQueueEntries(p, outTracks), hasMore, true
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
