package transport

import (
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// RetryPolicy controls retry behavior (GO_AGENT_PLAN.md §13).
type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultRetryPolicy is a conservative default.
var DefaultRetryPolicy = RetryPolicy{
	MaxRetries: 3,
	BaseDelay:  500 * time.Millisecond,
	MaxDelay:   20 * time.Second,
}

// retryableStatus reports whether an HTTP status is safe to retry: explicit rate
// limits and transient server errors (§13).
func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	}
	return false
}

// retryReason returns a short reason string for a retryable status, or "".
func retryReason(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "rate_limited"
	case status >= 500:
		return "server_error"
	default:
		return ""
	}
}

// backoff computes the delay before the given attempt (0-based) using
// exponential backoff with full jitter, honoring retryAfter when provided.
func (p RetryPolicy) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if p.MaxDelay > 0 && retryAfter > p.MaxDelay {
			return p.MaxDelay
		}
		return retryAfter
	}
	base := p.BaseDelay
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	d := float64(base) * math.Pow(2, float64(attempt))
	if p.MaxDelay > 0 && d > float64(p.MaxDelay) {
		d = float64(p.MaxDelay)
	}
	// Full jitter: uniform in [0, d].
	return time.Duration(rand.Int63n(int64(d) + 1))
}

// parseRetryAfter parses a Retry-After header (seconds or HTTP-date).
func parseRetryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
