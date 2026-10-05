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
	TUICommandPlayTrack
	TUICommandPlayStation
)

// Queue commands address entries by VISIBLE position (up-next view, current
// entry excluded); track IDs can duplicate within a queue.
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

// ContextTracksResult replies to TUICommandGetContextTracks; ReqToken lets the
// TUI drop results that no longer match the open popup.
type ContextTracksResult struct {
	ReqToken int
	Entries  []PlaybackStateQueueEntry
}

type PlaybackStateQueueEntry struct {
	ID         string
	Name       string
	Artist     string
	DurationMS int
	ImageURL   string
	// Queued marks manual-queue entries; context rows cannot be edited.
	Queued bool
}

type PlaybackStateUpdate struct {
	DeviceName       string
	DeviceID         string
	TrackID          string
	Volume           int
	TrackName        string
	ArtistName       string
	AlbumName        string
	AlbumImageURL    string
	Playing          bool
	ProgressMS       int
	DurationMS       int
	ShuffleState     bool
	RepeatContext    bool
	RepeatTrack      bool
	Queue            []PlaybackStateQueueEntry
	QueueHasMore     bool
	QueueIncluded    bool
	QueueMetaPending bool
	ContextURI       string

	// Error carries a transport-level failure; the TUI surfaces it as playbackErr.
	Error string
}
