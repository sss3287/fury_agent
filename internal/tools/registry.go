package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ahhh/fury_agent/internal/model"
)

// Tool is a single callable tool (GO_AGENT_PLAN.md §8.1).
type Tool interface {
	Definition() model.ToolDefinition
	// Execute runs the call. Implementations set Status/Content/timestamps.
	Execute(ctx context.Context, call model.ToolCall) model.ToolResult
}

// Registry holds the available tools and implements agent.ToolRunner.
type Registry struct {
	order []string
	tools map[string]Tool
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds a tool, replacing any tool with the same name.
func (r *Registry) Register(t Tool) {
	name := t.Definition().Name
	if _, ok := r.tools[name]; !ok {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

// Definitions returns tool declarations in registration order.
func (r *Registry) Definitions() []model.ToolDefinition {
	defs := make([]model.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		defs = append(defs, r.tools[name].Definition())
	}
	return defs
}

// Execute dispatches a tool call by name, filling in universal metadata and
// guaranteeing a well-formed result even for unknown tools or invalid arguments.
func (r *Registry) Execute(ctx context.Context, call model.ToolCall) model.ToolResult {
	start := time.Now().UTC()
	t, ok := r.tools[call.Name]
	if !ok {
		return finalize(model.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
			Status:    model.ToolStatusError,
			Content:   fmt.Sprintf("unknown tool %q", call.Name),
			Error:     "unknown tool",
		}, start)
	}
	// Arguments must be valid JSON (an object) before dispatch.
	if len(call.Arguments) > 0 && !json.Valid(call.Arguments) {
		return finalize(model.ToolResult{
			CallID:    call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
			Status:    model.ToolStatusError,
			Content:   "arguments are not valid JSON",
			Error:     "invalid arguments",
		}, start)
	}
	res := t.Execute(ctx, call)
	res.CallID = call.ID
	res.Name = call.Name
	if res.Arguments == nil {
		res.Arguments = call.Arguments
	}
	return finalize(res, start)
}

// finalize fills timestamps and duration if the tool left them unset.
func finalize(res model.ToolResult, start time.Time) model.ToolResult {
	if res.StartedAt.IsZero() {
		res.StartedAt = start
	}
	if res.EndedAt.IsZero() {
		res.EndedAt = time.Now().UTC()
	}
	if res.DurationNS == 0 {
		res.DurationNS = res.EndedAt.Sub(res.StartedAt).Nanoseconds()
	}
	if res.Status == "" {
		res.Status = model.ToolStatusSuccess
	}
	return res
}
