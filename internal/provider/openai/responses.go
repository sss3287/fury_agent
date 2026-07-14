// Package openai implements the OpenAI Responses API adapter (streaming events,
// tool calls, multiple output item types, structured output) behind the common
// model.Provider interface, plus a native escape hatch (GO_AGENT_PLAN.md §7.1).
// The normalized response supports multiple output item types rather than a
// single assistant string.
package openai

import "encoding/json"

type wireRequest struct {
	Model           string            `json:"model"`
	Input           []inputItem       `json:"input"`
	Instructions    string            `json:"instructions,omitempty"`
	Tools           []wireTool        `json:"tools,omitempty"`
	ToolChoice      json.RawMessage   `json:"tool_choice,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Stream          bool              `json:"stream,omitempty"`
	Text            *wireTextConfig   `json:"text,omitempty"`
	Reasoning       *wireReasoning    `json:"reasoning,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type wireReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type wireTextConfig struct {
	Format json.RawMessage `json:"format,omitempty"`
}

// inputItem is a polymorphic Responses input item: a role message, an assistant
// function_call, or a function_call_output (tool result).
type inputItem struct {
	Type    string      `json:"type,omitempty"`
	Role    string      `json:"role,omitempty"`
	Content []inputPart `json:"content,omitempty"`

	// function_call (assistant history)
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`

	// function_call_output (tool result)
	Output string `json:"output,omitempty"`
}

type inputPart struct {
	Type     string `json:"type"` // input_text | output_text | input_image | input_file
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type wireTool struct {
	Type        string          `json:"type"` // "function"
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// wireResponse is the non-streaming Responses result.
type wireResponse struct {
	ID                string          `json:"id"`
	Object            string          `json:"object"`
	Model             string          `json:"model"`
	Status            string          `json:"status"`
	Output            []outputItem    `json:"output"`
	Usage             wireUsage       `json:"usage"`
	IncompleteDetails *incomplete     `json:"incomplete_details"`
	Error             json.RawMessage `json:"error"`
}

type incomplete struct {
	Reason string `json:"reason"`
}

// outputItem is one item in the response output array. Multiple item types are
// supported: message (with content parts), function_call, reasoning.
type outputItem struct {
	Type    string       `json:"type"`
	ID      string       `json:"id,omitempty"`
	Role    string       `json:"role,omitempty"`
	Content []outputPart `json:"content,omitempty"`
	// function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// reasoning
	Summary []reasoningSummary `json:"summary,omitempty"`
}

type outputPart struct {
	Type string `json:"type"` // output_text | refusal
	Text string `json:"text,omitempty"`
}

type reasoningSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type wireUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type wireErrorEnvelope struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
