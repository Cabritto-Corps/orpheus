package tui

import (
	"io"
	"testing"
)

// The on-song-change hook child shares the TUI's tty: anything it prints
// persists on screen forever under the cell-diffing renderer. The seam must
// hand back a command whose output is discarded.
func TestSongChangeHookDiscardsChildOutput(t *testing.T) {
	cmd := newSongChangeCmd("notify-send {track} {artist} {id}", "Song", "Artist", "id123")
	if cmd == nil {
		t.Fatal("expected a command for a non-empty template")
	}
	if cmd.Stdout != io.Discard {
		t.Error("hook stdout is not discarded — child output corrupts the frame")
	}
	if cmd.Stderr != io.Discard {
		t.Error("hook stderr is not discarded — child output corrupts the frame")
	}
	if got := cmd.Args; len(got) != 4 || got[0] != "notify-send" || got[1] != "Song" || got[2] != "Artist" || got[3] != "id123" {
		t.Errorf("hook args = %q, want metadata substituted without a shell", got)
	}
	if newSongChangeCmd("   ", "Song", "Artist", "id123") != nil {
		t.Error("blank template must build no command")
	}
}
