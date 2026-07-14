package gemini

import (
	"context"
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
	c, err := New(Config{BaseURL: srv.URL, APIKey: "gemini-secret-key-abc", Model: "gemini-test", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGenerateTextAndUsage(t *testing.T) {
	var gotKey, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-goog-api-key")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"role":"model","parts":[{"text":"Hi there"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2,"totalTokenCount":10},
			"modelVersion":"gemini-test-001"
		}`))
	}))
	defer srv.Close()

	resp, err := testClient(t, srv).Generate(context.Background(), model.GenerateRequest{
		Model:    "gemini-test",
		System:   []model.ContentPart{model.Text("be nice")},
		Messages: []model.Message{{Role: model.RoleUser, Parts: []model.ContentPart{model.Text("hi")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "Hi there" {
		t.Errorf("text = %q", resp.Text())
	}
	if resp.StopReason != model.StopEndTurn {
		t.Errorf("stop = %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 8 || resp.Usage.TotalTokens != 10 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if gotKey != "gemini-secret-key-abc" {
		t.Errorf("api key not injected: %q", gotKey)
	}
	if !strings.Contains(gotPath, "gemini-test:generateContent") {
		t.Errorf("model not in path: %q", gotPath)
	}
}

func TestGenerateFunctionCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"role":"model","parts":[
				{"text":"calling"},
				{"functionCall":{"name":"lookup","args":{"q":"go"}}}
			]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":7,"totalTokenCount":12}
		}`))
	}))
	defer srv.Close()

	resp, err := testClient(t, srv).Generate(context.Background(), model.GenerateRequest{Model: "gemini-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "lookup" {
		t.Fatalf("bad tool calls: %+v", resp.ToolCalls)
	}
	if string(resp.ToolCalls[0].Arguments) != `{"q":"go"}` {
		t.Errorf("args = %s", resp.ToolCalls[0].Arguments)
	}
	if resp.StopReason != model.StopToolUse {
		t.Errorf("stop = %q want tool_use", resp.StopReason)
	}
	// Ordered content: text then tool_use.
	if len(resp.Content) != 2 || resp.Content[0].Type != model.PartText || resp.Content[1].Type != model.PartToolUse {
		t.Errorf("content order: %+v", resp.Content)
	}
}

func TestStreamingChunks(t *testing.T) {
	frames := []string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"go","args":{"x":1}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":5,"totalTokenCount":7}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("missing alt=sse: %q", r.URL.RawQuery)
		}
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

	stream, err := testClient(t, srv).Stream(context.Background(), model.GenerateRequest{Model: "gemini-test", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var text, args strings.Builder
	var stop model.StopReason
	var usage *model.Usage
	sawStart := false
	for {
		ev, ok := stream.Next()
		if !ok {
			break
		}
		switch ev.Type {
		case model.StreamMessageStart:
			sawStart = true
		case model.StreamTextDelta:
			text.WriteString(ev.TextDelta)
		case model.StreamToolCallDelta:
			args.WriteString(ev.ArgsDelta)
		case model.StreamMessageDelta:
			if ev.StopReason != "" {
				stop = ev.StopReason
			}
		case model.StreamUsage:
			usage = ev.Usage
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if !sawStart {
		t.Error("no message_start emitted")
	}
	if text.String() != "Hello" {
		t.Errorf("text = %q", text.String())
	}
	if args.String() != `{"x":1}` {
		t.Errorf("args = %q", args.String())
	}
	if stop != model.StopToolUse {
		t.Errorf("stop = %q", stop)
	}
	if usage == nil || usage.TotalTokens != 7 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestToolResultTranslation(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":1}}`))
	}))
	defer srv.Close()

	_, err := testClient(t, srv).Generate(context.Background(), model.GenerateRequest{
		Model: "gemini-test",
		Messages: []model.Message{
			{Role: model.RoleTool, Parts: []model.ContentPart{{Type: model.PartToolResult, ToolResult: &model.ToolResult{CallID: "c1", Name: "f", Content: "42"}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"functionResponse"`) || !strings.Contains(body, `"name":"f"`) || !strings.Contains(body, `"result":"42"`) {
		t.Errorf("functionResponse not in body: %s", body)
	}
}

func TestRetryOn500(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":1}}`))
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
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"nope"}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m", MaxRetries: 0, HTTPClient: srv.Client()})
	_, err := c.Generate(context.Background(), model.GenerateRequest{Model: "m"})
	pe, ok := err.(*model.ProviderError)
	if !ok {
		t.Fatalf("want ProviderError, got %T", err)
	}
	if pe.HTTPStatus != 403 || pe.Code != "PERMISSION_DENIED" || pe.Message != "nope" {
		t.Errorf("bad error: %+v", pe)
	}
}
