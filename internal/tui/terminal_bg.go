package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var (
	terminalBGOriginal string
	terminalBGLast     string
)

// CaptureTerminalBG remembers the terminal's current background (OSC 11
// query, best effort) so the themed background set during the session can
// be restored on exit.
func CaptureTerminalBG() {
	col := termenv.ConvertToRGB(termenv.BackgroundColor())
	terminalBGOriginal = rgbSpec(col.R, col.G, col.B)
	terminalBGLast = ""
}

// ApplyTerminalBG sets the terminal's own background to the theme's page
// color: the padding around the grid is painted with the terminal
// background, so matching it makes the page fill read as whole-window.
// ANSI-name pages (no hex) and ASCII profiles keep the transparent look.
func ApplyTerminalBG(page lipgloss.Color) {
	if lipgloss.DefaultRenderer().ColorProfile() == termenv.Ascii {
		return
	}
	hex := string(page)
	if !validHexColor(hex) || hex == terminalBGLast {
		return
	}
	if r, g, b, ok := hexToRGB(hex); ok {
		fmt.Fprintf(os.Stdout, "\x1b]11;%s\x1b\\", rgbSpec8(r, g, b))
		terminalBGLast = hex
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

// TerminalBGSync re-syncs the terminal background with the current theme
// page; theme changes return it as a tea.Cmd so the padding follows the
// live preview.
func TerminalBGSync() tea.Msg {
	ApplyTerminalBG(colorPage)
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
