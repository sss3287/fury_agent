package logging

import (
	"encoding/json"
	"time"
)

// TurnInput is the outbound side of a turn (GO_AGENT_PLAN.md §9.3).
type TurnInput struct {
	Normalized        json.RawMessage `json:"normalized,omitempty"`
	ProviderRequest   json.RawMessage `json:"provider_request,omitempty"`
	RequestBodySHA256 string          `json:"request_body_sha256,omitempty"`
}

// TurnAttempt records one model call within a turn. A turn may contain several
// attempts because tools were executed between model calls.
type TurnAttempt struct {
	AttemptID    string            `json:"attempt_id"`
	Request      json.RawMessage   `json:"request,omitempty"`
	StreamEvents []json.RawMessage `json:"stream_events,omitempty"`
	// StreamEventRange optionally references a sequence range in events.jsonl
	// instead of duplicating every delta (§9.3).
	StreamEventRange *SeqRange       `json:"stream_event_range,omitempty"`
	Response         json.RawMessage `json:"response,omitempty"`
	RawResponse      json.RawMessage `json:"raw_response,omitempty"`
	HTTP             json.RawMessage `json:"http,omitempty"`
	Usage            json.RawMessage `json:"usage,omitempty"`
	DurationNS       int64           `json:"duration_ns"`
}

// SeqRange is an inclusive range of event sequence numbers.
type SeqRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// TurnOutput is the final assistant side of a turn.
type TurnOutput struct {
	Normalized         json.RawMessage `json:"normalized,omitempty"`
	ProviderResponse   json.RawMessage `json:"provider_response,omitempty"`
	ResponseBodySHA256 string          `json:"response_body_sha256,omitempty"`
}

// TurnRecord is a completed logical user-to-final-assistant exchange written to
// turns.jsonl (GO_AGENT_PLAN.md §9.3).
type TurnRecord struct {
	SchemaVersion string    `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	TurnID        string    `json:"turn_id"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at"`
	DurationNS    int64     `json:"duration_ns"`

	Input          TurnInput         `json:"input"`
	Attempts       []TurnAttempt     `json:"attempts"`
	ToolExecutions []json.RawMessage `json:"tool_executions"`
	Output         TurnOutput        `json:"output"`
	Status         string            `json:"status"`
}

// NewTurnRecord starts a turn record with the schema version and start time set.
func NewTurnRecord(sessionID, turnID string) *TurnRecord {
	return &TurnRecord{
		SchemaVersion:  SchemaVersion,
		SessionID:      sessionID,
		TurnID:         turnID,
		StartedAt:      time.Now().UTC(),
		Attempts:       []TurnAttempt{},
		ToolExecutions: []json.RawMessage{},
	}
}

// SchemaVersion mirrors model.SchemaVersion for the logging schema (§9).
const SchemaVersion = "1.0"
