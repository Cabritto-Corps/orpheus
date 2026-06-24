package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"

	"orpheus/internal/cache"
	"orpheus/internal/librespot"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

type transportModel struct {
	status                  *spotify.PlaybackStatus
	queue                   []spotify.QueueItem
	queueHasMore            bool
	stableQueueLen          int
	pendingContextFrom      string
	pendingContextFromAt    time.Time
	transition              transportTransition
	playerCoverEpoch        uint64
	inputQueue              []playbackInput
	executorState           commandExecutorState
	actionInFlight          bool
	volDebouncePending      int
	volDebounceToken        int
	volSentAt               time.Time
	volSentTarget           int
	seekSentAt              time.Time
	seekSentTarget          int
	seekDebouncePending     int
	seekDebounceToken       int
	interpolationSyncAt     time.Time
	interpolationProgressMS int
	onSongChange            string
	lastPlayedID            string
	playbackErr             error
}

type browseModel struct {
	activePlaylistID             string
	activePlaylistOwnerID        string
	activePlaylistCollaborative  bool
	activePlaylistItemIDs        []string
	activePlaylistItemNextOffset int
	activePlaylistItemHasMore    bool
	activePlaylistItemLoading    bool
	activePlaylistLoadToken      int
	preloadedItemIDs             map[string]struct{}
	trackCache                   *cache.TTL[string, spotify.QueueItem]
	playlistsLoading             bool
	playlistsExhausted           bool
	albumsForbidden              bool
	playlistsErr                 error
	playlistsRetryCount          int
	playlistItemRetryCount       int
	currentUserID                string
	playlistList                 list.Model
	albumList                    list.Model
}

type uiModel struct {
	activeTab               tab
	helpOpen                bool
	navToken                int
	trackPopupOpen          bool
	trackPopupList          list.Model
	trackPopupKind          string
	trackPopupID            string
	trackPopupURI           string
	trackPopupName          string
	trackPopupItems         []spotify.QueueItem
	trackPopupWidth         int
	width                   int
	height                  int
	nerdFonts               bool
	cachedBodyLayout        bodyLayout
	cachedBodyLayoutValid   bool
	help                    help.Model
	keys                    keyMap
	pollInterval            time.Duration
	pollTick                int
	lastPollTime            time.Time
	coverRefreshTick        int
	playerCoverRefreshTick  int
	libraryCoverRefreshTick int
	libraryMetaRefreshTick  int
	actionFastPollUntil     time.Time
	stateFetchToken         uint64
	lastPlaybackStateSeq    uint64
	startupCoverBoostTicks  int
	imgs                    *imgCache
	statusQueueCache        *statusQueueSnapshotCache
	cover                   coverManager
}

type model struct {
	ctx             context.Context
	catalog         spotify.PlaylistCatalog
	service         *spotify.Service
	deviceName      string
	tuiCmdCh        chan librespot.TUICommand
	contextTracksCh chan<- []librespot.PlaybackStateQueueEntry
	ldr             *loader.BackgroundLoader

	transport transportModel
	browse    browseModel
	ui        uiModel
}
