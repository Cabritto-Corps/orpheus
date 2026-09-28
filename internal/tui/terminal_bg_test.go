package tui

import (
	"io"
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// withTerminalBGState pins the terminal-background globals and the style
// for one decision test; OSC 11 capture belongs to the live session, so
// tests drive the values directly and restore them after.
func withTerminalBGState(t *testing.T, original, last, style string) {
	t.Helper()
	prevOrig, prevLast, prevBg := terminalBGOriginal, terminalBGLast, activeBackgrounds
	terminalBGOriginal, terminalBGLast = original, last
	activeBackgrounds = themeBackgrounds{Style: style}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "0")
	t.Cleanup(func() {
		terminalBGOriginal, terminalBGLast = prevOrig, prevLast
		activeBackgrounds = prevBg
		t.Setenv("NO_COLOR", "1")
	})
}

func TestTerminalBGTargetDecisions(t *testing.T) {
	orig := "rgb:0000/0000/0000"
	cases := []struct {
		name     string
		original string
		last     string
		style    string
		page     string
		want     string
	}{
		{"no capture stays silent", "", "#0A0D12", "solid", "#0A0D12", ""},
		{"painted same page stays silent", orig, "rgb:0a0a/0d0d/1212", "solid", "#0A0D12", ""},
		{"painted new page emits", orig, "rgb:0a0a/0d0d/1212", "solid", "#101216", "rgb:1010/1212/1616"},
		{"painted invalid page stays silent", orig, "#0A0D12", "solid", "notacolor", ""},
		{"transparent with nothing set stays silent", orig, "", "transparent", "#0A0D12", ""},
		{"transparent entry restores the capture", orig, "#0A0D12", "transparent", "#0A0D12", orig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTerminalBGState(t, tc.original, tc.last, tc.style)
			if got := terminalBGTarget(lipgloss.Color(tc.page)); got != tc.want {
				t.Fatalf("terminalBGTarget(%q) = %q, want %q", tc.page, got, tc.want)
			}
		})
	}
}

func TestTerminalBGTargetAsciiStaysSilent(t *testing.T) {
	withTerminalBGState(t, "rgb:0000/0000/0000", "", "solid")
	t.Setenv("NO_COLOR", "1")
	if got := terminalBGTarget(lipgloss.Color("#0A0D12")); got != "" {
		t.Fatalf("ascii profile must stay silent, got %q", got)
	}
}

// TestApplyTerminalBGTransparentEntryRestores captures stdout around the
// entry transition: the captured original is re-emitted exactly once and
// the last-set marker clears so later frames stay silent.
func TestApplyTerminalBGTransparentEntryRestores(t *testing.T) {
	orig := "rgb:0000/0000/0000"
	withTerminalBGState(t, orig, "#0A0D12", "transparent")

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	ApplyTerminalBG(lipgloss.Color("#0A0D12"))
	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), orig) {
		t.Fatalf("transparent entry must restore the capture, emitted %q", out)
	}
	if terminalBGLast != "" {
		t.Fatalf("restore must clear the last-set marker, got %q", terminalBGLast)
	}
}
