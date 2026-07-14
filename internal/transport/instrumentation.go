// Package transport provides an instrumented, retrying HTTP client with an SSE
// reader. It records complete request/response metadata for the audit log while
// always redacting transport secrets, and it always injects authentication
// itself so callers can never supply credential headers (GO_AGENT_PLAN.md §3.3,
// §9.4).
package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/ahhh/fury_agent/internal/logging"
)

// Timing captures connection-level timings for one HTTP attempt (§9.4).
type Timing struct {
	DNSMs       float64 `json:"dns_ms,omitempty"`
	ConnectMs   float64 `json:"connect_ms,omitempty"`
	TLSMs       float64 `json:"tls_ms,omitempty"`
	FirstByteMs float64 `json:"first_byte_ms,omitempty"`
	TotalMs     float64 `json:"total_ms,omitempty"`
}

// HTTPRecord is the audit record for a single HTTP attempt. All header maps are
// already redacted; secrets never reach this struct's persisted form.
type HTTPRecord struct {
	Attempt         int                 `json:"attempt"`
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	QueryKeys       []string            `json:"query_keys,omitempty"`
	RequestHeaders  map[string][]string `json:"request_headers,omitempty"`
	RequestBody     json.RawMessage     `json:"request_body,omitempty"`
	RequestBodyHash string              `json:"request_body_sha256,omitempty"`

	Status           int                 `json:"status,omitempty"`
	ResponseHeaders  map[string][]string `json:"response_headers,omitempty"`
	ResponseBody     json.RawMessage     `json:"response_body,omitempty"`
	ResponseBodyHash string              `json:"response_body_sha256,omitempty"`

	RequestID   string            `json:"request_id,omitempty"`
	RateLimit   map[string]string `json:"rate_limit,omitempty"`
	Timing      Timing            `json:"timing"`
	RetryReason string            `json:"retry_reason,omitempty"`
	BackoffMs   int64             `json:"backoff_ms,omitempty"`
	DurationNS  int64             `json:"duration_ns"`
	NetworkErr  string            `json:"network_error,omitempty"`
}

type recordKeyType struct{}

var recordKey recordKeyType

func withRecord(ctx context.Context, rec *HTTPRecord) context.Context {
	return context.WithValue(ctx, recordKey, rec)
}

func recordFrom(ctx context.Context) *HTTPRecord {
	rec, _ := ctx.Value(recordKey).(*HTTPRecord)
	return rec
}

// instrumentedTransport wraps a base RoundTripper, wiring httptrace timings and
// response metadata into the *HTTPRecord carried in the request context.
type instrumentedTransport struct {
	base http.RoundTripper
}

func (t *instrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := recordFrom(req.Context())
	start := time.Now()
	var dnsStart, connStart, tlsStart time.Time
	if rec != nil {
		trace := &httptrace.ClientTrace{
			DNSStart:             func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
			DNSDone:              func(httptrace.DNSDoneInfo) { rec.Timing.DNSMs = msSince(dnsStart) },
			ConnectStart:         func(_, _ string) { connStart = time.Now() },
			ConnectDone:          func(_, _ string, _ error) { rec.Timing.ConnectMs = msSince(connStart) },
			TLSHandshakeStart:    func() { tlsStart = time.Now() },
			TLSHandshakeDone:     func(tls.ConnectionState, error) { rec.Timing.TLSMs = msSince(tlsStart) },
			GotFirstResponseByte: func() { rec.Timing.FirstByteMs = msSince(start) },
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	}

	resp, err := t.base.RoundTrip(req)
	if rec != nil {
		rec.Timing.TotalMs = msSince(start)
		rec.Method = req.Method
		rec.URL = sanitizeURL(req.URL)
		rec.QueryKeys = queryKeys(req.URL)
		rec.RequestHeaders = logging.RedactHeaders(req.Header)
		if err != nil {
			rec.NetworkErr = err.Error()
			return resp, err
		}
		rec.Status = resp.StatusCode
		rec.ResponseHeaders = logging.RedactHeaders(resp.Header)
		rec.RequestID = extractRequestID(resp.Header)
		rec.RateLimit = extractRateLimit(resp.Header)
	}
	return resp, err
}

func msSince(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(time.Since(t).Microseconds()) / 1000.0
}

// sanitizeURL strips the query string (keys are recorded separately) and any
// userinfo from the URL before logging.
func sanitizeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.RawQuery = ""
	c.User = nil
	return c.String()
}

func queryKeys(u *url.URL) []string {
	if u == nil {
		return nil
	}
	q := u.Query()
	if len(q) == 0 {
		return nil
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	return keys
}

// requestIDHeaders are response headers commonly carrying a provider request ID.
var requestIDHeaders = []string{
	"x-request-id",
	"request-id",
	"anthropic-request-id",
	"x-goog-request-id",
	"cf-ray",
}

func extractRequestID(h http.Header) string {
	for _, k := range requestIDHeaders {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func extractRateLimit(h http.Header) map[string]string {
	var out map[string]string
	for k, v := range h {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "ratelimit") || strings.Contains(lk, "rate-limit") || lk == "retry-after" {
			if out == nil {
				out = map[string]string{}
			}
			out[k] = strings.Join(v, ",")
		}
	}
	return out
}
