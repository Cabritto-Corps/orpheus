package librespot

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"
)

func NewAppPlayer(ctx context.Context, runtime *Runtime, sess *session.Session) (*AppPlayer, error) {
	countryCode := new(string)
	volumeUpdate := make(chan float32, 1)

	p := &AppPlayer{
		runtime:        runtime,
		sess:           sess,
		stop:           make(chan struct{}, 1),
		logout:         make(chan *AppPlayer, 1),
		countryCode:    countryCode,
		volumeUpdate:   volumeUpdate,
		queueMetaCache: make(map[string]PlaybackStateQueueEntry),
		playedTrackURIs: make(map[string]struct{}),
	}
	p.prefetchTimer = time.NewTimer(math.MaxInt64)
	p.prefetchTimer.Stop()

	p.initState()

	pl, err := player.NewPlayer(&player.Options{
		Spclient:     sess.Spclient(),
		AudioKey:     sess.AudioKey(),
		Events:       sess.Events(),
		Log:          runtime.Log,
		FlacEnabled:  runtime.Cfg.FlacEnabled,
		NormalisationEnabled:      true,
		NormalisationUseAlbumGain: false,
		NormalisationPregain:      0,
		CountryCode:  countryCode,
		AudioBackend: runtime.Cfg.AudioBackend,
		AudioDevice:  runtime.Cfg.AudioDevice,
		MixerDevice:  runtime.Cfg.MixerDevice,
		MixerControlName: runtime.Cfg.MixerControlName,
		AudioBufferTime:  runtime.Cfg.AudioBufferTime,
		AudioPeriodCount: runtime.Cfg.AudioPeriodCount,
		ExternalVolume:   runtime.Cfg.ExternalVolume,
		VolumeUpdate:     volumeUpdate,
	})
	if err != nil {
		return nil, fmt.Errorf("new player: %w", err)
	}
	p.player = pl
	return p, nil
}
