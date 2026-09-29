package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

// The centered title is positioned absolutely on the terminal, so a wide
// right zone can overlap it: the row must still never exceed the terminal
// width (a wider row wraps the header line and eats the chrome height).
func TestLayoutThreeZoneNeverExceedsWidth(t *testing.T) {
	rep := strings.Repeat
	cases := []struct {
		name                string
		w                   int
		left, center, right string
	}{
		{"wide-right-overlaps-centered-title", 80, rep("L", 10), rep("C", 40), rep("R", 25)},
		{"tight", 40, rep("L", 8), rep("C", 30), rep("R", 12)},
		{"tiny", 12, rep("L", 4), rep("C", 10), rep("R", 4)},
		{"empty-zones", 80, "", rep("C", 20), ""},
		{"roomy", 100, rep("L", 12), rep("C", 30), rep("R", 14)},
	}
	for _, tc := range cases {
		if out := layoutThreeZone(tc.w, tc.left, tc.center, tc.right); lipgloss.Width(out) != tc.w {
			t.Errorf("%s: row width %d, want %d: %q", tc.name, lipgloss.Width(out), tc.w, out)
		}
	}
}

// The fix must not move anything when all zones fit: the normal row is
// byte-identical to the hand-computed expectation.
func TestLayoutThreeZoneFittingRowUnchanged(t *testing.T) {
	out := layoutThreeZone(80, strings.Repeat("L", 10), strings.Repeat("C", 20), strings.Repeat("R", 10))
	want := strings.Repeat("L", 10) + strings.Repeat(" ", 20) + strings.Repeat("C", 20) + strings.Repeat(" ", 20) + strings.Repeat("R", 10)
	if out != want {
		t.Errorf("fitting row changed:\n got %q\nwant %q", out, want)
	}
}
