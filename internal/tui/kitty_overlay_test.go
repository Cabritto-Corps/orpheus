package tui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"

	"orpheus/internal/spotify"
)

func framedTestModel() model {
	m := NewLoaderModel()
	st := themePresetState("default")
	st.cover.Frame = "rounded"
	m.styles = buildThemeStyles(st)
	return m
}

func TestCoverArtDerivesSingleRect(t *testing.T) {
	m := NewLoaderModel()

	rect := framedTestModel().coverArt(30, 15)
	if !rect.framed {
		t.Fatal("expected 30x15 cell to take the frame")
	}
	if rect.cols != 28 || rect.rows != 13 {
		t.Fatalf("expected inset dims 28x13, got %dx%d", rect.cols, rect.rows)
	}
	if rect.row != bodyStartRow1Based+2+1 || rect.col != 2 {
		t.Fatalf("expected framed anchor one cell inside the cell origin, got (%d,%d)", rect.row, rect.col)
	}

	rect = m.coverArt(4, 2)
	if rect.framed {
		t.Fatal("expected 4x2 cell to skip the frame")
	}
	if rect.cols != 4 || rect.rows != 2 || rect.row != bodyStartRow1Based+2 || rect.col != 1 {
		t.Fatalf("expected unframed identity rect, got %+v", rect)
	}

	// Degenerate cell: empty, so neither renderer emits.
	if rect := m.coverArt(0, 0); !rect.empty() {
		t.Fatalf("expected empty rect for zero cell, got %+v", rect)
	}
}

func TestKittyOverlayFramedPlacementMatchesPanelAnchor(t *testing.T) {
	m := framedTestModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	// The ANSI cell and the kitty placement agree by construction (both consume coverArt).
	rect := m.coverArt(m.bodyLayout().coverCols, m.bodyLayout().coverRows)
	if !rect.framed {
		t.Fatal("expected the 120x40 layout to take the frame")
	}
	out := m.kittyOverlay()
	if out == "" {
		t.Fatal("expected overlay emission")
	}
	if want := fmt.Sprintf("\x1b[%d;%dH", rect.row, rect.col); !strings.Contains(out, want) {
		t.Fatalf("expected CUP at derived anchor %q, got %q", want, out)
	}
	if want := fmt.Sprintf("c=%d,r=%d", rect.cols, rect.rows); !strings.Contains(out, want) {
		t.Fatalf("expected display extents %q, got %q", want, out)
	}
}

func TestKittyPreviewIsPurgedWhenSwitchingToTabsWithoutSelection(t *testing.T) {
	t.Setenv("TMUX", "")
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabRecents
	m.browse.librarySettled = true
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["song-cover"] = "c29uZw=="
	m.browse.recentsList.SetItems([]list.Item{trackItem{item: spotify.QueueItem{ID: "song", Name: "Song", ImageURL: "song-cover"}}})
	first := m.kittyOverlay()
	if !strings.Contains(first, "a=T") {
		t.Fatal("expected Recents preview placement")
	}
	shownID := m.ui.imgs.overlay.shownID

	for _, nextTab := range []tab{tabAlbums, tabSearch} {
		if m.ui.activeTab != tabRecents {
			m.ui.activeTab = tabRecents
			if restored := m.kittyOverlay(); !strings.Contains(restored, "a=p") {
				t.Fatalf("expected Recents preview to return before switching to %s: %q", nextTab, restored)
			}
			shownID = m.ui.imgs.overlay.shownID
		}
		m.ui.activeTab = nextTab
		if nextTab == tabAlbums {
			m.browse.albumList.SetItems(nil)
		}
		hide := m.kittyOverlay()
		if !strings.Contains(hide, fmt.Sprintf("a=d,d=i,i=%d", shownID)) {
			t.Fatalf("switch to empty %s tab did not hide the old preview: %q", nextTab, hide)
		}
		if again := m.kittyOverlay(); !strings.Contains(again, "a=d,d=i") {
			t.Fatalf("empty %s tab stopped self-healing the hidden preview: %q", nextTab, again)
		}
	}
}

