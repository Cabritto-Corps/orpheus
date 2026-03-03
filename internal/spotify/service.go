package spotify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	spotifyapi "github.com/zmb3/spotify/v2"
	"golang.org/x/oauth2"
)

var (
	ErrDeviceNotFound    = errors.New("spotify target device not found")
	ErrNoActiveTrack     = errors.New("no active track")
	ErrNoPlaybackContext = errors.New("no playback context available; pick a playlist first")
)

type ErrorDiagnosis struct {
	Category   string
	NextStep   string
	APIStatus  int
	APIMessage string
}

type API interface {
	PlayerDevices(ctx context.Context) ([]spotifyapi.PlayerDevice, error)
	PlayerState(ctx context.Context, opts ...spotifyapi.RequestOption) (*spotifyapi.PlayerState, error)
	CurrentUser(ctx context.Context) (*spotifyapi.PrivateUser, error)
	CurrentUsersPlaylists(ctx context.Context, opts ...spotifyapi.RequestOption) (*spotifyapi.SimplePlaylistPage, error)
	CurrentUsersAlbums(ctx context.Context, opts ...spotifyapi.RequestOption) (*spotifyapi.SavedAlbumPage, error)
	GetPlaylistItems(ctx context.Context, playlistID spotifyapi.ID, opts ...spotifyapi.RequestOption) (*spotifyapi.PlaylistItemPage, error)
	GetQueue(ctx context.Context) (*spotifyapi.Queue, error)
	TransferPlayback(ctx context.Context, deviceID spotifyapi.ID, play bool) error
	PlayOpt(ctx context.Context, opt *spotifyapi.PlayOptions) error
	PauseOpt(ctx context.Context, opt *spotifyapi.PlayOptions) error
	NextOpt(ctx context.Context, opt *spotifyapi.PlayOptions) error
	PreviousOpt(ctx context.Context, opt *spotifyapi.PlayOptions) error
	QueueSongOpt(ctx context.Context, trackID spotifyapi.ID, opt *spotifyapi.PlayOptions) error
	VolumeOpt(ctx context.Context, percent int, opt *spotifyapi.PlayOptions) error
	SeekOpt(ctx context.Context, position int, opt *spotifyapi.PlayOptions) error
	ShuffleOpt(ctx context.Context, shuffle bool, opt *spotifyapi.PlayOptions) error
	RepeatOpt(ctx context.Context, state string, opt *spotifyapi.PlayOptions) error
}

type DeviceMode string

const (
	DeviceModeStrict             DeviceMode = "strict"
	DeviceModeRelaxed            DeviceMode = "relaxed"
	deviceLookupCacheTTL                    = 1 * time.Second
	deviceLookupRetryDelay                  = 150 * time.Millisecond
	deviceLookupRetryWindow                 = 900 * time.Millisecond
	deviceActivationPollInterval            = 150 * time.Millisecond
	deviceActivationWaitTimeout             = 2 * time.Second
	transferInitialRetryDelay               = 150 * time.Millisecond
	transferMaxAttempts                     = 3
	// apiRetryMaxAttempts bounds 5xx server-error retries only; rate limits
	// are handled transparently by rateLimitTransport before reaching here.
	apiRetryInitialDelay = 250 * time.Millisecond
	apiRetryMaxDelay     = 8 * time.Second
	apiRetryMaxAttempts  = 4 // total backoff ≤ 250ms+500ms+1s+2s ≈ 4 s
	apiRetryExponentCap  = 5
	rateLimitRetryDelay  = 5 * time.Second
	pollStatusBackoffMax = 10 * time.Second
)

type Options struct {
	Mode                DeviceMode
	AllowActiveFallback bool
	ItemsHTTPClient     *http.Client
}

type httpStatusError struct {
	status int
	err    error
}

func (e *httpStatusError) Error() string { return e.err.Error() }
func (e *httpStatusError) Unwrap() error { return e.err }

