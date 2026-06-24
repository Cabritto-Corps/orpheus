package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/cache"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func playbackCoverSubject(status *spotify.PlaybackStatus) string {
	if status == nil {
		return ""
	}
	if id := golibrespot.NormalizeSpotifyId(status.TrackID); id != "" {
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
	return golibrespot.NormalizeSpotifyId(queue[0].ID)
}

func (m *model) advancePlayerCoverEpochIfNeeded(prevStatus, nextStatus *spotify.PlaybackStatus, prevQueueHead, nextQueueHead string) {
	prevTrack := ""
	nextTrack := ""
	prevURL := ""
	nextURL := ""
	prevProgress := -1
	nextProgress := -1
	if prevStatus != nil {
		prevTrack = golibrespot.NormalizeSpotifyId(prevStatus.TrackID)
		prevURL = strings.TrimSpace(prevStatus.AlbumImageURL)
		prevProgress = prevStatus.ProgressMS
	}
	if nextStatus != nil {
		nextTrack = golibrespot.NormalizeSpotifyId(nextStatus.TrackID)
		nextURL = strings.TrimSpace(nextStatus.AlbumImageURL)
		nextProgress = nextStatus.ProgressMS
	}
	subjectChanged := playbackCoverSubjectChanged(prevStatus, nextStatus)
	trackChanged := prevTrack != "" && nextTrack != "" && prevTrack != nextTrack
	queueHeadChanged := prevQueueHead != "" && nextQueueHead != "" && prevQueueHead != nextQueueHead
	sameURL := prevURL != "" && prevURL == nextURL
	progressRewind := sameURL && prevProgress >= 0 && nextProgress >= 0 && prevProgress > nextProgress+progressRewindThresholdMS
	shouldAdvance := subjectChanged || trackChanged || queueHeadChanged || progressRewind
	if shouldAdvance {
		m.transport.playerCoverEpoch++
		if m.ui.imgs != nil && m.ui.imgs.protocol == imageProtocolKitty {
			m.ui.imgs.forceKittyRedraw()
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
	fromTrack := ""
	if m.transport.status != nil {
		fromTrack = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
	}
	m.transport.transition.Begin(time.Now(), fromTrack)
	if m.ui.imgs != nil && m.ui.imgs.protocol == imageProtocolKitty {
		m.ui.imgs.forceKittyRedraw()
	}
	m.syncExecutorState()
}

func (m *model) maybeClearTransportTransition(next *spotify.PlaybackStatus) {
	event := m.transport.transition.MaybeClear(next, time.Now())
	if event == transportEventNone {
		return
	}
	if m.ui.imgs != nil && m.ui.imgs.protocol == imageProtocolKitty {
		m.ui.imgs.forceKittyRedraw()
	}
	if event == transportEventStuck {
		m.ui.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
	}
	m.syncExecutorState()
}

func (m *model) shouldBlockTransportInput(msg tea.KeyMsg) bool {
	if !m.transport.transition.Pending() {
		return false
	}
	k := m.ui.keys
	return keyMatches(msg, k.PlayPause) ||
		keyMatches(msg, k.Next) ||
		keyMatches(msg, k.Prev) ||
		keyMatches(msg, k.Shuffle) ||
		keyMatches(msg, k.Loop)
}

func (m *model) beginReconcileAction(window time.Duration) {
	m.transport.actionInFlight = true
	m.syncExecutorState()
	if window > 0 {
		m.ui.actionFastPollUntil = time.Now().Add(window)
	}
}

func (m *model) clearPreloadedTracks() {
	for id := range m.browse.preloadedItemIDs {
		delete(m.browse.preloadedItemIDs, id)
	}
}

func (m *model) applyOptimisticSkip(next bool) {
	if m.transport.status == nil {
		return
	}
	m.transport.status.ProgressMS = 0
	m.transport.status.Playing = true
	m.transport.interpolationSyncAt = time.Time{}
	m.transport.interpolationProgressMS = 0
	if next && len(m.transport.queue) > 0 {
		m.transport.status.TrackID = m.transport.queue[0].ID
		m.transport.status.TrackName = m.transport.queue[0].Name
		m.transport.status.ArtistName = m.transport.queue[0].Artist
		m.transport.status.DurationMS = m.transport.queue[0].DurationMS
	}
	m.resetInterpolationBaseline()
}

func (m *model) interpolatePlaybackProgress(_ time.Duration) {
	if m.transport.status == nil || !m.transport.status.Playing || m.transport.status.DurationMS <= 0 || m.transport.transition.Pending() {
		return
	}
	if m.transport.interpolationSyncAt.IsZero() {
		m.transport.interpolationSyncAt = time.Now()
		m.transport.interpolationProgressMS = m.transport.status.ProgressMS
		return
	}
	elapsed := time.Since(m.transport.interpolationSyncAt)
	expected := m.transport.interpolationProgressMS + int(elapsed/time.Millisecond)
	m.transport.status.ProgressMS = min(expected, m.transport.status.DurationMS)
}

func (m *model) resetInterpolationBaseline() {
	m.transport.interpolationSyncAt = time.Now()
	if m.transport.status != nil {
		m.transport.interpolationProgressMS = m.transport.status.ProgressMS
	} else {
		m.transport.interpolationProgressMS = 0
	}
}

const progressSyncThresholdMS = 300

func (m *model) smoothApplyProgress(incomingProgress int) {
	if m.transport.status == nil {
		return
	}
	if m.transport.transition.Pending() || m.transport.interpolationSyncAt.IsZero() {
		m.transport.status.ProgressMS = incomingProgress
		m.resetInterpolationBaseline()
		return
	}
	elapsed := time.Since(m.transport.interpolationSyncAt)
	interpolated := m.transport.interpolationProgressMS + int(elapsed/time.Millisecond)
	if m.transport.status.DurationMS > 0 && interpolated > m.transport.status.DurationMS {
		interpolated = m.transport.status.DurationMS
	}
	delta := absInt(interpolated - incomingProgress)
	if delta <= progressSyncThresholdMS {
		return
	}
	m.transport.status.ProgressMS = incomingProgress
	m.resetInterpolationBaseline()
}

func (m *model) clearVolumeSettleTarget(observed int) {
	if m.transport.volSentTarget < 0 {
		return
	}
	if observed >= 0 && observed == m.transport.volSentTarget {
		m.transport.volSentTarget = -1
		return
	}
	if time.Since(m.transport.volSentAt) >= volSettleWindow {
		m.transport.volSentTarget = -1
	}
}

const (
	seekSettleToleranceMS     = 900
	seekBarEndBufferMS        = 250
	progressRewindThresholdMS = 5000
)

func (m *model) clampSeekTarget(target int) int {
	if target < 0 {
		target = 0
	}
	if m.transport.status == nil || m.transport.status.DurationMS <= 0 {
		return target
	}
	maxTarget := max(m.transport.status.DurationMS-seekBarEndBufferMS, 0)
	if target > maxTarget {
		return maxTarget
	}
	return target
}

func (m *model) seekSettleProgress() int {
	if m.transport.seekDebouncePending >= 0 {
		return m.clampSeekTarget(m.transport.seekDebouncePending)
	}
	if m.transport.seekSentTarget < 0 || time.Since(m.transport.seekSentAt) >= seekSettleWindow {
		if m.transport.status == nil {
			return 0
		}
		return m.clampSeekTarget(m.transport.status.ProgressMS)
	}
	progress := m.transport.seekSentTarget
	if m.transport.status != nil && m.transport.status.Playing {
		progress += int(time.Since(m.transport.seekSentAt) / time.Millisecond)
	}
	return m.clampSeekTarget(progress)
}

func (m *model) shouldApplySeekSettle(incoming *spotify.PlaybackStatus) bool {
	if incoming == nil {
		return false
	}
	// Don't settle when paused — incoming state has correct static position
	if !incoming.Playing {
		return false
	}
	if m.transport.seekDebouncePending < 0 && (m.transport.seekSentTarget < 0 || time.Since(m.transport.seekSentAt) >= seekSettleWindow) {
		return false
	}
	if m.transport.status == nil {
		return true
	}
	prevTrack := golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
	nextTrack := golibrespot.NormalizeSpotifyId(incoming.TrackID)
	if prevTrack == "" || nextTrack == "" {
		return true
	}
	return prevTrack == nextTrack
}

func (m *model) clearSeekSettleTarget(observed int) {
	if m.transport.seekSentTarget < 0 {
		return
	}
	if observed >= 0 && absInt(observed-m.transport.seekSentTarget) <= seekSettleToleranceMS {
		m.transport.seekSentTarget = -1
		return
	}
	if time.Since(m.transport.seekSentAt) >= seekSettleWindow {
		m.transport.seekSentTarget = -1
	}
}

func (m *model) applyStatusSettleOverrides(status *spotify.PlaybackStatus, observedVol int) {
	inVolSettle := m.transport.volDebouncePending >= 0 ||
		(m.transport.volSentTarget >= 0 && time.Since(m.transport.volSentAt) < volSettleWindow)
	if inVolSettle && status != nil && m.transport.volSentTarget >= 0 {
		status.Volume = m.transport.volSentTarget
	}
	incomingProgress := -1
	if status != nil {
		incomingProgress = status.ProgressMS
	}
	if m.shouldApplySeekSettle(status) {
		status.ProgressMS = m.clampSeekTarget(m.seekSettleProgress())
	}
	m.clearVolumeSettleTarget(observedVol)
	m.clearSeekSettleTarget(incomingProgress)
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
	if m.transport.pendingContextFrom == "" {
		return true
	}
	if time.Since(m.transport.pendingContextFromAt) > pendingContextTimeout {
		m.transport.pendingContextFrom = ""
		return true
	}
	if incomingTrack == m.transport.pendingContextFrom {
		m.transport.queue = nil
		m.transport.stableQueueLen = 0
		m.transport.queueHasMore = false
		return false
	}
	m.transport.pendingContextFrom = ""
	return true
}

func (m *model) applyMergedQueue(incoming []spotify.QueueItem, queueHasMore bool, updateStable bool, updateHasMore bool) {
	m.transport.queue = mergeQueueNames(m.transport.queue, incoming, m.browse.trackCache)
	if updateStable {
		m.transport.stableQueueLen = len(m.transport.queue)
	}
	if updateHasMore {
		m.transport.queueHasMore = queueHasMore
	}
	m.rebuildPreloadedFromQueue()
}

func mergeStatusFromPrevious(prev *spotify.PlaybackStatus, queue []spotify.QueueItem, next *spotify.PlaybackStatus, trackCache *cache.TTL[string, spotify.QueueItem]) *spotify.PlaybackStatus {
	if next == nil {
		return next
	}
	out := *next
	nextID := golibrespot.NormalizeSpotifyId(next.TrackID)
	sameTrack := func(id string) bool {
		return golibrespot.NormalizeSpotifyId(id) != "" && golibrespot.NormalizeSpotifyId(id) == nextID
	}
	// Carry prev's AlbumImageURL only on same-track pushes: intermediate librespot
	// state updates for a new track often omit the cover URL and a stale URL from
	// the previous track would render the wrong art until the second push arrives.
	if out.AlbumImageURL == "" && prev != nil && prev.AlbumImageURL != "" && sameTrack(prev.TrackID) {
		out.AlbumImageURL = prev.AlbumImageURL
	}
	if out.TrackName != "" && out.ArtistName != "" && out.DurationMS > 0 {
		return &out
	}
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

func mergeQueueNames(prev, next []spotify.QueueItem, cache *cache.TTL[string, spotify.QueueItem]) []spotify.QueueItem {
	if len(next) == 0 {
		return next
	}
	byID := make(map[string]spotify.QueueItem, len(prev))
	for _, q := range prev {
		byID[golibrespot.NormalizeSpotifyId(q.ID)] = q
	}
	out := make([]spotify.QueueItem, len(next))
	for i, q := range next {
		out[i] = q
		if q.Name != "" && q.Artist != "" {
			continue
		}
		key := golibrespot.NormalizeSpotifyId(q.ID)
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

func (m *model) rebuildPreloadedFromQueue() {
	if m.browse.preloadedItemIDs == nil {
		m.browse.preloadedItemIDs = make(map[string]struct{}, len(m.transport.queue))
	}
	newIDs := make(map[string]struct{}, len(m.transport.queue))
	for _, q := range m.transport.queue {
		if q.ID != "" {
			newIDs[golibrespot.NormalizeSpotifyId(q.ID)] = struct{}{}
		}
	}
	for k := range m.browse.preloadedItemIDs {
		if _, ok := newIDs[k]; !ok {
			delete(m.browse.preloadedItemIDs, k)
		}
	}
	for k := range newIDs {
		m.browse.preloadedItemIDs[k] = struct{}{}
	}
}
