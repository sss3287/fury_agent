package model

import "encoding/json"

// StreamEventType enumerates normalized streaming events. Providers emit
// provider-specific events; translation maps the ones we understand and emits
// StreamUnknown for the rest so no provider event is ever discarded
// (GO_AGENT_PLAN.md §3.4).
type StreamEventType string

const (
	// StreamMessageStart marks the beginning of a response.
	StreamMessageStart StreamEventType = "message_start"
	// StreamTextDelta carries an incremental text chunk (see TextDelta).
	StreamTextDelta StreamEventType = "text_delta"
	// StreamThinkingDelta carries an incremental thinking/reasoning chunk.
	StreamThinkingDelta StreamEventType = "thinking_delta"
	// StreamToolCallStart marks the start of a tool call (see ToolCall).
	StreamToolCallStart StreamEventType = "tool_call_start"
	// StreamToolCallDelta carries incremental tool-argument JSON (see ArgsDelta).
	StreamToolCallDelta StreamEventType = "tool_call_delta"
	// StreamToolCallDone marks a completed tool call.
	StreamToolCallDone StreamEventType = "tool_call_done"
	// StreamUsage carries updated usage/token accounting.
	StreamUsage StreamEventType = "usage"
	// StreamMessageDelta carries top-level updates such as stop reason.
	StreamMessageDelta StreamEventType = "message_delta"
	// StreamMessageStop marks the end of the response.
	StreamMessageStop StreamEventType = "message_stop"
	// StreamError carries a mid-stream provider error.
	StreamError StreamEventType = "error"
	// StreamUnknown preserves an event we do not model. Native holds the raw
	// frame so it can still be logged and inspected.
	StreamUnknown StreamEventType = "unknown"
)

// StreamEvent is a single normalized streaming event. Native always holds the
// raw provider frame (or SSE data) so the complete stream can be reconstructed
// and audited.
type StreamEvent struct {
	Type StreamEventType `json:"type"`

	// Index is the content-block index the event applies to, where relevant.
	Index int `json:"index,omitempty"`

	// TextDelta is set for StreamTextDelta.
	TextDelta string `json:"text_delta,omitempty"`
	// ThinkingDelta is set for StreamThinkingDelta.
	ThinkingDelta string `json:"thinking_delta,omitempty"`
	// ArgsDelta is set for StreamToolCallDelta (partial tool-argument JSON).
	ArgsDelta string `json:"args_delta,omitempty"`

	// ToolCall is set for StreamToolCallStart/Done (may have partial fields).
	ToolCall *ToolCall `json:"tool_call,omitempty"`

	// StopReason is set for StreamMessageDelta/Stop when known.
	StopReason StopReason `json:"stop_reason,omitempty"`
	// Usage is set for StreamUsage and terminal events when known.
	Usage *Usage `json:"usage,omitempty"`

	// Err is set for StreamError.
	Err string `json:"error,omitempty"`

	// Native is the raw provider frame this event was derived from. For
	// StreamUnknown it is the only meaningful field.
	Native json.RawMessage `json:"native,omitempty"`
}

// EventStream is a normalized streaming response. Callers must call Close.
// Next returns the next event; ok is false when the stream ends. Err reports a
// terminal error after Next returns ok=false.
type EventStream interface {
	Next() (ev StreamEvent, ok bool)
	Err() error
	Close() error
}
