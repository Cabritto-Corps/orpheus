package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"orpheus/internal/cache"
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
	playlistItemPageSize              = 100
	queuePollEvery                    = 2
	playlistLoadBatchSize             = 25
	playlistLoadMax                   = 500
	playlistItemPreloadMax            = 500
	coverPreloadWindow                = 20
	imageLoadRetryMax                 = 4
	coverRefreshEvery                 = 15
	playerCoverRefreshEvery           = 5
	libraryCoverRefreshEvery          = 150
	libraryCoverRefreshBatch          = 32
	libraryMetaRefreshEvery           = 300
	coverQueueDrainBatch              = 20
	kittyProtocolFallbackFailures     = 8
	trackMetadataTTL                  = 2 * time.Hour
	uiTickInterval                    = 200 * time.Millisecond
	navDebounceInterval               = 60 * time.Millisecond
	volSeekDebounceInterval           = 50 * time.Millisecond
	volSettleWindow                   = 3 * time.Second
	seekSettleWindow                  = 1200 * time.Millisecond
	reconcileActionWindow             = 2 * time.Second
	actionFastPollWindow              = 3 * time.Second
	idlePollBackoffMax                = 5 * time.Second
)

type playlistItem struct {
	summary spotify.PlaylistSummary
}

func (p playlistItem) Title() string { return p.summary.Name }
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

func newTrackPopupDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.SetHeight(2)
	d.SetSpacing(0)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colorMutedBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colorOffWhite).
		Padding(0, 0, 0, 2)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(colorMutedBlue).
		Padding(0, 0, 0, 2)

	return d
}

func newModel(ctx context.Context, catalog spotify.PlaylistCatalog, service *spotify.Service, cfg config.Config, tuiCmdCh chan librespot.TUICommand, contextTracksCh chan<- []librespot.PlaybackStateQueueEntry, ldr *loader.BackgroundLoader) model {
	delegate := newPlaylistDelegate()

	browser := list.New(nil, delegate, 40, 20)
	browser.SetShowTitle(false)
	browser.SetShowStatusBar(false)
	browser.SetFilteringEnabled(true)
	browser.SetShowFilter(true)
	browser.SetShowHelp(false)
	browser.FilterInput.Prompt = "Search: "
	applyListStyles(&browser)

	albums := list.New(nil, delegate, 40, 20)
	albums.SetShowTitle(false)
	albums.SetShowStatusBar(false)
	albums.SetFilteringEnabled(true)
	albums.SetShowFilter(true)
	albums.SetShowHelp(false)
	albums.FilterInput.Prompt = "Search: "
	applyListStyles(&albums)

	h := newHelp()

	m := model{
		ctx:             ctx,
		catalog:         catalog,
		service:         service,
		deviceName:      cfg.DeviceName,
		tuiCmdCh:        tuiCmdCh,
		contextTracksCh: contextTracksCh,
		ldr:             ldr,
		transport: transportModel{
			volDebouncePending:  -1,
			seekDebouncePending: -1,
			volSentTarget:       -1,
			seekSentTarget:      -1,
			onSongChange:        cfg.OnSongChange,
		},
		browse: browseModel{
			preloadedItemIDs: make(map[string]struct{}),
			trackCache:       cache.NewTTL[string, spotify.QueueItem](4096, trackMetadataTTL),
			playlistList:     browser,
			albumList:        albums,
			playlistsLoading: true,
		},
		ui: uiModel{
			pollInterval:           cfg.PollInterval,
			activeTab:              tabPlaylists,
			imgs:                   newImgCache(),
			statusQueueCache:       newStatusQueueSnapshotCache(),
			startupCoverBoostTicks: 40,
			cover:                  newCoverManager(),
			nerdFonts:              cfg.NerdFonts,
			help:                   h,
			keys:                   newKeys(),
		},
	}

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

func Run(ctx context.Context, catalog spotify.PlaylistCatalog, service *spotify.Service, cfg config.Config, tuiCmdCh chan librespot.TUICommand, playbackStateCh <-chan *librespot.PlaybackStateUpdate) error {
	contextTracksCh := make(chan []librespot.PlaybackStateQueueEntry, 1)
	ldr := loader.New(ctx, 128, NewTUIExecutor(ctx, catalog))
	m := newModel(ctx, catalog, service, cfg, tuiCmdCh, contextTracksCh, ldr)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if playbackStateCh != nil {
		StartPlaybackStateListener(playbackStateCh, p.Send, ctx)
	}
	StartContextTracksListener(contextTracksCh, p.Send, ctx)
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	m.preloadLikedSongsArt()
	return tea.Batch(
		m.getCurrentUserIDCmd(),
		m.pollCmd(true),
		m.tickCmd(),
	)
}

func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return key.Matches(msg, b)
}

func repeatModeString(repeatContext, repeatTrack bool) string {
	if repeatTrack {
		return "track"
	}
	if repeatContext {
		return "context"
	}
	return "off"
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
