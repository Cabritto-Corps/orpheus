package spotify

import (
	"context"
	"errors"
	"strings"
	"time"

	spotifyapi "github.com/zmb3/spotify/v2"
)

func IsTransientAPIError(err error) bool {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Status == 429 || apiErr.Status >= 500
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status == 429 || statusErr.status >= 500
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
}

// IsRateLimitError reports whether err is a Spotify 429 / rate-limit error.
func IsRateLimitError(err error) bool {
	return isRateLimitError(err)
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
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Status, true
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
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
	select {
	case <-ctx.Done():
		return err
	case <-time.After(wait):
		return nil
	}
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
	wait := apiRetryInitialDelay * time.Duration(1<<min(attempt, apiRetryExponentCap))
	if wait > apiRetryMaxDelay {
		wait = apiRetryMaxDelay
	}
	return wait
}

func isRateLimitError(err error) bool {
	var apiErr spotifyapi.Error
	if errors.As(err, &apiErr) && apiErr.Status == 429 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
}
