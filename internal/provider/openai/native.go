package openai

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ahhh/fury_agent/internal/model"
	"github.com/ahhh/fury_agent/internal/transport"
)

// nativeClient exposes the raw OpenAI API so every endpoint (models, files,
// embeddings, batches, images, audio, moderation, realtime) is reachable
// (GO_AGENT_PLAN.md §6.2, §7.1). Authentication is injected by the transport.
type nativeClient struct {
	tc *transport.Client
}

func (n *nativeClient) Do(ctx context.Context, req model.NativeRequest) (model.NativeResponse, error) {
	resp, err := n.tc.Do(ctx, toTransportRequest(req, false))
	if err != nil {
		return model.NativeResponse{}, err
	}
	return model.NativeResponse{
		Status:    resp.Status,
		Header:    resp.Header,
		Body:      resp.Body,
		RequestID: resp.RequestID,
	}, nil
}

func (n *nativeClient) Stream(ctx context.Context, req model.NativeRequest) (model.NativeEventStream, error) {
	resp, err := n.tc.Do(ctx, toTransportRequest(req, true))
	if err != nil {
		return nil, err
	}
	if resp.SSE == nil {
		return nil, model.ErrNativeStreamUnsupported
	}
	return &nativeStream{sse: resp.SSE}, nil
}

func toTransportRequest(req model.NativeRequest, stream bool) transport.Request {
	return transport.Request{
		Method:     req.Method,
		Path:       req.Path,
		Query:      req.Query,
		Header:     req.Header,
		Body:       req.Body,
		Stream:     stream || req.ResponseMode == model.NativeSSE,
		Timeout:    req.Timeout,
		MaxRetries: req.MaxRetries,
	}
}

type nativeStream struct {
	sse *transport.SSEReader
}

func (s *nativeStream) Next() ([]byte, bool) {
	ev, ok := s.sse.Next()
	if !ok {
		return nil, false
	}
	return ev.Data, true
}

func (s *nativeStream) Err() error   { return s.sse.Err() }
func (s *nativeStream) Close() error { return s.sse.Close() }

func providerErr(op string, resp *transport.Response) error {
	pe := &model.ProviderError{
		Provider:   providerName,
		Operation:  op,
		HTTPStatus: resp.Status,
		Retryable:  resp.Status == http.StatusTooManyRequests || resp.Status >= 500,
		Native:     resp.Body,
		RequestID:  resp.RequestID,
	}
	var we wireErrorEnvelope
	if json.Unmarshal(resp.Body, &we) == nil {
		pe.Code = we.Error.Code
		if pe.Code == "" {
			pe.Code = we.Error.Type
		}
		pe.Message = we.Error.Message
	}
	if pe.Message == "" {
		pe.Message = http.StatusText(resp.Status)
	}
	return pe
}

func wrapErr(op string, resp *transport.Response, err error) error {
	pe := &model.ProviderError{
		Provider:  providerName,
		Operation: op,
		Message:   err.Error(),
		Retryable: true,
		Cause:     err,
	}
	if resp != nil {
		pe.RequestID = resp.RequestID
	}
	return pe
}
