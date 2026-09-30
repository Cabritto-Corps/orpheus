package tui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	golibrespot "github.com/elxgy/go-librespot"
)

func (m model) headerView() string {
	w := m.ui.width

	var statusStr, centerL1, rightL1 string

	if m.transport.status != nil {
		playIcon, pauseIcon := m.playPauseGlyphs()
		if m.transport.status.Playing {
			statusStr = m.styles.styleHeaderPlaying.Render("[" + playIcon + " Playing]")
		} else {
			statusStr = m.styles.styleHeaderPaused.Render("[" + pauseIcon + " Paused]")
		}

		volBar := m.headerVolumeBar(m.transport.status.Volume)
		volText := m.styles.styleHeaderVolume.Render(fmt.Sprintf("%3d%%", m.transport.status.Volume))
		rightL1 = volBar + " " + volText
		if m.transport.status.ShuffleState {
			rightL1 += "  " + m.styles.styleDimmed.Render(m.icon(iconShuffle, iconShuffleNF))
		}
		if m.transport.status.RepeatTrack {
			rightL1 += "  " + m.styles.styleDimmed.Render(m.icon(iconRepeatTrack, iconRepeatTrackNF))
		} else if m.transport.status.RepeatContext {
			rightL1 += "  " + m.styles.styleDimmed.Render(m.icon(iconRepeatContext, iconRepeatContextNF))
		}

		availCenterW := max(10, w-lipgloss.Width(statusStr)-lipgloss.Width(rightL1)-2)
		trackName := m.transport.status.TrackName
		if trackName == "" {
			trackName = "Unknown track"
		}
		if m.transport.transition.Pending() {
			// The pushed status still describes the outgoing track while a
			// transition is pending; mark it so it never reads as "paused".
			trackName = truncate(trackName, max(availCenterW-1, 1)) + "…"
		}
		centerL1 = m.styles.styleHeaderCenter.Render(truncate(trackName, availCenterW))
	} else {
		statusStr = m.styles.styleHeaderPaused.Render("Orpheus")
		centerL1 = m.styles.styleHeaderSub.Render("no active playback")
		device := m.icon(iconDevice, iconDeviceNF) + " " + m.deviceName
		// Line 1 also carries the volume/status side only in the playing
		// state; here the device string must fit the full width or it wraps
		// and corrupts the chrome height.
		rightL1 = m.styles.styleHeaderStatus.Render(truncate(device, max(1, w-lipgloss.Width(statusStr)-2)))
	}

	line1 := layoutThreeZone(w, statusStr, centerL1, rightL1)

	var centerL2 string
	if m.transport.status != nil {
		artist := m.transport.status.ArtistName
		album := m.transport.status.AlbumName
		if artist == "" {
			artist = "-"
		}
		parts := artist
		if album != "" {
			parts += "  •  " + album
		}
		centerL2 = m.styles.styleHeaderSub.Render(truncate(parts, max(1, w-2)))
	}
	line2 := layoutThreeZone(w, "", centerL2, "")

	sep := m.styles.sectionDivider(w)
	return line1 + "\n" + line2 + "\n" + sep
}

func layoutThreeZone(w int, left, center, right string) string {
	leftW := lipgloss.Width(left)

	// The right zone must always fit: truncate it only when it alone
	// overflows what the left zone leaves of the row.
	right = fitCell(right, max(0, w-leftW))
	rightW := lipgloss.Width(right)

	// The centre zone owns exactly the space left of the fixed-size side
	// zones; it is truncated BEFORE joining so the row can never exceed the
	// terminal (a style would wrap instead of clip). The centered title is
	// positioned absolutely on the terminal, so it must additionally clear
	// both halves: shrink it until neither side zone reaches into the
	// centered block, else the row overflows by the overlap.
	centerBudget := min(max(0, w-leftW-rightW-2), max(0, w-2*leftW), max(0, w-2*rightW))
	center = fitCell(center, centerBudget)
	centerW := lipgloss.Width(center)

	// The title is centered on the terminal, not between the side zones:
	// growing a side zone (repeat/shuffle icons, volume) eats its own gap
	// instead of pushing the title aside. The truncation above guarantees
	// the zones never collide with the centered title, so the gaps clamp
	// to zero without ever exceeding the width.
	leftGap := max(0, (w-centerW)/2-leftW)
	rightGap := max(0, w-leftW-leftGap-centerW-rightW)

	return left +
		strings.Repeat(" ", leftGap) +
		center +
		strings.Repeat(" ", rightGap) +
		right
}

func (m model) headerVolumeBar(vol int) string {
	return m.styles.gradientBar(float64(vol)/100.0, volumeBarW)
}