type Service struct {
	client              API
	itemsHTTPClient     *http.Client
	mode                DeviceMode
	allowActiveFallback bool
	deviceCacheTTL      time.Duration
	deviceCacheMu       sync.RWMutex
	deviceCacheDevices  []spotifyapi.PlayerDevice
	deviceCacheExpiry   time.Time
	deviceCacheSet      bool
	currentUserIDMu     sync.RWMutex
	currentUserID       string
	currentUserIDSet    bool
}

type PlaybackStatus struct {
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
}

type QueueItem struct {
	ID         string
	Name       string
	Artist     string
	DurationMS int
}

type PlaylistSummary struct {
	ID            string
	Name          string
	URI           string
	Kind          string
	Owner         string
	OwnerID       string
	Collaborative bool
	TrackCount    int
	ImageURL      string
}

type PlaylistPage struct {
	Items      []PlaylistSummary
	Offset     int
	Limit      int
	NextOffset int
	HasMore    bool
}

type PlaylistTrackPage struct {
	TrackIDs   []string
	TrackInfos []QueueItem // parallel slice: name/artist/duration for each TrackID
	Offset     int
	Limit      int
	NextOffset int
	HasMore    bool
}

type PlaylistCatalog interface {
	ListUserPlaylistsPage(ctx context.Context, offset, limit int) (*PlaylistPage, error)
	ListSavedAlbumsPage(ctx context.Context, offset, limit int) (*PlaylistPage, error)
	ListPlaylistTrackIDsPage(ctx context.Context, playlistID string, offset, limit int) (*PlaylistTrackPage, error)
	CurrentUserID(ctx context.Context) (string, error)
}

const (
	ContextKindPlaylist = "playlist"
	ContextKindAlbum    = "album"
)

type DeviceDoctorReport struct {
	TargetDevice string
	Mode         DeviceMode
	MatchedBy    string
	MatchedName  string
	Discovered   []string
	Notes        []string
}

func DiagnoseError(err error) ErrorDiagnosis {
	if err == nil {
		return ErrorDiagnosis{Category: "none"}
	}
	msg := strings.ToLower(err.Error())

	switch {
	case errors.Is(err, ErrDeviceNotFound):
		return ErrorDiagnosis{
			Category: "device-not-found",
			NextStep: "ensure the target device is running and visible to Spotify (e.g. restart the player or run orpheus again)",
		}
	case errors.Is(err, ErrNoActiveTrack):
		return ErrorDiagnosis{
			Category: "no-active-track",
		}
	case errors.Is(err, ErrNoPlaybackContext):
		return ErrorDiagnosis{
			Category: "no-playback-context",
			NextStep: "select a playlist in TUI",
		}
	case errors.Is(err, context.DeadlineExceeded):
		if strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit") {
			return ErrorDiagnosis{
				Category: "rate-limit",
				NextStep: "wait a few seconds and retry",
			}
		}
		return ErrorDiagnosis{
			Category: "timeout",
			NextStep: "retry shortly; if recurring, check connectivity and Spotify API limits",
		}
	case errors.Is(err, context.Canceled):
		return ErrorDiagnosis{Category: "canceled"}
	}

	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) {
		diag := ErrorDiagnosis{
			Category:   "api-error",
			APIStatus:  apiErr.Status,
			APIMessage: apiErr.Message,
		}
		if apiErr.Status == 429 {
			diag.Category = "rate-limit"
			diag.NextStep = "wait a few seconds and retry"
			return diag
		}
		if apiErr.Status == 403 {
			if strings.Contains(strings.ToLower(apiErr.Message), "scope") {
				diag.Category = "scope-error"
			} else {
				diag.Category = "forbidden"
			}
			diag.NextStep = "re-run orpheus auth login; playback may require Spotify Premium"
			return diag
		}
		if apiErr.Status >= 500 {
			diag.Category = "spotify-server-error"
			diag.NextStep = "retry shortly; server-side Spotify issues are usually transient"
			return diag
		}
		return diag
	}

	switch {
	case strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit"):
		return ErrorDiagnosis{
			Category: "rate-limit",
			NextStep: "wait a few seconds and retry",
		}
	case strings.Contains(msg, "forbidden"):
		return ErrorDiagnosis{
			Category: "forbidden",
			NextStep: "re-run orpheus auth login; playback may require Spotify Premium",
		}
	case strings.Contains(msg, "scope"):
		return ErrorDiagnosis{
			Category: "scope-error",
			NextStep: "re-run orpheus auth login",
		}
	case strings.Contains(msg, "restricted"):
		return ErrorDiagnosis{
			Category: "device-restricted",
			NextStep: "use a non-restricted playback device",
		}
	case strings.Contains(msg, "no active device") || strings.Contains(msg, "active device"):
		return ErrorDiagnosis{
			Category: "no-active-device",
			NextStep: "open Spotify on the target device, then retry",
		}
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "tempor"):
		return ErrorDiagnosis{
			Category: "timeout",
			NextStep: "retry shortly; if recurring, check connectivity",
		}
	}

	if IsTransientAPIError(err) {
		return ErrorDiagnosis{
			Category: "transient-api-error",
			NextStep: "retry shortly",
		}
	}

	return ErrorDiagnosis{Category: "unknown"}
}

