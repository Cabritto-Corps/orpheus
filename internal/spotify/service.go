package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	transferInitialRetryDelay               = 150 * time.Millisecond
	// apiRetryMaxAttempts bounds 5xx server-error retries only; rate limits
	// are handled transparently by rateLimitTransport before reaching here.
	apiRetryInitialDelay = 250 * time.Millisecond
	apiRetryMaxDelay     = 8 * time.Second
	apiRetryMaxAttempts  = 4 // total backoff ≤ 250ms+500ms+1s+2s ≈ 4 s
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
	ListPlaylistTrackIDsPage(ctx context.Context, playlistID string, offset, limit int) (*PlaylistTrackPage, error)
	CurrentUserID(ctx context.Context) (string, error)
}

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
		delay := 5 * time.Second
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

func (s *Service) FindDeviceByName(ctx context.Context, target string) (*spotifyapi.PlayerDevice, error) {
	devices, err := s.getPlayerDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch spotify devices: %w", err)
	}
	device, _, err := s.resolveDevice(target, devices)
	if err == nil || !errors.Is(err, ErrDeviceNotFound) {
		return device, err
	}
	deadline := time.Now().Add(deviceLookupRetryWindow)
	for {
		devices, err = s.refreshPlayerDevices(ctx)
		if err != nil {
			return nil, fmt.Errorf("fetch spotify devices: %w", err)
		}
		device, _, err = s.resolveDevice(target, devices)
		if err == nil || !errors.Is(err, ErrDeviceNotFound) {
			return device, err
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(deviceLookupRetryDelay):
		}
	}
}

func (s *Service) DeviceDoctor(ctx context.Context, target string) (*DeviceDoctorReport, error) {
	devices, err := s.getPlayerDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch spotify devices: %w", err)
	}
	report := &DeviceDoctorReport{
		TargetDevice: target,
		Mode:         s.mode,
		Discovered:   make([]string, 0, len(devices)),
	}
	for _, d := range devices {
		report.Discovered = append(report.Discovered, fmt.Sprintf("%s (active=%t restricted=%t)", d.Name, d.Active, d.Restricted))
	}
	device, matchedBy, resolveErr := s.resolveDevice(target, devices)
	if resolveErr != nil {
		report.Notes = append(report.Notes, "No device match. Check that the target device is running and spotify_device_name matches.")
		return report, resolveErr
	}
	report.MatchedBy = matchedBy
	report.MatchedName = device.Name
	if device.Restricted {
		report.Notes = append(report.Notes, "Matched device is restricted and cannot receive commands.")
	}
	if !device.Active {
		report.Notes = append(report.Notes, "Device is discoverable but not active; transfer will be attempted on play actions.")
	}
	return report, nil
}

func (s *Service) EnsureDeviceActive(ctx context.Context, target string) (*spotifyapi.PlayerDevice, error) {
	device, err := s.FindDeviceByName(ctx, target)
	if err != nil {
		return nil, err
	}
	if device.Restricted {
		return nil, errors.New("spotify device is restricted")
	}
	if device.Active {
		return device, nil
	}
	if err := s.transferWithRetry(ctx, device.ID); err != nil {
		return nil, fmt.Errorf("transfer playback to %q: %w", target, err)
	}
	if err := s.waitForDeviceActive(ctx, device.ID); err != nil {
		return nil, err
	}
	device.Active = true
	return device, nil
}

func (s *Service) ListDevices(ctx context.Context) ([]spotifyapi.PlayerDevice, error) {
	devices, err := s.getPlayerDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch spotify devices: %w", err)
	}
	return devices, nil
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

func (s *Service) Play(ctx context.Context, target string) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		err := s.client.PlayOpt(ctx, &spotifyapi.PlayOptions{DeviceID: &deviceID})
		if err == nil {
			return nil
		}
		var apiErr spotifyapi.Error
		if errors.As(err, &apiErr) && (apiErr.Status == 403 || apiErr.Status == 404) {
			state, stateErr := s.playerStateWithRetry(ctx)
			if stateErr == nil && (state == nil || state.Item == nil) {
				return ErrNoPlaybackContext
			}
		}
		return err
	})
}

