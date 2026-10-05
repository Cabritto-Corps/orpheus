package tui

import (
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

// Startup sync bound: short enough not to stall the reveal visibly, long
// enough for a resumed session's first push to arrive.
const firstStateGrace = 2 * time.Second

const queueMetaRevealGrace = 1500 * time.Millisecond

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

// Progress changes within this window still count as frozen.
const heartbeatProgressToleranceMS = 1000

// Frozen pushes must not clear a live transport error.
func isFrozenHeartbeat(prev *spotify.PlaybackStatus, currentQueueLen int, msg playbackStateMsg) bool {
	if prev == nil || msg.status == nil {
		return false
	}
	if golibrespot.NormalizeSpotifyId(prev.TrackID) != golibrespot.NormalizeSpotifyId(msg.status.TrackID) {
		return false
	}
	if prev.Playing != msg.status.Playing {
		return false
	}
	if absInt(prev.ProgressMS-msg.status.ProgressMS) > heartbeatProgressToleranceMS {
		return false
	}
	if msg.queueIncluded && len(msg.queue) != currentQueueLen {
		return false
	}
	return true
}

func queueHeadTrackID(queue []spotify.QueueItem) string {
	if len(queue) == 0 {
		return ""
	}
	return golibrespot.NormalizeSpotifyId(queue[0].ID)
}

// Up-next view is the pushed queue as-is; the head is already excluded.
// IDs can duplicate, so stripping by ID would shift every command.
func (m model) visibleQueue() []spotify.QueueItem {
	return m.transport.queue
}

func (m model) currentContextURI() string {
	if m.transport.status == nil {
		return ""
	}
	return strings.TrimSpace(m.transport.status.ContextURI)
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
		// The intent revision re-emits on the epoch change alone.
		m.transport.playerCoverEpoch++
	}
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
	m.syncExecutorState()
}

func (m *model) maybeClearTransportTransition(next *spotify.PlaybackStatus) {
	event := m.transport.transition.MaybeClear(next, time.Now())
	if event == transportEventNone {
		return
	}
	if event == transportEventStuck {
		m.transport.playbackErr = errors.New("track didn't start — skip again")
	}
	m.syncExecutorState()
}

func (m *model) shouldBlockTransportInput(msg tea.KeyPressMsg) bool {
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

func (m *model) applyOptimisticSkip(next bool) {
	if m.transport.status == nil {
		return
	}
	m.transport.status.ProgressMS = 0
	m.transport.status.Playing = true
	m.transport.interpolationSyncAt = time.Time{}
	m.transport.interpolationProgressMS = 0
	if next {
		// Aim past the head: the view hides the playing track (e.g. repeat-one).
		for _, entry := range m.transport.queue {
			if entry.ID == m.transport.status.TrackID {
				continue
			}
			m.transport.status.TrackID = entry.ID
			m.transport.status.TrackName = entry.Name
			m.transport.status.ArtistName = entry.Artist
			m.transport.status.DurationMS = entry.DurationMS
			break
		}
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

func (m *model) trySendTransportSkip(kind librespot.TUICommandKind) bool {
	return m.trySendTUICommand(librespot.TUICommand{Kind: kind})
}

func (m *model) trySendTUICommand(cmd librespot.TUICommand) bool {
	if m.tuiCmdCh == nil {
		return false
	}
	select {
	case m.tuiCmdCh <- cmd:
		return true
	default:
		return false
	}
}

func (m *model) sendTUICommandOrRetry(cmd librespot.TUICommand) tea.Cmd {
	if m.trySendTUICommand(cmd) {
		return nil
	}
	return m.tuiCmdRetryCmd(cmd, maxRequeueRetries)
}

func (m *model) tuiCmdRetryCmd(cmd librespot.TUICommand, left int) tea.Cmd {
	if left <= 0 {
		return nil
	}
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg {
		return tuiCmdRetryMsg{cmd: cmd, left: left}
	})
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

func clampQueueCursor(cursor int, visible []spotify.QueueItem) int {
	return min(max(cursor, 0), max(0, len(visible)-1))
}

func (m *model) applyMergedQueue(incoming []spotify.QueueItem, queueHasMore bool, updateStable bool, updateHasMore bool) {
	m.transport.queue = mergeQueueNames(m.transport.queue, incoming)
	m.transport.queueCursor = clampQueueCursor(m.transport.queueCursor, m.visibleQueue())
	if updateStable {
		m.transport.stableQueueLen = len(m.transport.queue)
	}
	if updateHasMore {
		m.transport.queueHasMore = queueHasMore
	}
}

func mergeStatusFromPrevious(prev *spotify.PlaybackStatus, queue []spotify.QueueItem, next *spotify.PlaybackStatus) *spotify.PlaybackStatus {
	if next == nil {
		return next
	}
	out := *next
	nextID := golibrespot.NormalizeSpotifyId(next.TrackID)
	sameTrack := func(id string) bool {
		return golibrespot.NormalizeSpotifyId(id) != "" && golibrespot.NormalizeSpotifyId(id) == nextID
	}
	// Same-track only: a stale prev URL would render the wrong art until the second push.
	if out.AlbumImageURL == "" && prev != nil && prev.AlbumImageURL != "" && sameTrack(prev.TrackID) {
		out.AlbumImageURL = prev.AlbumImageURL
	}
	// Runs before the metadata early-return: complete metadata with a missing URL is exactly the skip case.
	if out.AlbumImageURL == "" {
		for _, q := range queue {
			if sameTrack(q.ID) && q.ImageURL != "" {
				out.AlbumImageURL = q.ImageURL
				break
			}
		}
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
	return &out
}

// mergePlaybackImageFromTracks fills missing playback art from the loaded
// Songs library. Connect/autoplay updates can contain a track ID and title
// but omit album art already known to the catalog.
func mergePlaybackImageFromTracks(status *spotify.PlaybackStatus, groups ...[]spotify.QueueItem) *spotify.PlaybackStatus {
	if status == nil || strings.TrimSpace(status.AlbumImageURL) != "" {
		return status
	}
	id := golibrespot.NormalizeSpotifyId(status.TrackID)
	if id == "" {
		return status
	}
	for _, group := range groups {
		for _, track := range group {
			if golibrespot.NormalizeSpotifyId(track.ID) != id || strings.TrimSpace(track.ImageURL) == "" {
				continue
			}
			out := *status
			out.AlbumImageURL = strings.TrimSpace(track.ImageURL)
			return &out
		}
	}
	return status
}

func mergeQueueNames(prev, next []spotify.QueueItem) []spotify.QueueItem {
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
	}
	return out
}
