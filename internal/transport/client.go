package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ahhh/fury_agent/internal/logging"
)

// AuthInjector sets provider authentication on an outbound request. The
// transport always calls it; callers can never supply credential headers
// themselves (GO_AGENT_PLAN.md §3.3).
type AuthInjector func(r *http.Request)

// callerSecretHeaders are stripped from caller-supplied headers before auth is
// injected, so a native request can never smuggle credentials into the log or
// onto the wire under caller control.
var callerSecretHeaders = []string{
	"Authorization", "Proxy-Authorization", "Cookie",
	"X-Api-Key", "Api-Key", "X-Goog-Api-Key",
}

// Config configures a Client.
type Config struct {
	BaseURL string
	Auth    AuthInjector
	Timeout time.Duration
	Retry   RetryPolicy
	// BodyCapBytes caps the body stored in HTTP records (0 = no cap).
	BodyCapBytes int
	// Redactor masks configured secrets in logged bodies. Optional.
	Redactor *logging.Redactor
	// HTTPClient overrides the underlying client (used by tests). Its Transport
	// is wrapped with instrumentation.
	HTTPClient *http.Client
}

// Client is an instrumented, retrying HTTP client bound to one provider base URL.
type Client struct {
	base    *url.URL
	auth    AuthInjector
	hc      *http.Client
	timeout time.Duration
	retry   RetryPolicy
	bodyCap int
	redact  *logging.Redactor
}

// New builds a Client from cfg.
func New(cfg Config) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	baseRT := hc.Transport
	if baseRT == nil {
		baseRT = http.DefaultTransport
	}
	hc.Transport = &instrumentedTransport{base: baseRT}
	retry := cfg.Retry
	if retry.MaxRetries == 0 && retry.BaseDelay == 0 {
		retry = DefaultRetryPolicy
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		base:    base,
		auth:    cfg.Auth,
		hc:      hc,
		timeout: timeout,
		retry:   retry,
		bodyCap: cfg.BodyCapBytes,
		redact:  cfg.Redactor,
	}, nil
}

// Request is a transport-level HTTP request. Path is joined onto the base URL
// unless it is an absolute URL.
type Request struct {
	Method  string
	Path    string
	Query   url.Values
	Header  http.Header
	Body    []byte
	Stream  bool
	Timeout time.Duration
	// MaxRetries overrides the client default when non-nil.
	MaxRetries *int
}

// Response is a transport-level HTTP response. For streaming requests SSE is set
// and Body is nil; the caller must Close SSE. Records holds one entry per
// attempt for the audit log.
type Response struct {
	Status    int
	Header    http.Header
	Body      []byte
	SSE       *SSEReader
	RequestID string
	Records   []HTTPRecord
}

// Do executes req with retries and full instrumentation.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	fullURL, err := c.resolveURL(req.Path, req.Query)
	if err != nil {
		return nil, err
	}
	maxRetries := c.retry.MaxRetries
	if req.MaxRetries != nil {
		maxRetries = *req.MaxRetries
	}
	timeout := c.timeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}

	var records []HTTPRecord
	for attempt := 0; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		rec := &HTTPRecord{Attempt: attempt}
		start := time.Now()

		httpReq, buildErr := c.buildRequest(withRecord(attemptCtx, rec), req.Method, fullURL, req.Header, req.Body)
		if buildErr != nil {
			cancel()
			return nil, buildErr
		}
		c.recordRequestBody(rec, req.Body)

		resp, doErr := c.hc.Do(httpReq)
		rec.DurationNS = time.Since(start).Nanoseconds()

		if doErr != nil {
			cancel()
			rec.NetworkErr = doErr.Error()
			// Retry connection-level failures before a response was received.
			if attempt < maxRetries && isRetryableNetErr(ctx, doErr) {
				rec.RetryReason = "network"
				d := c.retry.backoff(attempt, 0)
				rec.BackoffMs = d.Milliseconds()
				records = append(records, *rec)
				sleep(ctx, d)
				continue
			}
			records = append(records, *rec)
			return &Response{Records: records}, fmt.Errorf("http request failed: %w", doErr)
		}

		// Retryable status: read (small) error body, record, back off.
		if retryableStatus(resp.StatusCode) && attempt < maxRetries {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			c.recordResponseBody(rec, body)
			rec.RetryReason = retryReason(resp.StatusCode)
			ra := parseRetryAfter(resp.Header)
			d := c.retry.backoff(attempt, ra)
			rec.BackoffMs = d.Milliseconds()
			records = append(records, *rec)
			sleep(ctx, d)
			continue
		}

		if req.Stream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Hand back a live SSE reader; cancel fires when the caller Closes.
			records = append(records, *rec)
			return &Response{
				Status:    resp.StatusCode,
				Header:    resp.Header,
				SSE:       NewSSEReader(&cancelOnClose{ReadCloser: resp.Body, cancel: cancel}),
				RequestID: rec.RequestID,
				Records:   records,
			}, nil
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		c.recordResponseBody(rec, body)
		records = append(records, *rec)
		if readErr != nil {
			return &Response{Status: resp.StatusCode, Header: resp.Header, Records: records}, fmt.Errorf("read response body: %w", readErr)
		}
		return &Response{
			Status:    resp.StatusCode,
			Header:    resp.Header,
			Body:      body,
			RequestID: rec.RequestID,
			Records:   records,
		}, nil
	}
}

func (c *Client) resolveURL(path string, query url.Values) (string, error) {
	var u *url.URL
	var err error
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		u, err = url.Parse(path)
	} else {
		u, err = c.base.Parse(strings.TrimPrefix(path, "/"))
		if err == nil && !strings.HasSuffix(c.base.Path, "/") {
			// base.Parse treats base path as a file unless it ends in "/".
			u, err = url.Parse(strings.TrimRight(c.base.String(), "/") + "/" + strings.TrimPrefix(path, "/"))
		}
	}
	if err != nil {
		return "", fmt.Errorf("resolve url: %w", err)
	}
	if len(query) > 0 {
		q := u.Query()
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (c *Client) buildRequest(ctx context.Context, method, fullURL string, header http.Header, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, r)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Copy caller headers, then strip any credential headers the caller set.
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for _, h := range callerSecretHeaders {
		req.Header.Del(h)
	}
	// Transport injects authentication last so it always wins.
	if c.auth != nil {
		c.auth(req)
	}
	return req, nil
}

func (c *Client) recordRequestBody(rec *HTTPRecord, body []byte) {
	if len(body) == 0 {
		return
	}
	capped := logging.Cap(c.maybeRedact(body), c.bodyCap)
	rec.RequestBody = json.RawMessage(capped.Content)
	rec.RequestBodyHash = logging.SHA256Hex(body)
}

func (c *Client) recordResponseBody(rec *HTTPRecord, body []byte) {
	if len(body) == 0 {
		return
	}
	capped := logging.Cap(c.maybeRedact(body), c.bodyCap)
	rec.ResponseBody = json.RawMessage(capped.Content)
	rec.ResponseBodyHash = logging.SHA256Hex(body)
}

func (c *Client) maybeRedact(b []byte) []byte {
	if c.redact == nil {
		return b
	}
	return c.redact.ApplyBytes(b)
}

// cancelOnClose cancels the attempt context when the stream body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func isRetryableNetErr(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false // caller cancelled/timed out; do not retry
	}
	return err != nil
}
