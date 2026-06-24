package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

const playlistAPIPageSize = 50

func (m model) loadPlaylistsCmd(offset, limit int) tea.Cmd {
	catalog := m.resolveCatalog()
	if catalog == nil {
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
			type plResult struct {
				items []spotify.PlaylistSummary
				err   error
			}
			type alResult struct {
				items           []spotify.PlaylistSummary
				albumsForbidden bool
				err             error
			}
			plCh := make(chan plResult, 1)
			alCh := make(chan alResult, 1)

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
				return playlistsMsg{offset: 0, limit: limit, err: pr.err}
			}
			ar := <-alCh
			if ar.err != nil {
				return playlistsMsg{offset: 0, limit: limit, err: ar.err}
			}

			all := make([]spotify.PlaylistSummary, 0, len(pr.items)+len(ar.items))
			all = append(all, pr.items...)
			all = append(all, ar.items...)
			return playlistsMsg{
				items:           all,
				offset:          0,
				limit:           len(all),
				hasMore:         false,
				albumsForbidden: ar.albumsForbidden,
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
	catalog := m.resolveCatalog()
	if catalog == nil {
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
		if err := cache.ensureKittyEncoding(url, img, displayCols, displayRows); err != nil {
			return imageLoadedMsg{url: url, err: err}
		}
		cache.preRenderCovers(url, coverSizes)
		return imageLoadedMsg{url: url}
	}
}

func (m model) loadPlaylistItemsCmd(playlistID string, offset int, token int) tea.Cmd {
	catalog := m.resolveCatalog()
	if catalog == nil {
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
					slog.Warn("playlist preload page failed", "error", r.err)
					continue
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
		nextOffset := min(page.NextOffset, playlistItemPreloadMax)
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
		msgs := make([]imageLoadedMsg, 0, len(results))
		for _, r := range results {
			url := validURLs[r.Index]
			if r.Error != nil {
				msgs = append(msgs, imageLoadedMsg{url: url, err: r.Error})
				continue
			}
			imgData, ok := r.Data.(loader.ImageData)
			if !ok {
				msgs = append(msgs, imageLoadedMsg{url: url, err: fmt.Errorf("unexpected result type")})
				continue
			}
			img, _, err := image.Decode(bytes.NewReader(imgData.Data))
			if err != nil {
				msgs = append(msgs, imageLoadedMsg{url: url, err: fmt.Errorf("decode image: %w", err)})
				continue
			}
			coverSizes := m.currentCoverSizes()
			displayCols, displayRows := 0, 0
			if len(coverSizes) > 0 {
				displayCols, displayRows = coverSizes[0][0], coverSizes[0][1]
			}
			m.ui.imgs.setImage(url, img, displayCols, displayRows)
			if err := m.ui.imgs.ensureKittyEncoding(url, img, displayCols, displayRows); err != nil {
				msgs = append(msgs, imageLoadedMsg{url: url, err: err})
				continue
			}
			m.ui.imgs.preRenderCovers(url, coverSizes)
			msgs = append(msgs, imageLoadedMsg{url: url})
		}
		return imagesBatchLoadedMsg{results: msgs}
	}
}
