package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/cache"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func playbackCoverSubject(status *spotify.PlaybackStatus) string {
	if status == nil {
		return ""
	}
	if id := normalizeQueueID(status.TrackID); id != "" {
		return "id:" + id
	}
	name := strings.TrimSpace(status.TrackName)
	artist := strings.TrimSpace(status.ArtistName)
	if name == "" && artist == "" && status.DurationMS <= 0 {
		return ""
	}
	return "meta:" + name + "|" + artist + "|" + strconv.Itoa(status.DurationMS)
}

func playbackCoverSubjectChanged(prev, next *spotify.PlaybackStatus) bool {
	prevSubject := playbackCoverSubject(prev)
	nextSubject := playbackCoverSubject(next)
	if prevSubject == "" || nextSubject == "" {
		return false
	}
	return prevSubject != nextSubject
}

func queueHeadTrackID(queue []spotify.QueueItem) string {
	if len(queue) == 0 {
		return ""
	}
	return normalizeQueueID(queue[0].ID)
}

func (m *model) advancePlayerCoverEpochIfNeeded(prevStatus, nextStatus *spotify.PlaybackStatus, prevQueueHead, nextQueueHead string) {
	prevTrack := ""
	nextTrack := ""
	prevURL := ""
	nextURL := ""
	prevProgress := -1
	nextProgress := -1
	if prevStatus != nil {
		prevTrack = normalizeQueueID(prevStatus.TrackID)
		prevURL = strings.TrimSpace(prevStatus.AlbumImageURL)
		prevProgress = prevStatus.ProgressMS
	}
	if nextStatus != nil {
		nextTrack = normalizeQueueID(nextStatus.TrackID)
		nextURL = strings.TrimSpace(nextStatus.AlbumImageURL)
		nextProgress = nextStatus.ProgressMS
	}
	subjectChanged := playbackCoverSubjectChanged(prevStatus, nextStatus)
	trackChanged := prevTrack != "" && nextTrack != "" && prevTrack != nextTrack
	queueHeadChanged := prevQueueHead != "" && nextQueueHead != "" && prevQueueHead != nextQueueHead
	sameURL := prevURL != "" && prevURL == nextURL
	progressRewind := sameURL && prevProgress >= 0 && nextProgress >= 0 && prevProgress > nextProgress+5000
	shouldAdvance := nextURL != "" && (subjectChanged || trackChanged || queueHeadChanged || progressRewind)
	if shouldAdvance {
		m.playerCoverEpoch++
		if m.imgs != nil && m.imgs.protocol == imageProtocolKitty {
			m.imgs.forceKittyRedraw()
		}
	}
}

func cloneStatus(status *spotify.PlaybackStatus) *spotify.PlaybackStatus {
	if status == nil {
		return nil
	}
	cp := *status
	return &cp
}

func shouldQueueAlbumImageLoad(prev, next *spotify.PlaybackStatus) bool {
	if next == nil || strings.TrimSpace(next.AlbumImageURL) == "" {
		return false
	}
	if prev == nil {
		return true
	}
	if strings.TrimSpace(next.AlbumImageURL) != strings.TrimSpace(prev.AlbumImageURL) {
		return true
	}
	return playbackCoverSubjectChanged(prev, next)
}

func (m *model) beginTransportTransition() {
	m.transportTransitionPending = true
	m.transportTransitionStartedAt = time.Now()
	m.transportTransitionFromTrack = ""
	if m.status != nil {
		m.transportTransitionFromTrack = normalizeQueueID(m.status.TrackID)
	}
	if m.imgs != nil && m.imgs.protocol == imageProtocolKitty {
		m.imgs.forceKittyRedraw()
	}
	m.syncExecutorState()
}

func (m *model) maybeClearTransportTransition(next *spotify.PlaybackStatus) {
	if !m.transportTransitionPending || next == nil {
		return
	}
	nextTrack := normalizeQueueID(next.TrackID)
	if nextTrack != "" && nextTrack != m.transportTransitionFromTrack {
		m.transportTransitionPending = false
		if m.imgs != nil && m.imgs.protocol == imageProtocolKitty {
			m.imgs.forceKittyRedraw()
		}
		m.syncExecutorState()
		return
	}
	if nextTrack == m.transportTransitionFromTrack && next.ProgressMS < 2000 &&
		time.Since(m.transportTransitionStartedAt) < 4*time.Second {
		m.transportTransitionPending = false
		if m.imgs != nil && m.imgs.protocol == imageProtocolKitty {
			m.imgs.forceKittyRedraw()
		}
		m.syncExecutorState()
		return
	}
	if time.Since(m.transportTransitionStartedAt) > 4*time.Second {
		m.transportTransitionPending = false
		m.transportRecoveryPending = true
		m.transportStuckCount++
		m.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
		m.syncExecutorState()
	}
}

func (m *model) shouldBlockTransportInput(msg tea.KeyMsg) bool {
	if !m.transportTransitionPending {
		return false
	}
	k := m.keys
	return keyMatches(msg, k.PlayPause) ||
		keyMatches(msg, k.Next) ||
		keyMatches(msg, k.Prev) ||
		keyMatches(msg, k.Shuffle) ||
		keyMatches(msg, k.Loop)
}

