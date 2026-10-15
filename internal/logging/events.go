// Package logging implements append-only JSONL audit logging for the agent: a
// per-session event stream, completed turn-pair records, and a run summary
// (GO_AGENT_PLAN.md §9). Transport secrets are always redacted; raw model
// content is logged when enabled because that is a product requirement.
package logging

import (
	"encoding/json"
	"time"

	"github.com/ahhh/fury_agent/internal/buildinfo"
)

// EventType enumerates the event_type values written to events.jsonl.
type EventType string

const (
	EventSessionStart   EventType = "session_start"
	EventSessionEnd     EventType = "session_end"
	EventTurnStart      EventType = "turn_start"
	EventTurnComplete   EventType = "turn_complete"
	EventModelRequest   EventType = "model_request"
	EventModelResponse  EventType = "model_response_complete"
	EventStreamEvent    EventType = "model_stream_event"
	EventHTTP           EventType = "http_exchange"
	EventRetry          EventType = "retry"
	EventToolStart      EventType = "tool_start"
	EventToolComplete   EventType = "tool_complete"
	EventPolicyDecision EventType = "policy_decision"
	EventEBPF           EventType = "ebpf_event"
	EventLimit          EventType = "limit_reached"
	EventError          EventType = "error"
	EventDegradedLog    EventType = "degraded_logging"
)

// BuildMeta is the build block embedded in every event envelope.
type BuildMeta struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	ProviderTag string `json:"provider_tag"`
	GoVersion   string `json:"go_version"`
	TargetOS    string `json:"target_os"`
	TargetArch  string `json:"target_arch"`
}

func buildMeta(bi buildinfo.Info) BuildMeta {
	return BuildMeta{
		Version:     bi.Version,
		Commit:      bi.Commit,
		ProviderTag: bi.ProviderTag,
		GoVersion:   bi.GoVersion,
		TargetOS:    bi.TargetOS,
		TargetArch:  bi.TargetArch,
	}
}

// Envelope is the stable event envelope written to events.jsonl
// (GO_AGENT_PLAN.md §9.2). PrevHash/Hash implement the optional hash chain.
type Envelope struct {
	SchemaVersion string    `json:"schema_version"`
	EventID       string    `json:"event_id"`
	Sequence      int64     `json:"sequence"`
	TimestampUTC  time.Time `json:"timestamp_utc"`
	MonotonicNS   int64     `json:"monotonic_ns"`
	EventType     EventType `json:"event_type"`

	SessionID  string `json:"session_id"`
	TurnID     string `json:"turn_id,omitempty"`
	AttemptID  string `json:"attempt_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`

	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Build    BuildMeta `json:"build"`

	Data json.RawMessage `json:"data,omitempty"`

	// PrevHash and Hash form the optional integrity chain (§9.5). Hash is the
	// canonical hash of this envelope with Hash itself empty.
	PrevHash string `json:"prev_hash,omitempty"`
	Hash     string `json:"hash,omitempty"`
}
