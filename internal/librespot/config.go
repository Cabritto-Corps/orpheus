package librespot

import (
	"net/http"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/sessionconfig"
)

type Config struct {
	ConfigDir         string
	DeviceId          string
	DeviceName        string
	DeviceType        string
	ClientToken       string
	AudioBackend      string
	AudioDevice       string
	MixerDevice       string
	MixerControlName  string
	Bitrate           int
	VolumeSteps       uint32
	InitialVolume     uint32
	IgnoreLastVolume  bool
	ExternalVolume    bool
	DisableAutoplay   bool
	FlacEnabled       bool
	AudioCacheEnabled bool
	AudioCacheSizeMB  int64
	AudioCacheDir     string
	CrossfadeSeconds  float64
	ImageSize         string
	ZeroconfEnabled   bool
	AudioBufferTime   int
	AudioPeriodCount  int
}

func DefaultConfig() *Config {
	return &Config{
		DeviceName:        "orpheus",
		DeviceType:        "computer",
		AudioBackend:      "pulseaudio",
		AudioDevice:       "default",
		Bitrate:           160,
		VolumeSteps:       100,
		InitialVolume:     100,
		FlacEnabled:       false,
		AudioCacheEnabled: false,
		AudioCacheSizeMB:  1024,
		AudioCacheDir:     "",
		CrossfadeSeconds:  0,
		ImageSize:         "large",
		ZeroconfEnabled:   false,
		AudioBufferTime:   0,
		AudioPeriodCount:  0,
	}
}

func NewRuntime(cfg *Config, appState *golibrespot.AppState, log golibrespot.Logger, playbackStateCh chan<- *PlaybackStateUpdate) (*Runtime, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	deviceType, err := sessionconfig.ParseDeviceType(cfg.DeviceType)
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
		Client:          &http.Client{Timeout: httpClientTimeout},
		DeviceId:        deviceID,
		DeviceType:      deviceType,
		State:           appState,
		PlaybackStateCh: playbackStateCh,
	}, nil
}