func TestOverlayDisplacedImageDeletedByID(t *testing.T) {
	c := newImgCache()
	a := overlayIntent{tab: tabPlayer, subject: "track-1", url: "u1"}
	emit, tx, disp := c.commitOverlayIntent(a)
	if !emit || tx == 0 || disp != 0 {
		t.Fatalf("expected first commit to emit with fresh ID and no delete, got emit=%v tx=%d disp=%d", emit, tx, disp)
	}
	if emit, _, _ := c.commitOverlayIntent(a); emit {
		t.Fatal("expected unchanged intent to suppress output")
	}
	b := overlayIntent{tab: tabPlayer, subject: "track-2", url: "u2"}
	emit, tx2, disp2 := c.commitOverlayIntent(b)
	if !emit {
		t.Fatal("expected changed intent to emit")
	}
	if disp2 != tx {
		t.Fatalf("expected displaced ID %d (the shown image), got %d", tx, disp2)
	}
	if tx2 == tx {
		t.Fatal("expected a fresh transmission ID so renderer diff cannot swallow the emission")
	}

	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "QUFB"
	m.ui.imgs.encoded["u2"] = "QkJC"
	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected initial kitty render")
	}
	shownID := transmitImageID(t, first)
	m.transport.status.TrackID = "track-2"
	m.transport.status.AlbumImageURL = "u2"
	second := m.kittyOverlay()
	if second == "" {
		t.Fatal("expected redraw when player cover URL changes")
	}
	// The old image drops only after the new cover lands fully placed:
	// purge-first would flash the gap (terminals can present mid-transmission);
	// d=I drops data because nothing re-places a displaced cover.
	if want := fmt.Sprintf("d=I,i=%s", shownID); !strings.Contains(second, want) {
		t.Fatalf("expected old image purged by ID (%q), got %q", want, second)
	}
	if pi, ni := strings.Index(second, "d=I,i="+shownID), strings.Index(second, "QkJC"); pi < ni {
		t.Fatalf("expected purge strictly after placement, got %q", second)
	}
	if strings.Contains(second, "a=d,d=A") {
		t.Fatalf("expected no global delete-all on content swap, got %q", second)
	}
	if !strings.Contains(second, "QkJC") {
		t.Fatalf("expected redraw to include the NEW cover payload, got %q", second)
	}
	if strings.Contains(second, "QUFB") {
		t.Fatalf("expected the displaced cover payload gone from the swap, got %q", second)
	}
}

// Target the a=T packet specifically: emissions can carry leading delete
// packets whose own i= names a different image.
func transmitImageID(t *testing.T, emission string) string {
	t.Helper()
	for _, pkt := range strings.Split(emission, "\x1b_G")[1:] {
		opts, _, _ := strings.Cut(pkt, ";")
		if !strings.Contains(opts, "a=T") {
			continue
		}
		_, after, ok := strings.Cut(opts, "i=")
		if !ok {
			t.Fatalf("expected an image ID in %q", emission)
		}
		id := after
		if end := strings.IndexAny(id, ",\x1b"); end >= 0 {
			id = id[:end]
		}
		if id == "" {
			t.Fatalf("expected an image ID in %q", emission)
		}
		return id
	}
	t.Fatalf("expected a transmit packet in %q", emission)
	return ""
}

func TestKittyUnchangedIntentStaysSilent(t *testing.T) {
	// An unchanged intent emits nothing: re-placing every frame churned
	// erase/put loops the steady flow shows as flicker. The one unchanged
	// emission is a modal-close restore, which re-places the hidden placement.
	t.Setenv("TMUX", "")
	m := framedTestModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if !strings.Contains(first, "a=T") {
		t.Fatalf("expected the first emission to transmit, got %q", first)
	}
	shownID := transmitImageID(t, first)
	rect := m.coverArt(m.bodyLayout().coverCols, m.bodyLayout().coverRows)

	if second := m.kittyOverlay(); second != "" {
		t.Fatalf("expected an unchanged intent to stay silent, got %q", second)
	}

	m.ui.helpOpen = true
	if hide := m.kittyOverlay(); hide != "" && !strings.Contains(hide, "d=I") {
		if !strings.Contains(hide, fmt.Sprintf("d=i,i=%s", shownID)) {
			t.Fatalf("expected the modal hide, got %q", hide)
		}
		if strings.Contains(hide, "a=T") || strings.Contains(hide, "a=p") {
			t.Fatalf("expected the hide to be delete-only, got %q", hide)
		}
	}
	m.ui.helpOpen = false
	restored, _ := m.kittyOverlayBytes()
	if restored == "" {
		t.Fatal("expected a re-place on the close frame")
	}
	if strings.Contains(restored, "a=T") {
		t.Fatalf("expected close to re-place without retransmitting, got %q", restored)
	}
	for _, want := range []string{
		fmt.Sprintf("i=%s", shownID),
		fmt.Sprintf("c=%d,r=%d", rect.cols, rect.rows),
		fmt.Sprintf("\x1b[%d;%dH", rect.row, rect.col),
	} {
		if !strings.Contains(restored, want) {
			t.Fatalf("expected re-place to name %q, got %q", want, restored)
		}
	}
	if strings.Contains(restored, "d=I") {
		t.Fatalf("expected the restore to keep stored data, got %q", restored)
	}
	if !strings.Contains(restored, "p=") {
		t.Fatalf("expected a fresh placement ID on restore, got %q", restored)
	}
	if again := m.kittyOverlay(); again != "" {
		t.Fatalf("expected the restored overlay to settle silent, got %q", again)
	}
}

