package tui

import (
	"fmt"
	"image/color"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/muesli/termenv"
)

var (
	terminalBGOriginal string
	terminalBGLast     string
)

// CaptureTerminalBG remembers the terminal's current background (OSC 11
// query, best effort) so the themed background set during the session can
// be restored on exit. Unknown captures (terminals that answer neither
// OSC 11 nor the cursor report) disable the whole feature: without the
// original color a restore would guess, and guessing wrong is worse than
// leaving the padding transparent.
func CaptureTerminalBG() {
	c := termenv.BackgroundColor()
	if _, ok := c.(termenv.RGBColor); !ok {
		// Fallback values (COLORFGBG, hardcoded black) do not describe the
		// real background; restoring them would repaint it wrongly.
		terminalBGOriginal = ""
		terminalBGLast = ""
		return
	}
	col := termenv.ConvertToRGB(c)
	terminalBGOriginal = rgbSpec(col.R, col.G, col.B)
	terminalBGLast = ""
}

// terminalBGTarget is the pure decision behind ApplyTerminalBG: the spec
// to emit via OSC 11, or "" for silence. Transparent mode hands the
// background back to the terminal instead of claiming it, so entering it
// mid-session restores the captured original exactly once (Last clears,
// making the release idempotent); leaving it re-applies the page because
// Last no longer matches. Kept pure so the transitions are unit-testable
// without emitting escape sequences.
func terminalBGTarget(page color.Color, transparent bool) string {
	if terminalBGOriginal == "" {
		return ""
	}
	if !colorEnabled() {
		return ""
	}
	if transparent {
		if terminalBGLast == "" {
			return ""
		}
		return terminalBGOriginal
	}
	// Only hex-origin (RGB) pages get a spec: ANSI-name pages keep the
	// transparent look, as before.
	rgba, ok := page.(color.RGBA)
	if !ok {
		return ""
	}
	spec := rgbSpec8(rgba.R, rgba.G, rgba.B)
	if spec == terminalBGLast {
		return ""
	}
	return spec
}

// ApplyTerminalBG sets the terminal's own background to the theme's page
// color: the padding around the grid is painted with the terminal
// background, so matching it makes the page fill read as whole-window.
// ANSI-name pages (no hex) and ASCII profiles keep the transparent look.
func ApplyTerminalBG(page color.Color, transparent bool) {
	spec := terminalBGTarget(page, transparent)
	if spec == "" {
		return
	}
	fmt.Fprintf(os.Stdout, "\x1b]11;%s\x1b\\", spec)
	if transparent {
		terminalBGLast = ""
	} else {
		terminalBGLast = spec
	}
}

// RestoreTerminalBG puts back the background the terminal had at startup.
func RestoreTerminalBG() {
	if terminalBGOriginal == "" {
		return
	}
	fmt.Fprintf(os.Stdout, "\x1b]11;%s\x1b\\", terminalBGOriginal)
	terminalBGLast = ""
	terminalBGOriginal = ""
}

// TerminalBGSync re-syncs the terminal background with the page color
// captured at theme-apply time; theme changes return it as a tea.Cmd so
// the padding follows the live preview without reading theme state off
// the event loop.
func TerminalBGSync(page color.Color, transparent bool) tea.Msg {
	ApplyTerminalBG(page, transparent)
	return nil
}

// rgbSpec converts 0..1 channel floats to the 16-bit rgb: spec form
// terminals use in their own OSC 11 responses.
func rgbSpec(r, g, b float64) string {
	to8 := func(v float64) uint8 {
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		return uint8(v*255 + 0.5)
	}
	return rgbSpec8(to8(r), to8(g), to8(b))
}

func rgbSpec8(r, g, b uint8) string {
	return fmt.Sprintf("rgb:%02x%02x/%02x%02x/%02x%02x", r, r, g, g, b, b)
}
