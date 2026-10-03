package tui

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"orpheus/internal/spotify"
)

// Distinct cover URLs per item: the earlier geometry battery used one URL
// for every row, so no cursor movement could ever swap the art and the
// whole class passed green. These pins fail on the live-selection code.
func filterChurnModel(t *testing.T, protocol imageProtocol) model {
	t.Helper()
	m := NewLoaderModel()
	m.ui.width = 100
	m.ui.height = 40
	m.ui.activeTab = tabPlaylists
	items := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "alpha one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "2", Name: "alpha two", ImageURL: "u2"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "3", Name: "alpha three", ImageURL: "u3"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "4", Name: "beta one", ImageURL: "u4"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "5", Name: "gamma", ImageURL: "u5"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "6", Name: "delta", ImageURL: "u6"}},
	}
	m.browse.playlistList.SetItems(items)
	m.browse.playlistList.Select(3)
	m.browse.playlistList.SetSize(60, 30)
	m.ui.imgs.protocol = protocol
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.NRGBA{R: 200, G: 30, B: 90, A: 255})
		}
	}
	for _, u := range []string{"u1", "u2", "u3", "u4", "u5", "u6"} {
		m.ui.imgs.setImage(u, img, 0, 0)
	}
	m.ui.width = 100
	return m
}

// Opening search resets the list cursor to the top while the user's item
// was "beta one": the preview must keep showing the pre-filter selection
// instead of chasing the cursor through the intermediate.
func TestFilterToggleKeepsPreviewSelection(t *testing.T) {
	for _, protocol := range []imageProtocol{imageProtocolKitty, imageProtocolNone} {
		m := filterChurnModel(t, protocol)
		before, _ := previewGeometry(m)
		l := pumpListKeys(t, m.browse.playlistList, pressRune('/'))
		m.browse.playlistList = l
		sel, ok := m.stablePlaylistSelection()
		if !ok || sel.summary.ID != "4" {
			got := "none"
			if ok {
				got = sel.summary.ID
			}
			t.Fatalf("protocol=%v: preview followed the filter cursor reset, want ID 4 got %s", protocol, got)
		}
		after, _ := previewGeometry(m)
		if before != after {
			t.Fatalf("protocol=%v: preview panel twitched on filter open", protocol)
		}
	}
}

// Typing re-seats the match under the cursor on every keystroke: the
// preview must not move until the filter is accepted or cleared.
func TestFilterTypingKeepsPreviewFrozen(t *testing.T) {
	for _, protocol := range []imageProtocol{imageProtocolKitty, imageProtocolNone} {
		m := filterChurnModel(t, protocol)
		before, _ := previewGeometry(m)
		l := pumpListKeys(t, m.browse.playlistList, pressRune('/'))
		for _, r := range "alpha" {
			l = pumpListKeys(t, l, pressRune(r))
		}
		m.browse.playlistList = l
		if live, ok := m.selectedPlaylist(); !ok || live.summary.ID == "4" {
			t.Fatalf("protocol=%v: test setup broken, live selection never moved", protocol)
		}
		sel, ok := m.stablePlaylistSelection()
		if !ok || sel.summary.ID != "4" {
			got := "none"
			if ok {
				got = sel.summary.ID
			}
			t.Fatalf("protocol=%v: preview chased typing, want ID 4 got %s", protocol, got)
		}
		after, _ := previewGeometry(m)
		if before != after {
			t.Fatalf("protocol=%v: preview panel twitched while typing", protocol)
		}
	}
}

// Esc must snap the preview to the final cursor position in a single step:
// the intermediate filter selection must never display.
func TestFilterExitSnapsPreviewToFinal(t *testing.T) {
	m := filterChurnModel(t, imageProtocolKitty)
	l := pumpListKeys(t, m.browse.playlistList, pressRune('/'))
	for _, r := range "alpha" {
		l = pumpListKeys(t, l, pressRune(r))
	}
	l = pumpListKeys(t, l, tea.KeyPressMsg{Code: tea.KeyEscape})
	m.browse.playlistList = l
	sel, ok := m.stablePlaylistSelection()
	if !ok {
		t.Fatal("preview empty after filter exit")
	}
	live, ok := m.selectedPlaylist()
	if !ok || sel.summary.ID != live.summary.ID {
		t.Fatalf("preview %q did not snap to final cursor %q", sel.summary.ID, live.summary.ID)
	}
}

// The overlay slot must not commit a filter-intermediate subject: a commit
// here is a full image retransmit plus a displaced-image delete, i.e. the
// kitty flicker. Pin at delivery level: the shown URL never changes while
// filtering and no data-delete for the old image is emitted.
func TestFilterTypingCommitsNoOverlayTransmit(t *testing.T) {
	m := filterChurnModel(t, imageProtocolKitty)
	out, content := m.kittyOverlayBytes()
	if !content || m.ui.imgs.kittyDisplayedURL() != "u4" {
		t.Fatalf("setup broken: priming transmit missing, shown=%q", m.ui.imgs.kittyDisplayedURL())
	}
	_ = out
	l := pumpListKeys(t, m.browse.playlistList, pressRune('/'))
	for _, r := range "alpha" {
		l = pumpListKeys(t, l, pressRune(r))
	}
	m.browse.playlistList = l
	out, _ = m.kittyOverlayBytes()
	if got := m.ui.imgs.kittyDisplayedURL(); got != "u4" {
		t.Fatalf("overlay committed a filter-intermediate subject: shown=%q want u4", got)
	}
	if strings.Contains(out, "d=I") {
		t.Fatalf("overlay emitted a data-delete mid-filter (transmit churn): %q", out)
	}
}

