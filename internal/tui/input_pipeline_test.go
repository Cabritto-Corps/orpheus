package tui

import (
	"testing"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func TestRequeueFrontCarriesRetryCount(t *testing.T) {
	m := model{}

	// requeueFront prepends; the pump dequeues before executing, so each
	// requeue decision is made against a fresh queue.
	m.requeueFront(playbackInputNext, 0)
	if len(m.transport.inputQueue) != 1 || m.transport.inputQueue[0].retryCount != 1 {
		t.Fatalf("expected requeued next with retryCount=1, got %+v", m.transport.inputQueue)
	}

	m.transport.inputQueue = nil
	m.requeueFront(playbackInputNext, 1)
	if len(m.transport.inputQueue) != 1 || m.transport.inputQueue[0].retryCount != 2 {
		t.Fatalf("expected retryCount=2, got %+v", m.transport.inputQueue)
	}

	// The 3-retry cap: prevRetries == maxRequeueRetries-1 drops the action.
	m.transport.inputQueue = nil
	m.requeueFront(playbackInputNext, maxRequeueRetries-1)
	if len(m.transport.inputQueue) != 0 {
		t.Fatalf("expected cap to drop the action after %d retries, got %+v", maxRequeueRetries, m.transport.inputQueue)
	}
}

func TestExecutePlaybackInputRequeuesOnFullChannel(t *testing.T) {
	ch := make(chan librespot.TUICommand, 1)
	m := model{ui: uiModel{keys: newKeys()}, tuiCmdCh: ch}
	// Fill the channel so skip sends fail.
	ch <- librespot.TUICommand{Kind: librespot.TUICommandSkipNext}

	m.executePlaybackInput(playbackInputNext, 0)
	if len(m.transport.inputQueue) != 1 || m.transport.inputQueue[0].retryCount != 1 {
		t.Fatalf("expected requeued next with retryCount=1, got %+v", m.transport.inputQueue)
	}
}

func TestApplyOptimisticSkipSkipsPastCurrentTrack(t *testing.T) {
	m := model{}
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", TrackName: "One", Playing: true}
	m.transport.queue = []spotify.QueueItem{
		{ID: "track-1", Name: "One"},
		{ID: "track-2", Name: "Two", DurationMS: 3000},
	}

	m.applyOptimisticSkip(true)
	if m.transport.status.TrackID != "track-2" || m.transport.status.TrackName != "Two" {
		t.Fatalf("expected skip to aim past the current track, got %+v", m.transport.status)
	}
}

func TestApplyOptimisticSkipUsesHeadWhenItDiffers(t *testing.T) {
	m := model{}
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-9", TrackName: "Nine", Playing: true}
	m.transport.queue = []spotify.QueueItem{
		{ID: "track-1", Name: "One"},
		{ID: "track-2", Name: "Two", DurationMS: 3000},
	}

	m.applyOptimisticSkip(true)
	if m.transport.status.TrackID != "track-1" {
		t.Fatalf("expected skip to aim at queue head, got %+v", m.transport.status)
	}
}
