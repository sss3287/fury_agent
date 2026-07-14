package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ahhh/fury_agent/internal/model"
)

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: srv.URL, APIKey: "sk-openai-secret-xyz", Model: "gpt-test", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGenerateMultipleOutputItems(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{
			"id":"resp_1","object":"response","model":"gpt-test","status":"completed",
			"output":[
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Answer."}]},
				{"type":"function_call","call_id":"call_7","name":"lookup","arguments":"{\"k\":1}"}
			],
			"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}
		}`))
	}))
	defer srv.Close()

	resp, err := testClient(t, srv).Generate(context.Background(), model.GenerateRequest{
		Model:      "gpt-test",
		System:     []model.ContentPart{model.Text("be terse")},
		Messages:   []model.Message{{Role: model.RoleUser, Parts: []model.ContentPart{model.Text("hi")}}},
		ToolChoice: model.ToolChoice{Mode: model.ToolChoiceAuto},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Multiple output item types: text + function_call, ordered.
	if len(resp.Content) != 2 || resp.Content[0].Type != model.PartText || resp.Content[1].Type != model.PartToolUse {
		t.Fatalf("bad content: %+v", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "lookup" || string(resp.ToolCalls[0].Arguments) != `{"k":1}` {
		t.Errorf("bad tool call: %+v", resp.ToolCalls)
	}
	if resp.StopReason != model.StopToolUse {
		t.Errorf("stop = %q want tool_use", resp.StopReason)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("usage total = %d", resp.Usage.TotalTokens)
	}
	if gotAuth != "Bearer sk-openai-secret-xyz" {
		t.Errorf("auth not injected: %q", gotAuth)
	}
	// System text maps to instructions.
	if !strings.Contains(gotBody, `"instructions":"be terse"`) {
		t.Errorf("instructions missing: %s", gotBody)
	}
}

func TestStreamingTextAndTool(t *testing.T) {
	frames := []string{
		`data: {"type":"response.created","response":{}}`,
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"Hel"}`,
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"lo"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_9","name":"go"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"x\":"}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"2}"}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_9","name":"go"}}`,
		`data: {"type":"response.brand_new_event","output_index":2}`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","call_id":"call_9","name":"go","arguments":"{\"x\":2}"}],"usage":{"input_tokens":3,"output_tokens":8,"total_tokens":11}}}`,
		`data: [DONE]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, f := range frames {
			_, _ = io.WriteString(w, f+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	stream, err := testClient(t, srv).Stream(context.Background(), model.GenerateRequest{Model: "gpt-test", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var text, args strings.Builder
	sawUnknown := false
	var stop model.StopReason
	var usage *model.Usage
	for {
		ev, ok := stream.Next()
		if !ok {
			break
		}
		switch ev.Type {
		case model.StreamTextDelta:
			text.WriteString(ev.TextDelta)
		case model.StreamToolCallDelta:
			args.WriteString(ev.ArgsDelta)
		case model.StreamUnknown:
			sawUnknown = true
		case model.StreamMessageStop:
			stop = ev.StopReason
			usage = ev.Usage
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if text.String() != "Hello" {
		t.Errorf("text = %q", text.String())
	}
	if args.String() != `{"x":2}` {
		t.Errorf("args = %q", args.String())
	}
	if stop != model.StopToolUse {
		t.Errorf("stop = %q", stop)
	}
	if usage == nil || usage.TotalTokens != 11 {
		t.Errorf("usage = %+v", usage)
	}
	if !sawUnknown {
		t.Error("unknown event type not preserved")
	}
}

func TestToolResultRoundTripTranslation(t *testing.T) {
	// An assistant tool call followed by a tool result must translate into a
	// function_call item plus a function_call_output item.
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"id":"r","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	tc := model.ToolCall{ID: "call_1", Name: "f", Arguments: json.RawMessage(`{"a":1}`)}
	_, err := testClient(t, srv).Generate(context.Background(), model.GenerateRequest{
		Model: "gpt-test",
		Messages: []model.Message{
			{Role: model.RoleUser, Parts: []model.ContentPart{model.Text("go")}},
			{Role: model.RoleAssistant, Parts: []model.ContentPart{{Type: model.PartToolUse, ToolUse: &tc}}},
			{Role: model.RoleTool, Parts: []model.ContentPart{{Type: model.PartToolResult, ToolResult: &model.ToolResult{CallID: "call_1", Name: "f", Content: "42"}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"type":"function_call"`) || !strings.Contains(body, `"call_id":"call_1"`) {
		t.Errorf("function_call not in body: %s", body)
	}
	if !strings.Contains(body, `"type":"function_call_output"`) || !strings.Contains(body, `"output":"42"`) {
		t.Errorf("function_call_output not in body: %s", body)
	}
}

func TestRetryOn503(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"id":"r","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m", MaxRetries: 2, HTTPClient: srv.Client()})
	resp, err := c.Generate(context.Background(), model.GenerateRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "ok" || calls.Load() != 2 {
		t.Errorf("text=%q calls=%d", resp.Text(), calls.Load())
	}
}

func TestErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"invalid_api_key","message":"bad"}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m", MaxRetries: 0, HTTPClient: srv.Client()})
	_, err := c.Generate(context.Background(), model.GenerateRequest{Model: "m"})
	pe, ok := err.(*model.ProviderError)
	if !ok {
		t.Fatalf("want ProviderError, got %T: %v", err, err)
	}
	if pe.HTTPStatus != 401 || pe.Code != "invalid_api_key" || pe.Message != "bad" {
		t.Errorf("bad error: %+v", pe)
	}
}
