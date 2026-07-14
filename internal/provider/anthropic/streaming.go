package anthropic

import (
	"encoding/json"

	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/transport"
)

// eventStream adapts the Anthropic SSE stream to model.EventStream. Every raw
// frame is preserved in the emitted event's Native field, and unknown event
// types are surfaced as model.StreamUnknown so nothing is discarded
// (GO_AGENT_PLAN.md §3.4).
type eventStream struct {
	sse *transport.SSEReader
	err error
	// pending holds an event produced when a single frame maps to one event but
	// we also need to remember tool-call block identity across deltas.
	toolBlocks map[int]*model.ToolCall
}

func newEventStream(sse *transport.SSEReader) *eventStream {
	return &eventStream{sse: sse, toolBlocks: map[int]*model.ToolCall{}}
}

func (s *eventStream) Next() (model.StreamEvent, bool) {
	for {
		raw, ok := s.sse.Next()
		if !ok {
			s.err = s.sse.Err()
			return model.StreamEvent{}, false
		}
		ev, emit := s.translate(raw.Data)
		if emit {
			return ev, true
		}
		// Non-emitting frame (e.g. ping): keep reading.
	}
}

func (s *eventStream) Err() error   { return s.err }
func (s *eventStream) Close() error { return s.sse.Close() }

// anthropic SSE frame shapes (only fields we read).
type sseFrame struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	Message      json.RawMessage `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        sseDelta        `json:"delta"`
	Usage        *sseUsage       `json:"usage"`
	Error        json.RawMessage `json:"error"`
}

type sseDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	PartialJSON string `json:"partial_json"`
	Thinking    string `json:"thinking"`
	Signature   string `json:"signature"`
	StopReason  string `json:"stop_reason"`
}

type sseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (s *eventStream) translate(data []byte) (model.StreamEvent, bool) {
	var f sseFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return model.StreamEvent{Type: model.StreamUnknown, Native: rawCopy(data)}, true
	}
	native := rawCopy(data)
	switch f.Type {
	case "message_start":
		ev := model.StreamEvent{Type: model.StreamMessageStart, Native: native}
		if u := extractStartUsage(f.Message); u != nil {
			ev.Usage = u
		}
		return ev, true
	case "content_block_start":
		var cb struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(f.ContentBlock, &cb)
		if cb.Type == "tool_use" {
			tc := &model.ToolCall{ID: cb.ID, Name: cb.Name}
			s.toolBlocks[f.Index] = tc
			return model.StreamEvent{Type: model.StreamToolCallStart, Index: f.Index, ToolCall: tc, Native: native}, true
		}
		// text/thinking block start carries no normalized payload.
		return model.StreamEvent{Type: model.StreamUnknown, Index: f.Index, Native: native}, true
	case "content_block_delta":
		switch f.Delta.Type {
		case "text_delta":
			return model.StreamEvent{Type: model.StreamTextDelta, Index: f.Index, TextDelta: f.Delta.Text, Native: native}, true
		case "input_json_delta":
			return model.StreamEvent{Type: model.StreamToolCallDelta, Index: f.Index, ArgsDelta: f.Delta.PartialJSON, Native: native}, true
		case "thinking_delta":
			return model.StreamEvent{Type: model.StreamThinkingDelta, Index: f.Index, ThinkingDelta: f.Delta.Thinking, Native: native}, true
		default:
			// signature_delta and any future delta types are preserved.
			return model.StreamEvent{Type: model.StreamUnknown, Index: f.Index, Native: native}, true
		}
	case "content_block_stop":
		if tc, ok := s.toolBlocks[f.Index]; ok {
			return model.StreamEvent{Type: model.StreamToolCallDone, Index: f.Index, ToolCall: tc, Native: native}, true
		}
		return model.StreamEvent{Type: model.StreamUnknown, Index: f.Index, Native: native}, true
	case "message_delta":
		ev := model.StreamEvent{Type: model.StreamMessageDelta, StopReason: mapStopReason(f.Delta.StopReason), Native: native}
		if f.Usage != nil {
			ev.Usage = &model.Usage{OutputTokens: f.Usage.OutputTokens}
		}
		return ev, true
	case "message_stop":
		return model.StreamEvent{Type: model.StreamMessageStop, Native: native}, true
	case "error":
		return model.StreamEvent{Type: model.StreamError, Err: string(f.Error), Native: native}, true
	case "ping":
		return model.StreamEvent{}, false
	default:
		return model.StreamEvent{Type: model.StreamUnknown, Native: native}, true
	}
}

func extractStartUsage(msg json.RawMessage) *model.Usage {
	var m struct {
		Usage wireUsage `json:"usage"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil
	}
	if m.Usage.InputTokens == 0 && m.Usage.OutputTokens == 0 {
		return nil
	}
	u := mapUsage(m.Usage)
	return &u
}

func rawCopy(b []byte) json.RawMessage {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
