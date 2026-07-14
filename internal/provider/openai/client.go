package openai

import (
	"context"
	"net/http"
	"time"

	"github.com/ahhh/fury_agent/internal/logging"
	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/transport"
)

const (
	defaultBaseURL = "https://api.openai.com"
	providerName   = "openai"
)

// Config configures the OpenAI client.
type Config struct {
	BaseURL      string
	APIKey       string
	Model        string
	Organization string
	Timeout      time.Duration
	MaxRetries   int
	Redactor     *logging.Redactor
	HTTPClient   *http.Client
}

// Client is the OpenAI Responses API adapter.
type Client struct {
	tc       *transport.Client
	identity model.ProviderIdentity
}

// New builds an OpenAI client. The API key is injected by the transport.
func New(cfg Config) (*Client, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	apiKey := cfg.APIKey
	org := cfg.Organization
	auth := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+apiKey)
		if org != "" {
			r.Header.Set("OpenAI-Organization", org)
		}
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
		Streaming:        true,
		Tools:            true,
		ParallelTools:    true,
		StructuredOutput: true,
		Thinking:         true,
		Multimodal:       true,
		InputModalities:  []string{"text", "image", "file"},
	}, nil
}

func (c *Client) Native() model.NativeClient { return &nativeClient{tc: c.tc} }

// Generate performs a non-streaming Responses request.
func (c *Client) Generate(ctx context.Context, req model.GenerateRequest) (model.GenerateResponse, error) {
	req.Stream = false
	body, err := toWireRequest(req)
	if err != nil {
		return model.GenerateResponse{}, err
	}
	resp, err := c.tc.Do(ctx, transport.Request{Method: http.MethodPost, Path: "/v1/responses", Body: body})
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

// Stream performs a streaming Responses request.
func (c *Client) Stream(ctx context.Context, req model.GenerateRequest) (model.EventStream, error) {
	req.Stream = true
	body, err := toWireRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.tc.Do(ctx, transport.Request{Method: http.MethodPost, Path: "/v1/responses", Body: body, Stream: true})
	if err != nil {
		return nil, wrapErr("stream", resp, err)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, providerErr("stream", resp)
	}
	return newEventStream(resp.SSE), nil
}

// CountTokens is not offered as a standalone endpoint by the Responses API; use
// the native client for tokenizer/usage endpoints if the account exposes them.
func (c *Client) CountTokens(context.Context, model.TokenCountRequest) (model.TokenCountResponse, error) {
	return model.TokenCountResponse{}, &model.ProviderError{
		Provider:  providerName,
		Operation: "count_tokens",
		Message:   "token counting is not a Responses API endpoint; use the native client",
	}
}
