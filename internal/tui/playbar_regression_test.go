package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"orpheus/internal/spotify"
)

// The play bar must never render negative times or crash, at any transport
// state — stale transfers and past-end starts can push negative or
// beyond-duration positions, and the pre-resize startup frame can hand it a
// zero width.
func TestPlayBarNeverNegativeOrPanics(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "0")

	cases := []struct {
		name       string
		width      int
		progressMS int
		durationMS int
	}{
		{"progress past duration", 120, 240000, 200000},
		{"duration zero with progress", 120, 37000, 0},
		{"negative progress", 120, -5000, 200000},
		{"negative duration", 120, 37000, -200000},
		{"both negative", 120, -5000, -200000},
		{"narrow terminal", 10, 37000, 200000},
		{"narrow terminal no duration", 10, 37000, 0},
		{"zero width", 0, 37000, 200000},
		{"zero width no duration", 0, 37000, 0},
		{"control", 120, 37000, 200000},
	}
	for _, tc := range cases {
		m := framedTestModel()
		m.ui.width = tc.width
		m.ui.height = 40
		m.transport.status = &spotify.PlaybackStatus{
			Playing:    true,
			TrackName:  "probe",
			ArtistName: "probe",
			ProgressMS: tc.progressMS,
			DurationMS: tc.durationMS,
		}
		var out string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: playerBarView panicked: %v", tc.name, r)
				}
			}()
			out = m.playerBarView()
		}()
		if out == "" {
			continue
		}
		stripped := ansi.Strip(out)
		// The only legitimate hyphen runs are the "--:--" unknown-duration
		// marker; anything else is a rendered negative.
		if withMarker := strings.ReplaceAll(stripped, "--:--", ""); strings.Contains(withMarker, "-") {
			t.Errorf("%s: rendered a negative time: %q", tc.name, stripped)
		}
		for i, line := range strings.Split(stripped, "\n") {
			if tc.width >= 60 && lipgloss.Width(line) > tc.width {
				t.Errorf("%s: row %d is %d cells, wider than the %d-cell frame", tc.name, i, lipgloss.Width(line), tc.width)
			}
		}
	}
}

func TestFmtDurationNegativeClampsToZero(t *testing.T) {
	for _, ms := range []int{-1, -5000, -3600000} {
		if got := fmtDuration(ms); got != "0:00" {
			t.Errorf("fmtDuration(%d) = %q, want 0:00", ms, got)
		}
	}
}
