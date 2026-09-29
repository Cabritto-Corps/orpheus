package tui

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

type tab string

const (
	tabPlaylists                  tab = "playlists"
	tabAlbums                     tab = "albums"
	tabPlayer                     tab = "player"
	coverPreloadWindow                = 20
	imageLoadRetryMax                 = 4
	coverRefreshEvery                 = 15
	playerCoverRefreshEvery           = 5
	libraryCoverRefreshEvery          = 150
	libraryCoverRefreshBatch          = 32
	libraryMetaRefreshEvery           = 300
	coverQueueDrainBatch              = 20
	kittyProtocolFallbackFailures     = 8
	kittyProtocolRecoveryStreak       = 8
	uiTickInterval                    = 200 * time.Millisecond
	// 8s at the 200ms tick interval before a pending popup load gives up.
	trackPopupLoadTimeoutTicks = 40
	navDebounceInterval        = 60 * time.Millisecond
	volSeekDebounceInterval    = 50 * time.Millisecond
	volSettleWindow            = 3 * time.Second
	seekSettleWindow           = 1200 * time.Millisecond
)

type playlistItem struct {
	summary spotify.PlaylistSummary

	// nowPlaying is set per render by the delegate wrapper; it makes Title()
	// carry the now-playing glyph so the delegate cache key changes too.
	// glyph rides alongside: the marker text comes from the theme bundle
	// the delegate was built with, never a global.
	nowPlaying bool
	glyph      string
}

func (p playlistItem) Title() string {
	if p.nowPlaying && p.glyph != "" {
		return p.summary.Name + " " + p.glyph
	}
	return p.summary.Name
}
func (p playlistItem) FilterValue() string {
	return p.summary.Name
}

func (p playlistItem) Description() string {
	if p.summary.Kind == spotify.ContextKindLikedSongs {
		return "your saved tracks"
	}
	switch p.summary.Kind {
	case spotify.ContextKindAlbum:
		return "album by " + p.summary.Owner
	default:
		return "playlist by " + p.summary.Owner
	}
}

type trackItem struct {
	item spotify.QueueItem
}

func (t trackItem) Title() string       { return t.item.Name }
func (t trackItem) FilterValue() string { return t.item.Name }
func (t trackItem) Description() string { return t.item.Artist }