func solidNRGBA(c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, c)
		}
	}
	return img
}

func payloadSig(t *testing.T, payload string) string {
	t.Helper()
	if len(payload) < 96 {
		t.Fatalf("payload too short for ordering assertions: %d bytes", len(payload))
	}
	return payload[len(payload)/3 : len(payload)/3+48]
}

func assertPurgeAfterPayload(t *testing.T, emission, payload, what string) {
	t.Helper()
	sig := payloadSig(t, payload)
	pi := strings.Index(emission, "a=d,")
	ni := strings.Index(emission, sig)
	if ni < 0 {
		t.Fatalf("%s must carry the new payload, got %q", what, tail(emission, 200))
	}
	if pi < 0 {
		t.Fatalf("%s must drop what it displaces, got %q", what, tail(emission, 200))
	}
	if pi < ni {
		t.Fatalf("%s purges before the replacement is placed (blank window), got %q", what, tail(emission, 200))
	}
}

func TestKittyCoverChangeIsOneDirectSwap(t *testing.T) {
	t.Setenv("TMUX", "")
	m := framedTestModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.setImage("u1", solidNRGBA(color.NRGBA{R: 200, G: 30, B: 90, A: 255}), 0, 0)
	m.ui.imgs.setImage("u2", solidNRGBA(color.NRGBA{R: 30, G: 90, B: 200, A: 255}), 0, 0)

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected initial kitty render")
	}

	m.transport.status.TrackID = "track-2"
	m.transport.status.AlbumImageURL = "u2"
	out := m.kittyOverlay()
	newPayload := m.ui.imgs.encodedFor("u2")
	if got := m.ui.imgs.kittyDisplayedURL(); got != "u2" {
		t.Fatalf("cover change must commit immediately, slot still shows %q", got)
	}
	assertPurgeAfterPayload(t, out, newPayload, "cover change")
	if !strings.Contains(out, "z=-1") {
		t.Fatalf("cover transmit must sit under text (z=-1), got %q", tail(out, 200))
	}
	if got := strings.Count(out, "a=T"); got != 1 {
		t.Fatalf("cover change must be exactly one transmit, got %d in %q", got, tail(out, 200))
	}
	if again := m.kittyOverlay(); strings.Contains(again, "a=T") {
		t.Fatalf("settled swap must re-place, not retransmit, got %q", tail(again, 200))
	}
}

func TestKittyCoverHoldWhileLoading(t *testing.T) {
	t.Setenv("TMUX", "")
	m := framedTestModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.setImage("u1", solidNRGBA(color.NRGBA{R: 200, G: 30, B: 90, A: 255}), 0, 0)

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.transport.status.TrackID = "track-2"
	m.transport.status.AlbumImageURL = "u2" // never loaded
	if out := m.kittyOverlay(); out != "" {
		t.Fatalf("loading cover must hold the old image silently, got %q", tail(out, 200))
	}
	if got := m.ui.imgs.kittyDisplayedURL(); got != "u1" {
		t.Fatalf("loading cover must keep the old slot, shows %q", got)
	}
}

func TestKittyRapidChangesSettleOnNewest(t *testing.T) {
	t.Setenv("TMUX", "")
	m := framedTestModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.setImage("u1", solidNRGBA(color.NRGBA{R: 200, G: 30, B: 90, A: 255}), 0, 0)
	m.ui.imgs.setImage("u2", solidNRGBA(color.NRGBA{R: 30, G: 90, B: 200, A: 255}), 0, 0)
	m.ui.imgs.setImage("u3", solidNRGBA(color.NRGBA{R: 30, G: 200, B: 90, A: 255}), 0, 0)

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.transport.status.TrackID = "track-2"
	m.transport.status.AlbumImageURL = "u2"
	_ = m.kittyOverlay()
	m.transport.status.TrackID = "track-3"
	m.transport.status.AlbumImageURL = "u3"
	out := m.kittyOverlay()
	if got := m.ui.imgs.kittyDisplayedURL(); got != "u3" {
		t.Fatalf("rapid changes must settle on the newest cover, shows %q", got)
	}
	if !strings.Contains(out, m.ui.imgs.encodedFor("u3")[:64]) {
		t.Fatalf("rapid changes must transmit the newest cover, got %q", tail(out, 200))
	}
	if got := strings.Count(out, "a=T"); got != 1 {
		t.Fatalf("settled change must be exactly one transmit, got %d in %q", got, tail(out, 200))
	}
}
