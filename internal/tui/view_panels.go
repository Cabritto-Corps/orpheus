package tui

import (
	"fmt"
	"strings"
	"sync"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"

	"orpheus/internal/spotify"
)

// stablePreview remembers the last unfiltered browse selection per list so
// the cover preview never chases the filter cursor: opening search resets
// the list cursor to the top and every keystroke re-seats the match under
// it, so a live preview would swap art (and in kitty mode retransmit the
// whole image plus delete the displaced one) per keystroke. Readers call
// through the model helpers below on every frame: unfiltered frames refresh
// the memory, filtering frames serve it. Pointer-shared like the render
// caches, so every model copy agrees; a nil receiver serves live.
type stablePreview struct {
	mu          sync.Mutex
	playlist    playlistItem
	hasPlaylist bool
	album       playlistItem
	hasAlbum    bool
}

func (s *stablePreview) forPlaylists(filtering bool, live playlistItem, ok bool) (playlistItem, bool) {
	if s == nil {
		return live, ok
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !filtering {
		s.playlist, s.hasPlaylist = live, ok
		return live, ok
	}
	if !s.hasPlaylist {
		return live, ok
	}
	return s.playlist, true
}

func (s *stablePreview) forAlbums(filtering bool, live playlistItem, ok bool) (playlistItem, bool) {
	if s == nil {
		return live, ok
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !filtering {
		s.album, s.hasAlbum = live, ok
		return live, ok
	}
	if !s.hasAlbum {
		return live, ok
	}
	return s.album, true
}

// stablePlaylistSelection is the preview subject: the live cursor, except
// while the filter input is open, when it holds the pre-filter selection.
// Playback actions keep reading the live cursor; only display freezes.
func (m model) stablePlaylistSelection() (playlistItem, bool) {
	live, ok := m.selectedPlaylist()
	if m.styles == nil || m.styles.previewStable == nil {
		return live, ok
	}
	return m.styles.previewStable.forPlaylists(m.browse.playlistList.FilterState() == list.Filtering, live, ok)
}

func (m model) stableAlbumSelection() (playlistItem, bool) {
	live, ok := m.selectedAlbum()
	if m.styles == nil || m.styles.previewStable == nil {
		return live, ok
	}
	return m.styles.previewStable.forAlbums(m.browse.albumList.FilterState() == list.Filtering, live, ok)
}

// startupPending: the startup reveal gate. Content panels hold placeholders
// until the session is attached AND the first library load has resolved —
// everything then comes up together instead of trickling in.
func (m model) startupPending() bool {
	return m.transport.playerConnecting || !m.browse.librarySettled
}

func (m model) playlistsTabView() string {
	layout := m.bodyLayout()
	left := m.coverPreviewPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := m.styles.verticalDivider(layout.bodyH)
	right := m.playlistBrowserPanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) playlistBrowserPanel(w, h int) string {
	count := len(m.browse.playlistList.Items())
	label := m.styles.styleSectionLabel.Render("Playlists")
	countStr := m.styles.styleDimmed.Render(fmt.Sprintf("%d playlists", count))
	labelLine := label + "\n" + countStr + "\n" + m.styles.sectionDivider(w-1)

	var inner string
	if m.browse.playlistsErr != nil && len(m.browse.playlistList.Items()) == 0 {
		errStr := "failed to load: " + m.browse.playlistsErr.Error()
		rateHint := ""
		if hint, ok := spotify.RateLimitHint(m.browse.playlistsErr); ok {
			rateHint = "\n" + m.styles.styleDimmed.Render(hint)
		}
		inner = m.styles.styleError.Render(truncate(errStr, w-2)) + rateHint + "\n" + m.styles.styleDimmed.Render("r to retry")
	} else if m.startupPending() {
		// A stale-looking "No playlists yet" reads as data loss; hold the
		// connecting placeholder until the session itself is attached.
		if m.transport.playerConnecting {
			inner = m.styles.styleDimmed.Render(m.ui.spinner.View() + " connecting to Spotify...")
		} else {
			inner = m.styles.styleDimmed.Render(m.ui.spinner.View() + " loading library...")
		}
	} else if m.browse.playlistsLoading && len(m.browse.playlistList.Items()) == 0 {
		inner = m.styles.styleDimmed.Render(m.ui.spinner.View() + " loading library...")
	} else if len(m.browse.playlistList.Items()) == 0 {
		inner = m.styles.styleDimmed.Render("No playlists yet — press r to refresh")
	} else {
		inner = m.browse.playlistList.View()
	}
	if m.browse.albumsForbidden {
		inner += "\n" + m.styles.styleDimmed.Render("saved albums unavailable: re-run 'orpheus auth login' (needs user-library-read)")
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
		inner = inner + "\n" + m.styles.styleError.Render(truncate(errLine, max(12, w-2)))
	}

	content := labelLine + "\n" + inner
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) coverPreviewPanel(w, h, coverCols, coverRows int) string {
	label := m.styles.styleSectionLabel.Render("Preview")
	labelLine := label + "\n" + m.styles.sectionDivider(w)
	innerW := w - 2

	var coverStr string
	pl, plOk := m.stablePlaylistSelection()
	if plOk && pl.summary.ImageURL != "" {
		coverStr = m.coverOrPlaceholder(pl.summary.ImageURL, coverCols, coverRows)
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
			m.styles.stylePlaylistName.Render(truncate(pl.summary.Name, innerW)) + "\n" +
			m.styles.stylePlaylistOwner.Render(ownerLine)
	} else {
		meta = "\n" + m.styles.styleDimmed.Render("select an item")
	}

	content := labelLine + "\n" + coverStr + meta
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) playbackScreenView() string {
	layout := m.bodyLayout()
	left := m.albumCoverPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := m.styles.verticalDivider(layout.bodyH)
	right := m.queuePanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) albumsTabView() string {
	layout := m.bodyLayout()
	left := m.albumPreviewPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := m.styles.verticalDivider(layout.bodyH)
	right := m.albumBrowserPanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) albumBrowserPanel(w, h int) string {
	count := len(m.browse.albumList.Items())
	label := m.styles.styleSectionLabel.Render("Albums")
	countStr := m.styles.styleDimmed.Render(fmt.Sprintf("%d albums", count))
	labelLine := label + "\n" + countStr + "\n" + m.styles.sectionDivider(w-1)

	var inner string
	if m.browse.playlistsErr != nil && len(m.browse.albumList.Items()) == 0 {
		errStr := "failed to load: " + m.browse.playlistsErr.Error()
		rateHint := ""
		if hint, ok := spotify.RateLimitHint(m.browse.playlistsErr); ok {
			rateHint = "\n" + m.styles.styleDimmed.Render(hint)
		}
		inner = m.styles.styleError.Render(truncate(errStr, w-2)) + rateHint + "\n" + m.styles.styleDimmed.Render("r to retry")
	} else if m.startupPending() {
		// Same gate as the playlists panel: never show "No saved albums yet"
		// while the session itself is still attaching.
		if m.transport.playerConnecting {
			inner = m.styles.styleDimmed.Render(m.ui.spinner.View() + " connecting to Spotify...")
		} else {
			inner = m.styles.styleDimmed.Render(m.ui.spinner.View() + " loading albums...")
		}
	} else if m.browse.playlistsLoading && len(m.browse.albumList.Items()) == 0 {
		inner = m.styles.styleDimmed.Render(m.ui.spinner.View() + " loading albums...")
	} else if m.browse.albumsForbidden && len(m.browse.albumList.Items()) == 0 {
		inner = m.styles.styleDimmed.Render("saved albums unavailable — re-run 'orpheus auth login' (needs user-library-read)")
	} else if len(m.browse.albumList.Items()) == 0 {
		inner = m.styles.styleDimmed.Render("No saved albums yet — press r to refresh")
	} else {
		inner = m.browse.albumList.View()
	}

	content := labelLine + "\n" + inner
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) albumPreviewPanel(w, h, coverCols, coverRows int) string {
	label := m.styles.styleSectionLabel.Render("Preview")
	labelLine := label + "\n" + m.styles.sectionDivider(w)
	innerW := w - 2

	if m.startupPending() {
		// No cover, no meta until the reveal: art ahead of the connection is
		// exactly the early render this panel must not do.
		inner := m.styles.styleDimmed.Render(m.ui.spinner.View() + " connecting to Spotify...")
		content := labelLine + "\n" + inner
		return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
	}

	var coverStr string
	al, alOk := m.stableAlbumSelection()
	if alOk && al.summary.ImageURL != "" {
		coverStr = m.coverOrPlaceholder(al.summary.ImageURL, coverCols, coverRows)
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	meta := ""
	if alOk {
		meta = "\n" +
			m.styles.stylePlaylistName.Render(truncate(al.summary.Name, innerW)) + "\n" +
			m.styles.stylePlaylistOwner.Render("album by "+truncate(al.summary.Owner, innerW))
	} else {
		meta = "\n" + m.styles.styleDimmed.Render("select an album")
	}

	content := labelLine + "\n" + coverStr + meta
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) albumCoverPanel(w, h, coverCols, coverRows int) string {
	label := m.styles.styleSectionLabel.Render("Now Playing")
	labelLine := label + "\n" + m.styles.sectionDivider(w-1)
	innerW := w - 2

	if m.transport.playerConnecting {
		// Same reveal gate: placeholder art here renders before the session
		// exists, and "nothing playing" would deny the connecting state.
		inner := m.styles.styleDimmed.Render(m.ui.spinner.View() + " connecting to Spotify...")
		content := labelLine + "\n" + inner
		return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
	}
	if m.transport.status == nil && m.startupPending() {
		// Attached but library still loading, nothing playing yet: hold.
		inner := m.styles.styleDimmed.Render(m.ui.spinner.View() + " loading library...")
		content := labelLine + "\n" + inner
		return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
	}

	var coverStr string
	if m.transport.status != nil && m.transport.status.AlbumImageURL != "" {
		coverStr = m.coverOrPlaceholder(m.transport.status.AlbumImageURL, coverCols, coverRows)
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
			m.styles.styleTrackName.Render(truncate(trackName, innerW)) + "\n" +
			m.styles.styleArtistName.Render(truncate(artistName, innerW))
		if albumName != "" {
			meta += "\n" + m.styles.styleAlbumName.Render(truncate(albumName, innerW))
		}
	} else {
		meta = "\n" + m.styles.styleDimmed.Render("nothing playing")
	}

	content := labelLine + "\n" + coverStr + meta
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

func (m model) queuePanel(w, h int) string {
	label := m.styles.styleSectionLabel.Render("Up Next")
	divLine := m.styles.sectionDivider(w)

	grid := queueGridFor(w)
	colHeader := grid.header(m.styles)
	colDivider := m.styles.sectionDivider(w)

	headerLines := 4 // label, divider, column header, divider
	errLines := 0
	if m.transport.playbackErr != nil {
		errLines = 2
	}
	rowBudget := max(0, h-headerLines-errLines)

	lines := []string{label, divLine, colHeader, colDivider}

	displayQueue := m.visibleQueue()

	if m.transport.status == nil {
		if m.transport.playerConnecting {
			lines = append(lines, m.styles.styleDimmed.Render("  connecting to Spotify…"))
		} else if m.startupPending() {
			lines = append(lines, m.styles.styleDimmed.Render("  loading library..."))
		} else {
			lines = append(lines, m.styles.styleDimmed.Render("  nothing playing"))
		}
	}

	if len(displayQueue) == 0 {
		if m.transport.status != nil {
			lines = append(lines, m.styles.styleDimmed.Render("  queue is empty"))
		}
	} else {
		maxRows := max(0, rowBudget-1) // reserve the "+ more" line
		cursor := min(m.transport.queueCursor, len(displayQueue)-1)
		window, start := scrollRows(displayQueue, cursor, maxRows)
		for i, q := range window {
			qi := start + i
			row := grid.row(m.styles, w, qi+1, q.Name, q.Artist, q.DurationMS, qi == cursor)
			lines = append(lines, row)
		}

		stableVisibleQueueLen := m.transport.stableQueueLen
		if hidCurrent := len(m.transport.queue) > 0 && len(displayQueue) == len(m.transport.queue)-1; hidCurrent && stableVisibleQueueLen > 0 {
			stableVisibleQueueLen--
		}
		notVisible := max(0, stableVisibleQueueLen-(start+len(window)))
		if notVisible > 0 || m.transport.queueHasMore {
			marker := "+ more"
			if notVisible > 0 && !m.transport.queueHasMore {
				marker = fmt.Sprintf("+ %d more", notVisible)
			}
			lines = append(lines, m.styles.styleDimmed.Render("  "+marker))
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
		lines = append(lines, "", m.styles.styleError.Render(truncate(errLine, max(12, w-2))))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(content)
}

// Both renderers derive from the single coverArt, so ANSI and kitty can
// never disagree; the protocol reads under the cache lock so render sites
// don't race on the field.
func (m model) coverOrPlaceholder(url string, cols, rows int) string {
	rect := m.coverArt(cols, rows)
	if m.ui.imgs == nil {
		return m.placeholderArt(cols, rows)
	}
	if m.ui.imgs.protocolForRender() == imageProtocolKitty {
		if m.ui.imgs.hasKittyEncoding(url) {
			if rect.framed {
				return m.styles.coverFrameBox(cols, rows)
			}
			return m.blankArt(cols, rows)
		}
		return m.placeholderArt(cols, rows)
	}
	if s, ok := m.ui.imgs.cover(url, rect.cols, rect.rows, m.styles.colorProfile); ok {
		if rect.framed {
			return m.styles.coverFrameBoxWith(s, cols, rows)
		}
		return s
	}
	return m.placeholderArt(cols, rows)
}

// Blank by design: the kitty image sits over these cells at the inset offset.
func (s *themeStyles) coverFrameBox(cols, rows int) string {
	return s.coverFrameBoxWith(strings.Repeat(" ", cols-2), cols, rows)
}

func (s *themeStyles) coverFrameBoxWith(art string, cols, rows int) string {
	border, _ := s.coverFrameBorder()
	// lipgloss v2 Width/Height are total block budgets: the cell dims
	// already include the border ring, so an exactly-sized render must
	// not be shrunk again (that re-wraps every art row).
	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(s.colorBlue).
		Width(cols).
		Height(rows).
		Render(art)
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
	key := placeholderCacheKey{cols, rows}
	if cached, ok := m.styles.placeholder.get(key); ok {
		return cached
	}
	// Render an empty interior and let the style supply the full cell.
	out := lipgloss.NewStyle().
		Border(m.styles.themeBorder()).
		BorderForeground(m.styles.colorDivider).
		Width(cols).
		Height(rows).
		Render("")
	m.styles.placeholder.put(key, out)
	return out
}

// Header and rows share the constants so the grid aligns.
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
	g.artistW = min(min(20, max(6, budget*2/5)), max(0, budget-4))
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

func (g queueGrid) header(s *themeStyles) string {
	var b strings.Builder
	b.WriteString(s.styleQueueHeader.Render(strings.Repeat(" ", g.lead+g.idxW+1) + padCell("Title", g.titleW)))
	if g.artistW > 0 {
		b.WriteString("  " + s.styleQueueHeader.Render(padCell("Artist", g.artistW)))
	}
	b.WriteString(" " + s.styleQueueHeader.Render(alignRight("Len", g.durW)))
	return b.String()
}

// One style owns the whole padded row (single-owner selection).
func (g queueGrid) row(s *themeStyles, w, num int, title, artist string, durMS int, selected bool) string {
	name := truncate(title, g.titleW)
	artist = truncate(artist, g.artistW)
	dur := ""
	if durMS > 0 {
		dur = fmtDuration(durMS)
	}

	var b strings.Builder
	// The marker swaps one lead space, so row widths never change; mono terminals need it.
	lead := strings.Repeat(" ", g.lead)
	if mark := s.themeCursorGlyph(); selected && mark != "" {
		lead = mark + strings.Repeat(" ", max(0, g.lead-1))
	}
	b.WriteString(lead)
	b.WriteString(alignRight(fmt.Sprintf("%d.", num), g.idxW))
	b.WriteString(" ")
	b.WriteString(padCell(name, g.titleW))
	if g.artistW > 0 {
		b.WriteString("  ")
		b.WriteString(padCell(artist, g.artistW))
	}
	b.WriteString(" ")
	b.WriteString(alignRight(truncate(dur, g.durW), g.durW))

	row := b.String()
	if pad := w - lipgloss.Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	switch {
	case selected && w >= 40:
		return s.styleQueueSelected.Render(row)
	case selected:
		return s.styleQueueCursor.Render(row)
	default:
		return s.styleQueueTrack.Render(row)
	}
}
