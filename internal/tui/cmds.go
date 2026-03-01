package tui

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

var imgSemaphore = make(chan struct{}, 8)

const (
	pollRequestTimeout          = 5 * time.Second
	actionRequestTimeout        = 5 * time.Second
	catalogRequestTimeout       = 60 * time.Second
	playlistPageRequestTimeout  = 90 * time.Second
	playlistTrackRequestTimeout = 45 * time.Second
	preloadQueueBaseTimeout     = 12 * time.Second
	preloadQueuePerTrackTimeout = 250 * time.Millisecond
	preloadQueueMaxTimeout      = 3 * time.Minute
)

type pollMsg struct {
	status       *spotify.PlaybackStatus
	queue        []spotify.QueueItem
	queueFetched bool
	queueErr     error
	err          error
}

type actionMsg struct {
	action    string
	err       error
	rollback  *spotify.PlaybackStatus
	reconcile bool
}

type actionReconcileMsg struct {
	err          error
	rollback     *spotify.PlaybackStatus
	status       *spotify.PlaybackStatus
	queue        []spotify.QueueItem
	queueFetched bool
}

type playlistsMsg struct {
	items   []spotify.PlaylistSummary
	offset  int
	limit   int
	hasMore bool
	err     error
}

type playlistTracksMsg struct {
	playlistID string
	trackIDs   []string
	trackInfos []spotify.QueueItem
	nextOffset int
	hasMore    bool
	token      int
	err        error
}

type preloadQueueMsg struct {
	trackIDs []string
	err      error
}

type imageLoadedMsg struct {
	url string
	err error
}

type tickMsg time.Time

type navDebounceMsg struct {
	token int
}

type playbackStateMsg struct {
	status       *spotify.PlaybackStatus
	queue        []spotify.QueueItem
	queueHasMore bool
}

func StartPlaybackStateListener(playbackStateCh <-chan *librespot.PlaybackStateUpdate, send func(tea.Msg), ctx context.Context) {
	go func() {
		for {
			select {
			case u := <-playbackStateCh:
				if u == nil {
					continue
				}
				status, queue, queueHasMore := PlaybackStateFromLibrespot(u)
				send(playbackStateMsg{status: status, queue: queue, queueHasMore: queueHasMore})
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

func (m model) pollCmd(fetchQueue bool) tea.Cmd {
	if m.tuiCmdCh != nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, pollRequestTimeout)
		defer cancel()

		if !fetchQueue {
			status, err := m.service.Status(ctx)
			if err != nil {
				return pollMsg{err: err}
			}
			return pollMsg{status: status}
		}

		var status *spotify.PlaybackStatus
		var statusErr error
		var queue []spotify.QueueItem
		var queueErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			status, statusErr = m.service.Status(ctx)
		}()
		go func() {
			defer wg.Done()
			queue, queueErr = m.service.GetQueue(ctx)
		}()
		wg.Wait()

		if statusErr != nil {
			return pollMsg{err: statusErr}
		}
		if queueErr != nil {
			return pollMsg{status: status, queueErr: queueErr}
		}
		return pollMsg{status: status, queue: queue, queueFetched: true}
	}
}

func (m model) actionCmd(fn func(context.Context) error, action string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
		defer cancel()
		return actionMsg{action: action, err: fn(ctx)}
	}
}

func (m model) actionWithReconcileCmd(fn func(context.Context) error, rollback *spotify.PlaybackStatus) tea.Cmd {
	if m.service == nil {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
			defer cancel()
			if err := fn(ctx); err != nil {
				return actionReconcileMsg{err: err, rollback: rollback}
			}
			return actionReconcileMsg{}
		}
	}
	svc := m.service
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
		defer cancel()

		if err := fn(ctx); err != nil {
			return actionReconcileMsg{err: err, rollback: rollback}
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
		if statusErr != nil {
			return actionReconcileMsg{}
		}
		if queueErr != nil {
			return actionReconcileMsg{status: status, queueFetched: false}
		}
		return actionReconcileMsg{status: status, queue: queue, queueFetched: true}
	}
}

