package gemini

import (
	"encoding/json"

	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/transport"
)

// eventStream adapts Gemini's streamGenerateContent SSE stream to
// model.EventStream. Each SSE frame is a full GenerateContentResponse chunk that
// may carry several parts, a finish reason, and usage; these are expanded into
// one or more normalized events. Every raw frame is preserved in Native
// (GO_AGENT_PLAN.md §3.4).
type eventStream struct {
	sse       *transport.SSEReader
	err       error
	pending   []model.StreamEvent
	started   bool
	nextIndex int // next content-block index for tool calls (0 reserved for text)
}

func newEventStream(sse *transport.SSEReader) *eventStream {
	return &eventStream{sse: sse, nextIndex: 1}
}

func (s *eventStream) Next() (model.StreamEvent, bool) {
	for {
		if len(s.pending) > 0 {
			ev := s.pending[0]
			s.pending = s.pending[1:]
			return ev, true
		}
		raw, ok := s.sse.Next()
		if !ok {
			s.err = s.sse.Err()
			return model.StreamEvent{}, false
		}
		s.expand(raw.Data)
	}
}

func (s *eventStream) Err() error   { return s.err }
func (s *eventStream) Close() error { return s.sse.Close() }

func (s *eventStream) expand(data []byte) {
	native := rawCopy(data)
	var w wireResponse
	if err := json.Unmarshal(data, &w); err != nil {
		s.pending = append(s.pending, model.StreamEvent{Type: model.StreamUnknown, Native: native})
		return
	}
	if !s.started {
		s.started = true
		s.pending = append(s.pending, model.StreamEvent{Type: model.StreamMessageStart, Native: native})
	}
	if len(w.Candidates) > 0 {
		cand := w.Candidates[0]
		for i, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				idx := s.nextIndex
				s.nextIndex++
				tc := &model.ToolCall{ID: toolCallID(p.FunctionCall.Name, i), Name: p.FunctionCall.Name}
				s.pending = append(s.pending,
					model.StreamEvent{Type: model.StreamToolCallStart, Index: idx, ToolCall: tc, Native: native},
					model.StreamEvent{Type: model.StreamToolCallDelta, Index: idx, ArgsDelta: string(normalizeArgs(p.FunctionCall.Args)), Native: native},
					model.StreamEvent{Type: model.StreamToolCallDone, Index: idx, ToolCall: tc, Native: native},
				)
			case p.Thought:
				s.pending = append(s.pending, model.StreamEvent{Type: model.StreamThinkingDelta, Index: 0, ThinkingDelta: p.Text, Native: native})
			case p.Text != "":
				s.pending = append(s.pending, model.StreamEvent{Type: model.StreamTextDelta, Index: 0, TextDelta: p.Text, Native: native})
			}
		}
		if cand.FinishReason != "" {
			ev := model.StreamEvent{Type: model.StreamMessageDelta, StopReason: mapFinishReason(cand.FinishReason, false), Native: native}
			if hasFunctionCall(cand) {
				ev.StopReason = model.StopToolUse
			}
			s.pending = append(s.pending, ev)
		}
	}
	if w.UsageMetadata.TotalTokenCount > 0 {
		u := mapUsage(w.UsageMetadata)
		s.pending = append(s.pending, model.StreamEvent{Type: model.StreamUsage, Usage: &u, Native: native})
	}
}

func hasFunctionCall(c wireCandidate) bool {
	for _, p := range c.Content.Parts {
		if p.FunctionCall != nil {
			return true
		}
	}
	return false
}

func rawCopy(b []byte) json.RawMessage {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
