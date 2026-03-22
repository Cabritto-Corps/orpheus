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
	StateCh         chan<- *ApiEvent
	PlaybackStateCh chan<- *PlaybackStateUpdate
	droppedState    atomic.Uint64
	droppedPlayback atomic.Uint64
}

func (r *Runtime) Emit(ev *ApiEvent) {
	if r.StateCh == nil {
		return
	}
	select {
	case r.StateCh <- ev:
	default:
		n := r.droppedState.Add(1)
		if (n == 1 || n%100 == 0) && r.Log != nil {
			r.Log.Debugf("dropped state events=%d", n)
		}
	}
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

func (r *Runtime) DroppedStateEvents() uint64 {
	return r.droppedState.Load()
}

func (r *Runtime) DroppedPlaybackStateUpdates() uint64 {
	return r.droppedPlayback.Load()
}