func (m model) reconcileCmd() tea.Cmd {
	if m.service == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
		defer cancel()
		var status *spotify.PlaybackStatus
		var statusErr error
		var queue []spotify.QueueItem
		var queueErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			status, statusErr = m.service.Status(ctx)
		}()
		go func() {
			defer wg.Done()
			queue, queueErr = m.service.GetQueue(ctx)
		}()
		wg.Wait()
		if statusErr != nil {
			return actionReconcileMsg{err: statusErr}
		}
		if queueErr != nil {
			return actionReconcileMsg{status: status, queue: nil, queueFetched: false}
		}
		return actionReconcileMsg{status: status, queue: queue, queueFetched: true}
	}
}

const playlistAPIPageSize = 50

func (m model) loadPlaylistsCmd(offset, limit int) tea.Cmd {
	var catalog spotify.PlaylistCatalog
	if m.catalog != nil {
		catalog = m.catalog
	} else if m.service != nil {
		catalog = m.service
	} else {
		return nil
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = playlistAPIPageSize
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, playlistPageRequestTimeout)
		defer cancel()
		if offset == 0 {
			all := make([]spotify.PlaylistSummary, 0, playlistAPIPageSize)
			pageOffset := 0
			for {
				page, err := catalog.ListUserPlaylistsPage(ctx, pageOffset, playlistAPIPageSize)
				if err != nil {
					return playlistsMsg{offset: 0, limit: limit, err: err}
				}
				all = append(all, page.Items...)
				if !page.HasMore || len(page.Items) == 0 {
					break
				}
				pageOffset = page.NextOffset
				if len(all) >= playlistLoadMax {
					break
				}
			}
			return playlistsMsg{
				items:   all,
				offset:  0,
				limit:   len(all),
				hasMore: false,
			}
		}
		page, err := catalog.ListUserPlaylistsPage(ctx, offset, limit)
		if err != nil {
			return playlistsMsg{offset: offset, limit: limit, err: err}
		}
		return playlistsMsg{
			items:   page.Items,
			offset:  offset,
			limit:   limit,
			hasMore: page.HasMore,
		}
	}
}

func (m model) getCurrentUserIDCmd() tea.Cmd {
	var catalog spotify.PlaylistCatalog
	if m.catalog != nil {
		catalog = m.catalog
	} else if m.service != nil {
		catalog = m.service
	} else {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, catalogRequestTimeout)
		defer cancel()
		id, err := catalog.CurrentUserID(ctx)
		if err != nil {
			return currentUserIDMsg{err: err}
		}
		return currentUserIDMsg{userID: id}
	}
}