func (m *model) beginReconcileAction(window time.Duration) {
	m.actionInFlight = true
	m.syncExecutorState()
	if window > 0 {
		m.actionFastPollUntil = time.Now().Add(window)
	}
}

func (m *model) clearPreloadedTracks() {
	for id := range m.preloadedItemIDs {
		delete(m.preloadedItemIDs, id)
	}
}

func (m *model) applyOptimisticSkip(next bool) {
	if m.status == nil {
		return
	}
	m.status.ProgressMS = 0
	m.status.Playing = true
	if next && len(m.queue) > 0 {
		m.status.TrackID = m.queue[0].ID
		m.status.TrackName = m.queue[0].Name
		m.status.ArtistName = m.queue[0].Artist
	}
}

func (m *model) interpolatePlaybackProgress(step time.Duration) {
	if step <= 0 || m.status == nil || !m.status.Playing || m.status.DurationMS <= 0 {
		return
	}
	next := m.status.ProgressMS + int(step/time.Millisecond)
	m.status.ProgressMS = min(next, m.status.DurationMS)
}

func (m *model) clearVolumeSettleTarget(observed int) {
	if m.volSentTarget < 0 {
		return
	}
	if observed >= 0 && observed == m.volSentTarget {
		m.volSentTarget = -1
		return
	}
	if time.Since(m.volSentAt) >= volSettleWindow {
		m.volSentTarget = -1
	}
}

const seekSettleToleranceMS = 900

func (m *model) clampSeekTarget(target int) int {
	if target < 0 {
		target = 0
	}
	if m.status == nil || m.status.DurationMS <= 0 {
		return target
	}
	maxTarget := m.status.DurationMS - 250
	if maxTarget < 0 {
		maxTarget = 0
	}
	if target > maxTarget {
		return maxTarget
	}
	return target
}

func (m *model) seekSettleProgress() int {
	if m.seekDebouncePending >= 0 {
		return m.clampSeekTarget(m.seekDebouncePending)
	}
	if m.seekSentTarget < 0 || time.Since(m.seekSentAt) >= seekSettleWindow {
		if m.status == nil {
			return 0
		}
		return m.clampSeekTarget(m.status.ProgressMS)
	}
	progress := m.seekSentTarget
	if m.status != nil && m.status.Playing {
		progress += int(time.Since(m.seekSentAt) / time.Millisecond)
	}
	return m.clampSeekTarget(progress)
}

func (m *model) shouldApplySeekSettle(incoming *spotify.PlaybackStatus) bool {
	if incoming == nil {
		return false
	}
	if m.seekDebouncePending < 0 && (m.seekSentTarget < 0 || time.Since(m.seekSentAt) >= seekSettleWindow) {
		return false
	}
	if m.status == nil {
		return true
	}
	prevTrack := normalizeQueueID(m.status.TrackID)
	nextTrack := normalizeQueueID(incoming.TrackID)
	if prevTrack == "" || nextTrack == "" {
		return true
	}
	return prevTrack == nextTrack
}

func (m *model) clearSeekSettleTarget(observed int) {
	if m.seekSentTarget < 0 {
		return
	}
	if observed >= 0 && absInt(observed-m.seekSentTarget) <= seekSettleToleranceMS {
		m.seekSentTarget = -1
		return
	}
	if time.Since(m.seekSentAt) >= seekSettleWindow {
		m.seekSentTarget = -1
	}
}

