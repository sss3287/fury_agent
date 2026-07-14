package model

import (
	"encoding/json"
	"time"
)

// ToolDefinition declares a tool the model may call. Parameters is a JSON
// Schema object describing the arguments.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	// Native preserves provider-specific tool declaration extensions (for
	// example provider-hosted tool configuration).
	Native json.RawMessage `json:"native,omitempty"`
}

// ToolChoiceMode controls whether and how the model may call tools.
type ToolChoiceMode string

const (
	// ToolChoiceAuto lets the model decide (the provider default).
	ToolChoiceAuto ToolChoiceMode = "auto"
	// ToolChoiceNone forbids tool calls.
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceRequired forces at least one tool call.
	ToolChoiceRequired ToolChoiceMode = "required"
	// ToolChoiceTool forces a specific named tool (see Name).
	ToolChoiceTool ToolChoiceMode = "tool"
)

// ToolChoice expresses a tool-choice constraint. The zero value (empty Mode)
// means "unspecified" — let the provider apply its default.
type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode,omitempty"`
	Name string         `json:"name,omitempty"`
}

// ToolCall is a model request to invoke a tool. ID correlates the call with its
// result. Arguments is the raw JSON arguments as produced by the model.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	// Native preserves the raw provider tool-call block.
	Native json.RawMessage `json:"native,omitempty"`
}

// ToolStatus is the terminal status of a tool execution.
type ToolStatus string

const (
	ToolStatusSuccess   ToolStatus = "success"
	ToolStatusError     ToolStatus = "error"
	ToolStatusTimeout   ToolStatus = "timeout"
	ToolStatusCancelled ToolStatus = "cancelled"
	ToolStatusDenied    ToolStatus = "denied"
	ToolStatusTruncated ToolStatus = "truncated"
)

// ToolResult is the outcome of executing a ToolCall. It carries both a
// structured result and a human-readable form for the model, plus full
// execution metadata for the audit log (GO_AGENT_PLAN.md §8.1).
type ToolResult struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Status    ToolStatus      `json:"status"`
	Arguments json.RawMessage `json:"arguments,omitempty"`

	// Structured is a machine-readable result payload.
	Structured json.RawMessage `json:"structured,omitempty"`
	// Content is the human-readable result passed back to the model.
	Content string `json:"content"`
	// Error is set when Status indicates a failure.
	Error string `json:"error,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	DurationNS int64     `json:"duration_ns"`

	// Native preserves provider- or tool-specific execution metadata.
	Native json.RawMessage `json:"native,omitempty"`
}
