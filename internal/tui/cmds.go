package tui

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

const (
	pollRequestTimeout         = 5 * time.Second
	actionRequestTimeout       = 5 * time.Second
	catalogRequestTimeout      = 60 * time.Second
	playlistPageRequestTimeout = 90 * time.Second
	playlistItemRequestTimeout = 45 * time.Second
	statusQueueCacheTTL        = 500 * time.Millisecond
)

type statusQueueSnapshotCache struct {
	mu     sync.RWMutex
	at     time.Time
	status *spotify.PlaybackStatus
	queue  []spotify.QueueItem
	valid  bool
}

func newStatusQueueSnapshotCache() *statusQueueSnapshotCache {
	return &statusQueueSnapshotCache{}
}

type pollMsg struct {
	token        uint64
	status       *spotify.PlaybackStatus
	queue        []spotify.QueueItem
	queueFetched bool
	queueErr     error
	err          error
}

type actionMsg struct {
	token    uint64
	action   string
	err      error
	rollback *spotify.PlaybackStatus
}

type actionReconcileMsg struct {
	token        uint64
	err          error
	rollback     *spotify.PlaybackStatus
	status       *spotify.PlaybackStatus
	queue        []spotify.QueueItem
	queueFetched bool
}

type playlistsMsg struct {
	items           []spotify.PlaylistSummary
	offset          int
	limit           int
	hasMore         bool
	albumsForbidden bool
	err             error
}

type playlistItemsMsg struct {
	playlistID string
	itemIDs    []string
	itemInfos  []spotify.QueueItem
	nextOffset int
	hasMore    bool
	token      int
	err        error
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

type imagesBatchLoadedMsg struct {
	results []imageLoadedMsg
}

type tickMsg time.Time

type navDebounceMsg struct {
	token int
}

type playbackStateMsg struct {
	seq          uint64
	status       *spotify.PlaybackStatus
	queue        []spotify.QueueItem
	queueHasMore bool
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
				seq++
				status, queue, queueHasMore := PlaybackStateFromLibrespot(u)
				send(playbackStateMsg{seq: seq, status: status, queue: queue, queueHasMore: queueHasMore})
			case <-ctx.Done():
				return
			}
		}
	}()
}

func StartContextTracksListener(ch <-chan []librespot.PlaybackStateQueueEntry, send func(tea.Msg), ctx context.Context) {
	go func() {
		for {
			select {
			case entries := <-ch:
				items := make([]spotify.QueueItem, 0, len(entries))
				for _, e := range entries {
					items = append(items, spotify.QueueItem{ID: e.ID, Name: e.Name, Artist: e.Artist, DurationMS: e.DurationMS})
				}
				send(trackPopupItemsMsg{items: items})
			case <-ctx.Done():
				return
			}
		}
	}()
}

func PlaybackStateFromLibrespot(u *librespot.PlaybackStateUpdate) (*spotify.PlaybackStatus, []spotify.QueueItem, bool) {
	if u == nil {
		return nil, nil, false
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
	queue := make([]spotify.QueueItem, 0, len(u.Queue))
	for _, e := range u.Queue {
		queue = append(queue, spotify.QueueItem{ID: e.ID, Name: e.Name, Artist: e.Artist, DurationMS: e.DurationMS})
	}
	return status, queue, u.QueueHasMore
}

type volDebounceMsg struct {
	token int
}

type seekDebounceMsg struct {
	token int
}

type currentUserIDMsg struct {
	userID string
	err    error
}

func cloneQueueSnapshot(queue []spotify.QueueItem) []spotify.QueueItem {
	if len(queue) == 0 {
		return nil
	}
	cp := make([]spotify.QueueItem, len(queue))
	copy(cp, queue)
	return cp
}

func fetchStatusAndQueue(ctx context.Context, svc *spotify.Service, allowCached bool, cache *statusQueueSnapshotCache) (*spotify.PlaybackStatus, []spotify.QueueItem, error, error) {
	if svc == nil {
		return nil, nil, nil, nil
	}
	if allowCached && cache != nil {
		cache.mu.RLock()
		if cache.valid && time.Since(cache.at) <= statusQueueCacheTTL {
			status := cloneStatus(cache.status)
			queue := cloneQueueSnapshot(cache.queue)
			cache.mu.RUnlock()
			return status, queue, nil, nil
		}
		cache.mu.RUnlock()
	}
	var status *spotify.PlaybackStatus
	var statusErr error
	var queue []spotify.QueueItem
	var queueErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		status, statusErr = svc.Status(ctx)
	}()
	go func() {
		defer wg.Done()
		queue, queueErr = svc.GetQueue(ctx)
	}()
	wg.Wait()
	if statusErr == nil && queueErr == nil && cache != nil {
		cache.mu.Lock()
		cache.status = cloneStatus(status)
		cache.queue = cloneQueueSnapshot(queue)
		cache.at = time.Now()
		cache.valid = true
		cache.mu.Unlock()
	}
	return status, queue, statusErr, queueErr
}

func (m *model) nextStateFetchToken() uint64 {
	m.ui.stateFetchToken++
	return m.ui.stateFetchToken
}

func (m *model) pollCmd(fetchQueue bool) tea.Cmd {
	if m.tuiCmdCh != nil {
		return nil
	}
	token := m.nextStateFetchToken()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, pollRequestTimeout)
		defer cancel()

		if !fetchQueue {
			status, err := m.service.Status(ctx)
			if err != nil {
				return pollMsg{token: token, err: err}
			}
			return pollMsg{token: token, status: status}
		}
		status, queue, statusErr, queueErr := fetchStatusAndQueue(ctx, m.service, true, m.ui.statusQueueCache)

		if statusErr != nil {
			return pollMsg{token: token, err: statusErr}
		}
		if queueErr != nil {
			return pollMsg{token: token, status: status, queueErr: queueErr}
		}
		return pollMsg{token: token, status: status, queue: queue, queueFetched: true}
	}
}

func (m *model) actionCmd(fn func(context.Context) error, action string) tea.Cmd {
	token := m.nextStateFetchToken()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
		defer cancel()
		return actionMsg{token: token, action: action, err: fn(ctx)}
	}
}

func (m *model) actionWithReconcileCmd(fn func(context.Context) error, rollback *spotify.PlaybackStatus) tea.Cmd {
	token := m.nextStateFetchToken()
	svc := m.service
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
		defer cancel()

		if err := fn(ctx); err != nil {
			return actionReconcileMsg{token: token, err: err, rollback: rollback}
		}
		if svc == nil {
			return actionReconcileMsg{token: token}
		}

		status, queue, statusErr, queueErr := fetchStatusAndQueue(ctx, svc, false, m.ui.statusQueueCache)
		if statusErr != nil {
			return actionReconcileMsg{token: token}
		}
		if queueErr != nil {
			return actionReconcileMsg{token: token, status: status, queueFetched: false}
		}
		return actionReconcileMsg{token: token, status: status, queue: queue, queueFetched: true}
	}
}

func (m *model) reconcileCmd() tea.Cmd {
	if m.service == nil {
		return nil
	}
	token := m.nextStateFetchToken()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
		defer cancel()
		status, queue, statusErr, queueErr := fetchStatusAndQueue(ctx, m.service, true, m.ui.statusQueueCache)
		if statusErr != nil {
			return actionReconcileMsg{token: token, err: statusErr}
		}
		if queueErr != nil {
			return actionReconcileMsg{token: token, status: status, queue: nil, queueFetched: false}
		}
		return actionReconcileMsg{token: token, status: status, queue: queue, queueFetched: true}
	}
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
