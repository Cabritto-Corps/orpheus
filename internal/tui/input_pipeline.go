package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/librespot"
)

type commandExecutorState string

const (
	executorStateIdle              commandExecutorState = "idle"
	executorStateAwaitingAction    commandExecutorState = "awaiting-action"
	executorStateAwaitingTransport commandExecutorState = "awaiting-transport"
	maxInputQueueSize                                   = 96
)

type playbackInputKind string
type inputPriority int

type playbackInput struct {
	kind     playbackInputKind
	priority inputPriority
}

const (
	playbackInputRefresh   playbackInputKind = "refresh"
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
	case m.actionInFlight:
		m.executorState = executorStateAwaitingAction
	case m.transportTransitionPending:
		m.executorState = executorStateAwaitingTransport
	default:
		m.executorState = executorStateIdle
	}
}

func (m *model) enqueuePlaybackInput(action playbackInputKind) {
	if len(m.inputQueue) >= maxInputQueueSize {
		m.inputQueue = m.inputQueue[1:]
	}
	if isVolumeAction(action) {
		m.dropQueuedByPredicate(func(kind playbackInputKind) bool { return isVolumeAction(kind) })
	}
	if isSeekAction(action) {
		m.dropQueuedByPredicate(func(kind playbackInputKind) bool { return isSeekAction(kind) })
	}
	if shouldDedupQueuedAction(action) && m.hasQueuedAction(action) {
		return
	}
	if action == playbackInputRefresh && m.hasQueuedAction(action) {
		return
	}
	m.inputQueue = append(m.inputQueue, playbackInput{
		kind:     action,
		priority: inputPriorityOf(action),
	})
}

func (m *model) requeueFront(action playbackInputKind) {
	if len(m.inputQueue) >= maxInputQueueSize {
		m.inputQueue = m.inputQueue[:maxInputQueueSize-1]
	}
	m.inputQueue = append([]playbackInput{{kind: action, priority: inputPriorityOf(action)}}, m.inputQueue...)
}

func (m *model) pumpInputExecutor() tea.Cmd {
	for i := 0; i < 8; i++ {
		m.syncExecutorState()
		if m.executorState != executorStateIdle || len(m.inputQueue) == 0 {
			return nil
		}
		idx := m.dequeueNextInputIndex()
		action := m.inputQueue[idx].kind
		m.inputQueue = append(m.inputQueue[:idx], m.inputQueue[idx+1:]...)
		if cmd := m.executePlaybackInput(action); cmd != nil {
			return cmd
		}
	}
	return nil
}

func (m *model) consumeTransportRecoveryCmd() tea.Cmd {
	if !m.transportRecoveryPending || m.actionInFlight {
		return nil
	}
	m.transportRecoveryPending = false
	if m.tuiCmdCh != nil || m.service == nil {
		return nil
	}
	return m.pollCmd(true)
}

