package agent

import (
	"strings"

	"github.com/ahhh/fury_agent/internal/model"
)

// accumulator reconstructs a canonical GenerateResponse from a stream of
// normalized events (GO_AGENT_PLAN.md §5 step 7). It preserves ordered content:
// interleaved text, thinking, and tool-call blocks are kept in the order their
// blocks were opened.
type accumulator struct {
	blocks     []*block
	byIndex    map[int]*block
	stopReason model.StopReason
	usage      model.Usage
}

type block struct {
	kind      model.PartType
	text      strings.Builder
	thinking  strings.Builder
	signature string
	toolID    string
	toolName  string
	toolArgs  strings.Builder
	native    []byte
}

func newAccumulator() *accumulator {
	return &accumulator{byIndex: map[int]*block{}}
}

func (a *accumulator) blockAt(index int, kind model.PartType) *block {
	if b, ok := a.byIndex[index]; ok {
		return b
	}
	b := &block{kind: kind}
	a.byIndex[index] = b
	a.blocks = append(a.blocks, b)
	return b
}

func (a *accumulator) add(ev model.StreamEvent) {
	switch ev.Type {
	case model.StreamTextDelta:
		a.blockAt(ev.Index, model.PartText).text.WriteString(ev.TextDelta)
	case model.StreamThinkingDelta:
		a.blockAt(ev.Index, model.PartThinking).thinking.WriteString(ev.ThinkingDelta)
	case model.StreamToolCallStart:
		b := a.blockAt(ev.Index, model.PartToolUse)
		if ev.ToolCall != nil {
			b.toolID = ev.ToolCall.ID
			b.toolName = ev.ToolCall.Name
			if len(ev.ToolCall.Arguments) > 0 {
				b.toolArgs.Write(ev.ToolCall.Arguments)
			}
		}
	case model.StreamToolCallDelta:
		a.blockAt(ev.Index, model.PartToolUse).toolArgs.WriteString(ev.ArgsDelta)
	case model.StreamToolCallDone:
		if ev.ToolCall != nil {
			b := a.blockAt(ev.Index, model.PartToolUse)
			if b.toolID == "" {
				b.toolID = ev.ToolCall.ID
			}
			if b.toolName == "" {
				b.toolName = ev.ToolCall.Name
			}
		}
	case model.StreamMessageDelta, model.StreamMessageStop:
		if ev.StopReason != "" {
			a.stopReason = ev.StopReason
		}
		if ev.Usage != nil {
			a.mergeUsage(*ev.Usage)
		}
	case model.StreamUsage:
		if ev.Usage != nil {
			a.mergeUsage(*ev.Usage)
		}
	}
}

func (a *accumulator) mergeUsage(u model.Usage) {
	if u.InputTokens > 0 {
		a.usage.InputTokens = u.InputTokens
	}
	if u.OutputTokens > 0 {
		a.usage.OutputTokens = u.OutputTokens
	}
	if u.CachedInputTokens > 0 {
		a.usage.CachedInputTokens = u.CachedInputTokens
	}
	if u.ReasoningTokens > 0 {
		a.usage.ReasoningTokens = u.ReasoningTokens
	}
	if u.TotalTokens > 0 {
		a.usage.TotalTokens = u.TotalTokens
	}
}

func (a *accumulator) response() model.GenerateResponse {
	resp := model.GenerateResponse{
		StopReason: a.stopReason,
		Usage:      a.usage,
	}
	if resp.Usage.TotalTokens == 0 {
		resp.Usage.TotalTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}
	for _, b := range a.blocks {
		switch b.kind {
		case model.PartText:
			resp.Content = append(resp.Content, model.Text(b.text.String()))
		case model.PartThinking:
			resp.Content = append(resp.Content, model.ContentPart{
				Type:      model.PartThinking,
				Thinking:  b.thinking.String(),
				Signature: b.signature,
			})
		case model.PartToolUse:
			call := model.ToolCall{
				ID:        b.toolID,
				Name:      b.toolName,
				Arguments: []byte(b.toolArgs.String()),
			}
			resp.Content = append(resp.Content, model.ContentPart{Type: model.PartToolUse, ToolUse: &call})
			resp.ToolCalls = append(resp.ToolCalls, call)
		}
	}
	return resp
}
