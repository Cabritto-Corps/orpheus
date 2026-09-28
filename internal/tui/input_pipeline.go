package tui

import (
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/librespot"
	"orpheus/internal/playbackdomain"
)

type commandExecutorState string

const (
	executorStateIdle              commandExecutorState = "idle"
	executorStateAwaitingTransport commandExecutorState = "awaiting-transport"
	maxInputQueueSize                                   = 96
	seekStepMS                                          = 5000
	maxInputActionsPerTick                              = 8
	maxRequeueRetries                                   = 3
)

type playbackInputKind string
type inputPriority int

type playbackInput struct {
	kind       playbackInputKind
	priority   inputPriority
	retryCount int
}

const (
	playbackInputPlayPause playbackInputKind = "play-pause"
	playbackInputNext      playbackInputKind = "next"
	playbackInputPrev      playbackInputKind = "prev"
	playbackInputShuffle   playbackInputKind = "shuffle"
	playbackInputLoop      playbackInputKind = "loop"
	playbackInputVolUp     playbackInputKind = "vol-up"
	playbackInputVolDown   playbackInputKind = "vol-down"
	playbackInputSeekBack  playbackInputKind = "seek-back"
	playbackInputSeekFwd   playbackInputKind = "seek-fwd"

	inputPriorityLow      inputPriority = 0
	inputPriorityNormal   inputPriority = 1
	inputPriorityHigh     inputPriority = 2
	inputPriorityCritical inputPriority = 3
)

func (m *model) syncExecutorState() {
	switch {
	case m.transport.transition.Pending():
		m.transport.executorState = executorStateAwaitingTransport
	default:
		m.transport.executorState = executorStateIdle
	}
}

func (m *model) enqueuePlaybackInput(action playbackInputKind) {
	if len(m.transport.inputQueue) >= maxInputQueueSize {
		m.transport.inputQueue = m.transport.inputQueue[1:]
	}
	if isVolumeAction(action) {
		m.dropQueuedByPredicate(isVolumeAction)
	}
	if isSeekAction(action) {
		m.dropQueuedByPredicate(isSeekAction)
	}
	if shouldDedupQueuedAction(action) && m.hasQueuedAction(action) {
		return
	}
	m.transport.inputQueue = append(m.transport.inputQueue, playbackInput{
		kind:     action,
		priority: inputPriorityOf(action),
	})
}

func (m *model) requeueFront(action playbackInputKind, prevRetries int) {
	// The retry count travels with the popped action: matching against
	// inputQueue[0] never fired because the failed item was already dequeued.
	retries := prevRetries + 1
	if retries >= maxRequeueRetries {
		slog.Debug("dropping playback action after retries", "kind", action)
		return
	}
	if len(m.transport.inputQueue) >= maxInputQueueSize {
		m.transport.inputQueue = m.transport.inputQueue[:maxInputQueueSize-1]
	}
	m.transport.inputQueue = append([]playbackInput{{kind: action, priority: inputPriorityOf(action), retryCount: retries}}, m.transport.inputQueue...)
}

func (m *model) pumpInputExecutor() tea.Cmd {
	for range maxInputActionsPerTick {
		m.syncExecutorState()
		if len(m.transport.inputQueue) == 0 {
			return nil
		}
		idx := m.dequeueNextInputIndex()
		if m.transport.executorState != executorStateIdle {
			// Volume is an idempotent set-command that never touches
			// transition state, and the key handler deliberately
			// leaves it unblocked during transitions — let it drain
			// so the bar and the audio stay live while a track
			// change is in flight. Everything else stays queued.
			idx = m.dequeueNextVolumeIndex()
			if idx < 0 {
				return nil
			}
		}
		action := m.transport.inputQueue[idx].kind
		retryCount := m.transport.inputQueue[idx].retryCount
		m.transport.inputQueue = append(m.transport.inputQueue[:idx], m.transport.inputQueue[idx+1:]...)
		if cmd := m.executePlaybackInput(action, retryCount); cmd != nil {
			return cmd
		}
	}
	return nil
}

