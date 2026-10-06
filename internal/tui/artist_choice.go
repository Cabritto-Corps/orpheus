package tui

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

const artistTrackQueueSize = 50

type artistTracksMsg struct {
	token    int
	artistID string
	tracks   []spotify.QueueItem
	err      error
}

func (m model) openArtistChoice(result spotify.SearchResultItem) (tea.Model, tea.Cmd) {
	m.ui.artistChoiceOpen = true
	m.ui.artistChoiceResult = result
	m.ui.artistChoiceCursor = 0
	m.ui.artistChoiceLoading = false
	m.ui.artistChoiceErr = nil
	m.ui.artistChoiceReq++
	return m, m.kittyOverlayCmd()
}

func (m model) artistTracksCmd(token int, artistID string) tea.Cmd {
	catalog := m.resolveCatalog()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, catalogRequestTimeout)
		defer cancel()
		tracks, err := spotify.CollectArtistTracks(ctx, catalog, artistID, artistTrackQueueSize)
		return artistTracksMsg{token: token, artistID: artistID, tracks: tracks, err: err}
	}
}

func (m model) handleArtistTracksMsg(msg artistTracksMsg) (tea.Model, tea.Cmd) {
	if !m.ui.artistChoiceOpen {
		return m, nil
	}
	if msg.token != m.ui.artistChoiceReq {
		return m, nil
	}
	m.ui.artistChoiceLoading = false
	if msg.err != nil {
		slog.Warn("artist tracks fetch failed", "artist", msg.artistID, "error", msg.err.Error())
		m.ui.artistChoiceErr = msg.err
		return m, m.kittyOverlayCmd()
	}
	if len(msg.tracks) == 0 {
		m.ui.artistChoiceErr = errors.New("no playable tracks found for artist")
		return m, m.kittyOverlayCmd()
	}
	rand.Shuffle(len(msg.tracks), func(i, j int) { msg.tracks[i], msg.tracks[j] = msg.tracks[j], msg.tracks[i] })
	if len(msg.tracks) > artistTrackQueueSize {
		msg.tracks = msg.tracks[:artistTrackQueueSize]
	}
	result := m.ui.artistChoiceResult
	m.ui.artistChoiceOpen = false
	m.ui.artistChoiceReq++
	uris := make([]string, 0, len(msg.tracks))
	seeds := make([]librespot.PlaybackStateQueueEntry, 0, len(msg.tracks))
	for _, t := range msg.tracks {
		if strings.TrimSpace(t.ID) == "" {
			continue
		}
		uris = append(uris, "spotify:track:"+strings.TrimSpace(t.ID))
		seeds = append(seeds, librespot.PlaybackStateQueueEntry{ID: strings.TrimSpace(t.ID), Name: t.Name, Artist: t.Artist, DurationMS: t.DurationMS})
	}
	return m.playArtistTracks(result.URI, result.ImageURL, uris, seeds)
}

func (m model) playArtistTracks(artistURI, imageURL string, uris []string, seeds []librespot.PlaybackStateQueueEntry) (tea.Model, tea.Cmd) {
	m.ui.activeTab = tabPlayer
	m.transport.playbackErr = nil
	m.freezeSessionTrack(m.transport.status)
	if m.transport.status != nil {
		m.transport.pendingContextFrom = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
		m.transport.pendingContextFromAt = time.Now()
	}
	m.transport.queue = nil
	m.transport.queueHasMore = false
	m.transport.stableQueueLen = 0
	if m.transport.status != nil {
		m.transport.status.ProgressMS = 0
		m.transport.status.DurationMS = 0
	}
	m.transport.interpolationSyncAt = time.Time{}
	m.transport.interpolationProgressMS = 0
	m.beginTransportTransition()
	cmd := librespot.TUICommand{Kind: librespot.TUICommandPlayTracks, URI: artistURI, URIs: uris, Seed: seeds}
	return m, tea.Batch(m.sendTUICommandOrRetry(cmd), m.loadImageCmd(imageURL, true))
}

func (m model) handleArtistChoiceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	switch {
	case keyMatches(msg, k.CloseModal):
		m.ui.artistChoiceOpen = false
		m.ui.artistChoiceReq++
		return m, m.kittyOverlayCmd()
	case keyMatches(msg, k.Quit):
		return m, nil
	case keyMatches(msg, k.QueueUp), msg.Code == tea.KeyUp:
		if m.ui.artistChoiceCursor > 0 {
			m.ui.artistChoiceCursor--
		}
		return m, nil
	case keyMatches(msg, k.QueueDown), msg.Code == tea.KeyDown:
		if m.ui.artistChoiceCursor < 1 {
			m.ui.artistChoiceCursor++
		}
		return m, nil
	case keyMatches(msg, k.Select):
		result := m.ui.artistChoiceResult
		if m.ui.artistChoiceCursor == 1 {
			m.ui.artistChoiceOpen = false
			m.ui.artistChoiceReq++
			return m.playArtistStation(result)
		}
		if m.ui.artistChoiceLoading {
			return m, nil
		}
		m.ui.artistChoiceReq++
		m.ui.artistChoiceLoading = true
		m.ui.artistChoiceErr = nil
		return m, tea.Batch(m.artistTracksCmd(m.ui.artistChoiceReq, strings.TrimSpace(result.ID)), m.kittyOverlayCmd())
	default:
		return m, nil
	}
}

func (m model) artistChoiceView() string {
	modalW, boxH, _ := modalRect(m.ui.width, m.ui.height, 52, 10)
	title := m.styles.styleTrackPopupTitle.Render("  " + m.ui.artistChoiceResult.Name)
	var body string
	if m.ui.artistChoiceLoading {
		body = m.styles.styleTrackPopupLoading.Render("\n  " + m.ui.spinner.View() + " Loading artist tracks…\n")
	} else {
		rows := []string{
			m.styles.modalRow("Play artist tracks", "", m.ui.artistChoiceCursor == 0, modalW),
			m.styles.modalRow("Play artist station", "", m.ui.artistChoiceCursor == 1, modalW),
		}
		body = "\n" + rows[0] + "\n" + rows[1] + "\n"
		if m.ui.artistChoiceErr != nil {
			body += m.styles.styleError.Render(truncate(m.ui.artistChoiceErr.Error(), max(12, modalW-modalContentInset))) + "\n"
		}
	}
	k := m.ui.keys
	hint := m.styles.styleTrackPopupHint.Render(m.styles.hintLine([]key.Binding{
		key.NewBinding(key.WithKeys(k.QueueUp.Keys()...), key.WithHelp(k.QueueUp.Help().Key+"/"+k.QueueDown.Help().Key, "choose")),
		withDesc(k.Select, "play"),
		withDesc(k.CloseModal, "close"),
	}, modalW-modalContentInset))
	return m.styles.modalFrame(m.ui.width, m.ui.height, title, hint, body, modalW, boxH)
}
