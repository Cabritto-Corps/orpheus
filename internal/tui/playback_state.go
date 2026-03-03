package tui

import (
	"strings"
	"time"

	"orpheus/internal/spotify"
)

func cloneStatus(status *spotify.PlaybackStatus) *spotify.PlaybackStatus {
	if status == nil {
		return nil
	}
	cp := *status
	return &cp
}

func (m *model) beginReconcileAction(window time.Duration) {
	m.actionInFlight = true
	if window > 0 {
		m.actionFastPollUntil = time.Now().Add(window)
	}
}

func (m *model) clearPreloadedTracks() {
	for id := range m.preloadedTrackIDs {
		delete(m.preloadedTrackIDs, id)
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
		m.status.AlbumImageURL = ""
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
	// Avoid issuing a seek exactly at track end; some backends can stall there.
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

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (m *model) shouldApplyIncomingQueue(incomingTrack string) bool {
	if m.pendingContextFrom == "" {
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

func (m *model) applyMergedQueue(incoming []spotify.QueueItem, queueHasMore bool, updateStable bool, updateHasMore bool) {
	preserveTail := !(m.status != nil && m.status.ShuffleState)
	m.queue = mergeQueueWithRest(m.queue, incoming, m.trackCache, preserveTail)
	if updateStable {
		m.stableQueueLen = len(m.queue)
	}
	if updateHasMore {
		m.queueHasMore = queueHasMore
	}
	m.rebuildPreloadedFromQueue()
}

func mergeStatusFromPrevious(prev *spotify.PlaybackStatus, queue []spotify.QueueItem, next *spotify.PlaybackStatus) *spotify.PlaybackStatus {
	if next == nil {
		return next
	}
	out := *next
	if out.TrackName != "" && out.ArtistName != "" && out.DurationMS > 0 {
		return &out
	}
	sameTrack := func(id string) bool { return id != "" && id == next.TrackID }
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
	if len(queue) > 0 && sameTrack(queue[0].ID) {
		if out.TrackName == "" && queue[0].Name != "" {
			out.TrackName = queue[0].Name
		}
		if out.ArtistName == "" && queue[0].Artist != "" && queue[0].Artist != "-" {
			out.ArtistName = queue[0].Artist
		}
		if out.DurationMS <= 0 && queue[0].DurationMS > 0 {
			out.DurationMS = queue[0].DurationMS
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

func mergeQueueNames(prev, next []spotify.QueueItem, cache map[string]spotify.QueueItem) []spotify.QueueItem {
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
			if c, ok := cache[key]; ok {
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

func mergeQueueWithRest(prev, next []spotify.QueueItem, cache map[string]spotify.QueueItem, preserveTail bool) []spotify.QueueItem {
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
	if m.preloadedTrackIDs == nil {
		m.preloadedTrackIDs = make(map[string]struct{}, len(m.queue))
	} else {
		for k := range m.preloadedTrackIDs {
			delete(m.preloadedTrackIDs, k)
		}
	}
	for _, q := range m.queue {
		if q.ID != "" {
			m.preloadedTrackIDs[normalizeQueueID(q.ID)] = struct{}{}
		}
	}
}
