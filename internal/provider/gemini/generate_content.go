// Package gemini implements the Google Gemini generateContent adapter (content
// generation, SSE streaming, function calling, thinking) behind the common
// model.Provider interface, plus a native escape hatch (GO_AGENT_PLAN.md §7.3).
// Gemini parts, candidates, function calls, and grounding metadata are
// preserved rather than flattened.
package gemini

import "encoding/json"

type wireRequest struct {
	Contents          []wireContent   `json:"contents"`
	SystemInstruction *wireContent    `json:"systemInstruction,omitempty"`
	Tools             []wireTool      `json:"tools,omitempty"`
	ToolConfig        json.RawMessage `json:"toolConfig,omitempty"`
	GenerationConfig  *wireGenConfig  `json:"generationConfig,omitempty"`
}

type wireContent struct {
	Role  string     `json:"role,omitempty"`
	Parts []wirePart `json:"parts"`
}

type wirePart struct {
	Text             string                `json:"text,omitempty"`
	InlineData       *wireBlob             `json:"inlineData,omitempty"`
	FunctionCall     *wireFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *wireFunctionResponse `json:"functionResponse,omitempty"`
	Thought          bool                  `json:"thought,omitempty"`
}

type wireBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type wireFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type wireFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type wireTool struct {
	FunctionDeclarations []wireFuncDecl `json:"functionDeclarations,omitempty"`
}

type wireFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireGenConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	MaxOutputTokens  int             `json:"maxOutputTokens,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
	ThinkingConfig   *wireThinking   `json:"thinkingConfig,omitempty"`
}

type wireThinking struct {
	ThinkingBudget  int  `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
}

type wireResponse struct {
	Candidates    []wireCandidate `json:"candidates"`
	UsageMetadata wireUsage       `json:"usageMetadata"`
	ModelVersion  string          `json:"modelVersion"`
	ResponseID    string          `json:"responseId"`
}

type wireCandidate struct {
	Content           wireContent     `json:"content"`
	FinishReason      string          `json:"finishReason"`
	GroundingMetadata json.RawMessage `json:"groundingMetadata,omitempty"`
	SafetyRatings     json.RawMessage `json:"safetyRatings,omitempty"`
}

type wireUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
}

type wireErrorEnvelope struct {
	Error struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}
