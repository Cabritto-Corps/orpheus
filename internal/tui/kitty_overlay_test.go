package tui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

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

	// Framed cell: art insets one cell inside the ring on every side.
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

	// Unframed cell: identity.
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

	// The overlay must place the image at the derived rect — CUP anchor
	// plus display extents — so the ANSI cell and the kitty placement
	// agree by construction (both consume coverArt).
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

	// End to end: the old image ID is explicitly deleted, never globally.
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
	// terminals can present mid-transmission, and a purge-first order
	// would flash the gap. Data goes with the drop (nothing re-places
	// a displaced cover).
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

// transmitImageID extracts the stored image ID from a transmit emission's
// a=T packet (options sit between the packet framing and the first ';').
// It targets the a=T packet specifically: emissions can carry leading
// delete packets whose own i= names a different image.
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

func TestKittyUnchangedIntentReplacesFromStoredData(t *testing.T) {
	// Renderer repaints erase placements (the art rect is blank in the
	// text layer and blank runs win the erase optimization), so an
	// unchanged visible intent must re-place the stored image every
	// frame instead of emitting nothing: the data stays stored ID-keyed
	// in the terminal, and a=p restores the placement in ~70 bytes.
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

	// Every re-place pre-deletes image-scoped (d=i: placements only, data
	// survives — unlike uppercase d=I): this drops all placements of the
	// one shown image, including orphans from dropped emissions, and is
	// unconditional so no placement-ID bookkeeping can drift stale.
	second := m.kittyOverlay()
	if second == "" {
		t.Fatal("expected an unchanged intent to re-place, not emit nothing")
	}
	if strings.Contains(second, "a=T") || !strings.Contains(second, "a=p") {
		t.Fatalf("expected a payload-free re-place, got %q", second)
	}
	for _, want := range []string{
		fmt.Sprintf("i=%s", shownID),
		fmt.Sprintf("c=%d,r=%d", rect.cols, rect.rows),
		fmt.Sprintf("\x1b[%d;%dH", rect.row, rect.col),
		"z=-1",
		fmt.Sprintf("d=i,i=%s", shownID),
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("expected re-place to name %q, got %q", want, second)
		}
	}
	if strings.Contains(second, "d=I") {
		t.Fatalf("expected the pre-delete to preserve stored data, got %q", second)
	}
	// Second re-place: same shape, and a fresh p= takes its place, so
	// placements stay bounded by one on screen.
	third := m.kittyOverlay()
	if !strings.Contains(third, "d=i") || strings.Contains(third, "d=I") {
		t.Fatalf("expected a placement-only delete on re-place, got %q", third)
	}
	if strings.Contains(second, "p=2") || !strings.Contains(third, "p=2") {
		t.Fatalf("expected the placement ID to advance across frames, got %q then %q", second, third)
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

// A cover change is one direct swap: the new cover transmits in the
// first emission with the slot committing to it immediately — no blend
// intermediates, nothing pending. The old cover holds until the new one
// is loaded, then swaps atomically.
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
	// The slot commits to the new cover in the same emission: anything
	// still naming the old URL means an intermediate (fade) is pending.
	if got := m.ui.imgs.kittyDisplayedURL(); got != "u2" {
		t.Fatalf("cover change must commit immediately, slot still shows %q", got)
	}
	assertPurgeAfterPayload(t, out, newPayload, "cover change")
	// z=-1 is bonus layering under text only, not modal protection;
	// modal frames delete placements separately.
	if !strings.Contains(out, "z=-1") {
		t.Fatalf("cover transmit must sit under text (z=-1), got %q", tail(out, 200))
	}
	if got := strings.Count(out, "a=T"); got != 1 {
		t.Fatalf("cover change must be exactly one transmit, got %d in %q", got, tail(out, 200))
	}
	// Settled: the next frame re-places from stored data, no retransmit.
	if again := m.kittyOverlay(); strings.Contains(again, "a=T") {
		t.Fatalf("settled swap must re-place, not retransmit, got %q", tail(again, 200))
	}
}

// While the new cover is still loading the old one holds: no delete,
// no blank — the load completion drives the swap.
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

// Rapid target changes settle on the newest cover with one placement.
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
