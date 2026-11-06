// Copyright IBM Corp. 2021, 2026
// SPDX-License-Identifier: MPL-2.0

// Package client implements a minimal GraphQL client for the Open Collective API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the Open Collective GraphQL v2 API endpoint.
const DefaultEndpoint = "https://api.opencollective.com/graphql/v2"

// maxErrorBodyBytes limits how much of a non-JSON error body is included in errors.
const maxErrorBodyBytes = 4096

// Retry defaults. The public API allows few unauthenticated requests per
// minute and answers excess requests with 429 and a Retry-After header.
const (
	DefaultMaxRetries   = 5
	DefaultMaxRetryWait = 60 * time.Second
	baseRetryWait       = time.Second
)

// Client sends GraphQL requests to the Open Collective API.
type Client struct {
	endpoint     string
	token        string
	userAgent    string
	httpClient   *http.Client
	maxRetries   int
	maxRetryWait time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithToken sets the Open Collective personal token sent with every request.
func WithToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// WithRetry configures how often rate-limited (429) and transient 5xx
// responses are retried, and the longest single wait between attempts.
// maxRetries of 0 disables retrying.
func WithRetry(maxRetries int, maxWait time.Duration) Option {
	return func(c *Client) {
		c.maxRetries = max(maxRetries, 0)
		c.maxRetryWait = maxWait
	}
}

// WithUserAgent sets the User-Agent header sent with every request.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// New creates a Client for the given endpoint. An empty endpoint uses DefaultEndpoint.
func New(endpoint string, opts ...Option) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	c := &Client{
		endpoint:     endpoint,
		userAgent:    "opentofu-provider-opencollective",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		maxRetries:   DefaultMaxRetries,
		maxRetryWait: DefaultMaxRetryWait,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Endpoint returns the GraphQL endpoint the client talks to.
func (c *Client) Endpoint() string { return c.endpoint }

// Request is a GraphQL request body.
type Request struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
}

// Response is a raw GraphQL response body.
type Response struct {
	Data   json.RawMessage `json:"data"`
	Errors Errors          `json:"errors,omitempty"`
}

// Location is a position in a GraphQL document.
type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Error is a single GraphQL error as returned by the server.
type Error struct {
	Message    string         `json:"message"`
	Locations  []Location     `json:"locations,omitempty"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

func (e Error) Error() string {
	if len(e.Path) == 0 {
		return e.Message
	}

	parts := make([]string, len(e.Path))
	for i, p := range e.Path {
		parts[i] = fmt.Sprint(p)
	}

	return fmt.Sprintf("%s (path: %s)", e.Message, strings.Join(parts, "."))
}

// Errors is the list of GraphQL errors in a response.
type Errors []Error

func (e Errors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}

	return "graphql: " + strings.Join(msgs, "; ")
}

// HTTPError is returned when the server responds with a non-2xx status and no GraphQL errors.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("graphql: unexpected HTTP status %d: %s", e.StatusCode, e.Body)
}

// Do executes a GraphQL query or mutation and decodes the "data" field into out.
// out may be nil if the caller does not need the result.
//
// If the response contains GraphQL errors they are returned as Errors; any
// partial data is still decoded into out.
func (c *Client) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	return c.DoRequest(ctx, Request{Query: query, Variables: variables}, out)
}

// DoRequest is like Do but accepts a full Request, e.g. to set an operation name.
func (c *Client) DoRequest(ctx context.Context, gqlReq Request, out any) error {
	body, err := json.Marshal(gqlReq)
	if err != nil {
		return fmt.Errorf("graphql: encoding request: %w", err)
	}

	status, respBody, err := c.send(ctx, body)
	if err != nil {
		return err
	}

	var gqlResp Response
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		if status < 200 || status > 299 {
			return &HTTPError{StatusCode: status, Body: truncate(string(respBody), maxErrorBodyBytes)}
		}

		return fmt.Errorf("graphql: decoding response: %w", err)
	}

	if out != nil && len(gqlResp.Data) > 0 && string(gqlResp.Data) != "null" {
		if err := json.Unmarshal(gqlResp.Data, out); err != nil {
			return errors.Join(fmt.Errorf("graphql: decoding data: %w", err), errOrNil(gqlResp.Errors))
		}
	}

	if len(gqlResp.Errors) > 0 {
		return gqlResp.Errors
	}

	if status < 200 || status > 299 {
		return &HTTPError{StatusCode: status, Body: truncate(string(respBody), maxErrorBodyBytes)}
	}

	return nil
}

// send POSTs body to the endpoint, retrying rate-limited and transient
// failures. It returns the final status code and response body.
func (c *Client) send(ctx context.Context, body []byte) (int, []byte, error) {
	for attempt := 0; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
		if err != nil {
			return 0, nil, fmt.Errorf("graphql: building request: %w", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")

		if c.userAgent != "" {
			httpReq.Header.Set("User-Agent", c.userAgent)
		}

		if c.token != "" {
			httpReq.Header.Set("Personal-Token", c.token)
		}

		httpResp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return 0, nil, fmt.Errorf("graphql: sending request: %w", err)
		}

		respBody, err := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()
		if err != nil {
			return 0, nil, fmt.Errorf("graphql: reading response: %w", err)
		}

		if attempt >= c.maxRetries || !isRetryable(httpResp.StatusCode) {
			return httpResp.StatusCode, respBody, nil
		}

		wait := retryWait(httpResp.Header.Get("Retry-After"), attempt, c.maxRetryWait)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, nil, fmt.Errorf("graphql: waiting to retry after HTTP %d: %w", httpResp.StatusCode, ctx.Err())
		case <-timer.C:
		}
	}
}

func isRetryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}

	return false
}

// retryWait honours a Retry-After header (seconds, possibly fractional, or an
// HTTP date) and otherwise backs off exponentially. The result is capped at maxWait.
func retryWait(retryAfter string, attempt int, maxWait time.Duration) time.Duration {
	wait := time.Duration(float64(baseRetryWait) * math.Pow(2, float64(attempt)))

	if retryAfter != "" {
		if secs, err := strconv.ParseFloat(retryAfter, 64); err == nil && secs >= 0 {
			wait = time.Duration(secs * float64(time.Second))
		} else if t, err := http.ParseTime(retryAfter); err == nil {
			wait = time.Until(t)
		}
	}

	return min(max(wait, 0), maxWait)
}

func errOrNil(e Errors) error {
	if len(e) == 0 {
		return nil
	}

	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "..."
}