func (m model) tabBarView() string {
	key := tabBarCacheKey{m.ui.width, m.ui.activeTab}
	if cached, ok := m.styles.tabBar.get(key); ok {
		return cached
	}
	tabs := []struct {
		label string
		t     tab
	}{
		{"Playlists", tabPlaylists},
		{"Albums", tabAlbums},
		{"Player", tabPlayer},
	}
	var parts []string
	for _, entry := range tabs {
		if m.ui.activeTab == entry.t {
			parts = append(parts, m.styles.styleTabActive.Render(" "+entry.label+" "))
		} else {
			parts = append(parts, m.styles.styleTabInactive.Render(" "+entry.label+" "))
		}
	}
	sep := m.styles.styleDivider.Render("\u2502")
	bar := strings.Join(parts, sep)
	underline := m.styles.sectionDivider(m.ui.width)
	out := bar + "\n" + underline
	m.styles.tabBar.put(key, out)
	return out
}
func (m model) playerBarView() string {
	barW := m.ui.width

	sep := m.styles.sectionDivider(barW)

	if m.transport.status == nil {
		// Always render the full bar height: the idle placeholder must
		// occupy the same number of lines as the playing bar or the bottom
		// gutter shifts between idle and playing frames.
		return sep + "\n"
	}
	playIcon, pauseIcon := m.playPauseGlyphs()
	stateIcon := m.styles.styleHeaderPaused.Render(pauseIcon)
	if m.transport.status.Playing {
		stateIcon = m.styles.styleHeaderPlaying.Render(playIcon)
	}

	elapsedMs := m.transport.status.ProgressMS
	if m.transport.status.DurationMS > 0 && elapsedMs > m.transport.status.DurationMS {
		elapsedMs = m.transport.status.DurationMS
	}
	if elapsedMs < 0 {
		elapsedMs = 0
	}

	pct := 0.0
	if m.transport.status.DurationMS > 0 {
		pct = float64(elapsedMs) / float64(m.transport.status.DurationMS)
		pct = max(0, min(1, pct))
	}

	elapsed := m.styles.stylePlayerTime.Render(fmtDuration(elapsedMs))
	total := m.styles.stylePlayerTime.Render("--:--")
	if m.transport.status.DurationMS > 0 {
		total = m.styles.stylePlayerTime.Render(fmtDuration(m.transport.status.DurationMS))
	}

	elapsedW := lipgloss.Width(elapsed)
	totalW := lipgloss.Width(total)
	iconW := lipgloss.Width(stateIcon)
	progressW := barW - elapsedW - totalW - iconW - playerBarGaps*playerBarGap
	// Narrow terminals (and the pre-resize startup frame) leave no room
	// for the bar; a negative width must render empty, never panic Repeat.
	progressW = max(0, progressW)
	var progressStr string
	if m.transport.status.DurationMS <= 0 {
		_, empty := m.styles.themeBarRunes()
		progressStr = m.styles.styleProgressBarEmpty.Render(strings.Repeat(string(empty), progressW))
	} else {
		progressStr = m.styles.gradientBar(pct, progressW)
	}

	bar := "  " + stateIcon + "  " + elapsed + "  " + progressStr + "  " + total
	return sep + "\n" + bar
}

func (m model) trackPopupView() string {
	modalW, _, listH := popupModalSize(m.ui.width, m.ui.height)
	innerH := listH + 2

	title := m.styles.styleTrackPopupTitle.Render("  " + m.ui.trackPopupName)

	var body string
	if m.ui.trackPopupItems == nil {
		body = m.styles.styleTrackPopupLoading.Render("\n  " + m.ui.spinner.View() + " Loading...")
	} else if len(m.ui.trackPopupItems) == 0 {
		body = m.styles.styleTrackPopupLoading.Render("\n  No tracks found")
	} else {
		body = m.ui.trackPopupList.View()
	}
	var hint string
	if m.ui.trackPopupItems != nil {
		hint = m.styles.styleTrackPopupHint.Render(m.styles.hintLine([]key.Binding{m.ui.keys.Select, m.ui.keys.Filter, m.ui.keys.CloseModal}, modalW-modalContentInset))
	}

	return m.styles.modalFrame(m.ui.width, m.ui.height, title, hint, body, modalW, innerH)
}

// helpModalSize is the single source for the help modal's dimensions so the
// Update-side viewport rebuild and the View-side render can never drift.
func helpModalSize(termW, termH int) (modalW, innerH, contentW int) {
	wantedH := max(6, termH-headerH-2)
	var boxH int
	modalW, boxH, contentW = modalRect(termW, termH, termW-4, wantedH)
	innerH = boxH
	return modalW, innerH, contentW
}

