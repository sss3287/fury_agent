package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// ProviderError normalizes a provider failure without losing native details
// (GO_AGENT_PLAN.md §13). Providers and the transport return it so the loop can
// classify retryability and map to exit codes uniformly.
type ProviderError struct {
	Provider   string          `json:"provider"`
	Operation  string          `json:"operation"`
	HTTPStatus int             `json:"http_status,omitempty"`
	Code       string          `json:"code,omitempty"`
	Message    string          `json:"message"`
	RequestID  string          `json:"request_id,omitempty"`
	Retryable  bool            `json:"retryable"`
	RetryAfter time.Duration   `json:"retry_after,omitempty"`
	Native     json.RawMessage `json:"native,omitempty"`
	Cause      error           `json:"-"`
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil provider error>"
	}
	s := fmt.Sprintf("%s %s", e.Provider, e.Operation)
	if e.HTTPStatus != 0 {
		s += fmt.Sprintf(" (http %d)", e.HTTPStatus)
	}
	if e.Code != "" {
		s += fmt.Sprintf(" [%s]", e.Code)
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.RequestID != "" {
		s += fmt.Sprintf(" (request_id=%s)", e.RequestID)
	}
	return s
}

func (e *ProviderError) Unwrap() error { return e.Cause }
