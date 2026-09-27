package tui

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func newQueueKeyTestModel() model {
	m := newModel(context.Background(), nil, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
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

func TestQueueRemoveFollowsReboundKey(t *testing.T) {
	m := newQueueKeyTestModel()
	m.ui.keys.QueueRemove = key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "remove from queue"))
	cmdCh := make(chan librespot.TUICommand, 2)
	m.tuiCmdCh = cmdCh

	next, _ := m.handlePlaybackKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
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
	next, _ = m.handlePlaybackKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
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

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = next.(model)
	if m.browse.playlistList.FilterState() != list.Filtering {
		t.Fatal("expected the rebound filter key to start filtering")
	}

	m2 := newQueueKeyTestModel()
	m2.ui.activeTab = tabPlaylists
	m2.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "pl1", URI: "spotify:playlist:pl1", Name: "One"}},
	})
	next, _ = m2.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m2 = next.(model)
	if m2.browse.playlistList.FilterState() == list.Filtering {
		t.Fatal("unbound key must not start filtering with default bindings")
	}
}