func (m *model) trySendTransportSkip(kind librespot.TUICommandKind) bool {
	if m.tuiCmdCh == nil {
		return false
	}
	select {
	case m.tuiCmdCh <- librespot.TUICommand{Kind: kind}:
		return true
	default:
		return false
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

const pendingContextTimeout = 8 * time.Second

func (m *model) shouldApplyIncomingQueue(incomingTrack string) bool {
	if m.pendingContextFrom == "" {
		return true
	}
	if time.Since(m.pendingContextFromAt) > pendingContextTimeout {
		m.pendingContextFrom = ""
		return true
	}
	if incomingTrack == m.pendingContextFrom {
		m.queue = nil
		m.stableQueueLen = 0
		m.queueHasMore = false
		return false
	}
	m.pendingContextFrom = ""
	return true
}

func (m *model) applyMergedQueue(incoming []spotify.QueueItem, queueHasMore bool, updateStable bool, updateHasMore bool, shuffleActive bool) {
	preserveTail := !shuffleActive
	m.queue = mergeQueueWithRest(m.queue, incoming, m.trackCache, preserveTail)
	if updateStable {
		m.stableQueueLen = len(m.queue)
	}
	if updateHasMore {
		m.queueHasMore = queueHasMore
	}
	m.rebuildPreloadedFromQueue()
}

func mergeStatusFromPrevious(prev *spotify.PlaybackStatus, queue []spotify.QueueItem, next *spotify.PlaybackStatus, trackCache *cache.TTL[string, spotify.QueueItem]) *spotify.PlaybackStatus {
	if next == nil {
		return next
	}
	out := *next
	if out.AlbumImageURL == "" && prev != nil && prev.AlbumImageURL != "" {
		out.AlbumImageURL = prev.AlbumImageURL
	}
	if out.TrackName != "" && out.ArtistName != "" && out.DurationMS > 0 {
		return &out
	}
	nextID := normalizeQueueID(next.TrackID)
	sameTrack := func(id string) bool { return normalizeQueueID(id) != "" && normalizeQueueID(id) == nextID }
	if prev != nil && sameTrack(prev.TrackID) {
		if out.TrackName == "" && prev.TrackName != "" {
			out.TrackName = prev.TrackName
		}
		if out.ArtistName == "" && prev.ArtistName != "" {
			out.ArtistName = prev.ArtistName
		}
		if out.AlbumName == "" && prev.AlbumName != "" {
			out.AlbumName = prev.AlbumName
		}
		if out.AlbumImageURL == "" && prev.AlbumImageURL != "" {
			out.AlbumImageURL = prev.AlbumImageURL
		}
		if out.DurationMS <= 0 && prev.DurationMS > 0 {
			out.DurationMS = prev.DurationMS
		}
	}
	for _, q := range queue {
		if !sameTrack(q.ID) {
			continue
		}
		if out.TrackName == "" && q.Name != "" {
			out.TrackName = q.Name
		}
		if out.ArtistName == "" && q.Artist != "" && q.Artist != "-" {
			out.ArtistName = q.Artist
		}
		if out.DurationMS <= 0 && q.DurationMS > 0 {
			out.DurationMS = q.DurationMS
		}
		break
	}
	if trackCache != nil && nextID != "" && (out.TrackName == "" || out.ArtistName == "" || out.DurationMS <= 0) {
		if c, ok := trackCache.Peek(nextID); ok {
			if out.TrackName == "" && c.Name != "" {
				out.TrackName = c.Name
			}
			if out.ArtistName == "" && c.Artist != "" && c.Artist != "-" {
				out.ArtistName = c.Artist
			}
			if out.DurationMS <= 0 && c.DurationMS > 0 {
				out.DurationMS = c.DurationMS
			}
		}
	}
	return &out
}

func normalizeQueueID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return id
	}
	if strings.HasPrefix(id, "spotify:") {
		parts := strings.Split(id, ":")
		if len(parts) >= 3 {
			return parts[len(parts)-1]
		}
	}
	return id
}

func mergeQueueNames(prev, next []spotify.QueueItem, cache *cache.TTL[string, spotify.QueueItem]) []spotify.QueueItem {
	if len(next) == 0 {
		return next
	}
	byID := make(map[string]spotify.QueueItem, len(prev))
	for _, q := range prev {
		byID[normalizeQueueID(q.ID)] = q
	}
	out := make([]spotify.QueueItem, len(next))
	for i, q := range next {
		out[i] = q
		if q.Name != "" && q.Artist != "" {
			continue
		}
		key := normalizeQueueID(q.ID)
		if p, ok := byID[key]; ok {
			if out[i].Name == "" && p.Name != "" {
				out[i].Name = p.Name
			}
			if out[i].Artist == "" && p.Artist != "" {
				out[i].Artist = p.Artist
			}
			if out[i].DurationMS <= 0 && p.DurationMS > 0 {
				out[i].DurationMS = p.DurationMS
			}
		}
		if (out[i].Name == "" || out[i].Artist == "") && cache != nil {
			if c, ok := cache.Peek(key); ok {
				if out[i].Name == "" && c.Name != "" {
					out[i].Name = c.Name
				}
				if out[i].Artist == "" && c.Artist != "" {
					out[i].Artist = c.Artist
				}
				if out[i].DurationMS <= 0 && c.DurationMS > 0 {
					out[i].DurationMS = c.DurationMS
				}
			}
		}
	}
	return out
}

const librespotQueueWindow = 32

func mergeQueueWithRest(prev, next []spotify.QueueItem, cache *cache.TTL[string, spotify.QueueItem], preserveTail bool) []spotify.QueueItem {
	merged := mergeQueueNames(prev, next, cache)
	if !preserveTail || len(next) > librespotQueueWindow || len(prev) <= librespotQueueWindow {
		return merged
	}
	seen := make(map[string]struct{}, len(merged))
	for _, q := range merged {
		if q.ID != "" {
			seen[normalizeQueueID(q.ID)] = struct{}{}
		}
	}
	for i := librespotQueueWindow; i < len(prev); i++ {
		if prev[i].ID != "" {
			if _, dup := seen[normalizeQueueID(prev[i].ID)]; dup {
				continue
			}
		}
		merged = append(merged, prev[i])
	}
	return merged
}

func (m *model) rebuildPreloadedFromQueue() {
	if m.preloadedItemIDs == nil {
		m.preloadedItemIDs = make(map[string]struct{}, len(m.queue))
	} else {
		for k := range m.preloadedItemIDs {
			delete(m.preloadedItemIDs, k)
		}
	}
	for _, q := range m.queue {
		if q.ID != "" {
			m.preloadedItemIDs[normalizeQueueID(q.ID)] = struct{}{}
		}
	}
}
