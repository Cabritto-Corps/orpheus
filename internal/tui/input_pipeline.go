package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/librespot"
	"orpheus/internal/playbackdomain"
)

type commandExecutorState string

const (
	executorStateIdle              commandExecutorState = "idle"
	executorStateAwaitingAction    commandExecutorState = "awaiting-action"
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
	case m.transport.actionInFlight:
		m.transport.executorState = executorStateAwaitingAction
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
	if action == playbackInputRefresh && m.hasQueuedAction(action) {
		return
	}
	if action == playbackInputRefresh && m.transport.executorState != executorStateIdle {
		return
	}
	m.transport.inputQueue = append(m.transport.inputQueue, playbackInput{
		kind:     action,
		priority: inputPriorityOf(action),
	})
}

func (m *model) requeueFront(action playbackInputKind) {
	if len(m.transport.inputQueue) >= maxInputQueueSize {
		m.transport.inputQueue = m.transport.inputQueue[:maxInputQueueSize-1]
	}
	retries := 0
	if len(m.transport.inputQueue) > 0 && m.transport.inputQueue[0].kind == action {
		retries = m.transport.inputQueue[0].retryCount + 1
	}
	if retries >= maxRequeueRetries {
		return
	}
	m.transport.inputQueue = append([]playbackInput{{kind: action, priority: inputPriorityOf(action), retryCount: retries}}, m.transport.inputQueue...)
}

func (m *model) pumpInputExecutor() tea.Cmd {
	for range maxInputActionsPerTick {
		m.syncExecutorState()
		if m.transport.executorState != executorStateIdle || len(m.transport.inputQueue) == 0 {
			return nil
		}
		idx := m.dequeueNextInputIndex()
		if len(m.transport.inputQueue) == 0 {
			return nil
		}
		action := m.transport.inputQueue[idx].kind
		m.transport.inputQueue = append(m.transport.inputQueue[:idx], m.transport.inputQueue[idx+1:]...)
		if cmd := m.executePlaybackInput(action); cmd != nil {
			return cmd
		}
	}
	return nil
}

func (m *model) consumeTransportRecoveryCmd() tea.Cmd {
	if m.transport.actionInFlight || !m.transport.transition.RecoveryPending() {
		return nil
	}
	if !m.transport.transition.ConsumeRecovery() {
		return nil
	}
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
			if m.transport.status != nil && m.transport.status.Playing {
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
		rollback := cloneStatus(m.transport.status)
		shouldPlay := rollback == nil || !rollback.Playing
		if m.transport.status != nil {
			m.transport.status.Playing = shouldPlay
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
			m.ui.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
			return nil
		}
		rollback := cloneStatus(m.transport.status)
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
			m.ui.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
			return nil
		}
		rollback := cloneStatus(m.transport.status)
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
		rollback := cloneStatus(m.transport.status)
		nextShuffle := true
		if m.transport.status != nil {
			nextShuffle = !m.transport.status.ShuffleState
			m.transport.status.ShuffleState = nextShuffle
		}
		m.clearPreloadedTracks()
		m.transport.stableQueueLen = len(m.transport.queue)
		m.beginReconcileAction(0)
		return m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Shuffle(ctx, m.deviceName, nextShuffle)
		}, rollback)
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
				m.requeueFront(action)
			}
			return nil
		}
		if m.service == nil {
			return nil
		}
		rollback := cloneStatus(m.transport.status)
		next := playbackdomain.NextRepeatTraversalOptions(playbackdomain.TraversalOptions{RepeatContext: m.transport.status.RepeatContext, RepeatTrack: m.transport.status.RepeatTrack})
		m.transport.status.RepeatContext = next.RepeatContext
		m.transport.status.RepeatTrack = next.RepeatTrack
		m.beginReconcileAction(reconcileActionWindow)
		state := repeatModeString(m.transport.status.RepeatContext, m.transport.status.RepeatTrack)
		return m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.SetRepeat(ctx, m.deviceName, state)
		}, rollback)
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
