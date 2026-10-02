package tui

import (
	"testing"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func TestRequeueFrontCarriesRetryCount(t *testing.T) {
	m := model{}

	m.requeueFront(playbackInputNext, 0)
	if len(m.transport.inputQueue) != 1 || m.transport.inputQueue[0].retryCount != 1 {
		t.Fatalf("expected requeued next with retryCount=1, got %+v", m.transport.inputQueue)
	}

	m.transport.inputQueue = nil
	m.requeueFront(playbackInputNext, 1)
	if len(m.transport.inputQueue) != 1 || m.transport.inputQueue[0].retryCount != 2 {
		t.Fatalf("expected retryCount=2, got %+v", m.transport.inputQueue)
	}

	m.transport.inputQueue = nil
	m.requeueFront(playbackInputNext, maxRequeueRetries-1)
	if len(m.transport.inputQueue) != 0 {
		t.Fatalf("expected cap to drop the action after %d retries, got %+v", maxRequeueRetries, m.transport.inputQueue)
	}
}

func TestExecutePlaybackInputRequeuesOnFullChannel(t *testing.T) {
	ch := make(chan librespot.TUICommand, 1)
	m := model{ui: uiModel{keys: newKeys()}, tuiCmdCh: ch}
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

func volTestModel(ch chan librespot.TUICommand, vol int) model {
	m := model{ui: uiModel{keys: newKeys()}, tuiCmdCh: ch}
	m.transport.status = &spotify.PlaybackStatus{Volume: vol, TrackID: "t1", Playing: true}
	m.transport.volDebouncePending = -1
	m.transport.volSentTarget = -1
	m.transport.seekDebouncePending = -1
	m.transport.seekSentTarget = -1
	return m
}

func TestVolumeKeypressUpdatesBarSameFrame(t *testing.T) {
	ch := make(chan librespot.TUICommand, 8)
	m := volTestModel(ch, 50)
	m.enqueuePlaybackInput(playbackInputVolUp)
	_ = m.pumpInputExecutor()
	if m.transport.status.Volume != 55 {
		t.Fatalf("volume keypress did not move the bar same-frame: %d", m.transport.status.Volume)
	}
}

func TestVolumeLeadingEdgeSendsImmediately(t *testing.T) {
	ch := make(chan librespot.TUICommand, 8)
	m := volTestModel(ch, 50)
	m.enqueuePlaybackInput(playbackInputVolUp)
	_ = m.pumpInputExecutor()
	select {
	case cmd := <-ch:
		if cmd.Kind != librespot.TUICommandSetVolume || cmd.Volume != 55 {
			t.Fatalf("unexpected leading-edge command: %+v", cmd)
		}
	default:
		t.Fatal("volume command was not sent synchronously with the keypress")
	}
	if m.transport.volDebouncePending >= 0 {
		t.Fatal("committed target must not stay pending")
	}
	if m.transport.volSentTarget != 55 {
		t.Fatalf("sent target not recorded: %d", m.transport.volSentTarget)
	}
}

func TestVolumeBurstKeepsBarCurrent(t *testing.T) {
	ch := make(chan librespot.TUICommand, 16)
	m := volTestModel(ch, 50)
	for range 10 {
		m.enqueuePlaybackInput(playbackInputVolUp)
		_ = m.pumpInputExecutor()
	}
	if m.transport.status.Volume != 100 {
		t.Fatalf("rapid presses did not keep the bar current: %d", m.transport.status.Volume)
	}
	if len(ch) != 10 {
		t.Fatalf("expected one command per press, got %d", len(ch))
	}
	prev := -1
	for range 10 {
		cmd := <-ch
		if cmd.Volume <= prev {
			t.Fatal("commands out of order")
		}
		prev = cmd.Volume
	}
	if prev != 100 {
		t.Fatalf("last command must carry the final target, got %d", prev)
	}
}

func TestVolumeExecutesDuringTransportTransition(t *testing.T) {
	ch := make(chan librespot.TUICommand, 8)
	m := volTestModel(ch, 50)
	m.beginTransportTransition()
	m.enqueuePlaybackInput(playbackInputVolUp)
	_ = m.pumpInputExecutor()
	if m.transport.status.Volume != 55 {
		t.Fatalf("volume froze during transition: %d", m.transport.status.Volume)
	}
	select {
	case cmd := <-ch:
		if cmd.Kind != librespot.TUICommandSetVolume || cmd.Volume != 55 {
			t.Fatalf("unexpected command during transition: %+v", cmd)
		}
	default:
		t.Fatal("volume command was not sent during transition")
	}
}

func TestVolumeChannelFullFallsBackToDebounce(t *testing.T) {
	ch := make(chan librespot.TUICommand, 1)
	ch <- librespot.TUICommand{Kind: librespot.TUICommandShuffle}
	m := volTestModel(ch, 50)
	m.enqueuePlaybackInput(playbackInputVolUp)
	_ = m.pumpInputExecutor()
	if m.transport.status.Volume != 55 {
		t.Fatalf("display must move even when the channel is full: %d", m.transport.status.Volume)
	}
	if m.transport.volDebouncePending != 55 {
		t.Fatal("uncommitted target must stay pending for the debounce retry")
	}
	<-ch
	mm, _ := m.handleVolDebounceMsg(volDebounceMsg{token: m.transport.volDebounceToken})
	m = mm.(model)
	select {
	case cmd := <-ch:
		if cmd.Kind != librespot.TUICommandSetVolume || cmd.Volume != 55 {
			t.Fatalf("retry sent wrong command: %+v", cmd)
		}
	default:
		t.Fatal("debounce retry did not deliver the pending target")
	}
	if m.transport.volDebouncePending >= 0 || m.transport.volSentTarget != 55 {
		t.Fatal("retry must commit and record the sent target")
	}
}

func TestSeekStillTrailingDebounce(t *testing.T) {
	// Trailing-edge: a scrub burst coalesces into one player seek while the display moves optimistically.
	ch := make(chan librespot.TUICommand, 8)
	m := volTestModel(ch, 50)
	m.transport.status.DurationMS = 200000
	m.transport.status.ProgressMS = 60000
	m.enqueuePlaybackInput(playbackInputSeekFwd)
	_ = m.pumpInputExecutor()
	if m.transport.status.ProgressMS != 65000 {
		t.Fatalf("seek display did not move same-frame: %d", m.transport.status.ProgressMS)
	}
	if len(ch) != 0 {
		t.Fatal("seek must not send leading-edge")
	}
	if m.transport.seekDebouncePending != 65000 {
		t.Fatal("seek target must stay pending for the debounce tick")
	}
}

func TestRequeueFrontGiveUpSurfacesBusyError(t *testing.T) {
	m := model{}
	m.requeueFront(playbackInputNext, maxRequeueRetries-1)
	if len(m.transport.inputQueue) != 0 {
		t.Fatalf("expected the action to be dropped after %d retries, got %+v", maxRequeueRetries, m.transport.inputQueue)
	}
	if m.transport.playbackErr == nil || m.transport.playbackErr.Error() != "command could not be sent — player busy" {
		t.Fatalf("expected the give-up to surface a busy-player error, got %v", m.transport.playbackErr)
	}
}

func TestRequeueFrontSchedulesInputRetry(t *testing.T) {
	m := model{}
	cmd := m.requeueFront(playbackInputNext, 0)
	if cmd == nil {
		t.Fatal("expected a requeued action to schedule its own retry")
	}
	if _, ok := cmd().(inputRetryMsg); !ok {
		t.Fatalf("expected an input retry message, got %T", cmd())
	}
	if len(m.transport.inputQueue) != 1 || m.transport.inputQueue[0].retryCount != 1 {
		t.Fatalf("expected requeued next with retryCount=1, got %+v", m.transport.inputQueue)
	}
}

func TestRequeuedTransportActionRetriesOnInputTick(t *testing.T) {
	ch := make(chan librespot.TUICommand, 1)
	ch <- librespot.TUICommand{Kind: librespot.TUICommandShuffle}
	m := model{ui: uiModel{keys: newKeys()}, tuiCmdCh: ch}
	m.enqueuePlaybackInput(playbackInputNext)
	cmd := m.pumpInputExecutor()
	if cmd == nil {
		t.Fatal("expected a busy channel to schedule an input retry")
	}
	msg, ok := cmd().(inputRetryMsg)
	if !ok {
		t.Fatalf("expected an input retry message, got %T", msg)
	}
	next, _ := m.Update(msg)
	if got := next.(model); len(got.transport.inputQueue) != 1 || got.transport.inputQueue[0].kind != playbackInputNext {
		t.Fatalf("expected the retry tick to keep pumping the queued action, got %+v", got.transport.inputQueue)
	}
}