// newTrackPopupDelegate returns the popup's delegate: the themed default
// delegate wrapped in the render cache, so the track rows can carry the
// right-aligned duration while keeping the same styling.
func newTrackPopupDelegate(s *themeStyles, nowPlaying *string) cachedDelegate {
	c := &delegateCache{entries: make(map[delegateKey]string, 64)}
	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.SetHeight(2)
	d.SetSpacing(0)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Bold(s.themeBoldTitles).
		Foreground(s.colorBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(s.colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Italic(s.themeItalicDescs).
		Foreground(s.colorMutedBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(s.colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(s.colorOffWhite).
		Padding(0, 0, 0, 2)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Italic(s.themeItalicDescs).
		Foreground(s.colorMutedBlue).
		Padding(0, 0, 0, 2)

	return cachedDelegate{DefaultDelegate: d, cache: c, glyph: s.themeNowPlayingGlyph(), nowPlaying: nowPlaying}
}

func newModel(ctx context.Context, catalog spotify.PlaylistCatalog, cfg config.Config, tuiCmdCh chan librespot.TUICommand, contextTracksCh chan<- librespot.ContextTracksResult, ldr *loader.BackgroundLoader) model {
	state, resolvedPreset := LoadTheme(cfg.Theme, cfg.ThemePath)
	styles := buildThemeStyles(state)
	nowPlaying := new(string)
	browser := newBrowseList(styles, nowPlaying)
	albums := newBrowseList(styles, nowPlaying)
	imageStyle, imageStyleSet := cfg.ImageStyle, config.NormalizeImageStyle(cfg.ImageStyle) != ""
	if !imageStyleSet {
		imageStyle, imageStyleSet = config.ExplicitImageStyle(cfg.SettingsPath)
	}

	m := model{
		ctx:             ctx,
		catalog:         catalog,
		deviceName:      cfg.DeviceName,
		tuiCmdCh:        tuiCmdCh,
		contextTracksCh: contextTracksCh,
		ldr:             ldr,
		styles:          styles,
		nowPlaying:      nowPlaying,
		transport: transportModel{
			volDebouncePending:  -1,
			seekDebouncePending: -1,
			volSentTarget:       -1,
			seekSentTarget:      -1,
			onSongChange:        cfg.OnSongChange,
			songChangeInFlight:  &atomic.Bool{},
		},
		browse: browseModel{
			playlistList:     browser,
			albumList:        albums,
			playlistsLoading: true,
		},
		ui: uiModel{
			activeTab:              tabPlaylists,
			imgs:                   newImgCacheWithSelection(imageStyle, imageStyleSet, os.Getenv),
			spinner:                themedSpinner(styles),
			startupCoverBoostTicks: 40,
			cover:                  newCoverManager(),
			nerdFonts:              cfg.NerdFonts,
			keys:                   newKeysFromConfig(LoadKeys(cfg.KeysPath)),
			settings:               newSettingsModel(cfg, resolvedPreset),
		},
	}

	m.syncListKeyMaps()
	m.ui.settings.imageStyle = imageStyleOrDefault(imageStyle)
	m.ui.settings.imageStyleSet = imageStyleSet
	return m
}

func selectedImageURLFromList(l list.Model) string {
	sel, ok := l.SelectedItem().(playlistItem)
	if !ok {
		return ""
	}
	return sel.summary.ImageURL
}

func normalizeListPagination(l *list.Model) {
	visible := l.VisibleItems()
	if len(visible) == 0 {
		if l.FilterState() == list.Unfiltered {
			l.Paginator.Page = 0
		}
		return
	}
	perPage := l.Paginator.PerPage
	if perPage <= 0 {
		perPage = len(visible)
	}
	maxPage := (len(visible) - 1) / perPage
	l.Paginator.Page = clampInt(l.Paginator.Page, 0, maxPage)
	if l.FilterState() == list.Unfiltered {
		idx := l.GlobalIndex()
		if idx >= len(visible) {
			idx = 0
		}
		l.Select(idx)
	}
}

func (m *model) normalizeLibraryPagination() {
	normalizeListPagination(&m.browse.playlistList)
	normalizeListPagination(&m.browse.albumList)
}

func Run(ctx context.Context, catalog spotify.PlaylistCatalog, cfg config.Config, tuiCmdCh chan librespot.TUICommand, playbackStateCh <-chan *librespot.PlaybackStateUpdate) error {
	contextTracksCh := make(chan librespot.ContextTracksResult, 1)
	ldr := loader.New(ctx, 128, NewTUIExecutor(ctx, catalog))
	m := newModel(ctx, catalog, cfg, tuiCmdCh, contextTracksCh, ldr)
	// Match the terminal's own background (the padding around the grid)
	// to the theme's page color for the session; restore on exit.
	CaptureTerminalBG()
	defer RestoreTerminalBG()
	ApplyTerminalBG(m.styles.colorPage, m.styles.transparentFrame())
	p := tea.NewProgram(m)
	if playbackStateCh != nil {
		StartPlaybackStateListener(playbackStateCh, p.Send, ctx)
	}
	StartContextTracksListener(contextTracksCh, p.Send, ctx)
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		preloadLikedSongsArtCmd(m),
		m.loadPlaylistsCmd(),
		m.tickCmd(),
	)
}

func keyMatches(msg tea.KeyPressMsg, b key.Binding) bool {
	k := msg.Key()
	for _, spec := range b.Keys() {
		if matchKeySpec(k, spec) {
			return true
		}
	}
	return false
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