func (m model) loadImageCmd(url string) tea.Cmd {
	if url == "" {
		return nil
	}
	cache := m.imgs
	if !cache.beginLoad(url) {
		return nil
	}
	coverSizes := m.currentCoverSizes()
	return func() tea.Msg {
		defer cache.finishLoad(url)

		ctx, cancel := context.WithTimeout(m.ctx, imageFetchTimeout)
		defer cancel()

		select {
		case imgSemaphore <- struct{}{}:
			defer func() { <-imgSemaphore }()
		case <-ctx.Done():
			return imageLoadedMsg{url: url, err: ctx.Err()}
		}

		if _, ok := cache.getImage(url); ok {
			cache.preRenderCovers(url, coverSizes)
			return imageLoadedMsg{url: url}
		}

		img, err := fetchImageFromBytes(ctx, url)
		if err != nil {
			return imageLoadedMsg{url: url, err: err}
		}
		cache.setImage(url, img)
		cache.preRenderCovers(url, coverSizes)
		return imageLoadedMsg{url: url}
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

func (m model) loadPlaylistTracksCmd(playlistID string, offset int, token int) tea.Cmd {
	var catalog spotify.PlaylistCatalog
	if m.catalog != nil {
		catalog = m.catalog
	} else if m.service != nil {
		catalog = m.service
	} else {
		return nil
	}
	if playlistID == "" || offset < 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, playlistTrackRequestTimeout)
		defer cancel()
		if offset == 0 {
			first, err := catalog.ListPlaylistTrackIDsPage(ctx, playlistID, 0, playlistTrackPageSize)
			if err != nil {
				return playlistTracksMsg{playlistID: playlistID, token: token, err: err}
			}
			all := append([]string(nil), first.TrackIDs...)
			allInfos := append([]spotify.QueueItem(nil), first.TrackInfos...)

			if !first.HasMore || first.NextOffset <= 0 || len(all) >= playlistTrackPreloadMax {
				return playlistTracksMsg{
					playlistID: playlistID,
					trackIDs:   all,
					trackInfos: allInfos,
					nextOffset: len(all),
					hasMore:    false,
					token:      token,
				}
			}

			type pageResult struct {
				idx  int
				page *spotify.PlaylistTrackPage
				err  error
			}
			pageStart := first.NextOffset
			var pageOffsets []int
			for off := pageStart; off < playlistTrackPreloadMax; off += playlistTrackPageSize {
				pageOffsets = append(pageOffsets, off)
			}
			results := make([]pageResult, len(pageOffsets))
			var wg sync.WaitGroup
			for i, off := range pageOffsets {
				wg.Add(1)
				go func(idx, pageOff int) {
					defer wg.Done()
					limit := min(playlistTrackPageSize, playlistTrackPreloadMax-pageOff)
					pg, pErr := catalog.ListPlaylistTrackIDsPage(ctx, playlistID, pageOff, limit)
					results[idx] = pageResult{idx: idx, page: pg, err: pErr}
				}(i, off)
			}
			wg.Wait()

			for _, r := range results {
				if r.err != nil {
					break
				}
				if r.page == nil {
					break
				}
				all = append(all, r.page.TrackIDs...)
				allInfos = append(allInfos, r.page.TrackInfos...)
				if !r.page.HasMore {
					break
				}
			}
			return playlistTracksMsg{
				playlistID: playlistID,
				trackIDs:   all,
				trackInfos: allInfos,
				nextOffset: len(all),
				hasMore:    false,
				token:      token,
			}
		}
		if offset >= playlistTrackPreloadMax {
			return playlistTracksMsg{
				playlistID: playlistID,
				trackIDs:   nil,
				nextOffset: offset,
				hasMore:    false,
				token:      token,
			}
		}
		limit := min(playlistTrackPageSize, playlistTrackPreloadMax-offset)
		page, err := catalog.ListPlaylistTrackIDsPage(ctx, playlistID, offset, limit)
		if err != nil {
			return playlistTracksMsg{playlistID: playlistID, token: token, err: err}
		}
		nextOffset := page.NextOffset
		if nextOffset > playlistTrackPreloadMax {
			nextOffset = playlistTrackPreloadMax
		}
		hasMore := page.HasMore && nextOffset < playlistTrackPreloadMax
		return playlistTracksMsg{
			playlistID: playlistID,
			trackIDs:   page.TrackIDs,
			trackInfos: page.TrackInfos,
			nextOffset: nextOffset,
			hasMore:    hasMore,
			token:      token,
		}
	}
}

func (m model) preloadQueueCmd(trackIDs []string) tea.Cmd {
	if m.service == nil || len(trackIDs) == 0 {
		return nil
	}
	toQueue := append([]string(nil), trackIDs...)
	return func() tea.Msg {
		timeout := preloadQueueBaseTimeout + time.Duration(len(toQueue))*preloadQueuePerTrackTimeout
		if timeout > preloadQueueMaxTimeout {
			timeout = preloadQueueMaxTimeout
		}
		ctx, cancel := context.WithTimeout(m.ctx, timeout)
		defer cancel()
		queued, err := m.service.QueueTracks(ctx, m.deviceName, toQueue)
		return preloadQueueMsg{trackIDs: queued, err: err}
	}
}
