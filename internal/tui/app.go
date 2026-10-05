package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

type tab string

const (
	tabRecents               tab = "recents"
	tabPlaylists             tab = "playlists"
	tabAlbums                tab = "albums"
	tabSearch                tab = "search"
	tabPlayer                tab = "player"
	coverPreloadWindow           = 20
	imageLoadRetryMax            = 4
	coverRefreshEvery            = 15
	playerCoverRefreshEvery      = 5
	libraryCoverRefreshEvery     = 150
	libraryCoverRefreshBatch     = 32
	libraryMetaRefreshEvery      = 300
	coverQueueDrainBatch         = 20
	// Caps one push's enqueue: a huge shuffled context cannot flood the
	// queue; the chained drain finishes the rest.
	queueCoverSweepBatch = 64
	// Mirrors the backend head window: current cover plus this many
	// up-next covers stay pinned against LRU eviction.
	queueHeadPinWindow = 8
	// Caps a server-penalty park: a Retry-After of hours parks the sweep
	// ten minutes, then it re-evaluates.
	sweepPauseMax                 = 10 * time.Minute
	kittyProtocolFallbackFailures = 8
	kittyProtocolRecoveryStreak   = 8
	uiTickInterval                = 200 * time.Millisecond
	uiIdleTickInterval            = time.Second
	// Popup load give-up window: ticks × the 200 ms tick = 8 s.
	trackPopupLoadTimeoutTicks = 40
	navDebounceInterval        = 60 * time.Millisecond
	volSeekDebounceInterval    = 50 * time.Millisecond
	volSettleWindow            = 3 * time.Second
	seekSettleWindow           = 1200 * time.Millisecond
)

type playlistItem struct {
	summary spotify.PlaylistSummary
}