func (m *model) executePlaybackInput(action playbackInputKind, retryCount int) tea.Cmd {
	switch action {
	case playbackInputPlayPause:
		if m.tuiCmdCh != nil {
			kind := librespot.TUICommandResume
			if m.transport.status != nil && m.transport.status.Playing {
				kind = librespot.TUICommandPause
			}
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: kind}:
				return nil
			default:
				m.requeueFront(action, retryCount)
				return nil
			}
		}
		return nil
	case playbackInputNext:
		if m.tuiCmdCh != nil {
			if !m.trySendTransportSkip(librespot.TUICommandSkipNext) {
				m.requeueFront(action, retryCount)
				return nil
			}
			m.applyOptimisticSkip(true)
			m.beginTransportTransition()
			return nil
		}
		return nil
	case playbackInputPrev:
		if m.tuiCmdCh != nil {
			if !m.trySendTransportSkip(librespot.TUICommandSkipPrev) {
				m.requeueFront(action, retryCount)
				return nil
			}
			m.applyOptimisticSkip(false)
			m.beginTransportTransition()
			return nil
		}
		return nil
	case playbackInputShuffle:
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandShuffle}:
			default:
				m.requeueFront(action, retryCount)
				return nil
			}
			return nil
		}
		return nil
	case playbackInputLoop:
		if m.transport.status == nil {
			return nil
		}
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandCycleRepeat}:
				next := playbackdomain.NextRepeatTraversalOptions(playbackdomain.TraversalOptions{RepeatContext: m.transport.status.RepeatContext, RepeatTrack: m.transport.status.RepeatTrack})
				m.transport.status.RepeatContext = next.RepeatContext
				m.transport.status.RepeatTrack = next.RepeatTrack
			default:
				m.requeueFront(action, retryCount)
			}
			return nil
		}
		return nil
	case playbackInputVolUp:
		if m.transport.status == nil {
			return nil
		}
		var target int
		if m.transport.volDebouncePending >= 0 {
			target = clampInt(m.transport.volDebouncePending+5, 0, 100)
		} else {
			target = clampInt(m.transport.status.Volume+5, 0, 100)
		}
		m.transport.status.Volume = target
		m.transport.volDebouncePending = target
		m.transport.volDebounceToken++
		if m.trySendVolume(target) {
			return nil
		}
		return m.volDebounceCmd(m.transport.volDebounceToken)
	case playbackInputVolDown:
		if m.transport.status == nil {
			return nil
		}
		var target int
		if m.transport.volDebouncePending >= 0 {
			target = clampInt(m.transport.volDebouncePending-5, 0, 100)
		} else {
			target = clampInt(m.transport.status.Volume-5, 0, 100)
		}
		m.transport.status.Volume = target
		m.transport.volDebouncePending = target
		m.transport.volDebounceToken++
		if m.trySendVolume(target) {
			return nil
		}
		return m.volDebounceCmd(m.transport.volDebounceToken)
	case playbackInputSeekBack:
		if m.transport.status == nil {
			return nil
		}
		current := m.seekSettleProgress()
		target := m.clampSeekTarget(current - seekStepMS)
		if target == current {
			return nil
		}
		m.transport.status.ProgressMS = target
		m.transport.seekDebouncePending = target
		m.transport.seekDebounceToken++
		return m.seekDebounceCmd(m.transport.seekDebounceToken)
	case playbackInputSeekFwd:
		if m.transport.status == nil {
			return nil
		}
		current := m.seekSettleProgress()
		target := m.clampSeekTarget(current + seekStepMS)
		if target == current {
			return nil
		}
		m.transport.status.ProgressMS = target
		m.transport.seekDebouncePending = target
		m.transport.seekDebounceToken++
		return m.seekDebounceCmd(m.transport.seekDebounceToken)
	default:
		return nil
	}
}

func isVolumeAction(action playbackInputKind) bool {
	return action == playbackInputVolUp || action == playbackInputVolDown
}

// trySendVolume commits a volume target to the player immediately
// (leading edge) instead of waiting out the debounce interval. The
// trailing debounce timer stays as the fallback when the command
// channel is full: pending keeps the target and the token guards the
// retry, so no press is ever lost to a busy player.
func (m *model) trySendVolume(target int) bool {
	if m.tuiCmdCh == nil {
		return false
	}
	select {
	case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSetVolume, Volume: target}:
		m.transport.volDebouncePending = -1
		m.transport.volSentTarget = target
		m.transport.volSentAt = time.Now()
		return true
	default:
		return false
	}
}

func isSeekAction(action playbackInputKind) bool {
	return action == playbackInputSeekBack || action == playbackInputSeekFwd
}

func shouldDedupQueuedAction(action playbackInputKind) bool {
	return action == playbackInputShuffle
}

func inputPriorityOf(action playbackInputKind) inputPriority {
	switch action {
	case playbackInputNext, playbackInputPrev:
		return inputPriorityCritical
	case playbackInputPlayPause:
		return inputPriorityHigh
	case playbackInputShuffle, playbackInputLoop:
		return inputPriorityNormal
	case playbackInputSeekBack, playbackInputSeekFwd, playbackInputVolUp, playbackInputVolDown:
		return inputPriorityLow
	default:
		return inputPriorityLow
	}
}

func (m *model) dequeueNextInputIndex() int {
	if len(m.transport.inputQueue) == 0 {
		return 0
	}
	bestIdx := 0
	bestPriority := m.transport.inputQueue[0].priority
	for i := 1; i < len(m.transport.inputQueue); i++ {
		if m.transport.inputQueue[i].priority > bestPriority {
			bestPriority = m.transport.inputQueue[i].priority
			bestIdx = i
		}
	}
	return bestIdx
}

func (m *model) dequeueNextVolumeIndex() int {
	best := -1
	var bestPrio inputPriority
	for i, item := range m.transport.inputQueue {
		if !isVolumeAction(item.kind) {
			continue
		}
		if best < 0 || item.priority > bestPrio {
			best, bestPrio = i, item.priority
		}
	}
	return best
}

func (m *model) dropQueuedByPredicate(drop func(playbackInputKind) bool) {
	if len(m.transport.inputQueue) == 0 {
		return
	}
	dst := m.transport.inputQueue[:0]
	for _, item := range m.transport.inputQueue {
		if drop(item.kind) {
			continue
		}
		dst = append(dst, item)
	}
	m.transport.inputQueue = dst
}

func (m *model) hasQueuedAction(action playbackInputKind) bool {
	for _, item := range m.transport.inputQueue {
		if item.kind == action {
			return true
		}
	}
	return false
}
