package librespot

type TUICommandKind int

const (
	TUICommandPlayContext TUICommandKind = iota
	TUICommandPlayContextFromTrack
	TUICommandPause
	TUICommandResume
	TUICommandSeek
	TUICommandSkipNext
	TUICommandSkipPrev
	TUICommandSetVolume
	TUICommandShuffle
	TUICommandCycleRepeat
)

type TUICommand struct {
	Kind     TUICommandKind
	URI      string
	TrackID  string
	Position int64
	Volume   int
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
}
