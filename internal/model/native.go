package model

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNativeStreamUnsupported is returned by NativeClient.Stream when the
// provider or endpoint does not support SSE streaming.
var ErrNativeStreamUnsupported = errors.New("native streaming not supported")

// NativeResponseMode selects how a native response body is delivered.
type NativeResponseMode string

const (
	// NativeJSON reads the full body and exposes it as raw JSON bytes.
	NativeJSON NativeResponseMode = "json"
	// NativeBytes reads the full body as opaque bytes (uploads/downloads).
	NativeBytes NativeResponseMode = "bytes"
	// NativeSSE streams server-sent events (use NativeClient.Stream).
	NativeSSE NativeResponseMode = "sse"
)

// NativeRequest is a provider-native request that reaches any endpoint and
// field the common interface does not model (GO_AGENT_PLAN.md §3.3). The
// transport always injects authentication; callers must not set an
// Authorization or other credential header here — such headers are ignored and
// a redaction test asserts they never leak.
type NativeRequest struct {
	Method string `json:"method"`
	// Path is a provider-relative path (e.g. "/v1/messages"). For the router
	// build an approved absolute URL is also accepted.
	Path string `json:"path"`
	// Query holds URL query parameters.
	Query map[string][]string `json:"query,omitempty"`
	// Header holds additional non-secret headers.
	Header map[string][]string `json:"header,omitempty"`
	// Body is the raw request body. Prefer json.RawMessage for JSON bodies.
	Body []byte `json:"body,omitempty"`

	// ResponseMode selects JSON, bytes, or SSE handling. Defaults to JSON.
	ResponseMode NativeResponseMode `json:"response_mode,omitempty"`

	// Timeout overrides the transport default when non-zero.
	Timeout time.Duration `json:"timeout,omitempty"`
	// MaxRetries overrides the transport default when >= 0 (nil means default).
	MaxRetries *int `json:"max_retries,omitempty"`
}

// NativeResponse is a provider-native response.
type NativeResponse struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"header,omitempty"`
	// Body is the full response body for NativeJSON/NativeBytes modes.
	Body json.RawMessage `json:"body,omitempty"`
	// RequestID is the provider request identifier when present.
	RequestID string `json:"request_id,omitempty"`
}

// NativeEventStream is a raw SSE stream from a native request. Each Next call
// returns the next raw SSE data payload.
type NativeEventStream interface {
	Next() (data []byte, ok bool)
	Err() error
	Close() error
}

// NativeClient is the provider-native escape hatch. It is always available on
// every build so every provider endpoint is reachable without adding a new core
// interface method (GO_AGENT_PLAN.md §6.2).
type NativeClient interface {
	Do(ctx context.Context, req NativeRequest) (NativeResponse, error)
	Stream(ctx context.Context, req NativeRequest) (NativeEventStream, error)
}
