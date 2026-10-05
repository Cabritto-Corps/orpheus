package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/spotify"
)

const maxSongsInSongsTab = 100

func songIdentity(id string) string {
	if normalized := golibrespot.NormalizeSpotifyId(id); normalized != "" {
		return normalized
	}
	return strings.TrimSpace(id)
}

// mergeSongTracks keeps the first occurrence (source priority) while filling
// any metadata missing from a later duplicate.
func mergeSongTracks(groups ...[]spotify.QueueItem) []spotify.QueueItem {
	tracks := make([]spotify.QueueItem, 0)
	positions := make(map[string]int)
	for _, group := range groups {
		for _, track := range group {
			key := songIdentity(track.ID)
			if key == "" {
				continue
			}
			if index, ok := positions[key]; ok {
				previous := &tracks[index]
				if previous.Name == "" {
					previous.Name = track.Name
				}
				if previous.Artist == "" {
					previous.Artist = track.Artist
				} else if track.Artist != "" {
					previous.Artist = mergeArtistNames(previous.Artist, track.Artist)
				}
				if previous.DurationMS <= 0 {
					previous.DurationMS = track.DurationMS
				}
				if previous.ImageURL == "" {
					previous.ImageURL = track.ImageURL
				}
				continue
			}
			positions[key] = len(tracks)
			tracks = append(tracks, track)
			if len(tracks) == maxSongsInSongsTab {
				return tracks
			}
		}
	}
	return tracks
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

func (m *model) refreshSongsList() tea.Cmd {
	if m.browse.songsList.Paginator.PerPage <= 0 {
		return nil
	}
	var current []spotify.QueueItem
	if track := playbackQueueItem(m.transport.status); songIdentity(track.ID) != "" {
		current = append(current, track)
	}
	tracks := mergeSongTracks(
		current,
		m.browse.songsSessionRecentTracks,
		m.browse.songsRecentTracks,
		m.browse.songsSavedTracks,
		m.browse.songsCollectionTracks,
	)
	if slices.Equal(tracks, m.browse.songsTracks) {
		return nil
	}
	selectedID := ""
	if selected, ok := m.browse.songsList.SelectedItem().(trackItem); ok {
		selectedID = songIdentity(selected.item.ID)
	}
	m.browse.songsTracks = tracks
	items := make([]list.Item, 0, len(tracks))
	for _, track := range tracks {
		items = append(items, trackItem{item: track})
	}
	cmd := m.browse.songsList.SetItems(items)
	if m.browse.songsList.FilterState() == list.Unfiltered {
		selectionRestored := false
		if selectedID != "" {
			for index, item := range items {
				if songIdentity(item.(trackItem).item.ID) == selectedID {
					m.browse.songsList.Select(index)
					selectionRestored = true
					break
				}
			}
		}
		if !selectionRestored && len(items) > 0 {
			m.browse.songsList.Select(0)
		}
	}
	return cmd
}

func (m *model) selectSongByIdentity(id string) {
	id = songIdentity(id)
	if id == "" {
		return
	}
	for index, item := range m.browse.songsList.Items() {
		track, ok := item.(trackItem)
		if ok && songIdentity(track.item.ID) == id {
			m.browse.songsList.Select(index)
			return
		}
	}
}

func (m *model) focusCurrentSongOnEntry() tea.Cmd {
	if m.browse.songsSelectionTouched || m.browse.songsList.FilterState() != list.Unfiltered {
		return nil
	}
	status := m.transport.status
	if status == nil || songIdentity(status.TrackID) == "" {
		return nil
	}
	cmd := m.refreshSongsList()
	m.selectSongByIdentity(status.TrackID)
	return cmd
}

func (m *model) upsertCurrentPlaybackSong(previous, current *spotify.PlaybackStatus) tea.Cmd {
	previousID := ""
	if previous != nil {
		previousID = songIdentity(previous.TrackID)
	}
	currentID := ""
	if current != nil {
		currentID = songIdentity(current.TrackID)
	}
	if previousID != "" && previousID != currentID {
		m.browse.songsSessionRecentTracks = mergeSongTracks(
			[]spotify.QueueItem{playbackQueueItem(previous)},
			m.browse.songsSessionRecentTracks,
		)
	}
	return m.refreshSongsList()
}

func playbackQueueItem(status *spotify.PlaybackStatus) spotify.QueueItem {
	if status == nil {
		return spotify.QueueItem{}
	}
	return spotify.QueueItem{
		ID: status.TrackID, Name: status.TrackName, Artist: status.ArtistName,
		DurationMS: status.DurationMS, ImageURL: status.AlbumImageURL,
	}
}
