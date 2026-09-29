package spotify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type stubBase struct {
	calls  int
	header http.Header
	status []int
}

func (s *stubBase) RoundTrip(_ *http.Request) (*http.Response, error) {
	code := http.StatusTooManyRequests
	if len(s.status) > 0 {
		code = s.status[min(s.calls, len(s.status)-1)]
	}
	s.calls++
	return &http.Response{
		StatusCode: code,
		Header:     s.header,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func roundTripOnce(tb testing.TB, tr *rateLimitTransport, ctx context.Context) error {
	tb.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://x.test/", nil)
	if err != nil {
		tb.Fatal(err)
	}
	_, err = tr.RoundTrip(req)
	return err
}

// The reported bug: a multi-hour Retry-After slept straight into the page
// deadline and surfaced as a bare "context deadline exceeded", hiding the
// 429 from every classifier and hint downstream.
func TestRateLimitTransportFailsFastOnHugeRetryAfter(t *testing.T) {
	base := &stubBase{header: http.Header{"Retry-After": []string{"14886"}}}
	tr := newRateLimitTransport(base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := roundTripOnce(t, tr, ctx)
	elapsed := time.Since(start)

	var rateLimitErr *RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected typed *RateLimitError, got %v", err)
	}
	if rateLimitErr.RetryAfter != 14886*time.Second {
		t.Fatalf("expected RetryAfter 14886s, got %s", rateLimitErr.RetryAfter)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("429 must not be masked as a deadline: %v", err)
	}
	if !IsRateLimitError(err) {
		t.Fatalf("typed error must classify as rate limit: %v", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("expected fail-fast well under a second, took %s", elapsed)
	}
	if base.calls != 1 {
		t.Fatalf("expected exactly one outbound attempt, got %d", base.calls)
	}
}

// Short waits keep the old coalescing behavior: sleep, retry, succeed.
func TestRateLimitTransportCoalescesShortBurst(t *testing.T) {
	base := &stubBase{
		header: http.Header{"Retry-After": []string{"1"}},
		status: []int{http.StatusTooManyRequests, http.StatusOK},
	}
	tr := newRateLimitTransport(base)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://x.test/", nil)
	resp, err := tr.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected burst retry to succeed, got %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after retry, got %d", resp.StatusCode)
	}
	if base.calls != 2 {
		t.Fatalf("expected two outbound attempts, got %d", base.calls)
	}
	if elapsed < time.Second {
		t.Fatalf("expected the 1s backoff to be honored, took %s", elapsed)
	}
}

// A shared backoff recorded by another request must not sleep out a new
// request's deadline either — and must not touch the network at all.
func TestRateLimitTransportFailsFastOnSharedBackoff(t *testing.T) {
	base := &stubBase{}
	tr := newRateLimitTransport(base)
	tr.mu.Lock()
	tr.waitUntil = time.Now().Add(time.Hour)
	tr.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := roundTripOnce(t, tr, ctx)
	elapsed := time.Since(start)

	if _, ok := errors.AsType[*RateLimitError](err); !ok {
		t.Fatalf("expected typed *RateLimitError, got %v", err)
	}
	if base.calls != 0 {
		t.Fatalf("expected zero outbound attempts while backed off, got %d", base.calls)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("expected instant fail-fast, took %s", elapsed)
	}
}

func TestFailFastRateLimitBoundaries(t *testing.T) {
	far, cancelFar := context.WithTimeout(context.Background(), time.Hour)
	defer cancelFar()
	near, cancelNear := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelNear()

	if err := failFastRateLimit(far, 5*time.Second); err != nil {
		t.Fatalf("fittable wait must sleep, got %v", err)
	}
	if err := failFastRateLimit(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("short wait without deadline must sleep, got %v", err)
	}
	if err := failFastRateLimit(near, 5*time.Second); err == nil {
		t.Fatal("wait past the deadline must fail fast")
	}
	if err := failFastRateLimit(context.Background(), time.Hour); err == nil {
		t.Fatal("penalty-scale wait without deadline must fail fast")
	}
	if err := failFastRateLimit(far, 0); err != nil {
		t.Fatalf("zero wait must sleep, got %v", err)
	}
}

func TestDiagnoseLongRateLimitNamesRetryWindow(t *testing.T) {
	diag := DiagnoseError(&RateLimitError{RetryAfter: 14886 * time.Second})
	if diag.Category != "rate-limit" {
		t.Fatalf("expected rate-limit category, got %q", diag.Category)
	}
	if !strings.Contains(diag.NextStep, "4h") {
		t.Fatalf("expected the 4h window in the next step, got %q", diag.NextStep)
	}
	short := DiagnoseError(&RateLimitError{RetryAfter: 2 * time.Second})
	if short.Category != "rate-limit" || !strings.Contains(short.NextStep, "few seconds") {
		t.Fatalf("expected short-wait guidance, got %+v", short)
	}
}

func TestRateLimitErrorIsNotTransient(t *testing.T) {
	if IsTransientAPIError(&RateLimitError{RetryAfter: time.Hour}) {
		t.Fatal("a backed-off 429 must surface, not be re-driven by auto-retry")
	}
}

func TestRateLimitHintVariesWithWait(t *testing.T) {
	long, ok := RateLimitHint(&RateLimitError{RetryAfter: 14886 * time.Second})
	if !ok || !strings.Contains(long, "4h") {
		t.Fatalf("expected long-wait hint naming 4h, got %q", long)
	}
	if _, ok := RateLimitHint(errors.New("boom")); ok {
		t.Fatal("expected no hint for unrelated errors")
	}
}
