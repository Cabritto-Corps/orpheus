package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// artRect is the exact cell rect the art occupies and whether a frame ring
// insets it: the single source the ANSI renderer and the kitty overlay both
// derive from, so the two never disagree. The anchor is layout-invariant
// (panel label + divider above, panel flush left), so the rect derives from
// cell dims alone.
type artRect struct {
	row, col int
	cols     int
	rows     int
	framed   bool
}

func (r artRect) empty() bool {
	return r.cols <= 0 || r.rows <= 0
}

// coverArt derives the art rectangle from a laid-out cover cell: the cover
// always starts two lines below the body (panel label + divider) at the
// panel's first column, and a framed cover insets one cell inside the
// border ring on every side.
func (m model) coverArt(cellCols, cellRows int) artRect {
	cols, rows := cellCols, cellRows
	framed := m.styles.coverFrameFits(cols, rows)
	row, col := bodyStartRow1Based+2, 1
	if framed {
		cols, rows = cols-2, rows-2
		row, col = row+1, col+1
	}
	return artRect{row: row, col: col, cols: cols, rows: rows, framed: framed}
}

// overlayIntent is what the frame wants displayed: typed fields instead of
// the old colon-joined key, which broke when an earlier field gained a colon.
type overlayIntent struct {
	art      artRect
	tab      tab
	subject  string
	url      string
	revision uint64
}

// overlayState owns the single large preview slot. List thumbnails have their
// own placements but share nextID, so no two terminal images use the same ID.
type overlayState struct {
	shown   overlayIntent
	shownID uint64
	visible bool
	force   bool
	nextID  uint64
	// Placement IDs mint per emission: the renderer diffs frames and
	// swallows byte-identical emissions, so a restore would be dropped
	// unless it differs. Targeting is always image-scoped (d=i,i=) —
	// placement naming is stale-prone and Ghostty has point-delete
	// conformance gaps; nothing tracks the previous frame's p=.
	placementSeq uint64
	// purgeID names a stored image the terminal still holds after a slot
	// reset for a non-kitty protocol; the next emission purges it (data
	// and placements) exactly once instead of stranding it.
	purgeID uint64
	// pendingRestore: the modal hide erased the live placement, so the
	// next content emission must re-place even though the intent is
	// unchanged — the only reason an unchanged frame emits.
	pendingRestore bool
	// suppressOverlay drops content emissions at delivery: a modal opened
	// after the emission was built. Checked by the cmd closure when it
	// executes (build-time checks are stale); pure deletes bypass it.
	suppressOverlay bool
}

// commitOverlayIntent records the frame's intent and reports what to emit.
// Every transmit mints a fresh image ID — not for dedupe, but because the
// renderer swallows byte-identical emissions and a needed retransmit would
// be lost. IDs mint only on intent transitions. A displaced image is
// deleted by ID first, so image memory stays bounded by the one on screen.
func (c *imgCache) commitOverlayIntent(intent overlayIntent) (emit bool, transmitID, displacedID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.overlay.force && intent == c.overlay.shown && c.overlay.visible {
		return false, 0, 0
	}
	c.overlay.nextID++
	if c.overlay.visible {
		displacedID = c.overlay.shownID
	}
	// A new subject supersedes an empty/modal hide; only an unchanged subject
	// should consume pendingRestore and re-place the existing image.
	c.overlay.pendingRestore = false
	c.overlay.shown = intent
	c.overlay.shownID = c.overlay.nextID
	c.overlay.visible = true
	c.overlay.force = false
	return true, c.overlay.shownID, displacedID
}

// hideOverlayForEmptyPreview removes the active placement but retains its
// image data for an unchanged preview to restore. Repeat the delete each
// empty frame so a missed terminal packet cannot leave ghost art behind.
func (c *imgCache) hideOverlayForEmptyPreview() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.overlay.visible || c.overlay.shownID == 0 {
		return ""
	}
	c.overlay.pendingRestore = true
	return deleteKittyImage(c.overlay.shownID)
}

