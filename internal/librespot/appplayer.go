package librespot

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/dealer"
	"github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	"github.com/devgianlu/go-librespot/session"
	"github.com/devgianlu/go-librespot/tracks"
	"google.golang.org/protobuf/proto"

	"orpheus/internal/cache"
)

const volumeUpdateDebounce = 100 * time.Millisecond

type AppPlayer struct {
	runtime *Runtime
	sess    *session.Session

	stop   chan struct{}
	logout chan *AppPlayer

	player            *player.Player
	initialVolumeOnce sync.Once
	volumeUpdate      chan float32

	spotConnId string

	prodInfo    *ProductInfo
	countryCode *string

	state           *State
	primaryStream   *player.Stream
	secondaryStream *player.Stream

	prefetchTimer       *time.Timer
	shuffleRefreshTimer *time.Timer
	prefetchJobs        chan prefetchJob
	prefetchDone        chan prefetchResult

	transitionStreamMu    sync.Mutex
	transitionStreamCache map[string]*player.Stream
	transitionStreamOrder []string
	prefetchPending       map[string]struct{}
	prefetchGen           atomic.Uint64
	shuffleRefreshPending bool
	shuffleRefreshGen     uint64

	queueMetaCache *cache.LRU[string, PlaybackStateQueueEntry]
	queueMetaMu    sync.RWMutex

	playedTrackURIs map[string]struct{}
	playedTrackMu   sync.RWMutex

	queueResolveMu       sync.Mutex
	queueResolveInFlight bool
	namePreloadContext   string
	namePreloadToken     uint64
	namePreloadDone      bool

	trackStateVersion atomic.Uint64

	derivedQueueMu      sync.RWMutex
	derivedQueueKey     string
	derivedQueueEntries []PlaybackStateQueueEntry
	derivedQueueHasMore bool
	derivedQueueValid   bool
}

type prefetchJob struct {
	gen     uint64
	nextURI string
	target  golibrespot.SpotifyId
}

type prefetchResult struct {
	gen     uint64
	nextURI string
	target  golibrespot.SpotifyId
	stream  *player.Stream
	err     error
}

func (p *AppPlayer) newApiResponseStatusTrack(media *golibrespot.Media, position int64) *ApiResponseStatusTrack {
	imageSize := p.runtime.Cfg.ImageSize
	if imageSize == "" {
		imageSize = "default"
	}
	if media.IsTrack() {
		track := media.Track()
		var artists []string
		for _, a := range track.Artist {
			artists = append(artists, *a.Name)
		}
		albumCoverId := getBestImageIdForSize(track.Album.Cover, imageSize)
		if albumCoverId == nil && track.Album.CoverGroup != nil {
			albumCoverId = getBestImageIdForSize(track.Album.CoverGroup.Image, imageSize)
		}
		return &ApiResponseStatusTrack{
			Uri:           golibrespot.SpotifyIdFromGid(golibrespot.SpotifyIdTypeTrack, track.Gid).Uri(),
			Name:          *track.Name,
			ArtistNames:   artists,
			AlbumName:     *track.Album.Name,
			AlbumCoverUrl: p.prodInfo.ImageUrl(albumCoverId),
			Position:      position,
			Duration:      int(*track.Duration),
			ReleaseDate:   track.Album.Date.String(),
			TrackNumber:   int(*track.Number),
			DiscNumber:    int(*track.DiscNumber),
		}
	}
	episode := media.Episode()
	var episodeImages []*metadatapb.Image
	if episode.CoverImage != nil {
		episodeImages = episode.CoverImage.Image
	}
	albumCoverId := getBestImageIdForSize(episodeImages, imageSize)
	return &ApiResponseStatusTrack{
		Uri:           golibrespot.SpotifyIdFromGid(golibrespot.SpotifyIdTypeEpisode, episode.Gid).Uri(),
		Name:          *episode.Name,
		ArtistNames:   []string{*episode.Show.Name},
		AlbumName:     *episode.Show.Name,
		AlbumCoverUrl: p.prodInfo.ImageUrl(albumCoverId),
		Position:      position,
		Duration:      int(*episode.Duration),
		ReleaseDate:   "",
		TrackNumber:   0,
		DiscNumber:    0,
	}
}

