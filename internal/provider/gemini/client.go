package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/ahhh/fury_agent/internal/logging"
	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/transport"
)

const (
	defaultBaseURL = "https://generativelanguage.googleapis.com"
	apiVersion     = "v1beta"
	providerName   = "gemini"
)

// Config configures the Gemini client.
type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	Timeout    time.Duration
	MaxRetries int
	Redactor   *logging.Redactor
	HTTPClient *http.Client
}

// Client is the Gemini generateContent adapter.
type Client struct {
	tc       *transport.Client
	identity model.ProviderIdentity
	model    string
}

// New builds a Gemini client. The API key is injected by the transport via the
// x-goog-api-key header and never appears in caller headers or logs.
func New(cfg Config) (*Client, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	apiKey := cfg.APIKey
	auth := func(r *http.Request) {
		r.Header.Set("x-goog-api-key", apiKey)
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
		tc:    tc,
		model: cfg.Model,
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
		TokenCounting:    true,
		InputModalities:  []string{"text", "image", "document", "audio"},
	}, nil
}

func (c *Client) Native() model.NativeClient { return &nativeClient{tc: c.tc} }

func (c *Client) modelFor(req model.GenerateRequest) string {
	if req.Model != "" {
		return req.Model
	}
	return c.model
}

func (c *Client) path(mdl, method string) string {
	return "/" + apiVersion + "/models/" + mdl + ":" + method
}

// Generate performs a non-streaming generateContent request.
func (c *Client) Generate(ctx context.Context, req model.GenerateRequest) (model.GenerateResponse, error) {
	body, err := toWireRequest(req)
	if err != nil {
		return model.GenerateResponse{}, err
	}
	resp, err := c.tc.Do(ctx, transport.Request{
		Method: http.MethodPost,
		Path:   c.path(c.modelFor(req), "generateContent"),
		Body:   body,
	})
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

// Stream performs a streaming generateContent request (SSE).
func (c *Client) Stream(ctx context.Context, req model.GenerateRequest) (model.EventStream, error) {
	body, err := toWireRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.tc.Do(ctx, transport.Request{
		Method: http.MethodPost,
		Path:   c.path(c.modelFor(req), "streamGenerateContent"),
		Query:  url.Values{"alt": []string{"sse"}},
		Body:   body,
		Stream: true,
	})
	if err != nil {
		return nil, wrapErr("stream", resp, err)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, providerErr("stream", resp)
	}
	return newEventStream(resp.SSE), nil
}

// CountTokens counts tokens via the countTokens endpoint.
func (c *Client) CountTokens(ctx context.Context, req model.TokenCountRequest) (model.TokenCountResponse, error) {
	gr := model.GenerateRequest{Model: req.Model, System: req.System, Messages: req.Messages, Tools: req.Tools, Native: req.Native}
	body, err := toWireRequest(gr)
	if err != nil {
		return model.TokenCountResponse{}, err
	}
	mdl := req.Model
	if mdl == "" {
		mdl = c.model
	}
	resp, err := c.tc.Do(ctx, transport.Request{Method: http.MethodPost, Path: c.path(mdl, "countTokens"), Body: body})
	if err != nil {
		return model.TokenCountResponse{}, wrapErr("count_tokens", resp, err)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return model.TokenCountResponse{}, providerErr("count_tokens", resp)
	}
	var out struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return model.TokenCountResponse{}, err
	}
	return model.TokenCountResponse{InputTokens: out.TotalTokens, Raw: resp.Body}, nil
}
