package librespot

type TUICommandKind int

const (
	TUICommandPlayContext TUICommandKind = iota
	TUICommandPlayContextFromTrack
	TUICommandGetContextTracks
	TUICommandPause
	TUICommandResume
	TUICommandSeek
	TUICommandSkipNext
	TUICommandSkipPrev
	TUICommandSetVolume
	TUICommandShuffle
	TUICommandCycleRepeat
	TUICommandQueueRemove
	TUICommandQueueReorder
	TUICommandQueueJump
)

// Queue commands address entries by VISIBLE position (the "up next" view):
// position 0 is the entry the panel shows first, i.e. the fork's visible-view
// index where the currently playing queue entry (if any) is excluded. Track
// IDs are not used: they can duplicate within a queue.
type TUICommand struct {
	Kind             TUICommandKind
	URI              string
	TrackID          string
	Position         int64
	Volume           int
	ReqToken         int
	QueueIndex       int
	QueueTargetIndex int
	ResultCh         chan<- ContextTracksResult
}

// ContextTracksResult is the reply to TUICommandGetContextTracks. ReqToken
// echoes the request so the TUI can drop results that no longer match the
// open popup.
type ContextTracksResult struct {
	ReqToken int
	Entries  []PlaybackStateQueueEntry
}

type PlaybackStateQueueEntry struct {
	ID         string
	Name       string
	Artist     string
	DurationMS int
}

type PlaybackStateUpdate struct {
	DeviceName    string
	DeviceID      string
	TrackID       string
	Volume        int
	TrackName     string
	ArtistName    string
	AlbumName     string
	AlbumImageURL string
	Playing       bool
	ProgressMS    int
	DurationMS    int
	ShuffleState  bool
	RepeatContext bool
	RepeatTrack   bool
	Queue         []PlaybackStateQueueEntry
	QueueHasMore  bool
	QueueIncluded bool

	// Error carries a transport-level failure (e.g. connection lost). Empty
	// means healthy; the TUI surfaces non-empty values as playbackErr.
	Error string
}
