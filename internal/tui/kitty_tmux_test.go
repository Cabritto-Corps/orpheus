package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"

	"orpheus/internal/spotify"
)

func tmuxOverlayModel() model {
	m := framedTestModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = strings.Repeat("ABCD", 3000)
	return m
}

func TestKittyTmuxWrapsEveryGraphicsPacket(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test,1,2")
	m := tmuxOverlayModel()
	out := m.kittyOverlay()
	if out == "" {
		t.Fatal("expected overlay emission under tmux")
	}

	rect := m.coverArt(m.bodyLayout().coverCols, m.bodyLayout().coverRows)
	cup := fmt.Sprintf("\x1b[%d;%dH", rect.row, rect.col)
	cupIdx := strings.Index(out, cup)
	wrapIdx := strings.Index(out, "\x1bPtmux;")
	if cupIdx < 0 {
		t.Fatalf("expected CUP %q outside the wrapper, got %q", cup, out)
	}
	if wrapIdx < 0 {
		t.Fatalf("expected tmux DCS wrappers around graphics packets, got %q", out)
	}
	if cupIdx > wrapIdx {
		t.Fatal("the cursor move drives tmux's own cursor: it must stay outside the passthrough wrapper")
	}

	// One DCS wrapper per packet (this emission carries no delete).
	wantWrappers := (len(m.ui.imgs.encoded["u1"]) + kitty.MaxChunkSize - 1) / kitty.MaxChunkSize
	if got := strings.Count(out, "\x1bPtmux;"); got != wantWrappers {
		t.Fatalf("expected %d DCS wrappers (one per packet), got %d in %q", wantWrappers, got, out)
	}

	for i := 0; ; {
		j := strings.Index(out[i:], "\x1b_G")
		if j < 0 {
			break
		}
		j += i
		if j == 0 || out[j-1] != '\x1b' {
			t.Fatalf("found an unwrapped graphics packet at byte %d: tmux would swallow it", j)
		}
		i = j + 1
	}
}

func TestKittyNoTmuxLeavesBytesUntouched(t *testing.T) {
	t.Setenv("TMUX", "")
	m := tmuxOverlayModel()
	out := m.kittyOverlay()
	if out == "" {
		t.Fatal("expected overlay emission without tmux")
	}
	if strings.Contains(out, "\x1bPtmux;") {
		t.Fatalf("no tmux session: must not wrap graphics packets, got %q", out)
	}
	if !strings.HasPrefix(out, "\x1b7\x1b[") {
		t.Fatalf("expected save-cursor + CUP framing for the direct-write channel, got %q", out)
	}
}