func NewService(client API, opts Options) *Service {
	mode := opts.Mode
	if mode != DeviceModeRelaxed {
		mode = DeviceModeStrict
	}
	return &Service{
		client:              client,
		itemsHTTPClient:     opts.ItemsHTTPClient,
		mode:                mode,
		allowActiveFallback: opts.AllowActiveFallback,
		deviceCacheTTL:      deviceLookupCacheTTL,
	}
}

func (s *Service) CurrentUserID(ctx context.Context) (string, error) {
	s.currentUserIDMu.RLock()
	if s.currentUserIDSet {
		id := s.currentUserID
		s.currentUserIDMu.RUnlock()
		return id, nil
	}
	s.currentUserIDMu.RUnlock()
	s.currentUserIDMu.Lock()
	defer s.currentUserIDMu.Unlock()
	if s.currentUserIDSet {
		return s.currentUserID, nil
	}
	u, err := s.client.CurrentUser(ctx)
	if err != nil {
		return "", fmt.Errorf("current user: %w", err)
	}
	if u != nil {
		s.currentUserID = u.ID
	}
	s.currentUserIDSet = true
	return s.currentUserID, nil
}

// rateLimitTransport wraps an http.RoundTripper to transparently handle
// Spotify HTTP 429 rate-limit responses. When a 429 is received it:
//   - reads the Retry-After response header (defaults to 5 s)
//   - records a global "don't send before" deadline shared by all goroutines
//   - drains and closes the 429 body
//   - waits, then retries the same request
//
// On context cancellation the transport returns ctx.Err() immediately, so
// callers always get a clean error rather than the confusing
// "spotify: couldn't decode error: (17) [Too many requests]" string that
// zmb3/spotify produces when it falls through its own broken retry path.
type rateLimitTransport struct {
	base      http.RoundTripper
	mu        sync.Mutex
	waitUntil time.Time
}

func newRateLimitTransport(base http.RoundTripper) *rateLimitTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &rateLimitTransport{base: base}
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer body so it can be replayed on retry (GET requests have nil body).
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
	}

	for {
		// Honour the global rate-limit window before each attempt.
		t.mu.Lock()
		waitUntil := t.waitUntil
		t.mu.Unlock()

		if d := time.Until(waitUntil); d > 0 {
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(d):
			}
		}

		// Restore body for this attempt.
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			req.ContentLength = int64(len(bodyBytes))
		}

		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		// Parse Retry-After (fall back to 5 s if absent or unparseable).
		delay := rateLimitRetryDelay
		if raw := resp.Header.Get("Retry-After"); raw != "" {
			if secs, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 32); parseErr == nil && secs > 0 {
				delay = time.Duration(secs) * time.Second
			}
		}

		// Drain and close the 429 body; update the global wait window.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		t.mu.Lock()
		if next := time.Now().Add(delay); next.After(t.waitUntil) {
			t.waitUntil = next
		}
		t.mu.Unlock()

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(delay):
		}
	}
}