// ensureHelpViewport builds (or rebuilds) the help modal's viewport when the
// grouped help body overflows the modal. It runs from Update paths (open,
// resize) because View cannot persist state.
func (m *model) ensureHelpViewport() {
	_, innerH, contentW := helpModalSize(m.ui.width, m.ui.height)
	body := m.helpGroupedBody(contentW, innerH-4)
	if lipgloss.Height(body) > innerH-2 {
		v := viewport.New(viewport.WithWidth(contentW), viewport.WithHeight(innerH-2))
		v.SetContent(body)
		m.ui.helpViewport = &v
	} else {
		m.ui.helpViewport = nil
	}
}

// scrollHelp scrolls the help modal's viewport when the content overflows;
// a no-op otherwise.
func (m model) scrollHelp(dy int) model {
	if m.ui.helpViewport == nil {
		return m
	}
	vp := *m.ui.helpViewport
	if dy < 0 {
		vp.ScrollUp(-dy)
	} else {
		vp.ScrollDown(dy)
	}
	m.ui.helpViewport = &vp
	return m
}

func (m model) helpModalView() string {
	modalW, innerH, contentW := helpModalSize(m.ui.width, m.ui.height)

	hint := m.ui.keys.QueueUp.Help().Key + "/" + m.ui.keys.QueueDown.Help().Key + " scroll   " + m.ui.keys.ToggleHelp.Help().Key + " or " + m.ui.keys.CloseModal.Help().Key + " close"
	body := m.helpGroupedBody(contentW, innerH-4)
	if vp := m.ui.helpViewport; vp != nil {
		body = vp.View()
		if vp.AtTop() {
			hint = m.ui.keys.QueueUp.Help().Key + "/" + m.ui.keys.QueueDown.Help().Key + " scroll   " + m.ui.keys.CloseModal.Help().Key + " close"
		}
	}

	return m.styles.modalFrame(m.ui.width, m.ui.height, m.styles.styleModalTitle.Render("Help"),
		m.styles.styleModalHint.Render(hint), body, modalW, innerH)
}

// kittyOverlay builds this frame's overlay bytes for direct terminal
// emission via tea.Raw (see kittyOverlayCmd). View content CANNOT carry
// them: the v2 pipeline parks non-SGR escapes in zero-width cells the
// repaint engine never writes, and drops mid-text ones outright.
//
// Save/restore around the emission keeps the renderer's cursor model
// exact (CUP moves the physical cursor; DECRC puts it back), and the
// bytes are SGR-free so the renderer's delta-tracked pen stays exact too.
// frameKittyBytes wraps built overlay bytes for the wire: save/restore
// keeps the renderer's cursor model exact (CUP moves the physical
// cursor; DECRC puts it back), and the bytes are SGR-free so the
// renderer's delta-tracked pen stays exact too.
func frameKittyBytes(out string) string {
	if out == "" {
		return ""
	}
	framed := "\x1b7" + out + "\x1b8"
	dumpKittyOverlay(framed)
	return framed
}

func (m model) kittyOverlay() string {
	out, _ := m.kittyOverlayBytes()
	return frameKittyBytes(out)
}

// kittyOverlayCmd emits the current overlay bytes straight to the terminal
// through tea.Raw, which the program serializes with frame flushes (no
// interleave with repaints). Nil when the frame carries nothing: the byte
// builders already suppress no-op frames.
//
// Content emissions (transmit/re-place) are suppression-guarded at
// delivery: a modal opened after the bytes were built must not receive
// them. The closure checks the live slot flag when it executes, not
// when Update builds the string — a stale re-place built pre-modal is
// dropped instead of resurrecting the image over the scrim. Pure-delete
// emissions bypass the guard (stray deletes self-heal via
// re-place/restore; stray placements corrupt).
func (m model) kittyOverlayCmd() tea.Cmd {
	out, content := m.kittyOverlayBytes()
	if out == "" {
		return nil
	}
	framed := frameKittyBytes(out)
	if !content {
		return tea.Raw(framed)
	}
	imgs := m.ui.imgs
	inner := tea.Raw(framed)
	return func() tea.Msg {
		if imgs != nil && imgs.overlaySuppressed() {
			return nil
		}
		return inner()
	}
}

// dumpKittyOverlay appends the exact overlay bytes to ORPHEUS_KITTY_DUMP
// when set: `cat` that file in the same terminal to bisect app bytes
// versus terminal/tmux handling. Best-effort by design: diagnostics must
// never break rendering.
func dumpKittyOverlay(out string) {
	path := strings.TrimSpace(os.Getenv("ORPHEUS_KITTY_DUMP"))
	if path == "" || out == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(out)
}

