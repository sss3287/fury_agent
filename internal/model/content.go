// Package model defines the provider-agnostic message, request, response, and
// streaming types that power the agent loop, plus the native escape-hatch
// interface that preserves full provider coverage (GO_AGENT_PLAN.md §3.3, §3.4).
//
// The types here depend only on the standard library. No provider SDK type is
// ever exposed through this package. Provider-specific data that has no common
// representation is preserved verbatim via json.RawMessage fields so unknown
// fields and new API features pass through unchanged.
package model

import "encoding/json"

// Role identifies the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool carries tool results back to the model. Some providers model
	// tool results as user-role content blocks; translation layers adapt.
	RoleTool Role = "tool"
)

// PartType enumerates the kinds of content a message part can hold.
type PartType string

const (
	PartText       PartType = "text"
	PartImage      PartType = "image"
	PartDocument   PartType = "document"
	PartAudio      PartType = "audio"
	PartToolUse    PartType = "tool_use"
	PartToolResult PartType = "tool_result"
	PartThinking   PartType = "thinking"
	// PartNative preserves a provider-native content block that has no common
	// representation. Translation layers must round-trip it unchanged.
	PartNative PartType = "native"
)

// Blob is inline or referenced binary content (images, documents, audio).
// Exactly one of Data or URI should be set.
type Blob struct {
	MediaType string `json:"media_type"`
	// Data holds inline bytes; marshaled as base64 by encoding/json.
	Data []byte `json:"data,omitempty"`
	// URI references remote or provider-hosted content.
	URI string `json:"uri,omitempty"`
}

// ContentPart is a single typed element of a message. The zero value is not
// valid; set Type and the matching field.
type ContentPart struct {
	Type PartType `json:"type"`

	// Text is set for PartText and for the human-readable form of thinking.
	Text string `json:"text,omitempty"`

	// Blob is set for PartImage, PartDocument, and PartAudio.
	Blob *Blob `json:"blob,omitempty"`

	// ToolUse is set for PartToolUse (a model request to call a tool).
	ToolUse *ToolCall `json:"tool_use,omitempty"`

	// ToolResult is set for PartToolResult (a tool execution result).
	ToolResult *ToolResult `json:"tool_result,omitempty"`

	// Thinking carries reasoning/thinking metadata a provider permits the
	// client to receive. Signature preserves opaque provider signatures.
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// Native preserves the raw provider block for PartNative or any part that
	// carries provider-specific extensions alongside the normalized fields.
	Native json.RawMessage `json:"native,omitempty"`
}

// Text is a convenience constructor for a text part.
func Text(s string) ContentPart { return ContentPart{Type: PartText, Text: s} }

// Message is one turn in a conversation. Ordered Parts are never flattened into
// a single string: mixed text, tool, document, and thinking content is
// preserved in order (GO_AGENT_PLAN.md §7.2).
type Message struct {
	Role  Role          `json:"role"`
	Parts []ContentPart `json:"parts"`
	// Native preserves provider-specific message-level extensions.
	Native json.RawMessage `json:"native,omitempty"`
}