func (s *Service) Pause(ctx context.Context, target string) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.PauseOpt(ctx, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) Next(ctx context.Context, target string) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.NextOpt(ctx, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) Previous(ctx context.Context, target string) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.PreviousOpt(ctx, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) SetVolume(ctx context.Context, target string, volume int) error {
	if volume < 0 || volume > 100 {
		return errors.New("volume must be in range 0..100")
	}
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.VolumeOpt(ctx, volume, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) Seek(ctx context.Context, target string, positionMS int) error {
	if positionMS < 0 {
		return errors.New("position must be >= 0")
	}
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.SeekOpt(ctx, positionMS, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) Shuffle(ctx context.Context, target string, shuffle bool) error {
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.ShuffleOpt(ctx, shuffle, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) SetRepeat(ctx context.Context, target string, state string) error {
	state = strings.ToLower(strings.TrimSpace(state))
	if state != "off" && state != "context" && state != "track" {
		return errors.New("repeat state must be one of off, context, track")
	}
	return s.withDeviceCommand(ctx, target, func(deviceID spotifyapi.ID) error {
		return s.client.RepeatOpt(ctx, state, &spotifyapi.PlayOptions{DeviceID: &deviceID})
	})
}

func (s *Service) ListUserPlaylists(ctx context.Context, max int) ([]PlaylistSummary, error) {
	if max <= 0 {
		max = 100
	}

	const pageSize = 50
	offset := 0
	out := make([]PlaylistSummary, 0, min(max, pageSize))
	for len(out) < max {
		limit := min(pageSize, max-len(out))
		page, err := s.ListUserPlaylistsPage(ctx, offset, limit)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == 0 {
			break
		}
		out = append(out, page.Items...)
		if !page.HasMore || page.NextOffset <= offset {
			break
		}
		offset = page.NextOffset
	}
	return out, nil
}

func (s *Service) ListUserPlaylistsPage(ctx context.Context, offset, limit int) (*PlaylistPage, error) {
	if offset < 0 {
		return nil, errors.New("playlist offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}

	page, err := apiCallWithRetry(ctx, func() (*spotifyapi.SimplePlaylistPage, error) {
		return s.client.CurrentUsersPlaylists(ctx, spotifyapi.Limit(limit), spotifyapi.Offset(offset))
	})
	if err != nil {
		return nil, fmt.Errorf("fetch user playlists: %w", err)
	}

	out := &PlaylistPage{
		Offset: offset,
		Limit:  limit,
	}
	if page == nil || len(page.Playlists) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.Items = make([]PlaylistSummary, 0, len(page.Playlists))
	for _, pl := range page.Playlists {
		imageURL := ""
		if len(pl.Images) > 0 {
			imageURL = pl.Images[0].URL
		}
		out.Items = append(out.Items, PlaylistSummary{
			ID:            string(pl.ID),
			Name:          pl.Name,
			URI:           string(pl.URI),
			Owner:         pl.Owner.DisplayName,
			OwnerID:       pl.Owner.ID,
			Collaborative: pl.Collaborative,
			TrackCount:    int(pl.Tracks.Total),
			ImageURL:      imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = len(out.Items) >= limit
	return out, nil
}

func (s *Service) ListPlaylistTrackIDs(ctx context.Context, playlistID string, max int) ([]string, error) {
	playlistID = strings.TrimSpace(playlistID)
	if playlistID == "" {
		return nil, errors.New("playlist ID must not be empty")
	}
	if max <= 0 {
		max = 500
	}

	const pageSize = 100
	offset := 0
	out := make([]string, 0, min(max, pageSize))
	for len(out) < max {
		limit := min(pageSize, max-len(out))
		page, err := s.ListPlaylistTrackIDsPage(ctx, playlistID, offset, limit)
		if err != nil {
			return nil, err
		}
		if len(page.TrackIDs) > 0 {
			out = append(out, page.TrackIDs...)
		}
		if !page.HasMore || page.NextOffset <= offset {
			break
		}
		offset = page.NextOffset
	}
	return out, nil
}

func (s *Service) ListPlaylistTrackIDsPage(ctx context.Context, playlistID string, offset, limit int) (*PlaylistTrackPage, error) {
	playlistID = strings.TrimSpace(playlistID)
	if playlistID == "" {
		return nil, errors.New("playlist ID must not be empty")
	}
	if offset < 0 {
		return nil, errors.New("playlist offset must be >= 0")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}

	fetch := func() (*spotifyapi.PlaylistItemPage, error) {
		if s.itemsHTTPClient != nil {
			return s.fetchPlaylistItemsViaItemsEndpoint(ctx, playlistID, offset, limit)
		}
		return s.client.GetPlaylistItems(ctx, spotifyapi.ID(playlistID), spotifyapi.Limit(limit), spotifyapi.Offset(offset))
	}
	page, err := apiCallWithRetry(ctx, fetch)
	if err != nil {
		return nil, fmt.Errorf("fetch playlist tracks: %w", err)
	}

	out := &PlaylistTrackPage{
		Offset: offset,
		Limit:  limit,
	}
	if page == nil || len(page.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}

	out.TrackIDs = make([]string, 0, len(page.Items))
	out.TrackInfos = make([]QueueItem, 0, len(page.Items))
	for _, item := range page.Items {
		if item.Track.Track == nil || item.Track.Track.ID == "" {
			continue
		}
		t := item.Track.Track
		qi := QueueItem{ID: string(t.ID), Name: t.Name, DurationMS: int(t.Duration)}
		if len(t.Artists) > 0 {
			qi.Artist = t.Artists[0].Name
		}
		out.TrackIDs = append(out.TrackIDs, string(t.ID))
		out.TrackInfos = append(out.TrackInfos, qi)
	}
	out.NextOffset = offset + len(page.Items)
	out.HasMore = len(page.Items) >= limit
	return out, nil
}

func (s *Service) fetchPlaylistItemsViaItemsEndpoint(ctx context.Context, playlistID string, offset, limit int) (*spotifyapi.PlaylistItemPage, error) {
	u := spotifyAPIBase + "playlists/" + url.PathEscape(playlistID) + "/items?"
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	u += params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.itemsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{status: resp.StatusCode, err: fmt.Errorf("playlist items: %s", strings.TrimSpace(string(body)))}
	}
	var page spotifyapi.PlaylistItemPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decode playlist items: %w", err)
	}
	return &page, nil
}

func (s *Service) PlayPlaylist(ctx context.Context, target, playlistURI string) error {
	if strings.TrimSpace(playlistURI) == "" {
		return errors.New("playlist URI must not be empty")
	}

	device, err := s.FindDeviceByName(ctx, target)
	if err != nil {
		return err
	}

	uri := spotifyapi.URI(playlistURI)
	return s.client.PlayOpt(ctx, &spotifyapi.PlayOptions{
		DeviceID:        &device.ID,
		PlaybackContext: &uri,
	})
}

func (s *Service) QueueTracks(ctx context.Context, target string, trackIDs []string) ([]string, error) {
	if len(trackIDs) == 0 {
		return nil, nil
	}
	device, err := s.EnsureDeviceActive(ctx, target)
	if err != nil {
		return nil, err
	}
	queued := make([]string, 0, len(trackIDs))
	for _, trackID := range trackIDs {
		trackID = strings.TrimSpace(trackID)
		id := spotifyapi.ID(trackID)
		if id == "" {
			continue
		}
		if err := s.client.QueueSongOpt(ctx, id, &spotifyapi.PlayOptions{DeviceID: &device.ID}); err != nil {
			return queued, fmt.Errorf("queue track %q: %w", trackID, err)
		}
		queued = append(queued, trackID)
	}
	return queued, nil
}

func IsTransientAPIError(err error) bool {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Status == 429 || apiErr.Status >= 500
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status == 429 || statusErr.status >= 500
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
}

// IsRateLimitError reports whether err is a Spotify 429 / rate-limit error.
func IsRateLimitError(err error) bool {
	return isRateLimitError(err)
}

func IsForbidden(err error) bool {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) && apiErr.Status == 403 {
		return true
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.status == 403 {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "forbidden")
}

func HTTPStatusFromError(err error) (status int, ok bool) {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Status, true
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status, true
	}
	return 0, false
}

func isRetryableAPIError(err error) bool {
	if IsTransientAPIError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
}

func waitForAPIRetry(ctx context.Context, err error, attempt int) error {
	wait := retryDelayForAPIError(attempt)
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait {
		return err
	}
	select {
	case <-ctx.Done():
		return err
	case <-time.After(wait):
		return nil
	}
}

func apiCallWithRetry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		value, err := fn()
		if err == nil {
			return value, nil
		}
		if isRetryableAPIError(err) && !isRateLimitError(err) && attempt+1 < apiRetryMaxAttempts {
			if waitErr := waitForAPIRetry(ctx, err, attempt); waitErr != nil {
				return zero, waitErr
			}
			continue
		}
		return zero, err
	}
}

func (s *Service) playerStateWithRetry(ctx context.Context) (*spotifyapi.PlayerState, error) {
	return apiCallWithRetry(ctx, func() (*spotifyapi.PlayerState, error) {
		return s.client.PlayerState(ctx)
	})
}

func (s *Service) withDeviceCommand(ctx context.Context, target string, command func(deviceID spotifyapi.ID) error) error {
	device, err := s.FindDeviceByName(ctx, target)
	if err != nil {
		return err
	}
	if device.Restricted {
		return errors.New("spotify device is restricted")
	}

	if err := command(device.ID); err != nil {
		if device.Active || !shouldRetryDeviceCommandAfterTransfer(err) {
			return err
		}
		if err := s.transferWithRetry(ctx, device.ID); err != nil {
			return fmt.Errorf("transfer playback to %q: %w", target, err)
		}
		if err := s.waitForDeviceActive(ctx, device.ID); err != nil {
			return err
		}
		return command(device.ID)
	}
	return nil
}

func shouldRetryDeviceCommandAfterTransfer(err error) bool {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) {
		if apiErr.Status == 404 {
			return true
		}
		if apiErr.Status == 403 {
			msg := strings.ToLower(apiErr.Message)
			return strings.Contains(msg, "active device") || strings.Contains(msg, "no active device")
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "active device") || strings.Contains(msg, "no active device")
}

func retryDelayForAPIError(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	wait := apiRetryInitialDelay * time.Duration(1<<min(attempt, 5))
	if wait > apiRetryMaxDelay {
		wait = apiRetryMaxDelay
	}
	return wait
}

func isRateLimitError(err error) bool {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) && apiErr.Status == 429 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
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
					if backoff > 10*time.Second {
						backoff = 10 * time.Second
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

func (s *Service) resolveDevice(target string, devices []spotifyapi.PlayerDevice) (*spotifyapi.PlayerDevice, string, error) {
	target = strings.TrimSpace(target)
	targetLower := strings.ToLower(target)

	for _, device := range devices {
		if strings.EqualFold(device.Name, target) {
			d := device
			return &d, "exact", nil
		}
	}
	if s.mode == DeviceModeRelaxed && targetLower != "" {
		for _, device := range devices {
			if strings.Contains(strings.ToLower(device.Name), targetLower) {
				d := device
				return &d, "contains", nil
			}
		}
	}
	if s.mode == DeviceModeRelaxed && s.allowActiveFallback {
		for _, device := range devices {
			if device.Active && !device.Restricted {
				d := device
				return &d, "active-fallback", nil
			}
		}
	}

	names := make([]string, 0, len(devices))
	for _, device := range devices {
		names = append(names, device.Name)
	}
	return nil, "", fmt.Errorf("%w: target=%q discovered=%v", ErrDeviceNotFound, target, names)
}

func (s *Service) waitForDeviceActive(ctx context.Context, deviceID spotifyapi.ID) error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		devices, err := s.refreshPlayerDevices(ctx)
		if err == nil {
			for _, d := range devices {
				if d.ID == deviceID && d.Active {
					return nil
				}
			}
		}
		time.Sleep(deviceActivationPollInterval)
	}
	s.invalidateDeviceCache()
	return errors.New("device transfer timed out while waiting to become active")
}

func (s *Service) transferWithRetry(ctx context.Context, deviceID spotifyapi.ID) error {
	wait := transferInitialRetryDelay
	for i := 0; i < 3; i++ {
		err := s.client.TransferPlayback(ctx, deviceID, false)
		if err == nil {
			s.invalidateDeviceCache()
			return nil
		}
		s.invalidateDeviceCache()
		if !IsTransientAPIError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
			wait *= 2
		}
	}
	return errors.New("transfer playback failed after retries")
}

func (s *Service) getPlayerDevices(ctx context.Context) ([]spotifyapi.PlayerDevice, error) {
	now := time.Now()
	s.deviceCacheMu.RLock()
	if s.deviceCacheSet && now.Before(s.deviceCacheExpiry) {
		devices := append([]spotifyapi.PlayerDevice(nil), s.deviceCacheDevices...)
		s.deviceCacheMu.RUnlock()
		return devices, nil
	}
	s.deviceCacheMu.RUnlock()
	return s.refreshPlayerDevices(ctx)
}

func (s *Service) refreshPlayerDevices(ctx context.Context) ([]spotifyapi.PlayerDevice, error) {
	devices, err := apiCallWithRetry(ctx, func() ([]spotifyapi.PlayerDevice, error) {
		return s.client.PlayerDevices(ctx)
	})
	if err != nil {
		s.invalidateDeviceCache()
		return nil, err
	}
	copied := append([]spotifyapi.PlayerDevice(nil), devices...)
	s.deviceCacheMu.Lock()
	s.deviceCacheDevices = copied
	s.deviceCacheExpiry = time.Now().Add(s.deviceCacheTTL)
	s.deviceCacheSet = true
	s.deviceCacheMu.Unlock()
	return copied, nil
}

func (s *Service) invalidateDeviceCache() {
	s.deviceCacheMu.Lock()
	s.deviceCacheDevices = nil
	s.deviceCacheExpiry = time.Time{}
	s.deviceCacheSet = false
	s.deviceCacheMu.Unlock()
}