func (p playlistItem) Title() string {
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

type searchResultItem struct {
	result spotify.SearchResultItem
}

func (s searchResultItem) Title() string {
	badge := strings.ToUpper(s.result.Kind)
	if badge == "TRACK" {
		badge = "SONG"
	}
	return "[" + badge + "] " + s.result.Name
}

func (s searchResultItem) FilterValue() string { return s.result.Name + " " + s.result.Owner }

func (s searchResultItem) Description() string {
	if s.result.Kind == "track" {
		album := strings.TrimSpace(s.result.AlbumName)
		if album != "" {
			return s.result.Owner + " • " + album + " • " + fmtDuration(s.result.DurationMS)
		}
		return s.result.Owner + " • " + fmtDuration(s.result.DurationMS)
	}
	if s.result.Kind == "artist" {
		if len(s.result.Genres) > 0 {
			return "artist • " + strings.Join(s.result.Genres[:min(2, len(s.result.Genres))], ", ")
		}
		return "artist"
	}
	return fmt.Sprintf("%s • %d tracks", s.result.Owner, s.result.TrackCount)
}

func (t trackItem) Title() string       { return t.item.Name }
func (t trackItem) FilterValue() string { return t.item.Name + " " + t.item.Artist }
func (t trackItem) Description() string { return t.item.Artist }

// The themed default delegate wrapped in the render cache: rows carry the
// right-aligned duration in the same styling.
func newTrackPopupDelegate(s *themeStyles) cachedDelegate {
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

	return cachedDelegate{DefaultDelegate: d, cache: c}
}

func newModel(ctx context.Context, catalog spotify.PlaylistCatalog, cfg config.Config, tuiCmdCh chan librespot.TUICommand, contextTracksCh chan<- librespot.ContextTracksResult, ldr *loader.BackgroundLoader) model {
	state, resolvedPreset := LoadTheme(cfg.Theme, cfg.ThemePath)
	styles := buildThemeStyles(state)
	browser := newBrowseList(styles)
	albums := newBrowseList(styles)
	imageStyle, imageStyleSet := cfg.ImageStyle, config.NormalizeImageStyle(cfg.ImageStyle) != ""
	if !imageStyleSet {
		imageStyle, imageStyleSet = config.ExplicitImageStyle(cfg.SettingsPath)
	}
	imgs := newImgCacheWithSelection(imageStyle, imageStyleSet, os.Getenv)
	recents := list.New(nil, newSearchResultDelegate(styles, imgs), 40, 20)
	recents.SetShowTitle(false)
	recents.SetShowStatusBar(false)
	recents.SetFilteringEnabled(true)
	recents.SetShowFilter(true)
	recents.SetShowHelp(false)
	recents.FilterInput.Prompt = "Search: "
	applyListStyles(&recents, styles)
	searchList := list.New(nil, newSearchResultDelegate(styles, imgs), 40, 20)
	searchList.SetShowTitle(false)
	searchList.SetShowStatusBar(false)
	searchList.SetFilteringEnabled(false)
	searchList.SetShowFilter(false)
	searchList.SetShowHelp(false)
	applyListStyles(&searchList, styles)
	searchInput := textinput.New()
	searchInput.Prompt = "Search Spotify: "
	searchInput.Placeholder = "artist, album or track"
	searchInput.CharLimit = 200
	searchInput.Blur()
	m := model{
		ctx:             ctx,
		catalog:         catalog,
		catalogSource:   newCatalogSource(catalog),
		deviceName:      cfg.DeviceName,
		tuiCmdCh:        tuiCmdCh,
		contextTracksCh: contextTracksCh,
		ldr:             ldr,
		styles:          styles,
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
			recentsList:      recents,
			playlistsLoading: true,
			search: searchModel{
				input: searchInput,
				list:  searchList,
			},
		},
		ui: uiModel{
			activeTab:              tabPlaylists,
			config:                 cfg,
			imgs:                   imgs,
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

func selectedRecentImageURL(l list.Model) string {
	selected, ok := l.SelectedItem().(trackItem)
	if !ok {
		return ""
	}
	return selected.item.ImageURL
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
	l.Paginator.Page = min(max(l.Paginator.Page, 0), maxPage)
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

type ProgramHandle struct {
	Program *tea.Program
	done    chan error
}

func (h *ProgramHandle) Wait() error {
	return <-h.done
}

func Run(ctx context.Context, catalog spotify.PlaylistCatalog, cfg config.Config, tuiCmdCh chan librespot.TUICommand, playbackStateCh <-chan *librespot.PlaybackStateUpdate) error {
	handle, err := startProgram(ctx, catalog, cfg, tuiCmdCh, playbackStateCh, false)
	if err != nil {
		return err
	}
	return handle.Wait()
}

func Start(ctx context.Context, catalog spotify.PlaylistCatalog, cfg config.Config, tuiCmdCh chan librespot.TUICommand, playbackStateCh <-chan *librespot.PlaybackStateUpdate) (*ProgramHandle, error) {
	return startProgram(ctx, catalog, cfg, tuiCmdCh, playbackStateCh, true)
}

func startProgram(ctx context.Context, catalog spotify.PlaylistCatalog, cfg config.Config, tuiCmdCh chan librespot.TUICommand, playbackStateCh <-chan *librespot.PlaybackStateUpdate, markConnecting bool) (*ProgramHandle, error) {
	contextTracksCh := make(chan librespot.ContextTracksResult, 1)
	catalogSource := newCatalogSource(catalog)
	ldr := loader.New(ctx, 128, NewDynamicCatalogExecutor(ctx, catalogSource.get))
	m := newModel(ctx, catalog, cfg, tuiCmdCh, contextTracksCh, ldr)
	m.ui.clientIDSetupOpen = strings.TrimSpace(cfg.SpotifyClientID) == ""
	m.catalogSource = catalogSource
	m.transport.playerConnecting = markConnecting
	// Match the terminal's own background to the theme's page color for the
	// session; restore on exit (the padding around the grid).
	CaptureTerminalBG()
	ApplyTerminalBG(m.styles.colorPage, m.styles.transparentFrame(), m.styles.colorProfile)
	p := tea.NewProgram(m)
	handle := &ProgramHandle{Program: p, done: make(chan error, 1)}
	if playbackStateCh != nil {
		StartPlaybackStateListener(playbackStateCh, p.Send, ctx)
	}
	StartContextTracksListener(contextTracksCh, p.Send, ctx)
	go func() {
		_, err := p.Run()
		RestoreTerminalBG()
		handle.done <- err
	}()
	return handle, nil
}

func (m model) Init() tea.Cmd {
	if m.ui.authLoginOnly {
		return nil
	}
	return tea.Batch(
		preloadLikedSongsArtCmd(m),
		m.loadPlaylistsCmd(),
		m.tickCmd(),
	)
}

func keyMatches(msg tea.KeyPressMsg, b key.Binding) bool {
	k := msg.Key()
	// Terminals may attach lock-state modifiers (Caps/Num/Scroll Lock) to
	// every key event. They are state, not intentional shortcut modifiers.
	k.Mod &^= tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock
	for _, spec := range b.Keys() {
		if matchKeySpec(k, spec) {
			return true
		}
	}
	return false
}
