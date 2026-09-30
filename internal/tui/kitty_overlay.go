package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// artRect is the exact cell rectangle an image occupies, plus whether it
// sits inside a frame ring: the single source both the ANSI cover renderer
// and the kitty overlay derive from, so the two can never disagree about
// where the art sits. The anchor is layout-invariant (panel label +
// divider above the cell, panel flush left), so the rectangle derives from
// the cell dims alone; the layout struct carries no art fields.
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

// overlayIntent is what the frame wants displayed: a typed replacement for
// the old colon-joined key string (which extracted placement by scanning
// for the 4th colon and broke if any earlier field ever gained one).
type overlayIntent struct {
	art      artRect
	tab      tab
	subject  string
	url      string
	revision uint64
}

// overlayState is the single displayed-image slot. At most one cover is
// ever on screen, so one slot plus a monotonic ID counter is the whole
// bookkeeping: no chunk caches, no string keys, no parallel counters.
type overlayState struct {
	shown   overlayIntent
	shownID uint64
	visible bool
	force   bool
	nextID  uint64
	// Per-frame placement IDs: the renderer diffs whole frames and
	// swallows byte-identical emissions, so every re-place mints a fresh
	// p=. placementSeq never resets (like nextID). Targeting is always
	// image-scoped (d=i,i=) — placements churn every frame and Ghostty
	// has point-delete (d=i,p=) conformance gaps, so naming a placement
	// is both stale-prone and terminal-fragile; nothing tracks the
	// previous frame's p=.
	placementSeq uint64
	// purgeID names a stored image the terminal still holds after the
	// slot reset for a non-kitty protocol (style switch, auto-fallback).
	// The next overlay emission purges it (data and placements) instead
	// of stranding it: the gate pops it exactly once.
	purgeID uint64
	// suppressOverlay drops content emissions at delivery time: a modal
	// opened after an emission was built. Set at the end of every Update
	// from the live modal state; overlay cmd closures check it when they
	// execute. Pure-delete emissions bypass it (stray deletes self-heal
	// via re-place/restore; stray placements corrupt).
	suppressOverlay bool
}

// commitOverlayIntent records the frame's overlay intent and reports what
// to emit. An unchanged visible intent needs no transmit: the image data
// is already stored ID-keyed in the terminal, so the caller re-places from
// it instead (renderer repaints erase placements, and a re-place is tens of
// bytes versus retransmitting the whole payload).
//
// Every transmit mints a fresh image ID — not because the terminal dedupes
// (it doesn't), but because bubbletea's renderer diffs whole frames: an
// emission byte-identical to the previous frame would be swallowed and a
// needed retransmit lost. IDs mint only on intent transitions, never per
// tick. A displaced previous image is always deleted by ID first, so
// terminal image memory stays bounded by the one image on screen.
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
	c.overlay.shown = intent
	c.overlay.shownID = c.overlay.nextID
	c.overlay.visible = true
	c.overlay.force = false
	return true, c.overlay.shownID, displacedID
}

// clearOverlayIntent forgets the displayed image and reports the ID to
// delete. The remembered intent is kept: when content returns for the same
// intent the commit path retransmits (the terminal holds nothing).
// Repeated clears report 0 — deletion happens exactly once.
func (c *imgCache) clearOverlayIntent() (deletedID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.overlay.visible {
		return 0
	}
	deletedID = c.overlay.shownID
	c.overlay.visible = false
	c.overlay.shownID = 0
	return deletedID
}

// hideOverlayWhileModal drops the shown cover under an open modal with
// an image-scoped delete (every placement of the one shown image, data
// preserved for the restore on close). It fires on every modal frame
// while the slot believes an image is live — never once-and-silent: a
// missed delete or a resurrected placement self-heals on the next
// frame instead of stranding the image over the modal permanently.
// The delete is idempotent and tiny, so the repeat costs nothing.
func (c *imgCache) hideOverlayWhileModal() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.overlay.visible || c.overlay.shownID == 0 {
		return ""
	}
	return deleteKittyImage(c.overlay.shownID)
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

// frameOverlayPlacement rebuilds the current placement every frame with a
// fresh placement ID and reports the emission. Renderer repaints erase
// placements, so the placement must be re-emitted — but the renderer also
// diffs whole frames and swallows byte-identical emissions, so each
// re-place carries a new p=. The pre-delete is image-scoped (d=i,i=): it
// drops every placement of the one shown image — including orphans from
// emissions dropped after they were built — so a dropped frame loses
// nothing; the next re-place cleans up. The stored image data is
// untouched (lowercase d=i deletes placements only); only placements
// churn, tens of bytes a frame.
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
	// The terminal may still hold the shown image, so its ID carries
	// over as a pending purge: the next emission deletes it (data and
	// placements) instead of stranding it. nextID and placementSeq are
	// deliberately preserved across resets: reusing an ID could
	// resurrect a deleted image the terminal still holds.
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

// encodeKittyChunks frames base64 PNG bytes for transmit-and-display using
// the kitty wire encoding from x/ansi: chunking at the protocol's
// MaxChunkSize, canonical first-chunk options, quiet mode on the first
// chunk. A single chunk carries no m= flag (the library's canonical form);
// continuations carry only m: extra keys there trip Ghostty's chunked
// transfer path (observed: images silently missing).
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
			// Negative z renders under text (kitty spec); x/ansi only
			// emits z for positive values, so it rides as a literal.
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
