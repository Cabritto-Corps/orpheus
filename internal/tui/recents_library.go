package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/spotify"
)

const maxRecentsTracks = 100

func recentIdentity(id string) string {
	if normalized := golibrespot.NormalizeSpotifyId(id); normalized != "" {
		return normalized
	}
	return strings.TrimSpace(id)
}

// mergeRecentTracks keeps the first occurrence (source priority) while filling
// any metadata missing from a later duplicate.
func mergeRecentTracks(groups ...[]spotify.QueueItem) []spotify.QueueItem {
	tracks := make([]spotify.QueueItem, 0)
	positions := make(map[string]int)
	for _, group := range groups {
		for _, track := range group {
			key := recentIdentity(track.ID)
			if key == "" {
				continue
			}
			if index, ok := positions[key]; ok {
				fillRecentTrack(&tracks[index], track)
				continue
			}
			positions[key] = len(tracks)
			tracks = append(tracks, track)
			if len(tracks) == maxRecentsTracks {
				return tracks
			}
		}
	}
	return tracks
}

func fillRecentTrack(previous *spotify.QueueItem, track spotify.QueueItem) {
	if previous.Name == "" {
		previous.Name = track.Name
	}
	if previous.Artist == "" {
		previous.Artist = track.Artist
	} else if track.Artist != "" {
		previous.Artist = mergeArtistNames(previous.Artist, track.Artist)
	}
	if previous.Album == "" {
		previous.Album = track.Album
	}
	if previous.DurationMS <= 0 {
		previous.DurationMS = track.DurationMS
	}
	if previous.ImageURL == "" {
		previous.ImageURL = track.ImageURL
	}
}

func mergeArtistNames(first, second string) string {
	parts := strings.Split(first+", "+second, ",")
	seen := make(map[string]struct{}, len(parts))
	artists := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		artists = append(artists, name)
	}
	return strings.Join(artists, ", ")
}

func (m *model) refreshRecentsList() tea.Cmd {
	if m.browse.recentsList.Paginator.PerPage <= 0 {
		return nil
	}
	var current []spotify.QueueItem
	if track := playbackQueueItem(m.transport.status); recentIdentity(track.ID) != "" {
		current = append(current, track)
	}
	tracks := mergeRecentTracks(
		current,
		m.browse.sessionRecentTracks,
		m.browse.apiRecentTracks,
	)
	if slices.Equal(tracks, m.browse.recentsTracks) {
		return nil
	}
	selectedID := ""
	if selected, ok := m.browse.recentsList.SelectedItem().(trackItem); ok {
		selectedID = recentIdentity(selected.item.ID)
	}
	m.browse.recentsTracks = tracks
	items := make([]list.Item, 0, len(tracks))
	for _, track := range tracks {
		items = append(items, trackItem{item: track})
	}
	cmd := m.browse.recentsList.SetItems(items)
	if m.browse.recentsList.FilterState() == list.Unfiltered {
		selectionRestored := false
		if selectedID != "" {
			for index, item := range items {
				if recentIdentity(item.(trackItem).item.ID) == selectedID {
					m.browse.recentsList.Select(index)
					selectionRestored = true
					break
				}
			}
		}
		if !selectionRestored && len(items) > 0 {
			m.browse.recentsList.Select(0)
		}
	}
	return cmd
}

func (m *model) selectRecentByIdentity(id string) {
	id = recentIdentity(id)
	if id == "" {
		return
	}
	for index, item := range m.browse.recentsList.Items() {
		track, ok := item.(trackItem)
		if ok && recentIdentity(track.item.ID) == id {
			m.browse.recentsList.Select(index)
			return
		}
	}
}

func (m *model) focusCurrentRecentOnEntry() tea.Cmd {
	if m.browse.recentsSelectionTouched || m.browse.recentsList.FilterState() != list.Unfiltered {
		return nil
	}
	status := m.transport.status
	if status == nil || recentIdentity(status.TrackID) == "" {
		return nil
	}
	cmd := m.refreshRecentsList()
	m.selectRecentByIdentity(status.TrackID)
	return cmd
}

func (m *model) upsertCurrentPlaybackRecent(previous, current *spotify.PlaybackStatus) tea.Cmd {
	previousID := ""
	if previous != nil {
		previousID = recentIdentity(previous.TrackID)
	}
	currentID := ""
	if current != nil {
		currentID = recentIdentity(current.TrackID)
	}
	if previousID != "" && previousID != currentID {
		m.freezeSessionTrack(previous)
	}
	return m.refreshRecentsList()
}

func (m *model) freezeSessionTrack(status *spotify.PlaybackStatus) {
	if status == nil || recentIdentity(status.TrackID) == "" {
		return
	}
	m.browse.sessionRecentTracks = mergeRecentTracks(
		[]spotify.QueueItem{playbackQueueItem(status)},
		m.browse.sessionRecentTracks,
	)
}

func playbackQueueItem(status *spotify.PlaybackStatus) spotify.QueueItem {
	if status == nil {
		return spotify.QueueItem{}
	}
	return spotify.QueueItem{
		ID: status.TrackID, Name: status.TrackName, Artist: status.ArtistName,
		Album:      status.AlbumName,
		DurationMS: status.DurationMS, ImageURL: status.AlbumImageURL,
	}
}