func (m *model) executePlaybackInput(action playbackInputKind) tea.Cmd {
	switch action {
	case playbackInputRefresh:
		if m.tuiCmdCh != nil {
			return nil
		}
		return m.pollCmd(true)
	case playbackInputPlayPause:
		if m.tuiCmdCh != nil {
			kind := librespot.TUICommandResume
			if m.status != nil && m.status.Playing {
				kind = librespot.TUICommandPause
			}
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: kind}:
				return nil
			default:
				m.requeueFront(action)
				return nil
			}
		}
		rollback := cloneStatus(m.status)
		shouldPlay := rollback == nil || !rollback.Playing
		if m.status != nil {
			m.status.Playing = shouldPlay
		}
		m.beginReconcileAction(reconcileActionWindow)
		return m.actionWithReconcileCmd(func(ctx context.Context) error {
			if shouldPlay {
				return m.service.Play(ctx, m.deviceName)
			}
			return m.service.Pause(ctx, m.deviceName)
		}, rollback)
	case playbackInputNext:
		if m.tuiCmdCh != nil {
			if !m.trySendTransportSkip(librespot.TUICommandSkipNext) {
				m.requeueFront(action)
				return nil
			}
			m.beginTransportTransition()
			return nil
		}
		rollback := cloneStatus(m.status)
		m.applyOptimisticSkip(true)
		m.beginTransportTransition()
		m.beginReconcileAction(reconcileActionWindow)
		return m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Next(ctx, m.deviceName)
		}, rollback)
	case playbackInputPrev:
		if m.tuiCmdCh != nil {
			if !m.trySendTransportSkip(librespot.TUICommandSkipPrev) {
				m.requeueFront(action)
				return nil
			}
			m.beginTransportTransition()
			return nil
		}
		rollback := cloneStatus(m.status)
		m.applyOptimisticSkip(false)
		m.beginTransportTransition()
		m.beginReconcileAction(reconcileActionWindow)
		return m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Previous(ctx, m.deviceName)
		}, rollback)
	case playbackInputShuffle:
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandShuffle}:
			default:
				m.requeueFront(action)
				return nil
			}
			m.clearPreloadedTracks()
			return nil
		}
		rollback := cloneStatus(m.status)
		nextShuffle := true
		if m.status != nil {
			nextShuffle = !m.status.ShuffleState
			m.status.ShuffleState = nextShuffle
		}
		m.clearPreloadedTracks()
		m.stableQueueLen = len(m.queue)
		m.beginReconcileAction(0)
		return m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Shuffle(ctx, m.deviceName, nextShuffle)
		}, rollback)
	case playbackInputLoop:
		if m.status == nil {
			return nil
		}
		rollback := cloneStatus(m.status)
		m.status.RepeatContext, m.status.RepeatTrack = nextRepeatMode(m.status.RepeatContext, m.status.RepeatTrack)
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandCycleRepeat}:
			default:
				m.requeueFront(action)
			}
			return nil
		}
		if m.service == nil {
			return nil
		}
		m.beginReconcileAction(reconcileActionWindow)
		state := repeatModeString(m.status.RepeatContext, m.status.RepeatTrack)
		return m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.SetRepeat(ctx, m.deviceName, state)
		}, rollback)
	case playbackInputVolUp:
		if m.status == nil {
			return nil
		}
		target := 50
		if m.volDebouncePending >= 0 {
			target = clampInt(m.volDebouncePending+5, 0, 100)
		} else {
			target = clampInt(m.status.Volume+5, 0, 100)
		}
		m.status.Volume = target
		m.volDebouncePending = target
		m.volDebounceToken++
		return m.volDebounceCmd(m.volDebounceToken)
	case playbackInputVolDown:
		if m.status == nil {
			return nil
		}
		target := 50
		if m.volDebouncePending >= 0 {
			target = clampInt(m.volDebouncePending-5, 0, 100)
		} else {
			target = clampInt(m.status.Volume-5, 0, 100)
		}
		m.status.Volume = target
		m.volDebouncePending = target
		m.volDebounceToken++
		return m.volDebounceCmd(m.volDebounceToken)
	case playbackInputSeekBack:
		if m.status == nil {
			return nil
		}
		current := m.seekSettleProgress()
		target := m.clampSeekTarget(current - 5000)
		if target == current {
			return nil
		}
		m.status.ProgressMS = target
		m.seekDebouncePending = target
		m.seekDebounceToken++
		return m.seekDebounceCmd(m.seekDebounceToken)
	case playbackInputSeekFwd:
		if m.status == nil {
			return nil
		}
		current := m.seekSettleProgress()
		target := m.clampSeekTarget(current + 5000)
		if target == current {
			return nil
		}
		m.status.ProgressMS = target
		m.seekDebouncePending = target
		m.seekDebounceToken++
		return m.seekDebounceCmd(m.seekDebounceToken)
	default:
		return nil
	}
}

func isVolumeAction(action playbackInputKind) bool {
	return action == playbackInputVolUp || action == playbackInputVolDown
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
	bestIdx := 0
	bestPriority := m.inputQueue[0].priority
	for i := 1; i < len(m.inputQueue); i++ {
		if m.inputQueue[i].priority > bestPriority {
			bestPriority = m.inputQueue[i].priority
			bestIdx = i
		}
	}
	return bestIdx
}

func (m *model) dropQueuedByPredicate(drop func(playbackInputKind) bool) {
	if len(m.inputQueue) == 0 {
		return
	}
	dst := m.inputQueue[:0]
	for _, item := range m.inputQueue {
		if drop(item.kind) {
			continue
		}
		dst = append(dst, item)
	}
	m.inputQueue = dst
}

func (m *model) hasQueuedAction(action playbackInputKind) bool {
	for _, item := range m.inputQueue {
		if item.kind == action {
			return true
		}
	}
	return false
}
