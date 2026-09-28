package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

const (
	catalogRequestTimeout      = 60 * time.Second
	playlistPageRequestTimeout = 90 * time.Second
)

type playlistsMsg struct {
	items           []spotify.PlaylistSummary
	albumsForbidden bool
	err             error
}

type imageLoadedMsg struct {
	url string
	err error
}

type imageRetryMsg struct {
	url   string
	token int
}

type coverImageResolvedMsg struct {
	kind string
	id   string
	url  string
	err  error
}

type coverImageURLsBatchResolvedMsg struct {
	results []coverImageResolvedMsg
}

type indexedImageMsg struct {
	idx int
	msg imageLoadedMsg
}

type imagesBatchLoadedMsg struct {
	results []imageLoadedMsg
}

type tickMsg time.Time

type navDebounceMsg struct {
	token int
}

type playbackStateMsg struct {
	seq           uint64
	status        *spotify.PlaybackStatus
	queue         []spotify.QueueItem
	queueHasMore  bool
	queueIncluded bool
}

type connectionLostMsg struct {
	err error
}

type tuiCmdRetryMsg struct {
	cmd  librespot.TUICommand
	left int
}

func StartPlaybackStateListener(playbackStateCh <-chan *librespot.PlaybackStateUpdate, send func(tea.Msg), ctx context.Context) {
	go func() {
		var seq uint64
		for {
			select {
			case u := <-playbackStateCh:
				if u == nil {
					continue
				}
				if u.Error != "" {
					send(connectionLostMsg{err: errors.New(u.Error)})
					continue
				}
				seq++
				status, queue, queueHasMore, queueIncluded := PlaybackStateFromLibrespot(u)
				send(playbackStateMsg{seq: seq, status: status, queue: queue, queueHasMore: queueHasMore, queueIncluded: queueIncluded})
			case <-ctx.Done():
				return
			}
		}
	}()
}

func StartContextTracksListener(ch <-chan librespot.ContextTracksResult, send func(tea.Msg), ctx context.Context) {
	go func() {
		for {
			select {
			case res := <-ch:
				items := make([]spotify.QueueItem, 0, len(res.Entries))
				for _, e := range res.Entries {
					items = append(items, spotify.QueueItem{ID: e.ID, Name: e.Name, Artist: e.Artist, DurationMS: e.DurationMS, ImageURL: e.ImageURL})
				}
				send(trackPopupItemsMsg{token: res.ReqToken, items: items})
			case <-ctx.Done():
				return
			}
		}
	}()
}

func PlaybackStateFromLibrespot(u *librespot.PlaybackStateUpdate) (*spotify.PlaybackStatus, []spotify.QueueItem, bool, bool) {
	if u == nil {
		return nil, nil, false, false
	}
	status := &spotify.PlaybackStatus{
		DeviceName:    u.DeviceName,
		DeviceID:      u.DeviceID,
		TrackID:       u.TrackID,
		Volume:        u.Volume,
		TrackName:     u.TrackName,
		ArtistName:    u.ArtistName,
		AlbumName:     u.AlbumName,
		AlbumImageURL: u.AlbumImageURL,
		Playing:       u.Playing,
		ProgressMS:    u.ProgressMS,
		DurationMS:    u.DurationMS,
		ShuffleState:  u.ShuffleState,
		RepeatContext: u.RepeatContext,
		RepeatTrack:   u.RepeatTrack,
	}
	if !u.QueueIncluded {
		return status, nil, false, false
	}
	queue := make([]spotify.QueueItem, 0, len(u.Queue))
	for _, e := range u.Queue {
		queue = append(queue, spotify.QueueItem{ID: e.ID, Name: e.Name, Artist: e.Artist, DurationMS: e.DurationMS, ImageURL: e.ImageURL})
	}
	return status, queue, u.QueueHasMore, true
}

type volDebounceMsg struct {
	token int
}

type seekDebounceMsg struct {
	token int
}

func (m model) tickCmd() tea.Cmd {
	return tea.Tick(uiTickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) navDebounceCmd(token int) tea.Cmd {
	return tea.Tick(navDebounceInterval, func(time.Time) tea.Msg {
		return navDebounceMsg{token: token}
	})
}

func (m model) imageRetryCmd(url string, attempt int, token int) tea.Cmd {
	delay := min(time.Duration(attempt)*200*time.Millisecond, 1200*time.Millisecond)
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return imageRetryMsg{url: url, token: token}
	})
}

func (m model) volDebounceCmd(token int) tea.Cmd {
	return tea.Tick(volSeekDebounceInterval, func(time.Time) tea.Msg {
		return volDebounceMsg{token: token}
	})
}

func (m model) seekDebounceCmd(token int) tea.Cmd {
	return tea.Tick(volSeekDebounceInterval, func(time.Time) tea.Msg {
		return seekDebounceMsg{token: token}
	})
}
