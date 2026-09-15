package librespot

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestScheduleQueueTopUpDeduplicates(t *testing.T) {
	p := newTestAppPlayer()
	p.queueTopUpTimer = time.NewTimer(math.MaxInt64)

	p.scheduleQueueTopUp()
	if !p.queueTopUpInFlight {
		t.Fatal("expected in-flight flag after scheduling")
	}

	p.scheduleQueueTopUp()
	if !p.queueTopUpInFlight {
		t.Fatal("expected duplicate schedule to be a no-op")
	}
}

func TestTopUpQueueClearsInFlightWithoutTracks(t *testing.T) {
	p := newTestAppPlayer()
	p.queueTopUpTimer = time.NewTimer(math.MaxInt64)
	p.scheduleQueueTopUp()

	p.topUpQueue(context.Background())
	if p.queueTopUpInFlight {
		t.Fatal("expected in-flight flag to clear after the tick runs")
	}

	p.scheduleQueueTopUp()
	if !p.queueTopUpInFlight {
		t.Fatal("expected re-scheduling to work after completion")
	}
}