func NewClient(_ context.Context, tokenSource oauth2.TokenSource) *spotifyapi.Client {
	// Build the transport chain explicitly instead of using oauth2.NewClient(ctx, ...)
	// because that call inherits the HTTP client stored in ctx via the
	// oauth2.HTTPClient context key.  The stored client (oauthHTTPClient) has
	// ResponseHeaderTimeout=20 s which is appropriate for short OAuth token
	// refreshes, but too aggressive for Spotify API calls that may involve
	// rate-limit waits inside rateLimitTransport.  We use a clone of
	// http.DefaultTransport with ResponseHeaderTimeout disabled so that
	// per-operation context deadlines are the sole timeout mechanism.
	var base http.RoundTripper = http.DefaultTransport
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		clone := t.Clone()
		clone.ResponseHeaderTimeout = 0
		base = clone
	}
	apiClient := &http.Client{
		Transport: newRateLimitTransport(&oauth2.Transport{
			Base:   base,
			Source: oauth2.ReuseTokenSource(nil, tokenSource),
		}),
	}
	return spotifyapi.New(apiClient)
}

const spotifyAPIBase = "https://api.spotify.com/v1/"

func NewItemsHTTPClient(tokenSource oauth2.TokenSource) *http.Client {
	var base http.RoundTripper = http.DefaultTransport
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		clone := t.Clone()
		clone.ResponseHeaderTimeout = 0
		base = clone
	}
	return &http.Client{
		Transport: newRateLimitTransport(&oauth2.Transport{
			Base:   base,
			Source: oauth2.ReuseTokenSource(nil, tokenSource),
		}),
	}
}

func (s *Service) Status(ctx context.Context) (*PlaybackStatus, error) {
	state, err := s.playerStateWithRetry(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch player state: %w", err)
	}
	if state == nil || state.Item == nil {
		return nil, ErrNoActiveTrack
	}
	status := &PlaybackStatus{
		DeviceName:    state.Device.Name,
		DeviceID:      string(state.Device.ID),
		TrackID:       string(state.Item.ID),
		Volume:        int(state.Device.Volume),
		TrackName:     state.Item.Name,
		AlbumName:     state.Item.Album.Name,
		Playing:       state.Playing,
		ProgressMS:    int(state.Progress),
		DurationMS:    int(state.Item.Duration),
		ShuffleState:  state.ShuffleState,
		RepeatContext: strings.EqualFold(state.RepeatState, "context"),
		RepeatTrack:   strings.EqualFold(state.RepeatState, "track"),
	}
	if len(state.Item.Artists) > 0 {
		status.ArtistName = state.Item.Artists[0].Name
	}
	if len(state.Item.Album.Images) > 0 {
		status.AlbumImageURL = state.Item.Album.Images[0].URL
	}
	return status, nil
}

func (s *Service) GetQueue(ctx context.Context) ([]QueueItem, error) {
	q, err := apiCallWithRetry(ctx, func() (*spotifyapi.Queue, error) {
		return s.client.GetQueue(ctx)
	})
	if err != nil {
		return nil, fmt.Errorf("fetch queue: %w", err)
	}
	if q == nil || len(q.Items) == 0 {
		return nil, nil
	}
	out := make([]QueueItem, 0, len(q.Items))
	for _, t := range q.Items {
		item := QueueItem{ID: string(t.ID), Name: t.Name, DurationMS: int(t.Duration)}
		if len(t.Artists) > 0 {
			item.Artist = t.Artists[0].Name
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) playerStateWithRetry(ctx context.Context) (*spotifyapi.PlayerState, error) {
	return apiCallWithRetry(ctx, func() (*spotifyapi.PlayerState, error) {
		return s.client.PlayerState(ctx)
	})
}

func PollStatus(ctx context.Context, interval time.Duration, fetch func(context.Context) (*PlaybackStatus, error), onStatus func(*PlaybackStatus), onError func(error)) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	backoff := interval
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			status, err := fetch(ctx)
			if err != nil {
				onError(err)
				if IsTransientAPIError(err) {
					backoff *= 2
					if backoff > pollStatusBackoffMax {
						backoff = pollStatusBackoffMax
					}
					timer.Reset(backoff)
					continue
				}
				backoff = interval
				timer.Reset(interval)
				continue
			}
			backoff = interval
			onStatus(status)
			timer.Reset(interval)
		}
	}
}
