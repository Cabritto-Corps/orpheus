package tui

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// The on-song-change hook child shares the TUI's tty: anything it prints
// persists on screen forever under the cell-diffing renderer. The seam must
// hand back a command whose output is discarded.
func TestSongChangeHookDiscardsChildOutput(t *testing.T) {
	cmd, cancel := newSongChangeCmd("notify-send {track} {artist} {id}", "Song", "Artist", "id123")
	defer cancel()
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
	if got, cancel := newSongChangeCmd("   ", "Song", "Artist", "id123"); got != nil || cancel != nil {
		if cancel != nil {
			cancel()
		}
		t.Error("blank template must build no command")
	}
}

func TestSongChangeHookExecutesWithLiveContext(t *testing.T) {
	// An immediately-canceled builder must fail before Start.
	if err := runSongChangeHook("/bin/true", "Song", "Artist", "id123"); err != nil {
		t.Fatalf("hook runner failed: %v", err)
	}
}

func TestSongChangeHookEnforcesTimeout(t *testing.T) {
	cmd, hookCtx, cancel := newSongChangeCmdWithTimeout("/bin/sleep 5", "Song", "Artist", "id123", 100*time.Millisecond)
	defer cancel()
	if cmd == nil {
		t.Fatal("expected a command for a non-empty template")
	}
	start := time.Now()
	if err := cmd.Run(); err == nil {
		t.Fatal("expected the hook child to fail under the timeout")
	}
	if !errors.Is(hookCtx.Err(), context.DeadlineExceeded) {
		t.Fatalf("expected the hook deadline to expire, got %v", hookCtx.Err())
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("hook waited for the child instead of timing out: %v", elapsed)
	}
}