// kittyOverlayBytes builds this frame's overlay bytes and reports whether
// they place image data. content=false means a pure-delete emission (modal
// hide, surface clear, protocol purge): it bypasses delivery suppression.
// content=true (transmit, re-place) is suppression-guarded by
// kittyOverlayCmd — a modal opened after the build drops it on delivery.
func (m model) kittyOverlayBytes() (string, bool) {
	if m.ui.imgs == nil {
		return "", false
	}
	if m.ui.imgs.protocolForRender() != imageProtocolKitty {
		// A protocol switch resets the slot without naming the shown
		// image for deletion; the pending purge (if any) goes out once
		// here, then this path stays silent by design.
		if id := m.ui.imgs.takePendingKittyPurge(); id != 0 {
			return deleteKittyImageData(id), false
		}
		return "", false
	}
	if m.modalKind() != modalNone {
		// An open modal hides the shown image with an image-scoped
		// delete on every frame while the slot believes it is live —
		// never once-and-silent, so a missed delete or a resurrected
		// placement self-heals on the next frame. Placements sit under
		// the text layer (z=-1) as a bonus only; terminals may show
		// them through default-background cells. A pending
		// protocol-switch purge rides the next non-modal frame instead.
		return m.ui.imgs.hideOverlayWhileModal(), false
	}
	layout := m.bodyLayout()
	rect := m.coverArt(layout.coverCols, layout.coverRows)
	if rect.empty() {
		return deleteKittyImage(m.ui.imgs.clearOverlayIntent()), false
	}

	var url, subjectID string
	switch m.ui.activeTab {
	case tabPlaylists:
		if pl, ok := m.selectedPlaylist(); ok {
			url = pl.summary.ImageURL
			subjectID = strings.TrimSpace(pl.summary.ID)
		}
	case tabAlbums:
		if al, ok := m.selectedAlbum(); ok {
			url = al.summary.ImageURL
			subjectID = strings.TrimSpace(al.summary.ID)
		}
	case tabPlayer:
		if m.transport.status != nil {
			url = m.transport.status.AlbumImageURL
			subjectID = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
			if subjectID == "" {
				subjectID = strings.TrimSpace(m.transport.status.TrackName) + "|" + strings.TrimSpace(m.transport.status.ArtistName) + "|" + fmt.Sprintf("%d", m.transport.status.DurationMS)
			}
		}
	}
	if url == "" {
		return deleteKittyImage(m.ui.imgs.clearOverlayIntent()), false
	}

	encoded := m.ui.imgs.encodedFor(url)
	if encoded == "" {
		// Same surface with content still loading: hold the old cover
		// instead of flashing a blank gap; the load completion drives
		// the swap (hold the old, then swap — never blank). A tab
		// switch is a different surface, so its stale art still clears.
		if m.ui.imgs.kittyShownTab() == m.ui.activeTab {
			return "", false
		}
		return deleteKittyImage(m.ui.imgs.clearOverlayIntent()), false
	}

	revision := uint64(0)
	if m.ui.activeTab == tabPlayer {
		revision = m.transport.playerCoverEpoch
	}
	intent := overlayIntent{
		art:      rect,
		tab:      m.ui.activeTab,
		subject:  subjectID,
		url:      url,
		revision: revision,
	}
	return m.kittyTransmitNewCover(intent, rect, encoded)
}

// kittyTransmitNewCover commits a changed intent and emits one direct
// swap: the new cover transmits fully, then the displaced image drops.
// A pending protocol-switch purge (a reset that predates this transmit
// without an intervening emission) rides along front: the frame carries
// both, so no stranded image survives a rapid switch round trip.
func (m model) kittyTransmitNewCover(intent overlayIntent, rect artRect, encoded string) (string, bool) {
	emit, transmitID, displacedID := m.ui.imgs.commitOverlayIntent(intent)
	purgePrefix := ""
	if id := m.ui.imgs.takePendingKittyPurge(); id != 0 {
		purgePrefix = deleteKittyImageData(id)
	}
	if !emit {
		// Unchanged intent: the image data is already stored ID-keyed in
		// the terminal, but renderer repaints erase placements (the art
		// rect is blank in the text layer) and the renderer diff swallows
		// byte-identical emissions. Re-place with a fresh placement ID
		// every frame instead of emitting nothing.
		return purgePrefix + m.ui.imgs.frameOverlayPlacement(rect), true
	}
	payload := buildKittyPayload(encoded, rect.cols, rect.rows, transmitID)
	if payload == "" {
		return purgePrefix + deleteKittyImage(displacedID), false
	}
	// The replacement lands fully placed before the displaced image
	// drops: terminals can present mid-transmission, and a purge-first
	// order would flash the gap. C=1 (DoNotMoveCursor) keeps the cursor where CUP put
	// it. The hidden alt-screen cursor makes C=1-ignoring terminals
	// harmless, and bubbletea repositions the cursor itself whenever
	// input needs it (filter mode).
	return purgePrefix + fmt.Sprintf("\x1b[%d;%dH%s%s", rect.row, rect.col, payload, deleteKittyImageData(displacedID)), true
}
