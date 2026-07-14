package agent

import (
	"testing"

	"github.com/ahhh/fury_agent/internal/model"
)

func TestAccumulatorOrdersBlocks(t *testing.T) {
	acc := newAccumulator()
	// Interleave: text block 0, tool block 1, more text block 0.
	acc.add(model.StreamEvent{Type: model.StreamTextDelta, Index: 0, TextDelta: "Hello "})
	acc.add(model.StreamEvent{Type: model.StreamToolCallStart, Index: 1, ToolCall: &model.ToolCall{ID: "c1", Name: "search"}})
	acc.add(model.StreamEvent{Type: model.StreamToolCallDelta, Index: 1, ArgsDelta: `{"q":`})
	acc.add(model.StreamEvent{Type: model.StreamToolCallDelta, Index: 1, ArgsDelta: `"x"}`})
	acc.add(model.StreamEvent{Type: model.StreamTextDelta, Index: 0, TextDelta: "world"})
	acc.add(model.StreamEvent{Type: model.StreamMessageDelta, StopReason: model.StopToolUse, Usage: &model.Usage{InputTokens: 3, OutputTokens: 4}})

	resp := acc.response()
	if got := resp.Text(); got != "Hello world" {
		t.Errorf("text = %q want 'Hello world'", got)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "c1" || tc.Name != "search" || string(tc.Arguments) != `{"q":"x"}` {
		t.Errorf("bad tool call: %+v args=%s", tc, tc.Arguments)
	}
	if resp.StopReason != model.StopToolUse {
		t.Errorf("stop reason = %q", resp.StopReason)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Errorf("total tokens = %d want 7", resp.Usage.TotalTokens)
	}
	// Content order: block 0 (text) came first, block 1 (tool) second.
	if len(resp.Content) != 2 || resp.Content[0].Type != model.PartText || resp.Content[1].Type != model.PartToolUse {
		t.Errorf("bad content order: %+v", resp.Content)
	}
}
