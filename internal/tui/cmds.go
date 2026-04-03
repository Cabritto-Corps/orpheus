package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
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
	statusQueueCacheTTL        = 120 * time.Millisecond
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
	token     uint64
	action    string
	err       error
	rollback  *spotify.PlaybackStatus
	reconcile bool
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
	m.stateFetchToken++
	return m.stateFetchToken
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
		status, queue, statusErr, queueErr := fetchStatusAndQueue(ctx, m.service, true, m.statusQueueCache)

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
	if m.service == nil {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
			defer cancel()
			if err := fn(ctx); err != nil {
				return actionReconcileMsg{token: token, err: err, rollback: rollback}
			}
			return actionReconcileMsg{token: token}
		}
	}
	svc := m.service
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionRequestTimeout)
		defer cancel()

		if err := fn(ctx); err != nil {
			return actionReconcileMsg{token: token, err: err, rollback: rollback}
		}

		status, queue, statusErr, queueErr := fetchStatusAndQueue(ctx, svc, false, m.statusQueueCache)
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
		status, queue, statusErr, queueErr := fetchStatusAndQueue(ctx, m.service, true, m.statusQueueCache)
		if statusErr != nil {
			return actionReconcileMsg{token: token, err: statusErr}
		}
		if queueErr != nil {
			return actionReconcileMsg{token: token, status: status, queue: nil, queueFetched: false}
		}
		return actionReconcileMsg{token: token, status: status, queue: queue, queueFetched: true}
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
			playlistOffset := 0
			albumOffset := 0
			playlistMore := true
			albumMore := true
			albumsForbidden := false

			for playlistMore || albumMore {
				if playlistMore {
					pageSize := playlistAPIPageSize
					page, err := catalog.ListUserPlaylistsPage(ctx, playlistOffset, pageSize)
					if err != nil {
						return playlistsMsg{offset: 0, limit: limit, err: err}
					}
					if len(page.Items) > 0 {
						all = append(all, page.Items...)
					}
					playlistMore = page.HasMore && len(page.Items) > 0
					playlistOffset = page.NextOffset
				}

				if albumMore {
					pageSize := playlistAPIPageSize
					page, err := catalog.ListSavedAlbumsPage(ctx, albumOffset, pageSize)
					if err != nil {
						if spotify.IsForbidden(err) {
							albumsForbidden = true
							albumMore = false
						} else {
							return playlistsMsg{offset: 0, limit: limit, err: err}
						}
					} else {
						if len(page.Items) > 0 {
							all = append(all, page.Items...)
						}
						albumMore = page.HasMore && len(page.Items) > 0
						albumOffset = page.NextOffset
					}
				}
			}
			return playlistsMsg{
				items:           all,
				offset:          0,
				limit:           len(all),
				hasMore:         false,
				albumsForbidden: albumsForbidden,
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

func (m *model) loadImageCmd(url string, priority bool) tea.Cmd {
	if url == "" {
		return nil
	}
	cache := m.imgs
	if priority {
		cache.clearFailed(url)
	}
	if !cache.beginLoad(url) {
		return nil
	}
	if m.loader == nil {
		cache.finishLoad(url)
		return nil
	}
	return func() tea.Msg {
		defer cache.finishLoad(url)

		if img, ok := cache.getImage(url); ok {
			displayCols, displayRows := 0, 0
			coverSizes := m.currentCoverSizes()
			if len(coverSizes) > 0 {
				displayCols, displayRows = coverSizes[0][0], coverSizes[0][1]
			}
			if err := cache.ensureKittyEncoding(url, img, displayCols, displayRows); err != nil {
				return imageLoadedMsg{url: url, err: err}
			}
			cache.preRenderCovers(url, coverSizes)
			return imageLoadedMsg{url: url}
		}

		results := m.loader.Execute(LoadRequest{
			Type:    LoadTypeImage,
			Items:   []LoadItem{{URL: url}},
			Timeout: imageFetchTimeout,
		})
		if len(results) == 0 || results[0].Error != nil {
			if len(results) > 0 {
				return imageLoadedMsg{url: url, err: results[0].Error}
			}
			return imageLoadedMsg{url: url, err: fmt.Errorf("no results")}
		}

		imgData, ok := results[0].Data.(ImageData)
		if !ok {
			return imageLoadedMsg{url: url, err: fmt.Errorf("unexpected result type")}
		}
		img, _, err := image.Decode(bytes.NewReader(imgData.Data))
		if err != nil {
			return imageLoadedMsg{url: url, err: fmt.Errorf("decode image: %w", err)}
		}

		displayCols, displayRows := 0, 0
		coverSizes := m.currentCoverSizes()
		if len(coverSizes) > 0 {
			displayCols, displayRows = coverSizes[0][0], coverSizes[0][1]
		}
		cache.setImage(url, img, displayCols, displayRows)
		if err := cache.ensureKittyEncoding(url, img, displayCols, displayRows); err != nil {
			return imageLoadedMsg{url: url, err: err}
		}
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

func (m model) imageRetryCmd(url string, attempt int, token int) tea.Cmd {
	delay := time.Duration(attempt) * 200 * time.Millisecond
	if delay > 1200*time.Millisecond {
		delay = 1200 * time.Millisecond
	}
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

func (m model) loadPlaylistItemsCmd(playlistID string, offset int, token int) tea.Cmd {
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
		ctx, cancel := context.WithTimeout(m.ctx, playlistItemRequestTimeout)
		defer cancel()
		if offset == 0 {
			first, err := catalog.ListPlaylistItemsPage(ctx, playlistID, 0, playlistItemPageSize)
			if err != nil {
				return playlistItemsMsg{playlistID: playlistID, token: token, err: err}
			}
			all := append([]string(nil), first.ItemIDs...)
			allInfos := append([]spotify.QueueItem(nil), first.ItemInfos...)

			if !first.HasMore || first.NextOffset <= 0 || len(all) >= playlistItemPreloadMax {
				return playlistItemsMsg{
					playlistID: playlistID,
					itemIDs:    all,
					itemInfos:  allInfos,
					nextOffset: len(all),
					hasMore:    false,
					token:      token,
				}
			}

			type pageResult struct {
				idx  int
				page *spotify.PlaylistItemsPage
				err  error
			}
			pageStart := first.NextOffset
			var pageOffsets []int
			for off := pageStart; off < playlistItemPreloadMax; off += playlistItemPageSize {
				pageOffsets = append(pageOffsets, off)
			}
			results := make([]pageResult, len(pageOffsets))
			var wg sync.WaitGroup
			for i, off := range pageOffsets {
				wg.Add(1)
				go func(idx, pageOff int) {
					defer wg.Done()
					limit := min(playlistItemPageSize, playlistItemPreloadMax-pageOff)
					pg, pErr := catalog.ListPlaylistItemsPage(ctx, playlistID, pageOff, limit)
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
				all = append(all, r.page.ItemIDs...)
				allInfos = append(allInfos, r.page.ItemInfos...)
				if !r.page.HasMore {
					break
				}
			}
			return playlistItemsMsg{
				playlistID: playlistID,
				itemIDs:    all,
				itemInfos:  allInfos,
				nextOffset: len(all),
				hasMore:    false,
				token:      token,
			}
		}
		if offset >= playlistItemPreloadMax {
			return playlistItemsMsg{
				playlistID: playlistID,
				itemIDs:    nil,
				nextOffset: offset,
				hasMore:    false,
				token:      token,
			}
		}
		limit := min(playlistItemPageSize, playlistItemPreloadMax-offset)
		page, err := catalog.ListPlaylistItemsPage(ctx, playlistID, offset, limit)
		if err != nil {
			return playlistItemsMsg{playlistID: playlistID, token: token, err: err}
		}
		nextOffset := page.NextOffset
		if nextOffset > playlistItemPreloadMax {
			nextOffset = playlistItemPreloadMax
		}
		hasMore := page.HasMore && nextOffset < playlistItemPreloadMax
		return playlistItemsMsg{
			playlistID: playlistID,
			itemIDs:    page.ItemIDs,
			itemInfos:  page.ItemInfos,
			nextOffset: nextOffset,
			hasMore:    hasMore,
			token:      token,
		}
	}
}

func (m model) resolveContextImageURLCmd(kind, id string) tea.Cmd {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return nil
	}
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
		url, err := catalog.ResolveContextImageURL(ctx, kind, id)
		return coverImageResolvedMsg{
			kind: kind,
			id:   id,
			url:  strings.TrimSpace(url),
			err:  err,
		}
	}
}

func (m model) resolveContextImageURLsBatchCmd(items []struct{ Kind, ID string }) tea.Cmd {
	if len(items) == 0 {
		return nil
	}
	var catalog spotify.PlaylistCatalog
	if m.catalog != nil {
		catalog = m.catalog
	} else if m.service != nil {
		catalog = m.service
	} else {
		return nil
	}
	return func() tea.Msg {
		results := make([]coverImageResolvedMsg, 0, len(items))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 20)
		var mu sync.Mutex
		for _, item := range items {
			wg.Add(1)
			go func(kind, id string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				ctx, cancel := context.WithTimeout(m.ctx, catalogRequestTimeout)
				defer cancel()
				url, err := catalog.ResolveContextImageURL(ctx, kind, id)
				mu.Lock()
				results = append(results, coverImageResolvedMsg{
					kind: kind,
					id:   id,
					url:  strings.TrimSpace(url),
					err:  err,
				})
				mu.Unlock()
			}(item.Kind, item.ID)
		}
		wg.Wait()
		return coverImageURLsBatchResolvedMsg{results: results}
	}
}

func (m *model) loadImagesBatchCmd(urls []string) tea.Cmd {
	if len(urls) == 0 {
		return nil
	}
	validURLs := make([]string, 0, len(urls))
	for _, url := range urls {
		if url == "" {
			continue
		}
		if !m.imgs.beginLoad(url) {
			continue
		}
		validURLs = append(validURLs, url)
	}
	if len(validURLs) == 0 {
		return nil
	}
	return func() tea.Msg {
		results := make([]imageLoadedMsg, 0, len(validURLs))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		var mu sync.Mutex
		for _, u := range validURLs {
			wg.Add(1)
			go func(url string) {
				defer wg.Done()
				defer m.imgs.finishLoad(url)
				sem <- struct{}{}
				defer func() { <-sem }()
				ctx, cancel := context.WithTimeout(m.ctx, imageFetchTimeout)
				defer cancel()
				_, err := httpImageProvider{}.Fetch(ctx, url)
				mu.Lock()
				if err != nil {
					results = append(results, imageLoadedMsg{url: url, err: err})
				} else {
					results = append(results, imageLoadedMsg{url: url})
				}
				mu.Unlock()
			}(u)
		}
		wg.Wait()
		return imagesBatchLoadedMsg{results: results}
	}
}