// hideOverlayWhileModal drops the shown cover under an open modal with an
// image-scoped delete (placements only, data preserved for the close
// restore). It repeats every modal frame — never once-and-silent — so a
// missed delete or resurrected placement self-heals instead of stranding
// the image over the modal. Idempotent and tiny, so the repeat is free.
func (c *imgCache) hideOverlayWhileModal() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.overlay.visible || c.overlay.shownID == 0 {
		return ""
	}
	c.overlay.pendingRestore = true
	return deleteKittyImage(c.overlay.shownID)
}

// takePendingOverlayRestore pops the modal-hide restore flag.
func (c *imgCache) takePendingOverlayRestore() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ok := c.overlay.pendingRestore
	c.overlay.pendingRestore = false
	return ok
}

// overlaySuppressed reports whether content emissions must be dropped at
// delivery: a modal opened after they were built. Checked by overlay
// cmd closures at execution time, never at build time.
func (c *imgCache) overlaySuppressed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.overlay.suppressOverlay
}

// setOverlaySuppressed mirrors the live modal state onto the slot. The
// Update wrapper calls it after every Update; cmd closures built by
// earlier Updates observe it at delivery.
func (c *imgCache) setOverlaySuppressed(suppressed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.overlay.suppressOverlay = suppressed
}

// takePendingKittyPurge pops the stored image a protocol reset stranded
// in the terminal. It reports exactly once per reset; the caller purges
// data and placements with it (d=I).
func (c *imgCache) takePendingKittyPurge() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.overlay.purgeID
	c.overlay.purgeID = 0
	return id
}

// frameOverlayPlacement re-places the shown image with a fresh placement ID.
// The id churns only because the renderer diffs whole frames and swallows
// byte-identical emissions: a restored placement after a modal hide must
// not be swallowed. The pre-delete is image-scoped (d=i,i=): it drops every
// placement of the shown image — including orphans from emissions dropped
// after they were built — so a dropped frame loses nothing.
func (c *imgCache) frameOverlayPlacement(r artRect) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.overlay.visible || c.overlay.shownID == 0 || r.empty() {
		return ""
	}
	c.overlay.placementSeq++
	next := c.overlay.placementSeq
	id := c.overlay.shownID
	out := fmt.Sprintf("\x1b[%d;%dH", r.row, r.col)
	out += deleteKittyImage(id)
	o := kitty.Options{
		Action:          kitty.Put,
		ID:              int(id),
		PlacementID:     int(next),
		Columns:         r.cols,
		Rows:            r.rows,
		Quiet:           2,
		DoNotMoveCursor: true,
	}
	// x/ansi only emits z for positive values; negative z (under text,
	// per the kitty spec) rides as a literal so scrim text covers the
	// image with no delete/restore dance around modals.
	return out + kittyGraphicsPacket(nil, append(o.Options(), "z=-1")...)
}

// kittyShownTab reports which tab the displayed image belongs to, so the
// loading path can hold same-surface covers while clearing stale ones.
func (c *imgCache) kittyShownTab() tab {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.overlay.shown.tab
}

func (c *imgCache) forceKittyRedraw() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.overlay.force = true
}

func (c *imgCache) resetKittyOverlayState() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetKittyOverlayStateLocked()
}

func (c *imgCache) resetKittyOverlayStateLocked() {
	// The shown image may still live in the terminal: carry its ID as a
	// pending purge. nextID and placementSeq never reset — a reused ID
	// could resurrect a deleted image.
	purgeID := c.overlay.purgeID
	if c.overlay.visible && c.overlay.shownID != 0 {
		purgeID = c.overlay.shownID
	}
	nextID := c.overlay.nextID
	placementSeq := c.overlay.placementSeq
	c.overlay = overlayState{force: true, nextID: nextID, placementSeq: placementSeq, purgeID: purgeID}
}

func (c *imgCache) kittyDisplayedURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.overlay.shown.url)
}

