package tui

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"

	"orpheus/internal/librespot"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

type catalogSource struct {
	mu      sync.RWMutex
	current spotify.PlaylistCatalog
}

func newCatalogSource(catalog spotify.PlaylistCatalog) *catalogSource {
	return &catalogSource{current: catalog}
}

func (s *catalogSource) get() spotify.PlaylistCatalog {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *catalogSource) set(catalog spotify.PlaylistCatalog) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = catalog
}

type transportModel struct {
	status           *spotify.PlaybackStatus
	playerConnecting bool
	// Startup sync: the reveal waits for attach, the first library load,
	// and the first pushed state — bounded by a grace because an idle
	// backend never pushes.
	revealArmed             bool
	statePushSeen           bool
	revealGraceEnd          time.Time
	queueMetaPending        bool
	queueMetaRevealEnd      time.Time
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
	songChangeInFlight      *atomic.Bool
}

type browseModel struct {
	playlistsLoading    bool
	albumsForbidden     bool
	playlistsErr        error
	playlistsRetryCount int
	// filterRestorable: bubbles resets the cursor to the top on filter open
	// and never puts it back on cancel — / then esc would snap the preview
	// art to another item otherwise.
	filterRestorable bool
	filterSavedIdx   int
	// librarySettled: first library load resolved, success or failure.
	// Refreshes must never re-blank the panels.
	librarySettled bool
	playlistList   list.Model
	albumList      list.Model
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
	coverRefreshTick        int
	playerCoverRefreshTick  int
	libraryCoverRefreshTick int
	libraryMetaRefreshTick  int
	lastPlaybackStateSeq    uint64
	startupCoverBoostTicks  int
	imgs                    *imgCache
	cover                   coverManager
	settings                settingsModel
}

type model struct {
	ctx             context.Context
	catalog         spotify.PlaylistCatalog
	catalogSource   *catalogSource
	deviceName      string
	tuiCmdCh        chan librespot.TUICommand
	contextTracksCh chan<- librespot.ContextTracksResult
	ldr             *loader.BackgroundLoader

	// Pointers so bubbletea's by-value model copies stay coherent.
	styles *themeStyles

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
	settingsModeThemeOptions
)

// modalKind is DERIVED from the open flags — never stored — so open/close
// bookkeeping cannot drift out of sync. Order matches View()'s render precedence.
type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalSettingsRoot
	modalSettingsKeys
	modalSettingsTheme
	modalSettingsCapture
	modalSettingsThemeOptions
	modalTrackPopup
)

func (m model) modalKind() modalKind {
	if m.ui.helpOpen {
		return modalHelp
	}
	if s := m.ui.settings; s.open {
		switch s.mode {
		case settingsModeKeys:
			return modalSettingsKeys
		case settingsModeTheme:
			return modalSettingsTheme
		case settingsModeCapture:
			return modalSettingsCapture
		case settingsModeThemeOptions:
			return modalSettingsThemeOptions
		default:
			return modalSettingsRoot
		}
	}
	if m.ui.trackPopupOpen {
		return modalTrackPopup
	}
	return modalNone
}

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

	themeOptionsPreset string
	themeStatePending  themeState
	themeStateBackup   themeState
	optionsCursor      int

	themeOverrides map[string]any
	keysPath       string
	themePath      string
	configPath     string

	conflicts map[string]bool

	crossfadeEnabled bool
	crossfadeSeconds float64
	cacheEnabled     bool
	cacheSizeMB      int64
	imageStyle       string
	imageStyleSet    bool

	restartRequiredCrossfade bool
	restartRequiredCache     bool
	saveErr                  string
}

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
