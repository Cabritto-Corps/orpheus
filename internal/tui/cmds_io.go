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

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

const playlistAPIPageSize = 50

func (m model) loadSongsLibraryCmd(contexts []spotify.PlaylistSummary) tea.Cmd {
	catalog := m.resolveCatalog()
	if catalog == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
		defer cancel()
		collectionTracks := make([]spotify.QueueItem, 0)
		var firstErr error
		recent, err := catalog.ListRecentlyPlayedTracks(ctx, 50)
		if err != nil {
			firstErr = err
			recent = nil
		}
		priorityTracks := mergeSongTracks(
			[]spotify.QueueItem{playbackQueueItem(m.transport.status)},
			m.browse.songsSessionRecentTracks,
			recent,
			m.browse.songsSavedTracks,
		)
		known := make(map[string]struct{}, maxSongsInSongsTab)
		for _, track := range priorityTracks {
			known[songIdentity(track.ID)] = struct{}{}
		}
		slots := max(0, maxSongsInSongsTab-len(priorityTracks))
		scanBudget := min(maxSongsInSongsTab, slots)
		loadContext := func(kind, id string) {
			offset := 0
			for slots > 0 && scanBudget > 0 {
				var page *spotify.PlaylistItemsPage
				var err error
				limit := min(playlistAPIPageSize, scanBudget)
				if kind == spotify.ContextKindAlbum {
					page, err = catalog.ListAlbumTracksPage(ctx, id, offset, limit)
				} else {
					page, err = catalog.ListPlaylistItemsPage(ctx, id, offset, limit)
				}
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					return
				}
				advanced := max(len(page.ItemInfos), page.NextOffset-offset)
				scanBudget -= min(scanBudget, advanced)
				for _, track := range page.ItemInfos {
					key := songIdentity(track.ID)
					if key == "" {
						continue
					}
					if kind == spotify.ContextKindAlbum && track.ImageURL == "" {
						track.ImageURL = contextImageURL(contexts, id)
					}
					if _, exists := known[key]; exists {
						// Retain duplicate metadata so the final ordered merge can
						// fill fields missing from a higher-priority source.
						collectionTracks = append(collectionTracks, track)
						continue
					}
					known[key] = struct{}{}
					collectionTracks = append(collectionTracks, track)
					slots--
					if slots == 0 {
						return
					}
				}
				if !page.HasMore || page.NextOffset <= offset {
					return
				}
				offset = page.NextOffset
			}
		}
		for _, context := range contexts {
			if slots == 0 || scanBudget == 0 {
				break
			}
			if err := ctx.Err(); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			if context.ID != "" && (context.Kind == spotify.ContextKindAlbum || context.Kind == spotify.ContextKindPlaylist) {
				loadContext(context.Kind, context.ID)
			}
		}
		return songsLibraryMsg{recentTracks: recent, collectionTracks: collectionTracks, err: firstErr}
	}
}

func contextImageURL(contexts []spotify.PlaylistSummary, id string) string {
	for _, context := range contexts {
		if context.Kind == spotify.ContextKindAlbum && context.ID == id {
			return context.ImageURL
		}
	}
	return ""
}

