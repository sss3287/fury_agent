// Package fake is a deterministic, network-free model.Provider used by tests
// and the provider_fake build so the agent loop can be exercised end to end
// without a real API (GO_AGENT_PLAN.md Milestone 1).
package fake

import (
	"context"
	"encoding/json"

	"github.com/ahhh/fury_agent/internal/model"
)

// Step is one scripted model response.
type Step struct {
	Text       string
	ToolCalls  []model.ToolCall
	StopReason model.StopReason
	Usage      model.Usage
}

// Provider is a scripted provider. When Script is empty it uses a default
// heuristic: if a "process" tool is offered and no tool result has been seen
// yet, it requests one echo call, then produces a final text answer.
type Provider struct {
	Identity_ model.ProviderIdentity
	Script    []Step
	call      int
}

// New returns a fake provider with the given identity.
func New(id model.ProviderIdentity) *Provider {
	if id.Provider == "" {
		id.Provider = "fake"
	}
	if id.DefaultModel == "" {
		id.DefaultModel = "fake-1"
	}
	return &Provider{Identity_: id}
}

func (p *Provider) Identity() model.ProviderIdentity { return p.Identity_ }

func (p *Provider) Capabilities(context.Context) (model.Capabilities, error) {
	return model.Capabilities{Streaming: true, Tools: true, StructuredOutput: true}, nil
}

func (p *Provider) CountTokens(_ context.Context, req model.TokenCountRequest) (model.TokenCountResponse, error) {
	n := 0
	for _, m := range req.Messages {
		for _, part := range m.Parts {
			n += len(part.Text) / 4
		}
	}
	return model.TokenCountResponse{InputTokens: n}, nil
}

func (p *Provider) Native() model.NativeClient { return nativeClient{} }

// nextStep returns the scripted or heuristic response for this call.
func (p *Provider) nextStep(req model.GenerateRequest) Step {
	i := p.call
	p.call++
	if i < len(p.Script) {
		return p.Script[i]
	}
	// Heuristic default.
	if hasTool(req.Tools, "process") && !hasToolResult(req.Messages) {
		args, _ := json.Marshal(map[string]any{"command": []string{"echo", "hello from the process tool"}})
		return Step{
			ToolCalls: []model.ToolCall{{
				ID:        "call_fake_1",
				Name:      "process",
				Arguments: args,
			}},
			StopReason: model.StopToolUse,
			Usage:      model.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		}
	}
	return Step{
		Text:       "Done. This is a deterministic response from the fake provider.",
		StopReason: model.StopEndTurn,
		Usage:      model.Usage{InputTokens: 12, OutputTokens: 9, TotalTokens: 21},
	}
}

func (p *Provider) Generate(_ context.Context, req model.GenerateRequest) (model.GenerateResponse, error) {
	return stepToResponse(p.nextStep(req)), nil
}

func (p *Provider) Stream(_ context.Context, req model.GenerateRequest) (model.EventStream, error) {
	return newStream(p.nextStep(req)), nil
}

func stepToResponse(s Step) model.GenerateResponse {
	resp := model.GenerateResponse{
		StopReason: s.StopReason,
		Usage:      s.Usage,
		ResponseID: "resp_fake",
	}
	if resp.StopReason == "" {
		resp.StopReason = model.StopEndTurn
	}
	if s.Text != "" {
		resp.Content = append(resp.Content, model.Text(s.Text))
	}
	for i := range s.ToolCalls {
		tc := s.ToolCalls[i]
		resp.Content = append(resp.Content, model.ContentPart{Type: model.PartToolUse, ToolUse: &tc})
		resp.ToolCalls = append(resp.ToolCalls, tc)
	}
	resp.Raw = json.RawMessage(`{"fake":true}`)
	return resp
}

func hasTool(tools []model.ToolDefinition, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func hasToolResult(msgs []model.Message) bool {
	for _, m := range msgs {
		if m.Role == model.RoleTool {
			return true
		}
		for _, part := range m.Parts {
			if part.Type == model.PartToolResult {
				return true
			}
		}
	}
	return false
}
