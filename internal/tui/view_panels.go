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
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
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
			if m.ui.imgs.hasImage(url) {
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
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
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
	} else {
		inner = m.browse.albumList.View()
	}

	content := labelLine + "\n" + inner
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
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
			if m.ui.imgs.hasImage(url) {
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
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) albumCoverPanel(w, h, coverCols, coverRows int) string {
	label := styleSectionLabel.Render("Now Playing")
	labelLine := label + "\n" + sectionDivider(w-1)
	innerW := w - 2

	var coverStr string
	if m.transport.status != nil && m.transport.status.AlbumImageURL != "" {
		url := m.transport.status.AlbumImageURL
		if m.ui.imgs != nil && m.ui.imgs.protocol == imageProtocolKitty {
			if m.ui.imgs.hasImage(url) {
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
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) queuePanel(w, h int) string {
	label := styleSectionLabel.Render("Up Next")
	divLine := sectionDivider(w)
	contentLines := h - 4

	idxW := 3
	contentW := max(12, w-idxW-4)
	artistW := min(20, max(6, contentW*2/5))
	durW := 5
	titleW := max(4, contentW-artistW-durW-2)

	colHeader := styleQueueHeader.Render(
		strings.Repeat(" ", 1+idxW+1) + fmt.Sprintf("%-*s  %-*s %-5s", titleW, "Title", artistW, "Artist", "Len"),
	)
	colDivider := sectionDivider(w)

	lines := []string{label, divLine, colHeader, colDivider}

	displayQueue := m.transport.queue
	hidCurrentFromQueue := false
	if m.transport.status != nil && len(displayQueue) > 0 {
		currentID := golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
		if currentID != "" && golibrespot.NormalizeSpotifyId(displayQueue[0].ID) == currentID {
			displayQueue = displayQueue[1:]
			hidCurrentFromQueue = true
		}
	}
	if m.transport.status == nil {
		lines = append(lines, styleDimmed.Render("  nothing playing"))
	}

	if len(displayQueue) == 0 {
		lines = append(lines, styleDimmed.Render("  queue is empty"))
	} else {
		maxRows := contentLines - 2
		n := min(len(displayQueue), maxRows)
		for i := range n {
			q := displayQueue[i]
			idx := styleQueueIndex.Render(fmt.Sprintf("%*d.", idxW-1, i+1))
			title := truncate(q.Name, titleW)
			artist := truncate(q.Artist, artistW)
			dur := ""
			if q.DurationMS > 0 {
				dur = fmtDuration(q.DurationMS)
			}
			titlePad := titleW - lipgloss.Width(title)
			artistPad := artistW - lipgloss.Width(artist)
			if titlePad < 0 {
				titlePad = 0
			}
			if artistPad < 0 {
				artistPad = 0
			}
			lines = append(lines, " "+idx+" "+styleQueueTrack.Render(title)+strings.Repeat(" ", titlePad)+"  "+styleQueueArtist.Render(artist)+strings.Repeat(" ", artistPad)+" "+stylePlayerTime.Render(dur))
		}

		stableVisibleQueueLen := m.transport.stableQueueLen
		if hidCurrentFromQueue && stableVisibleQueueLen > 0 {
			stableVisibleQueueLen--
		}
		notVisible := max(0, stableVisibleQueueLen-n)
		if notVisible > 0 {
			if m.transport.queueHasMore {
				lines = append(lines, styleDimmed.Render("  + more"))
			} else {
				lines = append(lines, styleDimmed.Render(fmt.Sprintf("  + %d more", notVisible)))
			}
		} else if m.transport.queueHasMore {
			lines = append(lines, styleDimmed.Render("  + more"))
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
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
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
	style := stylePlaceholderBorder
	top := style.Render("╭" + strings.Repeat("─", cols-2) + "╮")
	mid := style.Render("│" + strings.Repeat(" ", cols-2) + "│")
	bot := style.Render("╰" + strings.Repeat("─", cols-2) + "╯")

	midRows := rows - 2
	var sb strings.Builder
	sb.WriteString(top)
	for range midRows {
		sb.WriteByte('\n')
		sb.WriteString(mid)
	}
	sb.WriteByte('\n')
	sb.WriteString(bot)
	return sb.String()
}
