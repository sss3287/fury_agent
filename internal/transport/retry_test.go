package transport

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryableStatus(t *testing.T) {
	retry := []int{429, 500, 502, 503, 504}
	noRetry := []int{200, 201, 400, 401, 403, 404, 422}
	for _, s := range retry {
		if !retryableStatus(s) {
			t.Errorf("status %d should be retryable", s)
		}
	}
	for _, s := range noRetry {
		if retryableStatus(s) {
			t.Errorf("status %d should not be retryable", s)
		}
	}
}

func TestBackoffHonorsRetryAfterAndCap(t *testing.T) {
	p := RetryPolicy{MaxRetries: 5, BaseDelay: 100 * time.Millisecond, MaxDelay: 2 * time.Second}
	// Retry-After wins and is capped at MaxDelay.
	if d := p.backoff(0, 10*time.Second); d != 2*time.Second {
		t.Errorf("retry-after cap: got %v want 2s", d)
	}
	if d := p.backoff(0, 500*time.Millisecond); d != 500*time.Millisecond {
		t.Errorf("retry-after: got %v want 500ms", d)
	}
	// Exponential backoff stays within [0, cap].
	for attempt := 0; attempt < 6; attempt++ {
		d := p.backoff(attempt, 0)
		if d < 0 || d > 2*time.Second {
			t.Errorf("attempt %d: backoff %v out of range", attempt, d)
		}
	}
}

func TestParseRetryAfterSeconds(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "3")
	if d := parseRetryAfter(h); d != 3*time.Second {
		t.Errorf("got %v want 3s", d)
	}
	h.Set("Retry-After", "not-a-number")
	if d := parseRetryAfter(h); d != 0 {
		t.Errorf("bad header should yield 0, got %v", d)
	}
}
