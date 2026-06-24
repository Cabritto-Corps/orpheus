package librespot

import "time"

const (
	spclientTimeout          = 30 * time.Second
	contextResolveTimeout    = 30 * time.Second
	trackTransitionTimeout   = 30 * time.Second
	loadCurrentTrackTimeout  = 30 * time.Second
	prefetchJobTimeout       = 30 * time.Second
	metadataBatchTimeout     = 15 * time.Second
	stateAdapterBatchTimeout = 8 * time.Second
	shuffleContextTimeout    = 5 * time.Second
	contextTracksBgTimeout   = 30 * time.Second
	httpClientTimeout        = 30 * time.Second
)