func (m model) loadPlaylistsCmd() tea.Cmd {
	catalog := m.resolveCatalog()
	if catalog == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, playlistPageRequestTimeout)
		defer cancel()
		type plResult struct {
			items []spotify.PlaylistSummary
			err   error
		}
		type alResult struct {
			items           []spotify.PlaylistSummary
			albumsForbidden bool
			err             error
		}
		type savedResult struct {
			items []spotify.QueueItem
			err   error
		}
		plCh := make(chan plResult, 1)
		alCh := make(chan alResult, 1)
		savedCh := make(chan savedResult, 1)

		go func() {
			var all []spotify.PlaylistSummary
			playlistOffset := 0
			playlistMore := true
			for playlistMore {
				page, err := catalog.ListUserPlaylistsPage(ctx, playlistOffset, playlistAPIPageSize)
				if err != nil {
					plCh <- plResult{err: err}
					return
				}
				if len(page.Items) > 0 {
					all = append(all, page.Items...)
				}
				playlistMore = page.HasMore && len(page.Items) > 0
				playlistOffset = page.NextOffset
			}
			plCh <- plResult{items: all}
		}()

		go func() {
			var all []spotify.QueueItem
			offset := 0
			more := true
			for more && len(all) < maxSongsInSongsTab {
				limit := min(playlistAPIPageSize, maxSongsInSongsTab-len(all))
				page, err := catalog.ListSavedTracksPage(ctx, offset, limit)
				if err != nil {
					savedCh <- savedResult{err: err}
					return
				}
				all = append(all, page.ItemInfos[:min(len(page.ItemInfos), maxSongsInSongsTab-len(all))]...)
				more = page.HasMore && len(page.ItemInfos) > 0
				offset = page.NextOffset
			}
			savedCh <- savedResult{items: all}
		}()

		go func() {
			var all []spotify.PlaylistSummary
			albumOffset := 0
			albumMore := true
			albumsForbidden := false
			for albumMore {
				page, err := catalog.ListSavedAlbumsPage(ctx, albumOffset, playlistAPIPageSize)
				if err != nil {
					if spotify.IsForbidden(err) {
						albumsForbidden = true
						albumMore = false
					} else {
						alCh <- alResult{err: err}
						return
					}
				} else {
					if len(page.Items) > 0 {
						all = append(all, page.Items...)
					}
					albumMore = page.HasMore && len(page.Items) > 0
					albumOffset = page.NextOffset
				}
			}
			alCh <- alResult{items: all, albumsForbidden: albumsForbidden}
		}()

		pr := <-plCh
		if pr.err != nil {
			return playlistsMsg{err: pr.err}
		}
		ar := <-alCh
		if ar.err != nil {
			return playlistsMsg{err: ar.err}
		}
		sr := <-savedCh
		if sr.err != nil {
			return playlistsMsg{err: sr.err}
		}

		all := make([]spotify.PlaylistSummary, 0, len(pr.items)+len(ar.items))
		all = append(all, pr.items...)
		all = append(all, ar.items...)
		return playlistsMsg{
			items:           all,
			savedTracks:     sr.items,
			albumsForbidden: ar.albumsForbidden,
		}
	}
}

func (m *model) loadImageCmd(url string, priority bool) tea.Cmd {
	if url == "" {
		return nil
	}
	cache := m.ui.imgs
	if priority {
		cache.clearFailed(url)
	}
	if !cache.beginLoad(url) {
		return nil
	}
	if m.ldr == nil {
		cache.finishLoad(url)
		return nil
	}
	return func() tea.Msg {
		defer cache.finishLoad(url)

		if img, ok := cache.getImage(url); ok {
			coverSizes := m.currentCoverSizes()
			if err := cache.ensureKittyEncoding(url, img); err != nil {
				return imageLoadedMsg{url: url, err: err}
			}
			cache.preRenderCovers(url, coverSizes, m.styles.colorProfile)
			return imageLoadedMsg{url: url}
		}

		results := m.ldr.Execute(loader.LoadRequest{
			Type:    loader.LoadTypeImage,
			Items:   []loader.LoadItem{{URL: url}},
			Timeout: imageFetchTimeout,
		})
		if len(results) == 0 || results[0].Error != nil {
			if len(results) > 0 {
				return imageLoadedMsg{url: url, err: results[0].Error}
			}
			return imageLoadedMsg{url: url, err: fmt.Errorf("no results")}
		}

		imgData, ok := results[0].Data.(loader.ImageData)
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
		if err := cache.ensureKittyEncoding(url, img); err != nil {
			return imageLoadedMsg{url: url, err: err}
		}
		cache.preRenderCovers(url, coverSizes, m.styles.colorProfile)
		return imageLoadedMsg{url: url}
	}
}

func (m model) resolveContextImageURLCmd(kind, id string) tea.Cmd {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return nil
	}
	if m.ldr == nil {
		return nil
	}
	return func() tea.Msg {
		results := m.ldr.Execute(loader.LoadRequest{
			Type:    loader.LoadTypeContextImageURL,
			Items:   []loader.LoadItem{{Kind: kind, ID: id}},
			Timeout: catalogRequestTimeout,
		})
		if len(results) == 0 {
			return coverImageResolvedMsg{kind: kind, id: id}
		}
		r := results[0]
		if r.Error != nil {
			return coverImageResolvedMsg{kind: kind, id: id, err: r.Error}
		}
		urlData, ok := r.Data.(loader.ImageURLData)
		if !ok {
			return coverImageResolvedMsg{kind: kind, id: id, err: fmt.Errorf("unexpected result type %T", r.Data)}
		}
		return coverImageResolvedMsg{kind: kind, id: id, url: strings.TrimSpace(urlData.URL)}
	}
}

