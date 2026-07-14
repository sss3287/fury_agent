package fake

import (
	"context"
	"encoding/json"

	"github.com/ahhh/fury_agent/internal/model"
)

// stream replays a Step as a sequence of normalized stream events.
type stream struct {
	events []model.StreamEvent
	i      int
}

func newStream(s Step) *stream {
	evs := []model.StreamEvent{{Type: model.StreamMessageStart}}
	idx := 0
	if s.Text != "" {
		evs = append(evs, model.StreamEvent{Type: model.StreamTextDelta, Index: idx, TextDelta: s.Text})
		idx++
	}
	for i := range s.ToolCalls {
		tc := s.ToolCalls[i]
		evs = append(evs,
			model.StreamEvent{Type: model.StreamToolCallStart, Index: idx, ToolCall: &model.ToolCall{ID: tc.ID, Name: tc.Name}},
			model.StreamEvent{Type: model.StreamToolCallDelta, Index: idx, ArgsDelta: string(tc.Arguments)},
			model.StreamEvent{Type: model.StreamToolCallDone, Index: idx, ToolCall: &model.ToolCall{ID: tc.ID, Name: tc.Name}},
		)
		idx++
	}
	sr := s.StopReason
	if sr == "" {
		sr = model.StopEndTurn
	}
	usage := s.Usage
	evs = append(evs,
		model.StreamEvent{Type: model.StreamMessageDelta, StopReason: sr, Usage: &usage},
		model.StreamEvent{Type: model.StreamMessageStop},
	)
	return &stream{events: evs}
}

func (s *stream) Next() (model.StreamEvent, bool) {
	if s.i >= len(s.events) {
		return model.StreamEvent{}, false
	}
	ev := s.events[s.i]
	s.i++
	return ev, true
}

func (s *stream) Err() error   { return nil }
func (s *stream) Close() error { return nil }

// nativeClient is a stub native client for the fake provider.
type nativeClient struct{}

func (nativeClient) Do(_ context.Context, req model.NativeRequest) (model.NativeResponse, error) {
	body, _ := json.Marshal(map[string]any{"echo_path": req.Path, "method": req.Method})
	return model.NativeResponse{Status: 200, Body: body}, nil
}

func (nativeClient) Stream(context.Context, model.NativeRequest) (model.NativeEventStream, error) {
	return nil, model.ErrNativeStreamUnsupported
}
