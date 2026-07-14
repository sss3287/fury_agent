package model

import "encoding/json"

// SchemaVersion is the frozen version of the logging and record schemas
// (GO_AGENT_PLAN.md §9). Bump only on incompatible changes.
const SchemaVersion = "1.0"

// ProviderIdentity is the compile-time identity of the linked provider.
type ProviderIdentity struct {
	// Provider is the family: openai, anthropic, gemini, router, fake.
	Provider string `json:"provider"`
	// DefaultModel is the build-time model identifier.
	DefaultModel string `json:"default_model"`
	// BaseURL is the effective API base URL.
	BaseURL string `json:"base_url,omitempty"`
}

// Capabilities describes what the linked provider/model supports. Fields are
// advisory: they improve validation and defaults but must not gate the native
// escape hatch (GO_AGENT_PLAN.md §6.3).
type Capabilities struct {
	Streaming        bool     `json:"streaming"`
	Tools            bool     `json:"tools"`
	ParallelTools    bool     `json:"parallel_tools"`
	StructuredOutput bool     `json:"structured_output"`
	Thinking         bool     `json:"thinking"`
	Multimodal       bool     `json:"multimodal"`
	TokenCounting    bool     `json:"token_counting"`
	InputModalities  []string `json:"input_modalities,omitempty"`
}

// OutputFormat constrains the model's output shape.
type OutputFormat string

const (
	// OutputText is unconstrained text (the default).
	OutputText OutputFormat = ""
	// OutputJSON requests a JSON object without a fixed schema.
	OutputJSON OutputFormat = "json"
	// OutputJSONSchema requests schema-constrained JSON (see Schema).
	OutputJSONSchema OutputFormat = "json_schema"
)

// OutputConstraint requests structured output. The zero value means no
// constraint.
type OutputConstraint struct {
	Format OutputFormat `json:"format,omitempty"`
	// Name labels the schema for providers that require it.
	Name string `json:"name,omitempty"`
	// Schema is a JSON Schema object for OutputJSONSchema.
	Schema json.RawMessage `json:"schema,omitempty"`
}

// GenerateRequest is the normalized model request (GO_AGENT_PLAN.md §3.4).
type GenerateRequest struct {
	Model       string           `json:"model"`
	System      []ContentPart    `json:"system,omitempty"`
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  ToolChoice       `json:"tool_choice,omitempty"`
	Output      OutputConstraint `json:"output,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	MaxOutput   int              `json:"max_output,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	// Thinking requests reasoning/thinking where the provider supports it.
	Thinking *ThinkingConfig   `json:"thinking,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	// Native carries provider-native request fields merged over the translated
	// body (GO_AGENT_PLAN.md §3.4). It is applied last so callers can reach any
	// request field the common shape does not model.
	Native json.RawMessage `json:"native,omitempty"`
}

// ThinkingConfig requests reasoning/thinking output.
type ThinkingConfig struct {
	Enabled bool `json:"enabled"`
	// BudgetTokens optionally caps thinking tokens where the provider supports
	// it. Zero means "provider default".
	BudgetTokens int `json:"budget_tokens,omitempty"`
}

// Usage holds token accounting returned by the provider.
type Usage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int `json:"reasoning_tokens,omitempty"`
	TotalTokens       int `json:"total_tokens,omitempty"`
}

// StopReason is the normalized reason generation ended.
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopMaxTokens StopReason = "max_tokens"
	StopToolUse   StopReason = "tool_use"
	StopStop      StopReason = "stop_sequence"
	StopContent   StopReason = "content_filter"
	StopOther     StopReason = "other"
)

// GenerateResponse is the normalized model response. It retains the raw provider
// response and IDs so nothing is lost (GO_AGENT_PLAN.md §3.4).
type GenerateResponse struct {
	// Content is the ordered assistant content (text, tool calls, thinking).
	Content []ContentPart `json:"content"`
	// ToolCalls is a convenience view of tool_use parts in Content.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	StopReason StopReason `json:"stop_reason"`
	Usage      Usage      `json:"usage"`

	// ResponseID is the provider's response identifier.
	ResponseID string `json:"response_id,omitempty"`
	// RequestID is the provider's request identifier (often an HTTP header).
	RequestID string `json:"request_id,omitempty"`
	Model     string `json:"model,omitempty"`

	// Raw is the complete provider response JSON, preserved verbatim.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// Text returns the concatenated text parts of the response, in order.
func (r GenerateResponse) Text() string {
	var s string
	for _, p := range r.Content {
		if p.Type == PartText {
			s += p.Text
		}
	}
	return s
}

// TokenCountRequest asks the provider to count tokens for a request.
type TokenCountRequest struct {
	Model    string           `json:"model"`
	System   []ContentPart    `json:"system,omitempty"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
	Native   json.RawMessage  `json:"native,omitempty"`
}

// TokenCountResponse is the result of a token count.
type TokenCountResponse struct {
	InputTokens int             `json:"input_tokens"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}
