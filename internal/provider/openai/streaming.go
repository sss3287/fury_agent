package openai

import (
	"encoding/json"

	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/transport"
)

// eventStream adapts the Responses SSE stream to model.EventStream. Every raw
// frame is preserved; unknown event types surface as model.StreamUnknown
// (GO_AGENT_PLAN.md §3.4).
type eventStream struct {
	sse *transport.SSEReader
	err error
}

func newEventStream(sse *transport.SSEReader) *eventStream {
	return &eventStream{sse: sse}
}

func (s *eventStream) Next() (model.StreamEvent, bool) {
	for {
		raw, ok := s.sse.Next()
		if !ok {
			s.err = s.sse.Err()
			return model.StreamEvent{}, false
		}
		// OpenAI terminates the stream with a "[DONE]" sentinel on some paths.
		if string(raw.Data) == "[DONE]" {
			continue
		}
		ev, emit := s.translate(raw.Data)
		if emit {
			return ev, true
		}
	}
}

func (s *eventStream) Err() error   { return s.err }
func (s *eventStream) Close() error { return s.sse.Close() }

type sseFrame struct {
	Type        string          `json:"type"`
	OutputIndex int             `json:"output_index"`
	Delta       string          `json:"delta"`
	Item        json.RawMessage `json:"item"`
	Response    json.RawMessage `json:"response"`
}

func (s *eventStream) translate(data []byte) (model.StreamEvent, bool) {
	var f sseFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return model.StreamEvent{Type: model.StreamUnknown, Native: rawCopy(data)}, true
	}
	native := rawCopy(data)
	switch f.Type {
	case "response.created":
		return model.StreamEvent{Type: model.StreamMessageStart, Native: native}, true
	case "response.output_text.delta":
		return model.StreamEvent{Type: model.StreamTextDelta, Index: f.OutputIndex, TextDelta: f.Delta, Native: native}, true
	case "response.reasoning_summary_text.delta":
		return model.StreamEvent{Type: model.StreamThinkingDelta, Index: f.OutputIndex, ThinkingDelta: f.Delta, Native: native}, true
	case "response.function_call_arguments.delta":
		return model.StreamEvent{Type: model.StreamToolCallDelta, Index: f.OutputIndex, ArgsDelta: f.Delta, Native: native}, true
	case "response.output_item.added":
		if tc := toolCallFromItem(f.Item); tc != nil {
			return model.StreamEvent{Type: model.StreamToolCallStart, Index: f.OutputIndex, ToolCall: tc, Native: native}, true
		}
		return model.StreamEvent{Type: model.StreamUnknown, Index: f.OutputIndex, Native: native}, true
	case "response.output_item.done":
		if tc := toolCallFromItem(f.Item); tc != nil {
			return model.StreamEvent{Type: model.StreamToolCallDone, Index: f.OutputIndex, ToolCall: tc, Native: native}, true
		}
		return model.StreamEvent{Type: model.StreamUnknown, Index: f.OutputIndex, Native: native}, true
	case "response.completed", "response.incomplete":
		ev := model.StreamEvent{Type: model.StreamMessageStop, Native: native}
		if len(f.Response) > 0 {
			var w wireResponse
			if json.Unmarshal(f.Response, &w) == nil {
				u := mapUsage(w.Usage)
				ev.Usage = &u
				ev.StopReason = mapStopReason(w, hasFunctionCall(w))
			}
		}
		return ev, true
	case "error", "response.failed":
		return model.StreamEvent{Type: model.StreamError, Err: string(data), Native: native}, true
	default:
		return model.StreamEvent{Type: model.StreamUnknown, Index: f.OutputIndex, Native: native}, true
	}
}

func toolCallFromItem(raw json.RawMessage) *model.ToolCall {
	if len(raw) == 0 {
		return nil
	}
	var it outputItem
	if json.Unmarshal(raw, &it) != nil || it.Type != "function_call" {
		return nil
	}
	return &model.ToolCall{ID: it.CallID, Name: it.Name, Arguments: json.RawMessage(it.Arguments)}
}

func hasFunctionCall(w wireResponse) bool {
	for _, it := range w.Output {
		if it.Type == "function_call" {
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
