package librespot

import (
	"net/http"
	"sync/atomic"

	golibrespot "github.com/elxgy/go-librespot"
	devicespb "github.com/elxgy/go-librespot/proto/spotify/connectstate/devices"
)

type Runtime struct {
	Log             golibrespot.Logger
	Cfg             *Config
	Client          *http.Client
	DeviceId        string
	DeviceType      devicespb.DeviceType
	State           *golibrespot.AppState
	PlaybackStateCh chan<- *PlaybackStateUpdate
	droppedPlayback atomic.Uint64
}

func (r *Runtime) EmitPlaybackState(update *PlaybackStateUpdate) {
	if r.PlaybackStateCh == nil || update == nil {
		return
	}
	select {
	case r.PlaybackStateCh <- update:
	default:
		n := r.droppedPlayback.Add(1)
		if (n == 1 || n%100 == 0) && r.Log != nil {
			r.Log.Debugf("dropped playback state updates=%d", n)
		}
	}
}

func (r *Runtime) DroppedPlaybackStateUpdates() uint64 {
	return r.droppedPlayback.Load()
}
