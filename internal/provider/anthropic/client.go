package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ahhh/fury_agent/internal/logging"
	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/transport"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	defaultVersion = "2023-06-01"
	providerName   = "anthropic"
)

// Config configures the Anthropic client.
type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	Version    string
	Timeout    time.Duration
	MaxRetries int
	Redactor   *logging.Redactor
	// HTTPClient overrides the underlying HTTP client (tests inject httptest).
	HTTPClient *http.Client
}

// Client is the Anthropic provider adapter.
type Client struct {
	tc       *transport.Client
	identity model.ProviderIdentity
}

// New builds an Anthropic client. The API key is injected by the transport on
// every request; it never appears in caller-supplied headers or logs.
func New(cfg Config) (*Client, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	version := cfg.Version
	if version == "" {
		version = defaultVersion
	}
	apiKey := cfg.APIKey
	auth := func(r *http.Request) {
		r.Header.Set("x-api-key", apiKey)
		r.Header.Set("anthropic-version", version)
		if r.Header.Get("Content-Type") == "" {
			r.Header.Set("Content-Type", "application/json")
		}
	}
	tc, err := transport.New(transport.Config{
		BaseURL:    base,
		Auth:       auth,
		Timeout:    cfg.Timeout,
		Retry:      transport.RetryPolicy{MaxRetries: cfg.MaxRetries},
		Redactor:   cfg.Redactor,
		HTTPClient: cfg.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		tc: tc,
		identity: model.ProviderIdentity{
			Provider:     providerName,
			DefaultModel: cfg.Model,
			BaseURL:      base,
		},
	}, nil
}

func (c *Client) Identity() model.ProviderIdentity { return c.identity }

func (c *Client) Capabilities(context.Context) (model.Capabilities, error) {
	return model.Capabilities{
		Streaming:       true,
		Tools:           true,
		ParallelTools:   true,
		Thinking:        true,
		Multimodal:      true,
		TokenCounting:   true,
		InputModalities: []string{"text", "image", "document"},
	}, nil
}

func (c *Client) Native() model.NativeClient { return &nativeClient{tc: c.tc} }

// Generate performs a non-streaming Messages request.
func (c *Client) Generate(ctx context.Context, req model.GenerateRequest) (model.GenerateResponse, error) {
	req.Stream = false
	body, err := toWireRequest(req)
	if err != nil {
		return model.GenerateResponse{}, err
	}
	resp, err := c.tc.Do(ctx, transport.Request{Method: http.MethodPost, Path: "/v1/messages", Body: body})
	if err != nil {
		return model.GenerateResponse{}, wrapErr("generate", resp, err)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return model.GenerateResponse{}, providerErr("generate", resp)
	}
	out, err := fromWireResponse(resp.Body)
	if err != nil {
		return model.GenerateResponse{}, err
	}
	if out.RequestID == "" {
		out.RequestID = resp.RequestID
	}
	return out, nil
}

// Stream performs a streaming Messages request.
func (c *Client) Stream(ctx context.Context, req model.GenerateRequest) (model.EventStream, error) {
	req.Stream = true
	body, err := toWireRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.tc.Do(ctx, transport.Request{Method: http.MethodPost, Path: "/v1/messages", Body: body, Stream: true})
	if err != nil {
		return nil, wrapErr("stream", resp, err)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, providerErr("stream", resp)
	}
	return newEventStream(resp.SSE), nil
}

// CountTokens counts tokens for a request via the count_tokens endpoint.
func (c *Client) CountTokens(ctx context.Context, req model.TokenCountRequest) (model.TokenCountResponse, error) {
	gr := model.GenerateRequest{Model: req.Model, System: req.System, Messages: req.Messages, Tools: req.Tools, Native: req.Native}
	body, err := toWireRequest(gr)
	if err != nil {
		return model.TokenCountResponse{}, err
	}
	// count_tokens rejects max_tokens/stream; strip them.
	body = stripFields(body, "max_tokens", "stream")
	resp, err := c.tc.Do(ctx, transport.Request{Method: http.MethodPost, Path: "/v1/messages/count_tokens", Body: body})
	if err != nil {
		return model.TokenCountResponse{}, wrapErr("count_tokens", resp, err)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return model.TokenCountResponse{}, providerErr("count_tokens", resp)
	}
	var out struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return model.TokenCountResponse{}, err
	}
	return model.TokenCountResponse{InputTokens: out.InputTokens, Raw: resp.Body}, nil
}

func stripFields(body []byte, fields ...string) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	for _, f := range fields {
		delete(m, f)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
