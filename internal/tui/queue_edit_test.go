package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func newQueueKeyTestModel() model {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil, nil, nil)
	m.transport.status = &spotify.PlaybackStatus{TrackID: "spotify:track:playing"}
	m.transport.queue = []spotify.QueueItem{
		{ID: "spotify:track:a", Name: "A"},
		{ID: "spotify:track:b", Name: "B"},
		{ID: "spotify:track:c", Name: "C"},
	}
	m.transport.stableQueueLen = len(m.transport.queue)
	return m
}

func TestQueueCursorMoves(t *testing.T) {
	m := newQueueKeyTestModel()
	// visibleQueue hides the playing head when it matches the status track;
	// here the head does not match, so all 3 entries are visible.
	next, _ := m.handlePlaybackKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(model)
	if m.transport.queueCursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.transport.queueCursor)
	}
	next, _ = m.handlePlaybackKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.transport.queueCursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.transport.queueCursor)
	}
	// up at top stays clamped
	next, _ = m.handlePlaybackKey(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(model)
	if m.transport.queueCursor != 0 {
		t.Fatalf("cursor = %d, want 0 (clamped)", m.transport.queueCursor)
	}
}

func TestQueueRemoveSendsCommand(t *testing.T) {
	m := newQueueKeyTestModel()
	cmdCh := make(chan librespot.TUICommand, 1)
	m.tuiCmdCh = cmdCh
	defer close(cmdCh)

	next, _ := m.handlePlaybackKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(model)

	select {
	case cmd := <-cmdCh:
		if cmd.Kind != librespot.TUICommandQueueRemove || cmd.QueueIndex != 0 {
			t.Fatalf("unexpected command %+v", cmd)
		}
	default:
		t.Fatal("expected a remove command on the channel")
	}
}

func TestQueueJumpBlockedDuringTransition(t *testing.T) {
	m := newQueueKeyTestModel()
	cmdCh := make(chan librespot.TUICommand, 1)
	defer close(cmdCh)
	m.tuiCmdCh = cmdCh
	m.transport.transition.Begin(time.Now(), "spotify:track:a")

	next, _ := m.handlePlaybackKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	select {
	case cmd := <-cmdCh:
		t.Fatalf("jump must be blocked during transition, got %+v", cmd)
	default:
	}
}

func TestQueueCursorClampedOnQueueChange(t *testing.T) {
	m := newQueueKeyTestModel()
	m.transport.queueCursor = 2

	m.applyMergedQueue(m.transport.queue[:1], false, false, false)
	if m.transport.queueCursor != 0 {
		t.Fatalf("cursor = %d, want 0 after queue shrink", m.transport.queueCursor)
	}
}
