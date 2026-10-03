package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"orpheus/internal/spotify"
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
		// strings.Index is a byte offset; measure the prefix in display cells.
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
	if got := lipgloss.Width(strings.TrimSpace(line)); got > w {
		t.Fatalf("content exceeds the width: %d", got)
	}
}

// A wide right zone can overlap the absolutely positioned centered title;
// a wider row would wrap the header line and eat the chrome height.
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

// Centered by separate formulas, the two center-column rows measured a
// couple of cells apart on the rendered header; both spans must center
// within one cell of each other.
func TestHeaderTextSharesCenterColumn(t *testing.T) {
	cases := []struct {
		name   string
		status *spotify.PlaybackStatus
	}{
		{name: "playing", status: &spotify.PlaybackStatus{TrackName: "Bohemian Rhapsody", ArtistName: "Queen", AlbumName: "A Night at the Opera", Playing: true, Volume: 80}},
		{name: "paused-short", status: &spotify.PlaybackStatus{TrackName: "One", ArtistName: "Metallica", AlbumName: "…And Justice for All", Playing: false, Volume: 45}},
		{name: "shuffle-repeat", status: &spotify.PlaybackStatus{TrackName: "Untitled", ArtistName: "Interpol", AlbumName: "Turn On the Bright Lights", Playing: true, Volume: 70, ShuffleState: true, RepeatContext: true}},
	}
	for _, w := range []int{60, 100, 160} {
		for _, tc := range cases {
			m := NewLoaderModel()
			m.ui.width = w
			m.ui.height = 40
			m.transport.status = tc.status

			lines := strings.Split(ansi.Strip(m.headerView()), "\n")
			mid1 := spanMid(lines[0], tc.status.TrackName)
			mid2 := trimmedSpanMid(lines[1])
			if absInt(mid1-mid2) > 1 {
				t.Errorf("w=%d %s: header lines drift apart: titleMid=%d subMid=%d", w, tc.name, mid1, mid2)
			}
		}
	}
}

func spanMid(line, needle string) int {
	stripped := ansi.Strip(line)
	before, _, ok := strings.Cut(stripped, needle)
	if !ok {
		return 0
	}
	first := ansi.StringWidth(before)
	return first + (ansi.StringWidth(needle)+1)/2
}

func trimmedSpanMid(line string) int {
	stripped := ansi.Strip(line)
	first := ansi.StringWidth(stripped[:len(stripped)-len(strings.TrimLeft(stripped, " "))])
	content := strings.TrimRight(strings.TrimLeft(stripped, " "), " ")
	return first + ansi.StringWidth(content)/2
}

// The fix must leave an all-zones-fit row byte-identical.
func TestLayoutThreeZoneFittingRowUnchanged(t *testing.T) {
	out := layoutThreeZone(80, strings.Repeat("L", 10), strings.Repeat("C", 20), strings.Repeat("R", 10))
	want := strings.Repeat("L", 10) + strings.Repeat(" ", 20) + strings.Repeat("C", 20) + strings.Repeat(" ", 20) + strings.Repeat("R", 10)
	if out != want {
		t.Errorf("fitting row changed:\n got %q\nwant %q", out, want)
	}
}
