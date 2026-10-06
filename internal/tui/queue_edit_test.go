package tui

import (
	"context"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func newQueueKeyTestModel() model {
	m := newModel(context.Background(), nil, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	m.transport.status = &spotify.PlaybackStatus{TrackID: "spotify:track:playing", ContextURI: "spotify:playlist:testctx"}
	m.transport.queue = []spotify.QueueItem{
		{ID: "spotify:track:a", Name: "A", Queued: true},
		{ID: "spotify:track:b", Name: "B", Queued: true},
		{ID: "spotify:track:c", Name: "C", Queued: true},
	}
	m.transport.stableQueueLen = len(m.transport.queue)
	return m
}

func TestQueueCursorMoves(t *testing.T) {
	m := newQueueKeyTestModel()
	// The pushed queue is already the visible view.
	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	if m.transport.queueCursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.transport.queueCursor)
	}
	next, _ = m.handlePlaybackKey(tea.KeyPressMsg{Code: tea.KeyUp})
	m = next.(model)
	if m.transport.queueCursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.transport.queueCursor)
	}
	next, _ = m.handlePlaybackKey(tea.KeyPressMsg{Code: tea.KeyUp})
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

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
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

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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

func TestQueueRemoveFollowsReboundKey(t *testing.T) {
	m := newQueueKeyTestModel()
	m.ui.keys.QueueRemove = key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "remove from queue"))
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	m = next.(model)
	select {
	case cmd := <-cmdCh:
		if cmd.Kind != librespot.TUICommandQueueRemove || cmd.QueueIndex != 0 {
			t.Fatalf("unexpected command %+v", cmd)
		}
	default:
		t.Fatal("expected a remove command on the rebound key")
	}

	// The old literal no longer removes: dispatch follows the binding.
	next, _ = m.handlePlaybackKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(model)
	select {
	case cmd := <-cmdCh:
		t.Fatalf("rebound remove must not fire on the old literal, got %+v", cmd)
	default:
	}
}

func TestFilterFollowsReboundKey(t *testing.T) {
	m := newQueueKeyTestModel()
	m.ui.activeTab = tabPlaylists
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "pl1", URI: "spotify:playlist:pl1", Name: "One"}},
	})
	m.ui.keys.Filter = key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "search"))

	next, _ := m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = next.(model)
	if m.browse.playlistList.FilterState() != list.Filtering {
		t.Fatal("expected the rebound filter key to start filtering")
	}

	m2 := newQueueKeyTestModel()
	m2.ui.activeTab = tabPlaylists
	m2.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "pl1", URI: "spotify:playlist:pl1", Name: "One"}},
	})
	next, _ = m2.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m2 = next.(model)
	if m2.browse.playlistList.FilterState() == list.Filtering {
		t.Fatal("unbound key must not start filtering with default bindings")
	}
}

func queueRegionTestModel() model {
	m := newQueueKeyTestModel()
	m.transport.queue = []spotify.QueueItem{
		{ID: "spotify:track:q1", Name: "Q1", Queued: true},
		{ID: "spotify:track:q2", Name: "Q2", Queued: true},
		{ID: "spotify:track:c1", Name: "C1"},
		{ID: "spotify:track:c2", Name: "C2"},
	}
	return m
}

func drainCmdCh(ch chan librespot.TUICommand) *librespot.TUICommand {
	select {
	case cmd := <-ch:
		return &cmd
	default:
		return nil
	}
}

func TestQueueRemoveContextRowSends(t *testing.T) {
	m := queueRegionTestModel()
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh
	m.transport.queueCursor = 2

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(model)
	if m.transport.playbackErr != nil {
		t.Fatalf("context-row remove must not hint, got %v", m.transport.playbackErr)
	}
	cmd := drainCmdCh(cmdCh)
	if cmd == nil || cmd.Kind != librespot.TUICommandQueueRemove || cmd.QueueIndex != 2 {
		t.Fatalf("expected remove 2, got %+v", cmd)
	}
}

func TestQueueReorderContextRowsSend(t *testing.T) {
	m := queueRegionTestModel()
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh
	m.transport.queueCursor = 2

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	m = next.(model)
	if m.transport.playbackErr != nil {
		t.Fatalf("context reorder must not hint, got %v", m.transport.playbackErr)
	}
	cmd := drainCmdCh(cmdCh)
	if cmd == nil || cmd.Kind != librespot.TUICommandQueueReorder || cmd.QueueIndex != 2 || cmd.QueueTargetIndex != 3 {
		t.Fatalf("expected reorder 2->3, got %+v", cmd)
	}
}

func TestQueueReorderIntoContextRowHints(t *testing.T) {
	m := queueRegionTestModel()
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh
	m.transport.queueCursor = 1

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	m = next.(model)
	if m.transport.playbackErr == nil {
		t.Fatal("expected a hint error moving a queued row into context rows")
	}
	if cmd := drainCmdCh(cmdCh); cmd != nil {
		t.Fatalf("cross-region reorder must not send, got %+v", cmd)
	}
}

func TestQueueReorderManualRowsStillSend(t *testing.T) {
	m := queueRegionTestModel()
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh
	m.transport.queueCursor = 0

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: ']', Text: "]"})
	m = next.(model)
	if m.transport.playbackErr != nil {
		t.Fatalf("manual reorder must not hint, got %v", m.transport.playbackErr)
	}
	cmd := drainCmdCh(cmdCh)
	if cmd == nil || cmd.Kind != librespot.TUICommandQueueReorder || cmd.QueueIndex != 0 || cmd.QueueTargetIndex != 1 {
		t.Fatalf("expected reorder 0->1, got %+v", cmd)
	}
}

func TestQueueJumpContextRowPlaysFromTrack(t *testing.T) {
	m := queueRegionTestModel()
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh
	m.transport.queueCursor = 2

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	cmd := drainCmdCh(cmdCh)
	if cmd == nil || cmd.Kind != librespot.TUICommandPlayContextFromTrack {
		t.Fatalf("context-row enter must play from track, got %+v", cmd)
	}
	if cmd.URI != "spotify:playlist:testctx" || cmd.TrackID != "spotify:track:c1" {
		t.Fatalf("wrong play-from-track target: %+v", cmd)
	}
	if m.transport.pendingContextFrom != "spotify:track:playing" {
		t.Fatalf("pendingContextFrom = %q, want current track", m.transport.pendingContextFrom)
	}
}

func TestQueueJumpManualRowJumps(t *testing.T) {
	m := queueRegionTestModel()
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh
	m.transport.queueCursor = 0

	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	cmd := drainCmdCh(cmdCh)
	if cmd == nil || cmd.Kind != librespot.TUICommandQueueJump || cmd.QueueIndex != 0 {
		t.Fatalf("queued-row enter must jump, got %+v", cmd)
	}
}

func TestQueueHeadMatchingCurrentStaysVisible(t *testing.T) {
	// The old ID-based head strip hid this row and shifted every command.
	m := newQueueKeyTestModel()
	m.transport.queue = []spotify.QueueItem{
		{ID: "spotify:track:playing", Name: "Same", Queued: true},
		{ID: "spotify:track:other", Name: "Other", Queued: true},
	}
	if got := len(m.visibleQueue()); got != 2 {
		t.Fatalf("visible rows = %d, want 2 (no head strip)", got)
	}
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh
	m.transport.queueCursor = 0
	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(model)
	cmd := drainCmdCh(cmdCh)
	if cmd == nil || cmd.Kind != librespot.TUICommandQueueRemove || cmd.QueueIndex != 0 {
		t.Fatalf("duplicate head remove must address 0, got %+v", cmd)
	}
}
