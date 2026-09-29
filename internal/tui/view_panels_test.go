package tui

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func framedCoverTestImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 32), G: uint8(y * 32), B: 128, A: 255})
		}
	}
	return img
}

func assertBlockDimensions(t *testing.T, out string, cols, rows int) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) != rows {
		t.Fatalf("framed cover has %d rows, want %d:\n%s", len(lines), rows, out)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width != cols {
			t.Fatalf("framed cover row %d is %d cells, want %d:\n%s", i+1, width, cols, out)
		}
	}
}

func TestFramedCoverKeepsOuterCellDimensions(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "0")

	m := framedTestModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.imgs.protocol = imageProtocolNone

	const url = "https://example.com/framed-cover"
	m.ui.imgs.setImage(url, framedCoverTestImage(), 8, 8)

	const cols, rows = 30, 15
	rect := m.coverArt(cols, rows)
	if !rect.framed {
		t.Fatal("expected the test cell to take the cover frame")
	}
	out := m.coverOrPlaceholder(url, cols, rows)
	assertBlockDimensions(t, out, cols, rows)
	if !strings.Contains(out, "▀") {
		t.Fatalf("expected framed art to contain the cover render, got:\n%s", out)
	}
}

func TestCoverPlaceholderKeepsOuterCellDimensions(t *testing.T) {
	m := framedTestModel()
	const cols, rows = 30, 15
	assertBlockDimensions(t, m.placeholderArt(cols, rows), cols, rows)
}