func (m model) resolveContextImageURLsBatchCmd(items []struct{ Kind, ID string }) tea.Cmd {
	if len(items) == 0 {
		return nil
	}
	if m.ldr == nil {
		return nil
	}
	loadItems := make([]loader.LoadItem, 0, len(items))
	for _, item := range items {
		loadItems = append(loadItems, loader.LoadItem{Kind: item.Kind, ID: item.ID})
	}
	return func() tea.Msg {
		results := m.ldr.Execute(loader.LoadRequest{
			Type:    loader.LoadTypeContextImageURL,
			Items:   loadItems,
			Timeout: catalogRequestTimeout,
		})
		msgs := make([]coverImageResolvedMsg, 0, len(results))
		for _, r := range results {
			urlData, ok := r.Data.(loader.ImageURLData)
			if !ok {
				urlData = loader.ImageURLData{}
			}
			msgs = append(msgs, coverImageResolvedMsg{
				kind: items[r.Index].Kind,
				id:   items[r.Index].ID,
				url:  strings.TrimSpace(urlData.URL),
				err:  r.Error,
			})
		}
		return coverImageURLsBatchResolvedMsg{results: msgs}
	}
}

func (m *model) loadImagesBatchCmd(urls []string) tea.Cmd {
	if len(urls) == 0 {
		return nil
	}
	if m.ldr == nil {
		return nil
	}
	validURLs := make([]string, 0, len(urls))
	for _, url := range urls {
		if url == "" {
			continue
		}
		if !m.ui.imgs.beginLoad(url) {
			continue
		}
		validURLs = append(validURLs, url)
	}
	if len(validURLs) == 0 {
		return nil
	}
	return func() tea.Msg {
		loadItems := make([]loader.LoadItem, 0, len(validURLs))
		for _, url := range validURLs {
			loadItems = append(loadItems, loader.LoadItem{URL: url})
		}
		results := m.ldr.Execute(loader.LoadRequest{
			Type:    loader.LoadTypeImage,
			Items:   loadItems,
			Timeout: imageFetchTimeout,
		})
		coverSizes := m.currentCoverSizes()
		decodeCols, decodeRows := 0, 0
		if len(coverSizes) > 0 {
			decodeCols, decodeRows = coverSizes[0][0], coverSizes[0][1]
		}
		msgs := make([]imageLoadedMsg, len(results))
		answered := make(map[int]struct{}, len(results))
		done := make(chan indexedImageMsg, len(results))
		psem := make(chan struct{}, 8)
		var pwg sync.WaitGroup
		for ri, r := range results {
			if r.Index >= 0 && r.Index < len(validURLs) {
				answered[r.Index] = struct{}{}
			}
			url := validURLs[r.Index]
			if r.Error != nil {
				msgs[ri] = imageLoadedMsg{url: url, err: r.Error}
				continue
			}
			imgData, ok := r.Data.(loader.ImageData)
			if !ok {
				msgs[ri] = imageLoadedMsg{url: url, err: fmt.Errorf("unexpected result type")}
				continue
			}
			pwg.Add(1)
			go func(idx int, data []byte, target string) {
				defer pwg.Done()
				psem <- struct{}{}
				defer func() { <-psem }()
				img, _, err := image.Decode(bytes.NewReader(data))
				if err != nil {
					done <- indexedImageMsg{idx, imageLoadedMsg{url: target, err: fmt.Errorf("decode image: %w", err)}}
					return
				}
				m.ui.imgs.setImage(target, img, decodeCols, decodeRows)
				if err := m.ui.imgs.ensureKittyEncoding(target, img); err != nil {
					done <- indexedImageMsg{idx, imageLoadedMsg{url: target, err: err}}
					return
				}
				m.ui.imgs.preRenderCovers(target, coverSizes, m.styles.colorProfile)
				done <- indexedImageMsg{idx, imageLoadedMsg{url: target}}
			}(ri, imgData.Data, url)
		}
		pwg.Wait()
		close(done)
		for im := range done {
			msgs[im.idx] = im.msg
		}
		// The loader can return fewer results than requested (e.g. pool
		// context done); synthesize failures so inflight flags don't leak.
		for i := range validURLs {
			if _, ok := answered[i]; ok {
				continue
			}
			msgs = append(msgs, imageLoadedMsg{url: validURLs[i], err: loader.ErrLoadFailed})
		}
		return imagesBatchLoadedMsg{results: msgs}
	}
}
