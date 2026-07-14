// Package anthropic implements the Anthropic Messages API adapter (Messages,
// SSE streaming content blocks, tool use, thinking, token counting) behind the
// common model.Provider interface, plus a native escape hatch
// (GO_AGENT_PLAN.md §7.2). Ordered content blocks are never flattened.
package anthropic

import "encoding/json"

// Wire types for the Messages API. Unknown fields are preserved on responses via
// json.RawMessage where it matters; request types intentionally omit optional
// fields so req.Native can supply anything not modeled here.

type wireRequest struct {
	Model       string            `json:"model"`
	MaxTokens   int               `json:"max_tokens"`
	System      []wireText        `json:"system,omitempty"`
	Messages    []wireMessage     `json:"messages"`
	Tools       []wireTool        `json:"tools,omitempty"`
	ToolChoice  json.RawMessage   `json:"tool_choice,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	Thinking    *wireThinking     `json:"thinking,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type wireThinking struct {
	Type         string `json:"type"` // "enabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type wireText struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

// wireBlock is a request-side content block. Only the fields relevant to the set
// type are populated.
type wireBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// image / document
	Source *wireSource `json:"source,omitempty"`
}

type wireSource struct {
	Type      string `json:"type"` // "base64" | "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// wireResponse is the non-streaming Messages response.
type wireResponse struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	Role       string      `json:"role"`
	Model      string      `json:"model"`
	Content    []respBlock `json:"content"`
	StopReason string      `json:"stop_reason"`
	Usage      wireUsage   `json:"usage"`
}

// respBlock is a response-side content block. RawInput preserves tool_use input.
type respBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
}

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// wireError is the Anthropic error envelope.
type wireError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
