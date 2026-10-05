package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"
)

// Thumbnail placements share the cover's ID allocator, but own their data so
// moving the preview cannot delete an image still used by a list row.
type thumbnailPlacement struct {
	id      uint64
	url     string
	encoded string
	art     artRect
}

func (m model) kittyThumbnailOverlayBytes() (string, bool) {
	cache := m.ui.imgs
	if cache == nil {
		return "", false
	}
	wanted := m.thumbnailPlacements()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.thumbnails == nil {
		cache.thumbnails = make(map[int]thumbnailPlacement)
	}
	var out strings.Builder
	content := false
	for slot, shown := range cache.thumbnails {
		if _, ok := wanted[slot]; !ok {
			out.WriteString(deleteKittyImageData(shown.id))
			delete(cache.thumbnails, slot)
		}
	}
	// Emit in screen order (not map order) to keep output deterministic.
	for slot := 0; slot < len(wanted); slot++ {
		next := wanted[slot]
		shown := cache.thumbnails[slot]
		if next.url == shown.url && next.art == shown.art && next.encoded == shown.encoded {
			continue
		}
		if next.encoded == "" {
			out.WriteString(deleteKittyImageData(shown.id))
			delete(cache.thumbnails, slot)
			continue
		}
		cache.overlay.nextID++
		next.id = cache.overlay.nextID
		fmt.Fprintf(&out, "\x1b[%d;%dH", next.art.row, next.art.col)
		out.WriteString(buildKittyPayload(next.encoded, next.art.cols, next.art.rows, next.id))
		out.WriteString(deleteKittyImageData(shown.id))
		cache.thumbnails[slot] = next
		content = true
	}
	return out.String(), content
}

func (m model) thumbnailPlacements() map[int]thumbnailPlacement {
	wanted := make(map[int]thumbnailPlacement)
	if m.ui.imgs.protocolForRender() != imageProtocolKitty ||
		tooSmallFrame(m.ui.width, m.ui.height) || m.modalKind() != modalNone || m.startupPending() {
		return wanted
	}
	layout := m.bodyLayout()
	col := layout.leftW + 1
	if m.ui.width < 64 {
		col = 1
	}
	var rows list.Model
	row := bodyStartRow1Based
	switch m.ui.activeTab {
	case tabSearch:
		rows = m.browse.search.list
		// Section title, status, divider and query input.
		row += 4
	case tabSongs:
		rows = m.browse.songsList
		// Section title, count, divider and the list's filter/title row.
		row += 4
		if rows.FilterState() == list.Filtering {
			row++ // Filter title has one bottom padding row.
		}
	default:
		return wanted
	}
	items := rows.VisibleItems()
	if len(items) == 0 || rows.Paginator.PerPage <= 0 {
		return wanted
	}
	start, end := rows.Paginator.GetSliceBounds(len(items))
	for slot, item := range items[start:end] {
		url := ""
		switch item := item.(type) {
		case searchResultItem:
			url = item.result.ImageURL
		case trackItem:
			url = item.item.ImageURL
		}
		art := artRect{row: row + slot*(searchThumbHeight+1), col: col, cols: searchThumbWidth, rows: searchThumbHeight}
		// Never place over the footer or outside the viewport on tiny terminals.
		if art.row+art.rows > bodyStartRow1Based+layout.bodyH || art.col+art.cols-1 > m.ui.width {
			break
		}
		wanted[slot] = thumbnailPlacement{url: url, encoded: m.ui.imgs.encodedFor(url), art: art}
	}
	return wanted
}
