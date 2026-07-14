package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ahhh/fury_agent/internal/buildinfo"
	"github.com/ahhh/fury_agent/internal/logging"
	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/provider/fake"
)

// stubTool is a minimal ToolRunner that records calls and returns a fixed result.
type stubTool struct {
	calls int
	def   model.ToolDefinition
}

func (s *stubTool) Definitions() []model.ToolDefinition { return []model.ToolDefinition{s.def} }

func (s *stubTool) Execute(_ context.Context, call model.ToolCall) model.ToolResult {
	s.calls++
	return model.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Status:  model.ToolStatusSuccess,
		Content: "tool ran ok",
	}
}

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	l, err := logging.New(logging.Options{Dir: t.TempDir()}, buildinfo.Get(), "ses_loop")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestRunTextOnly(t *testing.T) {
	p := fake.New(model.ProviderIdentity{})
	p.Script = []fake.Step{{Text: "hello there", StopReason: model.StopEndTurn}}
	ag := New(p, nil, newTestLogger(t), Config{Model: "fake-1"})

	res, err := ag.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != "hello there" {
		t.Errorf("text = %q", res.Text)
	}
	if res.ModelCalls != 1 {
		t.Errorf("model calls = %d want 1", res.ModelCalls)
	}
}

func TestRunToolLoop(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"x": 1})
	p := fake.New(model.ProviderIdentity{})
	p.Script = []fake.Step{
		{ToolCalls: []model.ToolCall{{ID: "c1", Name: "t", Arguments: args}}, StopReason: model.StopToolUse},
		{Text: "final answer", StopReason: model.StopEndTurn},
	}
	tool := &stubTool{def: model.ToolDefinition{Name: "t"}}
	ag := New(p, tool, newTestLogger(t), Config{Model: "fake-1"})

	res, err := ag.Run(context.Background(), "do it")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if tool.calls != 1 {
		t.Errorf("tool calls = %d want 1", tool.calls)
	}
	if res.Text != "final answer" {
		t.Errorf("text = %q want 'final answer'", res.Text)
	}
	if res.ModelCalls != 2 {
		t.Errorf("model calls = %d want 2", res.ModelCalls)
	}
	if res.ToolCalls != 1 {
		t.Errorf("tool calls counter = %d want 1", res.ToolCalls)
	}
}

func TestRunStreamingMatchesNonStreaming(t *testing.T) {
	mk := func(stream bool) Result {
		p := fake.New(model.ProviderIdentity{})
		p.Script = []fake.Step{{Text: "streamed text", StopReason: model.StopEndTurn}}
		ag := New(p, nil, newTestLogger(t), Config{Model: "fake-1", Stream: stream})
		res, err := ag.Run(context.Background(), "hi")
		if err != nil {
			t.Fatalf("run stream=%v: %v", stream, err)
		}
		return res
	}
	if a, b := mk(false), mk(true); a.Text != b.Text {
		t.Errorf("streaming mismatch: non-stream %q vs stream %q", a.Text, b.Text)
	}
}

func TestModelCallLimitStops(t *testing.T) {
	// A script that always requests a tool would loop forever; the limit stops it.
	args, _ := json.Marshal(map[string]any{})
	p := &loopingProvider{args: args}
	tool := &stubTool{def: model.ToolDefinition{Name: "t"}}
	ag := New(p, tool, newTestLogger(t), Config{Model: "fake-1", Limits: Limits{MaxModelCalls: 3, MaxToolCalls: 100, MaxToolCallsTurn: 100}})

	res, err := ag.Run(context.Background(), "loop")
	var le *LimitError
	if err == nil {
		t.Fatal("expected a limit error")
	}
	if !asLimit(err, &le) {
		t.Fatalf("expected LimitError, got %v", err)
	}
	if res.ModelCalls != 3 {
		t.Errorf("model calls = %d want 3", res.ModelCalls)
	}
	if ExitCode(err) != ExitSuccess {
		t.Errorf("limit should map to exit 0, got %d", ExitCode(err))
	}
}

// loopingProvider always asks for a tool call, to exercise the model-call limit.
type loopingProvider struct{ args []byte }

func (l *loopingProvider) Identity() model.ProviderIdentity {
	return model.ProviderIdentity{Provider: "loop"}
}
func (l *loopingProvider) Capabilities(context.Context) (model.Capabilities, error) {
	return model.Capabilities{Tools: true}, nil
}
func (l *loopingProvider) CountTokens(context.Context, model.TokenCountRequest) (model.TokenCountResponse, error) {
	return model.TokenCountResponse{}, nil
}
func (l *loopingProvider) Native() model.NativeClient { return nil }
func (l *loopingProvider) Generate(context.Context, model.GenerateRequest) (model.GenerateResponse, error) {
	tc := model.ToolCall{ID: "c", Name: "t", Arguments: l.args}
	return model.GenerateResponse{
		Content:    []model.ContentPart{{Type: model.PartToolUse, ToolUse: &tc}},
		ToolCalls:  []model.ToolCall{tc},
		StopReason: model.StopToolUse,
	}, nil
}
func (l *loopingProvider) Stream(context.Context, model.GenerateRequest) (model.EventStream, error) {
	return nil, nil
}

func asLimit(err error, target **LimitError) bool {
	for err != nil {
		if le, ok := err.(*LimitError); ok {
			*target = le
			return true
		}
		type unwrap interface{ Unwrap() error }
		u, ok := err.(unwrap)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
