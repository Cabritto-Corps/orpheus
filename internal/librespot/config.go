package librespot

import (
	"net/http"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
)

type Config struct {
	ConfigDir        string
	DeviceId         string
	DeviceName       string
	DeviceType       string
	ClientToken      string
	AudioBackend     string
	AudioDevice      string
	MixerDevice      string
	MixerControlName string
	Bitrate          int
	VolumeSteps      uint32
	InitialVolume    uint32
	IgnoreLastVolume bool
	ExternalVolume   bool
	DisableAutoplay  bool
	FlacEnabled      bool
	ImageSize        string
	ZeroconfEnabled  bool
	AudioBufferTime  int
	AudioPeriodCount int
}

func DefaultConfig() *Config {
	return &Config{
		DeviceName:       "orpheus",
		DeviceType:       "computer",
		AudioBackend:     "pulseaudio",
		AudioDevice:      "default",
		Bitrate:          160,
		VolumeSteps:      100,
		InitialVolume:    100,
		FlacEnabled:      false,
		ImageSize:        "large",
		ZeroconfEnabled:  false,
		AudioBufferTime:  0,
		AudioPeriodCount: 0,
	}
}

func NewRuntime(cfg *Config, appState *golibrespot.AppState, log golibrespot.Logger, stateCh chan<- *ApiEvent, playbackStateCh chan<- *PlaybackStateUpdate) (*Runtime, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	deviceType, err := parseDeviceType(cfg.DeviceType)
	if err != nil {
		return nil, err
	}
	deviceID := cfg.DeviceId
	if deviceID == "" && appState != nil {
		deviceID = appState.DeviceId
	}
	return &Runtime{
		Log:             log,
		Cfg:             cfg,
		Client:          &http.Client{Timeout: 30 * time.Second},
		DeviceId:        deviceID,
		DeviceType:      deviceType,
		State:           appState,
		StateCh:         stateCh,
		PlaybackStateCh: playbackStateCh,
	}, nil
}