func TestStablePreviewUnit(t *testing.T) {
	var nilStab *stablePreview
	live := playlistItem{summary: spotify.PlaylistSummary{ID: "9"}}
	if got, _ := nilStab.forPlaylists(true, live, true); got.summary.ID != "9" {
		t.Fatal("nil stabilizer must serve live")
	}
	stab := &stablePreview{}
	if got, _ := stab.forPlaylists(true, live, true); got.summary.ID != "9" {
		t.Fatal("empty memory must fall back to live while filtering")
	}
	saved := playlistItem{summary: spotify.PlaylistSummary{ID: "1"}}
	stab.forPlaylists(false, saved, true)
	if got, _ := stab.forPlaylists(true, live, true); got.summary.ID != "1" {
		t.Fatalf("filtering must serve memory, got %s", got.summary.ID)
	}
	if got, _ := stab.forPlaylists(false, live, true); got.summary.ID != "9" {
		t.Fatalf("unfiltered must refresh memory, got %s", got.summary.ID)
	}
	alb := playlistItem{summary: spotify.PlaylistSummary{ID: "A"}}
	stab.forAlbums(false, alb, true)
	if got, _ := stab.forAlbums(true, live, true); got.summary.ID != "A" {
		t.Fatalf("album slot independent, got %s", got.summary.ID)
	}
	if got, _ := stab.forPlaylists(true, live, true); got.summary.ID != "9" {
		t.Fatalf("playlist slot must track its own refreshes, got %s", got.summary.ID)
	}
}

// Esc on search is a cancel: the cursor and the preview must return to the
// pre-filter selection — the exact reported bug (cover art + border moving
// on search quit). Drives the app-level key path: the restore lives in
// updateBrowseList, not inside bubbles.
func TestFilterCancelRestoresSelectionAndArt(t *testing.T) {
	m := filterChurnModel(t, imageProtocolKitty)
	before, _ := previewGeometry(m)
	beforeURL := m.ui.imgs.kittyDisplayedURL()

	next, _ := m.Update(pressRune('/'))
	m = next.(model)
	if m.browse.playlistList.FilterState() != list.Filtering {
		t.Fatal("expected Filtering state after /")
	}
	for _, r := range "alpha" {
		next, _ = m.Update(pressRune(r))
		m = next.(model)
	}
	if live, ok := m.selectedPlaylist(); !ok || live.summary.ID == "4" {
		t.Fatalf("test setup broken, live selection never moved (got %v)", live)
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)

	if got := m.browse.playlistList.GlobalIndex(); got != 3 {
		t.Fatalf("esc must restore the pre-filter cursor, got %d", got)
	}
	sel, ok := m.stablePlaylistSelection()
	if !ok || sel.summary.ID != "4" {
		got := "none"
		if ok {
			got = sel.summary.ID
		}
		t.Fatalf("preview left the pre-filter item after cancel, got %s", got)
	}
	after, afterOverlay := previewGeometry(m)
	if before != after {
		bo, ao := strings.Split(before, "\n"), strings.Split(after, "\n")
		for i := range max(len(bo), len(ao)) {
			a, b := "", ""
			if i < len(bo) {
				a = bo[i]
			}
			if i < len(ao) {
				b = ao[i]
			}
			if a != b {
				t.Fatalf("preview panel changed across filter cancel at line %d:\n- %q\n+ %q", i+1, a, b)
			}
		}
	}
	if got := m.ui.imgs.kittyDisplayedURL(); got != beforeURL {
		t.Fatalf("overlay subject changed across cancel: %q -> %q", beforeURL, got)
	}
	if strings.Contains(afterOverlay, "d=I") {
		t.Fatalf("cancel retransmitted the image (displaced delete): %q", afterOverlay)
	}
}

// Enter keeps the filtered selection — accept is not a cancel.
func TestFilterAcceptKeepsFilteredSelection(t *testing.T) {
	m := filterChurnModel(t, imageProtocolKitty)
	next, _ := m.Update(pressRune('/'))
	m = next.(model)
	if m.browse.playlistList.FilterState() != list.Filtering {
		t.Fatal("expected Filtering state after /")
	}
	// Typing drains its FilterMatchesMsg at list level: the app-level batch
	// carries tick/debounce cmds that would block for their durations.
	l := pumpListKeys(t, m.browse.playlistList, pressRune('b'), pressRune('e'), pressRune('t'), pressRune('a'))
	m.browse.playlistList = l
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.browse.playlistList.FilterState() != list.FilterApplied {
		t.Fatal("expected FilterApplied after accept")
	}
	if got := m.browse.playlistList.GlobalIndex(); got != 3 {
		t.Fatalf("accept must keep the filtered selection (beta one), got index %d", got)
	}
}
