package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/spotify"
)

func (m model) playlistsTabView() string {
	layout := m.getBodyLayout()
	left := m.coverPreviewPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := verticalDivider(layout.bodyH)
	right := m.playlistBrowserPanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) playlistBrowserPanel(w, h int) string {
	count := len(m.browse.playlistList.Items())
	label := styleSectionLabel.Render("Playlists")
	countStr := styleDimmed.Render(fmt.Sprintf("%d playlists", count))
	labelLine := label + "\n" + countStr + "\n" + sectionDivider(w-1)

	var inner string
	if m.browse.playlistsErr != nil && len(m.browse.playlistList.Items()) == 0 {
		errStr := "failed to load: " + m.browse.playlistsErr.Error()
		rateHint := ""
		if strings.Contains(m.browse.playlistsErr.Error(), "429") || strings.Contains(strings.ToLower(m.browse.playlistsErr.Error()), "rate limit") {
			rateHint = "\n" + styleDimmed.Render("Run 'orpheus auth login' to use your own API quota.")
		}
		inner = styleError.Render(truncate(errStr, w-2)) + rateHint + "\n" + styleDimmed.Render("r to retry")
	} else if m.browse.playlistsLoading && len(m.browse.playlistList.Items()) == 0 {
		inner = styleDimmed.Render("loading library...")
	} else if len(m.browse.playlistList.Items()) == 0 {
		inner = styleDimmed.Render("No playlists yet — press r to refresh")
	} else {
		inner = m.browse.playlistList.View()
	}
	if m.browse.albumsForbidden {
		inner += "\n" + styleDimmed.Render("saved albums unavailable: re-run 'orpheus auth login' (needs user-library-read)")
	}

	if m.transport.playbackErr != nil {
		diag := spotify.DiagnoseError(m.transport.playbackErr)
		errLine := m.transport.playbackErr.Error()
		if diag.Category != "" && diag.Category != "unknown" {
			errLine = diag.Category + ": " + errLine
		}
		if diag.NextStep != "" {
			errLine += " — " + diag.NextStep
		}
		inner = inner + "\n" + styleError.Render(truncate(errLine, max(12, w-2)))
	}

	content := labelLine + "\n" + inner
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) coverPreviewPanel(w, h, coverCols, coverRows int) string {
	label := styleSectionLabel.Render("Preview")
	labelLine := label + "\n" + sectionDivider(w)
	innerW := w - 2

	var coverStr string
	pl, plOk := m.selectedPlaylist()
	if plOk && pl.summary.ImageURL != "" {
		url := pl.summary.ImageURL
		if m.ui.imgs != nil && m.ui.imgs.protocol == imageProtocolKitty {
			if m.ui.imgs.hasKittyEncoding(url) {
				coverStr = m.blankArt(coverCols, coverRows)
			} else {
				coverStr = m.placeholderArt(coverCols, coverRows)
			}
		} else if s, cached := m.ui.imgs.cover(url, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = m.placeholderArt(coverCols, coverRows)
		}
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	meta := ""
	if plOk {
		ownerLine := "playlist by " + truncate(pl.summary.Owner, innerW)
		if pl.summary.Kind == spotify.ContextKindAlbum {
			ownerLine = "album by " + truncate(pl.summary.Owner, innerW)
		}
		meta = "\n" +
			stylePlaylistName.Render(truncate(pl.summary.Name, innerW)) + "\n" +
			stylePlaylistOwner.Render(ownerLine)
	} else {
		meta = "\n" + styleDimmed.Render("select an item")
	}

	content := labelLine + "\n" + coverStr + meta
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) playbackScreenView() string {
	layout := m.getBodyLayout()
	left := m.albumCoverPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := verticalDivider(layout.bodyH)
	right := m.queuePanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) albumsTabView() string {
	layout := m.getBodyLayout()
	left := m.albumPreviewPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := verticalDivider(layout.bodyH)
	right := m.albumBrowserPanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) albumBrowserPanel(w, h int) string {
	count := len(m.browse.albumList.Items())
	label := styleSectionLabel.Render("Albums")
	countStr := styleDimmed.Render(fmt.Sprintf("%d albums", count))
	labelLine := label + "\n" + countStr + "\n" + sectionDivider(w-1)

	var inner string
	if m.browse.playlistsErr != nil && len(m.browse.albumList.Items()) == 0 {
		errStr := "failed to load: " + m.browse.playlistsErr.Error()
		inner = styleError.Render(truncate(errStr, w-2)) + "\n" + styleDimmed.Render("r to retry")
	} else if m.browse.playlistsLoading && len(m.browse.albumList.Items()) == 0 {
		inner = styleDimmed.Render("loading albums...")
	} else if m.browse.albumsForbidden && len(m.browse.albumList.Items()) == 0 {
		inner = styleDimmed.Render("saved albums unavailable — re-run 'orpheus auth login' (needs user-library-read)")
	} else if len(m.browse.albumList.Items()) == 0 {
		inner = styleDimmed.Render("No saved albums yet — press r to refresh")
	} else {
		inner = m.browse.albumList.View()
	}

	content := labelLine + "\n" + inner
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) albumPreviewPanel(w, h, coverCols, coverRows int) string {
	label := styleSectionLabel.Render("Preview")
	labelLine := label + "\n" + sectionDivider(w)
	innerW := w - 2

	var coverStr string
	al, alOk := m.selectedAlbum()
	if alOk && al.summary.ImageURL != "" {
		url := al.summary.ImageURL
		if m.ui.imgs != nil && m.ui.imgs.protocol == imageProtocolKitty {
			if m.ui.imgs.hasKittyEncoding(url) {
				coverStr = m.blankArt(coverCols, coverRows)
			} else {
				coverStr = m.placeholderArt(coverCols, coverRows)
			}
		} else if s, cached := m.ui.imgs.cover(url, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = m.placeholderArt(coverCols, coverRows)
		}
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	meta := ""
	if alOk {
		meta = "\n" +
			stylePlaylistName.Render(truncate(al.summary.Name, innerW)) + "\n" +
			stylePlaylistOwner.Render("album by "+truncate(al.summary.Owner, innerW))
	} else {
		meta = "\n" + styleDimmed.Render("select an album")
	}

	content := labelLine + "\n" + coverStr + meta
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) albumCoverPanel(w, h, coverCols, coverRows int) string {
	label := styleSectionLabel.Render("Now Playing")
	labelLine := label + "\n" + sectionDivider(w-1)
	innerW := w - 2

	var coverStr string
	if m.transport.status != nil && m.transport.status.AlbumImageURL != "" {
		url := m.transport.status.AlbumImageURL
		if m.ui.imgs != nil && m.ui.imgs.protocol == imageProtocolKitty {
			if m.ui.imgs.hasKittyEncoding(url) {
				coverStr = m.blankArt(coverCols, coverRows)
			} else {
				coverStr = m.placeholderArt(coverCols, coverRows)
			}
		} else if s, cached := m.ui.imgs.cover(url, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = m.placeholderArt(coverCols, coverRows)
		}
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	var meta string
	if m.transport.status != nil {
		trackName := m.transport.status.TrackName
		artistName := m.transport.status.ArtistName
		albumName := m.transport.status.AlbumName
		if trackName == "" {
			trackName = "Unknown track"
		}
		if artistName == "" {
			artistName = "-"
		}
		meta = "\n" +
			styleTrackName.Render(truncate(trackName, innerW)) + "\n" +
			styleArtistName.Render(truncate(artistName, innerW))
		if albumName != "" {
			meta += "\n" + styleAlbumName.Render(truncate(albumName, innerW))
		}
	} else {
		meta = "\n" + styleDimmed.Render("nothing playing")
	}

	content := labelLine + "\n" + coverStr + meta
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) queuePanel(w, h int) string {
	label := styleSectionLabel.Render("Up Next")
	divLine := sectionDivider(w)

	grid := queueGridFor(w)
	colHeader := grid.header()
	colDivider := sectionDivider(w)

	headerLines := 4 // label, divider, column header, divider
	errLines := 0
	if m.transport.playbackErr != nil {
		errLines = 2
	}
	rowBudget := max(0, h-headerLines-errLines)

	lines := []string{label, divLine, colHeader, colDivider}

	displayQueue := m.visibleQueue()
	currentID := ""
	if m.transport.status != nil {
		currentID = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
	}

	if m.transport.status == nil {
		lines = append(lines, styleDimmed.Render("  nothing playing"))
	}

	if len(displayQueue) == 0 {
		if m.transport.status != nil {
			lines = append(lines, styleDimmed.Render("  queue is empty"))
		}
	} else {
		maxRows := max(0, rowBudget-1) // reserve the "+ more" line
		window := min(len(displayQueue), maxRows)
		cursor := min(m.transport.queueCursor, len(displayQueue)-1)
		start := 0
		if cursor >= maxRows && maxRows > 0 {
			start = cursor - maxRows + 1
		}
		for i := range window {
			qi := start + i
			q := displayQueue[qi]
			row := grid.row(w, qi+1, q.Name, q.Artist, q.DurationMS, currentID != "" && golibrespot.NormalizeSpotifyId(q.ID) == currentID, qi == cursor)
			lines = append(lines, row)
		}

		stableVisibleQueueLen := m.transport.stableQueueLen
		if hidCurrent := len(m.transport.queue) > 0 && len(displayQueue) == len(m.transport.queue)-1; hidCurrent && stableVisibleQueueLen > 0 {
			stableVisibleQueueLen--
		}
		notVisible := max(0, stableVisibleQueueLen-(start+window))
		if notVisible > 0 || m.transport.queueHasMore {
			marker := "+ more"
			if notVisible > 0 && !m.transport.queueHasMore {
				marker = fmt.Sprintf("+ %d more", notVisible)
			}
			lines = append(lines, styleDimmed.Render("  "+marker))
		}
	}

	if m.transport.playbackErr != nil {
		diag := spotify.DiagnoseError(m.transport.playbackErr)
		errLine := m.transport.playbackErr.Error()
		if diag.Category != "" && diag.Category != "unknown" {
			errLine = diag.Category + ": " + errLine
		}
		if diag.NextStep != "" {
			errLine += " — " + diag.NextStep
		}
		lines = append(lines, "", styleError.Render(truncate(errLine, max(12, w-2))))
	}

	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) blankArt(cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	line := strings.Repeat(" ", cols)
	var sb strings.Builder
	sb.WriteString(line)
	for i := 1; i < rows; i++ {
		sb.WriteByte('\n')
		sb.WriteString(line)
	}
	return sb.String()
}

func (m model) placeholderArt(cols, rows int) string {
	if cols <= 2 || rows <= 2 {
		return ""
	}
	key := placeholderCacheKey{cols, rows, themeEpoch}
	if cached, ok := placeholderCache.get(key); ok {
		return cached
	}
	style := stylePlaceholderBorder
	top := style.Render("\u256d" + strings.Repeat("\u2500", cols-2) + "\u256e")
	mid := style.Render("\u2502" + strings.Repeat(" ", cols-2) + "\u2502")
	bot := style.Render("\u2570" + strings.Repeat("\u2500", cols-2) + "\u256f")

	midRows := rows - 2
	var sb strings.Builder
	sb.WriteString(top)
	for range midRows {
		sb.WriteByte('\n')
		sb.WriteString(mid)
	}
	sb.WriteByte('\n')
	sb.WriteString(bot)
	out := sb.String()
	placeholderCache.put(key, out)
	return out
}

// queueGrid is the shared column layout for the up-next panel's header and
// rows: one reserved marker gutter, a right-aligned index, flexible
// title/artist columns and a right-aligned duration, all inside the panel
// width. Header and rows come from the same constants so the grid aligns.
type queueGrid struct {
	lead    int
	idxW    int
	titleW  int
	artistW int
	durW    int
}

func queueGridFor(w int) queueGrid {
	g := queueGrid{lead: 1, idxW: 4, durW: 8}
	g.durW = min(g.durW, max(4, (w-9)/6))
	budget := w - g.lead - g.idxW - 1 - 2 - 1 - g.durW // title+artist
	g.artistW = min(20, max(6, budget*2/5))
	if g.artistW > max(0, budget-4) {
		g.artistW = max(0, budget-4)
	}
	g.titleW = max(4, budget-g.artistW)
	if budget < 8 {
		g.titleW = max(4, budget)
		g.artistW = 0
	}
	if g.lead+g.idxW+1+g.titleW+2+g.artistW+1+g.durW > w {
		g.titleW = max(4, g.titleW-(g.lead+g.idxW+1+g.titleW+2+g.artistW+1+g.durW-w))
	}
	return g
}

func (g queueGrid) header() string {
	var b strings.Builder
	b.WriteString(styleQueueHeader.Render(strings.Repeat(" ", g.lead+g.idxW+1) + padCell("Title", g.titleW)))
	if g.artistW > 0 {
		b.WriteString("  " + styleQueueHeader.Render(padCell("Artist", g.artistW)))
	}
	b.WriteString(" " + styleQueueHeader.Render(alignRight("Len", g.durW)))
	return b.String()
}

// row renders one queue row: unstyled cells padded to the grid, then exactly
// one style applied over the whole padded row (single-owner selection).
func (g queueGrid) row(w, num int, title, artist string, durMS int, playing, selected bool) string {
	name := truncate(title, g.titleW)
	artist = truncate(artist, g.artistW)
	dur := ""
	if durMS > 0 {
		dur = fmtDuration(durMS)
	}

	var b strings.Builder
	b.WriteString(strings.Repeat(" ", g.lead))
	b.WriteString(alignRight(fmt.Sprintf("%d.", num), g.idxW))
	b.WriteString(" ")
	b.WriteString(padCell(name, g.titleW))
	if g.artistW > 0 {
		b.WriteString("  ")
		b.WriteString(padCell(artist, g.artistW))
	}
	b.WriteString(" ")
	b.WriteString(alignRight(dur, g.durW))

	row := b.String()
	if pad := w - lipgloss.Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	switch {
	case selected && w >= 40:
		return styleQueueSelected.Render(row)
	case playing:
		return styleQueuePlaying.Render(strings.TrimRight(row, " ") + " " + iconNowPlaying + " ")
	case selected:
		return styleQueueCursor.Render(row)
	default:
		return styleQueueTrack.Render(row)
	}
}

const iconNowPlaying = "♪"
