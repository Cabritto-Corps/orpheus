package tui

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"

	"orpheus/internal/cache"
	"orpheus/internal/librespot"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

type transportModel struct {
	status                  *spotify.PlaybackStatus
	queue                   []spotify.QueueItem
	queueCursor             int
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
	queueFingerprint        uint64
	songChangeInFlight      *atomic.Bool
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
	spinner                 spinner.Model
	navToken                int
	trackPopupOpen          bool
	trackPopupList          list.Model
	trackPopupKind          string
	trackPopupID            string
	trackPopupURI           string
	trackPopupName          string
	trackPopupItems         []spotify.QueueItem
	trackPopupReqToken      int
	trackPopupWaitTicks     int
	trackPopupWidth         int
	width                   int
	height                  int
	nerdFonts               bool
	helpViewport            *viewport.Model
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
	settings                settingsModel
}

type model struct {
	ctx             context.Context
	catalog         spotify.PlaylistCatalog
	service         *spotify.Service
	deviceName      string
	tuiCmdCh        chan librespot.TUICommand
	contextTracksCh chan<- librespot.ContextTracksResult
	ldr             *loader.BackgroundLoader

	transport transportModel
	browse    browseModel
	ui        uiModel
}

type settingsMode int

const (
	settingsModeRoot settingsMode = iota
	settingsModeKeys
	settingsModeTheme
	settingsModeCapture
)

type settingsModel struct {
	open        bool
	mode        settingsMode
	cursor      int
	keysCursor  int
	captureKey  string
	pendingKey  string
	themePreset string
	themeCursor int
	themeBackup string
	keysPath    string
	themePath   string
	envPath     string

	keysTable *table.Model
	conflicts map[string]bool

	crossfadeEnabled bool
	crossfadeSeconds float64
	cacheEnabled     bool
	cacheSizeMB      int64

	restartRequiredCrossfade bool
	restartRequiredCache     bool
	keysTableDirty           bool
}

// settingsKeyActions derives the settings keys-menu rows (order + labels)
// from the shared action registry.
var settingsKeyActions = func() []struct {
	action string
	label  string
} {
	out := make([]struct {
		action string
		label  string
	}, 0, len(actionRegistry))
	for _, m := range actionRegistry {
		out = append(out, struct {
			action string
			label  string
		}{m.action, m.label})
	}
	return out
}()

var settingsThemeOrder = themeRegistryNames()