// tmuxKittyPassthrough reports whether graphics packets need tmux's DCS
// passthrough wrapper: tmux consumes unwrapped kitty sequences, so inside
// tmux every packet rides inside \x1bPtmux; with doubled inner ESCs.
func tmuxKittyPassthrough() bool {
	return strings.TrimSpace(os.Getenv("TMUX")) != ""
}

// kittyGraphicsPacket is the single seam for kitty graphics bytes: every
// transmit and delete packet flows through here, so the tmux passthrough
// rule cannot drift between paths.
func kittyGraphicsPacket(payload []byte, opts ...string) string {
	seq := ansi.KittyGraphics(payload, opts...)
	if tmuxKittyPassthrough() {
		return wrapTmuxPassthrough(seq)
	}
	return seq
}

// wrapTmuxPassthrough wraps one graphics packet for tmux passthrough: the
// whole original packet (framing ESCs included) sits inside DCS with every
// inner ESC doubled, and only the DCS terminator stays raw. Cursor moves
// and the overlay CR stay outside the wrapper: they drive tmux's own
// cursor, which the outer terminal maps back to the pane.
func wrapTmuxPassthrough(seq string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// deleteKittyImageData purges one stored image outright (placements and
// data). Only the direct swap uses it: the displaced cover is never
// re-placed, so its data goes with its placements. The cover slot stays
// on the placement-only delete below, which its re-place path depends on.
func deleteKittyImageData(id uint64) string {
	if id == 0 {
		return ""
	}
	return kittyGraphicsPacket(nil, fmt.Sprintf("a=d,d=I,i=%d", id))
}

// deleteKittyImage emits a targeted delete for one displayed image ID.
// Deleting an ID the terminal does not know is a no-op, so repeating a
// delete (modal frames, reordered events) is safe.
func deleteKittyImage(id uint64) string {
	if id == 0 {
		return ""
	}
	return kittyGraphicsPacket(nil, fmt.Sprintf("a=d,d=i,i=%d", id))
}

func chunkBase64(encoded string, size int) []string {
	if size <= 0 {
		return nil
	}
	var parts []string
	for off := 0; off < len(encoded); off += size {
		end := min(off+size, len(encoded))
		parts = append(parts, encoded[off:end])
	}
	return parts
}

// encodeKittyChunks frames base64 PNG bytes for transmit-and-display:
// chunked at the protocol's MaxChunkSize, canonical first-chunk options.
// A single chunk carries no m=; continuations only m= — extra keys trip
// Ghostty's chunked-transfer path (observed: images silently missing).
func encodeKittyChunks(chunks []string, cols, rows int, imageID uint64) string {
	var sb strings.Builder
	for i, part := range chunks {
		last := i == len(chunks)-1
		var seq string
		if i == 0 {
			o := kitty.Options{
				Action:          kitty.TransmitAndPut,
				Format:          kitty.PNG,
				ID:              int(imageID),
				Columns:         cols,
				Rows:            rows,
				Quiet:           2,
				DoNotMoveCursor: true,
			}
			opts := o.Options()
			// Negative z renders under text (kitty spec); x/ansi skips it, so it
			// rides as a literal.
			opts = append(opts, "z=-1")
			if !last {
				opts = append(opts, "m=1")
			}
			seq = kittyGraphicsPacket([]byte(part), opts...)
		} else if last {
			seq = kittyGraphicsPacket([]byte(part), "m=0")
		} else {
			seq = kittyGraphicsPacket([]byte(part), "m=1")
		}
		sb.WriteString(seq)
	}
	return sb.String()
}

// buildKittyPayload frames one already-encoded cover for transmission. The
// expensive PNG encode happens once per URL at load time; this only slices
// the cached base64 and frames it, so it runs cheaply on every overlay
// emission (which itself happens only on intent transitions, never per
// tick) — no chunk cache needed.
func buildKittyPayload(encoded string, cols, rows int, imageID uint64) string {
	if encoded == "" || cols <= 0 || rows <= 0 {
		return ""
	}
	return encodeKittyChunks(chunkBase64(encoded, kitty.MaxChunkSize), cols, rows, imageID)
}