func (p *AppPlayer) handleAccesspointPacket(pktType ap.PacketType, payload []byte) error {
	switch pktType {
	case ap.PacketTypeProductInfo:
		var prod ProductInfo
		if err := xml.Unmarshal(payload, &prod); err != nil {
			return fmt.Errorf("failed unmarshalling ProductInfo: %w", err)
		}
		if len(prod.Products) != 1 {
			return fmt.Errorf("invalid ProductInfo")
		}
		p.prodInfo = &prod
		return nil
	case ap.PacketTypeCountryCode:
		*p.countryCode = string(payload)
		return nil
	default:
		return nil
	}
}

func (p *AppPlayer) handleDealerMessage(ctx context.Context, msg dealer.Message) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if strings.HasPrefix(msg.Uri, "hm://pusher/v1/connections/") {
		p.spotConnId = msg.Headers["Spotify-Connection-Id"]
		if len(p.spotConnId) >= 16 {
			p.runtime.Log.Debugf("received connection id: %s...%s", p.spotConnId[:16], p.spotConnId[len(p.spotConnId)-16:])
		} else {
			p.runtime.Log.Debugf("received connection id: %s", p.spotConnId)
		}
		if err := p.putConnectState(ctx, connectpb.PutStateReason_NEW_DEVICE); err != nil {
			return fmt.Errorf("failed initial state put: %w", err)
		}
		if !p.runtime.Cfg.ExternalVolume && len(p.runtime.Cfg.MixerDevice) == 0 {
			p.initialVolumeOnce.Do(func() {
				if lastVolume := p.runtime.State.LastVolume; !p.runtime.Cfg.IgnoreLastVolume && lastVolume != nil {
					p.updateVolume(*lastVolume)
				} else {
					p.updateVolume(p.runtime.Cfg.InitialVolume * player.MaxStateVolume / p.runtime.Cfg.VolumeSteps)
				}
			})
		}
	} else if strings.HasPrefix(msg.Uri, "hm://connect-state/v1/connect/volume") {
		var setVolCmd connectpb.SetVolumeCommand
		if err := proto.Unmarshal(msg.Payload, &setVolCmd); err != nil {
			return fmt.Errorf("failed unmarshalling SetVolumeCommand: %w", err)
		}
		p.updateVolume(uint32(setVolCmd.Volume))
	} else if strings.HasPrefix(msg.Uri, "hm://connect-state/v1/connect/logout") {
		p.runtime.Log.WithField("username", golibrespot.ObfuscateUsername(p.sess.Username())).Debugf("requested logout")
		p.logout <- p
	} else if strings.HasPrefix(msg.Uri, "hm://connect-state/v1/cluster") {
		var clusterUpdate connectpb.ClusterUpdate
		if err := proto.Unmarshal(msg.Payload, &clusterUpdate); err != nil {
			return fmt.Errorf("failed unmarshalling ClusterUpdate: %w", err)
		}
		stopBeingActive := p.state.active && clusterUpdate.Cluster.ActiveDeviceId != p.runtime.DeviceId && clusterUpdate.Cluster.PlayerState.Timestamp > p.state.lastTransferTimestamp
		if !stopBeingActive {
			return nil
		}
		name := " "
		if device := clusterUpdate.Cluster.Device[clusterUpdate.Cluster.ActiveDeviceId]; device != nil {
			name = device.Name
		}
		p.runtime.Log.Infof("playback was transferred to %s", name)
		return p.stopPlayback(ctx)
	}
	return nil
}

