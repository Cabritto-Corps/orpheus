package librespot

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	golibrespotcache "github.com/elxgy/go-librespot/cache"
	"github.com/elxgy/go-librespot/player"
	"github.com/elxgy/go-librespot/session"

	"orpheus/internal/cache"
	"orpheus/internal/config"
)

func NewAppPlayer(ctx context.Context, runtime *Runtime, sess *session.Session) (*AppPlayer, error) {
	volumeUpdate := make(chan float32, 1)

	p := &AppPlayer{
		runtime:        runtime,
		sess:           sess,
		baseCtx:        ctx,
		stop:           make(chan struct{}, 1),
		runDone:        make(chan struct{}),
		volumeUpdate:   volumeUpdate,
		prefetchJobs:   make(chan prefetchJob, 16),
		prefetchDone:   make(chan prefetchResult, 16),
		queueMetaCache: cache.NewLRU[string, PlaybackStateQueueEntry](8192),
	}
	var audioCache *golibrespotcache.Cache
	if runtime.Cfg.AudioCacheEnabled {
		dir := runtime.Cfg.AudioCacheDir
		if dir == "" {
			if base, err := os.UserCacheDir(); err == nil {
				dir = filepath.Join(base, "orpheus", "audio-cache")
			} else if fallback, err := config.DefaultConfigDir(); err == nil {
				dir = filepath.Join(fallback, "cache", "audio")
			}
		}
		ac, err := golibrespotcache.New(runtime.Log, dir, runtime.Cfg.AudioCacheSizeMB*1024*1024)
		if err != nil {
			runtime.Log.WithError(err).Warn("audio cache disabled")
		} else {
			audioCache = ac
		}
	}

	p.prefetchTimer = time.NewTimer(math.MaxInt64)
	p.prefetchTimer.Stop()
	p.shuffleRefreshTimer = time.NewTimer(math.MaxInt64)
	p.shuffleRefreshTimer.Stop()
	p.connectStateTimer = time.NewTimer(math.MaxInt64)
	p.connectStateTimer.Stop()
	p.queueTopUpTimer = time.NewTimer(math.MaxInt64)
	p.queueTopUpTimer.Stop()
	p.transitionCache = newTransitionCache()

	p.initState()

	pl, err := player.NewPlayer(&player.Options{
		Spclient:                  sess.Spclient(),
		AudioKey:                  sess.AudioKey(),
		Events:                    sess.Events(),
		Log:                       runtime.Log,
		FlacEnabled:               runtime.Cfg.FlacEnabled,
		Cache:                     audioCache,
		CrossfadeSamples:          int(runtime.Cfg.CrossfadeSeconds * float64(player.SampleRate*player.Channels)),
		NormalisationEnabled:      true,
		NormalisationUseAlbumGain: false,
		NormalisationPregain:      0,
		CountryCode:               new(string),
		AudioBackend:              runtime.Cfg.AudioBackend,
		AudioDevice:               runtime.Cfg.AudioDevice,
		MixerDevice:               runtime.Cfg.MixerDevice,
		MixerControlName:          runtime.Cfg.MixerControlName,
		AudioBufferTime:           runtime.Cfg.AudioBufferTime,
		AudioPeriodCount:          runtime.Cfg.AudioPeriodCount,
		ExternalVolume:            runtime.Cfg.ExternalVolume,
		VolumeUpdate:              volumeUpdate,
	})
	if err != nil {
		return nil, fmt.Errorf("new player: %w", err)
	}
	p.player = pl
	return p, nil
}
