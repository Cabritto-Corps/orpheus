package spotify

import (
	"context"
	"errors"
	"testing"
	"time"

	spotifyapi "github.com/zmb3/spotify/v2"
)

func TestIsForbidden(t *testing.T) {
	if !IsForbidden(spotifyapi.Error{Status: 403, Message: "forbidden"}) {
		t.Fatal("expected spotify 403 to be forbidden")
	}
	if !IsForbidden(&httpStatusError{status: 403, err: spotifyapi.Error{Status: 403, Message: "forbidden"}}) {
		t.Fatal("expected wrapped 403 to be forbidden")
	}
}

func TestRetryDelayForAPIErrorCapsAtMax(t *testing.T) {
	wait := retryDelayForAPIError(99)
	if wait != apiRetryMaxDelay {
		t.Fatalf("expected retry delay to cap at %s, got %s", apiRetryMaxDelay, wait)
	}
	if retryDelayForAPIError(0) <= 0*time.Millisecond {
		t.Fatal("expected positive retry delay")
	}
}

func TestAPICallWithRetryRetriesTransientThenSucceeds(t *testing.T) {
	attempts := 0
	start := time.Now()
	got, err := apiCallWithRetry(context.Background(), func() (int, error) {
		attempts++
		if attempts == 1 {
			return 0, spotifyapi.Error{Status: 500, Message: "server error"}
		}
		return 42, nil
	})
	if err != nil {
		t.Fatalf("expected retry to eventually succeed, got error: %v", err)
	}
	if got != 42 {
		t.Fatalf("expected value from successful retry, got %d", got)
	}
	if attempts != 2 {
		t.Fatalf("expected exactly one retry, got %d attempts", attempts)
	}
	if elapsed := time.Since(start); elapsed < apiRetryInitialDelay {
		t.Fatalf("expected backoff wait before retry, got elapsed=%s", elapsed)
	}
}

func TestAPICallWithRetryDoesNotRetryRateLimit(t *testing.T) {
	attempts := 0
	_, err := apiCallWithRetry(context.Background(), func() (int, error) {
		attempts++
		return 0, spotifyapi.Error{Status: 429, Message: "too many requests"}
	})
	if err == nil {
		t.Fatal("expected rate-limit error to be returned")
	}
	if attempts != 1 {
		t.Fatalf("expected rate-limit error to skip retries, got %d attempts", attempts)
	}
}

func TestWaitForAPIRetryReturnsOriginalErrorWhenDeadlineTooSoon(t *testing.T) {
	orig := errors.New("boom")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := waitForAPIRetry(ctx, orig, 0)
	if !errors.Is(got, orig) {
		t.Fatalf("expected original error when retry wait exceeds deadline, got %v", got)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("expected fast return when retry cannot fit deadline, got %s", elapsed)
	}
}