func (p *AppPlayer) handlePlayerCommand(ctx context.Context, req dealer.RequestPayload) error {
	p.state.lastCommand = &req
	p.runtime.Log.Debugf("handling %s player command from %s", req.Command.Endpoint, req.SentByDeviceId)
	switch req.Command.Endpoint {
	case "transfer":
		if len(req.Command.Data) == 0 {
			p.runtime.Emit(&ApiEvent{Type: ApiEventTypeActive})
			return nil
		}
		var transferState connectpb.TransferState
		if err := proto.Unmarshal(req.Command.Data, &transferState); err != nil {
			return fmt.Errorf("failed unmarshalling TransferState: %w", err)
		}
		p.state.lastTransferTimestamp = transferState.Playback.Timestamp
		ctxTracks, err := tracks.NewTrackListFromContext(ctx, p.runtime.Log, p.sess.Spclient(), transferState.CurrentSession.Context)
		if err != nil {
			return fmt.Errorf("failed creating track list: %w", err)
		}
		if sessId := transferState.CurrentSession.OriginalSessionId; sessId != nil {
			p.state.player.SessionId = *sessId
		} else {
			sessionId := make([]byte, 16)
			_, _ = rand.Read(sessionId)
			p.state.player.SessionId = base64.StdEncoding.EncodeToString(sessionId)
		}
		p.state.setActive(true)
		p.state.player.IsPlaying = false
		p.state.player.IsBuffering = false
		p.state.player.Options = transferState.Options
		pause := transferState.Playback.IsPaused && req.Command.Options.RestorePaused != "resume"
		p.state.player.Timestamp = transferState.Playback.Timestamp
		p.state.player.PositionAsOfTimestamp = int64(transferState.Playback.PositionAsOfTimestamp)
		p.state.setPaused(pause)
		p.state.player.PlayOrigin = transferState.CurrentSession.PlayOrigin
		p.state.player.PlayOrigin.DeviceIdentifier = req.SentByDeviceId
		p.state.player.ContextUri = transferState.CurrentSession.Context.Uri
		p.state.player.ContextUrl = transferState.CurrentSession.Context.Url
		p.state.player.ContextRestrictions = transferState.CurrentSession.Context.Restrictions
		p.state.player.Suppressions = transferState.CurrentSession.Suppressions
		p.state.player.ContextMetadata = map[string]string{}
		for k, v := range transferState.CurrentSession.Context.Metadata {
			p.state.player.ContextMetadata[k] = v
		}
		for k, v := range ctxTracks.Metadata() {
			p.state.player.ContextMetadata[k] = v
		}
		contextSpotType := golibrespot.InferSpotifyIdTypeFromContextUri(p.state.player.ContextUri)
		currentTrack := golibrespot.ContextTrackToProvidedTrack(contextSpotType, transferState.Playback.CurrentTrack)
		if err := ctxTracks.TrySeek(ctx, tracks.ProvidedTrackComparator(contextSpotType, currentTrack)); err != nil {
			return fmt.Errorf("failed seeking to track: %w", err)
		}
		if err := ctxTracks.ToggleShuffle(ctx, transferState.Options.ShufflingContext); err != nil {
			return fmt.Errorf("failed shuffling context")
		}
		p.state.queueID = 0
		for _, track := range transferState.Queue.Tracks {
			if track.Uid == "" || track.Uid[0] != 'q' {
				continue
			}
			n, err := strconv.ParseUint(track.Uid[1:], 10, 64)
			if err != nil {
				continue
			}
			p.state.queueID = max(p.state.queueID, n)
		}
		for _, track := range transferState.Queue.Tracks {
			ctxTracks.AddToQueue(track)
		}
		ctxTracks.SetPlayingQueue(transferState.Queue.IsPlayingQueue)
		p.state.tracks = ctxTracks
		p.state.player.Track = ctxTracks.CurrentTrack()
		p.state.player.PrevTracks = ctxTracks.PrevTracks()
		p.state.player.NextTracks = ctxTracks.NextTracks(ctx, nil)
		p.state.player.Index = ctxTracks.Index()
		if err := p.loadCurrentTrack(ctx, pause, true); err != nil {
			return fmt.Errorf("failed loading current track (transfer): %w", err)
		}
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypeActive})
		return nil
	case "play":
		p.state.setActive(true)
		p.state.player.PlayOrigin = req.Command.PlayOrigin
		p.state.player.PlayOrigin.DeviceIdentifier = req.SentByDeviceId
		p.state.player.Suppressions = req.Command.Options.Suppressions
		if req.Command.Options.PlayerOptionsOverride != nil {
			p.state.player.Options.ShufflingContext = req.Command.Options.PlayerOptionsOverride.ShufflingContext
			p.state.player.Options.RepeatingTrack = req.Command.Options.PlayerOptionsOverride.RepeatingTrack
			p.state.player.Options.RepeatingContext = req.Command.Options.PlayerOptionsOverride.RepeatingContext
		}
		var skipTo skipToFunc
		if len(req.Command.Options.SkipTo.TrackUri) > 0 || len(req.Command.Options.SkipTo.TrackUid) > 0 || req.Command.Options.SkipTo.TrackIndex > 0 {
			index := -1
			skipTo = func(track *connectpb.ContextTrack) bool {
				if len(req.Command.Options.SkipTo.TrackUid) > 0 && req.Command.Options.SkipTo.TrackUid == track.Uid {
					return true
				}
				if len(req.Command.Options.SkipTo.TrackUri) > 0 && req.Command.Options.SkipTo.TrackUri == track.Uri {
					return true
				}
				if req.Command.Options.SkipTo.TrackIndex != 0 && len(req.Command.Options.SkipTo.TrackUri) == 0 && len(req.Command.Options.SkipTo.TrackUid) == 0 {
					index += 1
					return index == req.Command.Options.SkipTo.TrackIndex
				}
				return false
			}
		}
		return p.loadContext(ctx, req.Command.Context, skipTo, req.Command.Options.InitiallyPaused, true)
	case "pause":
		return p.pause(ctx)
	case "resume":
		return p.play(ctx)
	case "seek_to":
		var position int64
		if req.Command.Relative == "current" {
			position = p.currentPositionMs() + req.Command.Position
		} else if req.Command.Relative == "beginning" {
			position = req.Command.Position
		} else if req.Command.Relative == "" {
			if pos, ok := req.Command.Value.(float64); ok {
				position = int64(pos)
			} else {
				p.runtime.Log.Warnf("unsupported seek_to position type: %T", req.Command.Value)
				return nil
			}
		} else {
			p.runtime.Log.Warnf("unsupported seek_to relative position: %s", req.Command.Relative)
			return nil
		}
		if err := p.seek(ctx, position); err != nil {
			return fmt.Errorf("failed seeking stream: %w", err)
		}
		return nil
	case "skip_prev":
		return p.skipPrev(ctx, req.Command.Options.AllowSeeking)
	case "skip_next":
		return p.skipNext(ctx, req.Command.Track)
	case "update_context":
		if req.Command.Context.Uri != p.state.player.ContextUri {
			p.runtime.Log.Warnf("ignoring context update for wrong uri: %s", req.Command.Context.Uri)
			return nil
		}
		p.state.player.ContextRestrictions = req.Command.Context.Restrictions
		if p.state.player.ContextMetadata == nil {
			p.state.player.ContextMetadata = map[string]string{}
		}
		for k, v := range req.Command.Context.Metadata {
			p.state.player.ContextMetadata[k] = v
		}
		p.updateState(ctx)
		return nil
	case "set_repeating_context":
		val := req.Command.Value.(bool)
		return p.setOptions(ctx, &val, nil, nil)
	case "set_repeating_track":
		val := req.Command.Value.(bool)
		return p.setOptions(ctx, nil, &val, nil)
	case "set_shuffling_context":
		val := req.Command.Value.(bool)
		return p.setOptions(ctx, nil, nil, &val)
	case "set_options":
		return p.setOptions(ctx, req.Command.RepeatingContext, req.Command.RepeatingTrack, req.Command.ShufflingContext)
	case "set_queue":
		p.setQueue(ctx, req.Command.PrevTracks, req.Command.NextTracks)
		return nil
	case "add_to_queue":
		p.addToQueue(ctx, req.Command.Track)
		return nil
	default:
		return fmt.Errorf("unsupported player command: %s", req.Command.Endpoint)
	}
}

