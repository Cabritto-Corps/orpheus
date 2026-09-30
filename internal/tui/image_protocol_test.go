package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"

	"orpheus/internal/spotify"
)

func TestDetectImageProtocol(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want imageProtocol
	}{
		{
			name: "override none",
			env: map[string]string{
				"ORPHEUS_IMAGE_PROTOCOL": "none",
				"KITTY_WINDOW_ID":        "1",
			},
			want: imageProtocolNone,
		},
		{
			name: "kitty window id",
			env: map[string]string{
				"KITTY_WINDOW_ID": "1",
			},
			want: imageProtocolKitty,
		},
		{
			name: "ghostty term program",
			env: map[string]string{
				"TERM_PROGRAM": "ghostty",
			},
			want: imageProtocolKitty,
		},
		{
			name: "fallback ansi",
			env:  map[string]string{},
			want: imageProtocolNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectImageProtocol(func(key string) string {
				return tc.env[key]
			})
			if got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestEncodeKittyChunksCanonicalFraming(t *testing.T) {
	t.Setenv("TMUX", "")
	payload := strings.Repeat("A", 9000)
	out := encodeKittyChunks(chunkBase64(payload, kitty.MaxChunkSize), 10, 6, 7)

	// First chunk carries the full transmit-and-display options in the
	// library's canonical order, with cursor motion suppressed.
	for _, want := range []string{"f=100", "q=2", "i=7", "c=10", "r=6", "C=1", "a=T", "m=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("first chunk missing %q in %q", want, out[:120])
		}
	}
	if strings.Count(out, "\x1b_G") != 3 {
		t.Fatal("expected three chunk packets")
	}
	// Continuations carry only the more flag; the last one terminates
	// the transmission. Quiet mode lives on the first chunk alone.
	if !strings.Contains(out, "\x1b_Gm=1;") {
		t.Fatal("expected middle chunks as bare m=1 continuations")
	}
	if !strings.HasSuffix(out, "\x1b_Gm=0;"+strings.Repeat("A", 9000%kitty.MaxChunkSize)+"\x1b\\") {
		t.Fatal("expected last chunk to terminate with m=0")
	}
}

func TestEncodeKittyChunksOmitsMoreFlagForSingleChunk(t *testing.T) {
	out := buildKittyPayload("ZmFrZQ==", 10, 6, 42)
	if !strings.Contains(out, "i=42") {
		t.Fatalf("expected kitty payload to include image id, got %q", out)
	}
	if !strings.Contains(out, "a=T") {
		t.Fatalf("expected transmit-and-display action, got %q", out)
	}
	if strings.Contains(out, "m=") {
		t.Fatalf("single-chunk payload must not carry a more flag, got %q", out)
	}
}

func TestKittyRecoveryNeverFiresWithoutFallback(t *testing.T) {
	m := model{ui: uiModel{imgs: newImgCache(), cover: newCoverManager()}}
	m.ui.imgs.setProtocol(imageProtocolNone)
	for range kittyProtocolRecoveryStreak * 2 {
		m.maybeRecoverKittyProtocol()
	}
	if m.ui.imgs.protocolForRender() != imageProtocolNone {
		t.Fatal("recovery must not enable kitty on a terminal where it never worked")
	}
	if m.ui.cover.kittyRecoveryStreak != 0 {
		t.Fatalf("recovery streak must not accumulate without a fallback, got %d", m.ui.cover.kittyRecoveryStreak)
	}
}

func TestKittyRecoveryFollowsRealFallback(t *testing.T) {
	const url = "https://img/cover"
	m := model{ui: uiModel{imgs: newImgCache(), cover: newCoverManager()}}
	m.ui.imgs.setProtocol(imageProtocolKitty)
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: url}
	m.ui.cover.playerCoverFailStreak = kittyProtocolFallbackFailures
	m.maybeFallbackFromKittyOnPlayerFailures(url)
	if m.ui.imgs.protocolForRender() != imageProtocolNone {
		t.Fatal("expected fallback to disable kitty after repeated player cover failures")
	}
	for range kittyProtocolRecoveryStreak {
		m.maybeRecoverKittyProtocol()
	}
	if m.ui.imgs.protocolForRender() != imageProtocolKitty {
		t.Fatal("expected recovery to re-enable kitty after a healthy streak following a real fallback")
	}
	if m.ui.cover.kittyFellBack {
		t.Fatal("expected the fallback flag to clear once kitty is re-enabled")
	}
}
