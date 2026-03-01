package librespot

import (
	"net/http"

	devicespb "github.com/devgianlu/go-librespot/proto/spotify/connectstate/devices"
	golibrespot "github.com/devgianlu/go-librespot"
)

type Runtime struct {
	Log              golibrespot.Logger
	Cfg              *Config
	Client           *http.Client
	DeviceId         string
	DeviceType       devicespb.DeviceType
	State            *golibrespot.AppState
	StateCh          chan<- *ApiEvent
	PlaybackStateCh  chan<- *PlaybackStateUpdate
}

func (r *Runtime) Emit(ev *ApiEvent) {
	if r.StateCh == nil {
		return
	}
	select {
	case r.StateCh <- ev:
	default:
	}
}

func (r *Runtime) EmitPlaybackState(update *PlaybackStateUpdate) {
	if r.PlaybackStateCh == nil || update == nil {
		return
	}
	select {
	case r.PlaybackStateCh <- update:
	default:
	}
}