func (p *AppPlayer) handleDealerRequest(ctx context.Context, req dealer.Request) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch req.MessageIdent {
	case "hm://connect-state/v1/player/command":
		return p.handlePlayerCommand(ctx, req.Payload)
	default:
		p.runtime.Log.Warnf("unknown dealer request: %s", req.MessageIdent)
		return nil
	}
}

func (p *AppPlayer) handleTUICommand(ctx context.Context, cmd TUICommand) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if handled, err := p.handleTUIContextCommand(ctx, cmd); handled || err != nil {
		return err
	}
	if handled, err := p.handleTUIPlaybackCommand(ctx, cmd); handled || err != nil {
		return err
	}
	return fmt.Errorf("unknown TUI command: %d", cmd.Kind)
}

func (p *AppPlayer) emitPlaybackState() {
	u := p.BuildPlaybackStateUpdate()
	if u != nil {
		hasUnknown := false
		for _, e := range u.Queue {
			if e.Name == "Unknown track" {
				hasUnknown = true
				break
			}
		}
		if hasUnknown {
			contextKey := ""
			if p.state != nil && p.state.player != nil {
				contextKey = p.state.player.ContextUri
			}
			if !p.isContextNamePreloadDone(contextKey) && p.state != nil {
				p.preloadContextQueueMetadata(p.state.tracks, contextKey)
			}
		}
		p.runtime.EmitPlaybackState(u)
	}
}

