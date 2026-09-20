package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLayoutThreeZoneCentersTitleOnTerminal(t *testing.T) {
	const w = 100
	title := "Artist — Some Track Title That Fits"

	position := func(left, right string) int {
		line := layoutThreeZone(w, left, title, right)
		if got := lipgloss.Width(line); got > w {
			t.Fatalf("line exceeds the width: %d > %d", got, w)
		}
		idx := strings.Index(line, title)
		if idx < 0 {
			t.Fatalf("title not found in %q", line)
		}
		// strings.Index is a byte offset; glyphs before it are multi-byte,
		// so measure the prefix in display cells.
		return lipgloss.Width(line[:idx])
	}

	base := position("[▶ Playing]", "  84% "+strings.Repeat("▓", volumeBarW))
	withRepeat := position("[▶ Playing]", "  84% "+strings.Repeat("▓", volumeBarW)+"  ↻")
	withBoth := position("[▶ Playing]", "  84% "+strings.Repeat("▓", volumeBarW)+"  ⇄ ↻¹")

	want := (w - lipgloss.Width(title)) / 2
	if base != want || withRepeat != want || withBoth != want {
		t.Fatalf("title column must stay at %d: base=%d repeat=%d both=%d", want, base, withRepeat, withBoth)
	}
}

func TestLayoutThreeZoneCollisionFallsBackInsideWidth(t *testing.T) {
	const w = 60
	title := "A Very Long Track Title That Would Not Fit Between The Zones"
	left := strings.Repeat("L", 30)
	right := strings.Repeat("R", 20)

	line := layoutThreeZone(w, left, title, right)
	if got := lipgloss.Width(line); got > w {
		t.Fatalf("line exceeds the width: %d > %d", got, w)
	}
	// The title is truncated to the zone budget when the sides collide.
	if got := lipgloss.Width(strings.TrimSpace(line)); got > w {
		t.Fatalf("content exceeds the width: %d", got)
	}
}
