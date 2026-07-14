package anthropic

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
	c, err := New(Config{
		BaseURL:    srv.URL,
		APIKey:     "sk-ant-secret-value-123",
		Model:      "claude-test",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGenerateNonStreaming(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("x-request-id", "req_abc")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1","type":"message","role":"assistant","model":"claude-test",
			"content":[{"type":"text","text":"Hello!"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":10,"output_tokens":3}
		}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	resp, err := c.Generate(context.Background(), model.GenerateRequest{
		Model:    "claude-test",
		Messages: []model.Message{{Role: model.RoleUser, Parts: []model.ContentPart{model.Text("hi")}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Text() != "Hello!" {
		t.Errorf("text = %q", resp.Text())
	}
	if resp.StopReason != model.StopEndTurn {
		t.Errorf("stop = %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if resp.RequestID != "req_abc" {
		t.Errorf("request id = %q", resp.RequestID)
	}
	if gotAuth != "sk-ant-secret-value-123" {
		t.Errorf("auth header not injected: %q", gotAuth)
	}
	// max_tokens must be defaulted into the request body.
	if !strings.Contains(gotBody, `"max_tokens"`) {
		t.Errorf("request missing max_tokens: %s", gotBody)
	}
}

func TestGenerateToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"id":"msg_2","type":"message","role":"assistant","model":"claude-test",
			"content":[
				{"type":"text","text":"Let me check."},
				{"type":"tool_use","id":"toolu_1","name":"search","input":{"q":"go"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":20,"output_tokens":15}
		}`))
	}))
	defer srv.Close()

	resp, err := testClient(t, srv).Generate(context.Background(), model.GenerateRequest{Model: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "search" || string(tc.Arguments) != `{"q":"go"}` {
		t.Errorf("bad tool call: %+v args=%s", tc, tc.Arguments)
	}
	// Ordered content preserved: text then tool_use.
	if len(resp.Content) != 2 || resp.Content[0].Type != model.PartText || resp.Content[1].Type != model.PartToolUse {
		t.Errorf("content order wrong: %+v", resp.Content)
	}
}

func TestStreamingWithToolAndUnknownEvents(t *testing.T) {
	frames := []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":0}}}`,
		`event: ping` + "\n" + `data: {"type":"ping"}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi "}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"there"}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_9","name":"lookup"}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"k\":"}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"1}"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":1}`,
		`event: surprise_event` + "\n" + `data: {"type":"surprise_event","note":"future"}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
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

	stream, err := testClient(t, srv).Stream(context.Background(), model.GenerateRequest{Model: "claude-test", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var text, args strings.Builder
	sawUnknown := false
	var stop model.StopReason
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
		case model.StreamMessageDelta:
			if ev.StopReason != "" {
				stop = ev.StopReason
			}
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if text.String() != "Hi there" {
		t.Errorf("text = %q", text.String())
	}
	if args.String() != `{"k":1}` {
		t.Errorf("args = %q", args.String())
	}
	if stop != model.StopToolUse {
		t.Errorf("stop = %q", stop)
	}
	if !sawUnknown {
		t.Error("unknown event type was not preserved as StreamUnknown")
	}
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m", MaxRetries: 2, HTTPClient: srv.Client()})
	resp, err := c.Generate(context.Background(), model.GenerateRequest{Model: "m"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Text() != "ok" {
		t.Errorf("text = %q", resp.Text())
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls (1 retry), got %d", calls.Load())
	}
}

func TestErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m", MaxRetries: 0, HTTPClient: srv.Client()})
	_, err := c.Generate(context.Background(), model.GenerateRequest{Model: "m"})
	var pe *model.ProviderError
	if !as(err, &pe) {
		t.Fatalf("expected ProviderError, got %v", err)
	}
	if pe.HTTPStatus != 401 || pe.Code != "authentication_error" || pe.Message != "bad key" {
		t.Errorf("bad provider error: %+v", pe)
	}
	if pe.Retryable {
		t.Error("401 should not be retryable")
	}
}

func TestNativeStreamNotBlockedByCommonInterface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A native call to an arbitrary endpoint with an arbitrary body.
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-x"}]}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	resp, err := c.Native().Do(context.Background(), model.NativeRequest{Method: "GET", Path: "/v1/models"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Data []struct{ ID string } `json:"data"`
	}
	if json.Unmarshal(resp.Body, &out) != nil || len(out.Data) != 1 || out.Data[0].ID != "claude-x" {
		t.Errorf("bad native body: %s", resp.Body)
	}
}

func TestCountTokens(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	out, err := c.CountTokens(context.Background(), model.TokenCountRequest{
		Model:    "claude-test",
		Messages: []model.Message{{Role: model.RoleUser, Parts: []model.ContentPart{model.Text("hi")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.InputTokens != 42 {
		t.Errorf("tokens = %d want 42", out.InputTokens)
	}
	// count_tokens must not carry max_tokens/stream.
	if strings.Contains(body, "max_tokens") || strings.Contains(body, "stream") {
		t.Errorf("count_tokens body should omit max_tokens/stream: %s", body)
	}
}

// as is a tiny errors.As shim to avoid importing errors in every assertion.
func as(err error, target **model.ProviderError) bool {
	for err != nil {
		if pe, ok := err.(*model.ProviderError); ok {
			*target = pe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