func (p *AppPlayer) Close() {
	select {
	case p.stop <- struct{}{}:
	default:
	}
	p.player.Close()
}

func (p *AppPlayer) Run(ctx context.Context, tuiCmdCh <-chan TUICommand) {
	go p.runPrefetchWorker(ctx)
	err := p.sess.Dealer().Connect(ctx)
	if err != nil {
		p.runtime.Log.WithError(err).Error("failed connecting to dealer")
		p.Close()
		return
	}
	apRecv := p.sess.Accesspoint().Receive(ap.PacketTypeProductInfo, ap.PacketTypeCountryCode)
	msgRecv := p.sess.Dealer().ReceiveMessage("hm://pusher/v1/connections/", "hm://connect-state/v1/")
	reqRecv := p.sess.Dealer().ReceiveRequest("hm://connect-state/v1/player/command")
	playerRecv := p.player.Receive()
	volumeTimer := time.NewTimer(time.Minute)
	volumeTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case pkt, ok := <-apRecv:
			if !ok {
				continue
			}
			if err := p.handleAccesspointPacket(pkt.Type, pkt.Payload); err != nil {
				p.runtime.Log.WithError(err).Warn("failed handling accesspoint packet")
			}
		case msg, ok := <-msgRecv:
			if !ok {
				continue
			}
			if err := p.handleDealerMessage(ctx, msg); err != nil {
				p.runtime.Log.WithError(err).Warn("failed handling dealer message")
			}
		case req, ok := <-reqRecv:
			if !ok {
				continue
			}
			if err := p.handleDealerRequest(ctx, req); err != nil {
				p.runtime.Log.WithError(err).Warn("failed handling dealer request")
				req.Reply(false)
			} else {
				p.runtime.Log.Debugf("sending successful reply for dealer request")
				req.Reply(true)
			}
		case cmd, ok := <-tuiCmdCh:
			if !ok {
				continue
			}
			if err := p.handleTUICommand(ctx, cmd); err != nil {
				p.runtime.Log.WithError(err).Warn("failed handling TUI command")
			}
		case ev, ok := <-playerRecv:
			if !ok {
				continue
			}
			p.handlePlayerEvent(ctx, &ev)
		case <-p.prefetchTimer.C:
			p.prefetchNext(ctx)
		case <-p.shuffleRefreshTimer.C:
			p.handleShuffleCacheRefresh(ctx)
		case res := <-p.prefetchDone:
			p.handlePrefetchResult(res)
		case volume := <-p.volumeUpdate:
			p.state.device.Volume = uint32(math.Round(float64(volume * player.MaxStateVolume)))
			volumeTimer.Reset(volumeUpdateDebounce)
		case <-volumeTimer.C:
			p.volumeUpdated(ctx)
		}
	}
}
