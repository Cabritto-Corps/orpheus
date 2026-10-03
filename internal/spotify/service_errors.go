package spotify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	spotifyapi "github.com/zmb3/spotify/v2"
)

func IsTransientAPIError(err error) bool {
	if _, ok := errors.AsType[*RateLimitError](err); ok {
		return false
	}
	if apiErr, ok := errors.AsType[spotifyapi.Error](err); ok {
		return apiErr.Status == 429 || apiErr.Status >= 500
	}
	if statusErr, ok := errors.AsType[*httpStatusError](err); ok {
		return statusErr.status == 429 || statusErr.status >= 500
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
}

func IsRateLimitError(err error) bool {
	return isRateLimitError(err)
}

// Longer waits are server penalties; sleeping them out masks the 429 as a downstream timeout.
const rateLimitMaxWait = 30 * time.Second

// Surfaces a 429 wait too long to absorb; short waits keep the coalescing retry.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("spotify rate limited, retry after %s", formatRetryAfter(e.RetryAfter))
}

func failFastRateLimit(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	if wait > rateLimitMaxWait {
		return &RateLimitError{RetryAfter: wait}
	}
	if dl, ok := ctx.Deadline(); ok && wait > time.Until(dl) {
		return &RateLimitError{RetryAfter: wait}
	}
	return nil
}

func formatRetryAfter(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	default:
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
}

func rateLimitNextStep(d time.Duration) string {
	if d >= time.Minute {
		return "rate limited — retry in about " + formatRetryAfter(d)
	}
	return "wait a few seconds and retry"
}

func RateLimitRetryAfter(err error) (time.Duration, bool) {
	if rateLimitErr, ok := errors.AsType[*RateLimitError](err); ok {
		return rateLimitErr.RetryAfter, true
	}
	return 0, false
}

func RateLimitHint(err error) (string, bool) {
	wait, ok := RateLimitRetryAfter(err)
	if !ok {
		return "", false
	}
	if wait >= time.Minute {
		return "rate limited — retry in about " + formatRetryAfter(wait), true
	}
	return "Run 'orpheus auth login' to use your own API quota.", true
}

func IsForbidden(err error) bool {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) && apiErr.Status == 403 {
		return true
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.status == 403 {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "forbidden")
}

func HTTPStatusFromError(err error) (status int, ok bool) {
	if apiErr, ok := errors.AsType[spotifyapi.Error](err); ok {
		return apiErr.Status, true
	}
	if statusErr, ok := errors.AsType[*httpStatusError](err); ok {
		return statusErr.status, true
	}
	return 0, false
}

func isRetryableAPIError(err error) bool {
	if IsTransientAPIError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
}

func waitForAPIRetry(ctx context.Context, err error, attempt int) error {
	wait := retryDelayForAPIError(attempt)
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait {
		return err
	}
	if sleepErr := sleepWithContext(ctx, wait); sleepErr != nil {
		return err
	}
	return nil
}

func apiCallWithRetry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		value, err := fn()
		if err == nil {
			return value, nil
		}
		if isRetryableAPIError(err) && !isRateLimitError(err) && attempt+1 < apiRetryMaxAttempts {
			if waitErr := waitForAPIRetry(ctx, err, attempt); waitErr != nil {
				return zero, waitErr
			}
			continue
		}
		return zero, err
	}
}

func retryDelayForAPIError(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	wait := min(apiRetryInitialDelay*time.Duration(1<<min(attempt, apiRetryExponentCap)), apiRetryMaxDelay)
	return wait
}

func isRateLimitError(err error) bool {
	if _, ok := errors.AsType[*RateLimitError](err); ok {
		return true
	}
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) && apiErr.Status == 429 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
}
