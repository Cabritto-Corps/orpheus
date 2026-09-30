package tui

import (
	"context"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"

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
	playlistsLoading    bool
	albumsForbidden     bool
	playlistsErr        error
	playlistsRetryCount int
	playlistList        list.Model
	albumList           list.Model
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
	deviceName      string
	tuiCmdCh        chan librespot.TUICommand
	contextTracksCh chan<- librespot.ContextTracksResult
	ldr             *loader.BackgroundLoader

	// styles is the fully-built theme bundle, swapped wholesale on every
	// theme change; nowPlaying is the shared context-URI pointer the list
	// delegates read so the now-playing marker follows track changes.
	// Both are pointers so bubbletea's by-value model copies stay coherent.
	styles     *themeStyles
	nowPlaying *string

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

// modalKind is the single vocabulary for "which dialog owns the frame".
// It is DERIVED from the existing open flags — never stored — so open/
// close bookkeeping cannot drift out of sync, and settings persistence
// (which lives in settingsModel's content fields, not in open/mode) is
// untouched. Order matches View()'s render precedence: help, settings,
// popup. A new surface registers here and flows into the frame, the focus
// trap, the filter set and the kitty gate together.
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

	// themeOptionsPreset is the editor's base palette (row 0 may change
	// it); pendingState is the live draft; stateBackup holds the applied
	// theme to restore on esc.
	themeOptionsPreset string
	themeStatePending  themeState
	themeStateBackup   themeState
	optionsCursor      int

	// themeOverrides caches the parsed theme.json so per-frame view paths
	// (picker rows, root value) do not re-read the file at the 200ms tick.
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
