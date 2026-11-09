// Package apiclient is the Go client for the versioned ESF execution API.
//
// It wraps submit, status, events, evidence and cancel over the API's wire
// types, applies bearer authentication and classifies not-found and malformed
// responses. Callers in this module use this package instead of importing the
// server package directly.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/api"
)

// The API's versioned wire types. Aliases keep the contract defined once in
// internal/api while callers depend only on this package.
type (
	RunRequest = api.RunRequest
	RunStatus  = api.RunStatus
	Condition  = api.Condition
	Event      = api.Event
	Evidence   = api.Evidence
)

// ErrRunNotFound reports an operation on a run the execution API does not
// know. API responses carrying the run_not_found code satisfy errors.Is.
var ErrRunNotFound = errors.New("apiclient: run not found")

// APIError is a non-2xx response from the execution API.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("apiclient: HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("apiclient: %s: %s (HTTP %d)", e.Code, e.Message, e.StatusCode)
}

// Is classifies the stable run_not_found code as ErrRunNotFound.
func (e *APIError) Is(target error) bool {
	return target == ErrRunNotFound && e.Code == "run_not_found"
}

// ResponseError is a successful response that does not satisfy the response
// contract: invalid JSON, or JSON missing a field the contract requires.
type ResponseError struct {
	Field string
	Err   error
}

func (e *ResponseError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("apiclient: decode response: %v", e.Err)
	}
	return fmt.Sprintf("apiclient: response is missing required field %q", e.Field)
}

func (e *ResponseError) Unwrap() error { return e.Err }

// Client talks to one execution API endpoint.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New validates the endpoint and bearer token and returns a client. The base
// URL may carry a path prefix, as when the API sits behind a proxy.
func New(baseURL, token string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("apiclient: parse base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("apiclient: the base URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("apiclient: the base URL must name a host")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("apiclient: plaintext http is allowed only for a loopback host")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("apiclient: a bearer token is required")
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Submit starts a run, or returns the existing run for the same identity.
func (c *Client) Submit(ctx context.Context, request RunRequest) (RunStatus, error) {
	var status RunStatus
	if err := c.do(ctx, http.MethodPost, "/v1/runs", request, &status); err != nil {
		return RunStatus{}, err
	}
	if err := requireStatus(status); err != nil {
		return RunStatus{}, err
	}
	return status, nil
}

// Status reports a run's current state.
func (c *Client) Status(ctx context.Context, runID string) (RunStatus, error) {
	var status RunStatus
	if err := c.do(ctx, http.MethodGet, runPath(runID), nil, &status); err != nil {
		return RunStatus{}, err
	}
	if err := requireStatus(status); err != nil {
		return RunStatus{}, err
	}
	return status, nil
}

// Events reports each condition's current snapshot with its stable sequence
// number. Pass zero to read all conditions; the sequence number identifies a
// condition and does not advance when its state changes in place, so callers
// reconcile by condition type and time rather than assuming a transition stream.
func (c *Client) Events(ctx context.Context, runID string, after int) ([]Event, error) {
	var events []Event
	endpoint := runPath(runID) + "/events?after=" + strconv.Itoa(after)
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// Evidence returns a run's durable manifest as an equivalent JSON object. The
// bytes are re-encoded, so callers must compare parsed values, not raw bodies.
func (c *Client) Evidence(ctx context.Context, runID string) (Evidence, error) {
	var evidence Evidence
	if err := c.do(ctx, http.MethodGet, runPath(runID)+"/evidence", nil, &evidence); err != nil {
		return Evidence{}, err
	}
	if evidence.RunID == "" {
		return Evidence{}, &ResponseError{Field: "run_id"}
	}
	if len(evidence.Manifest) == 0 {
		return Evidence{}, &ResponseError{Field: "manifest"}
	}
	return evidence, nil
}

// Cancel requests cancellation and reports the run's current state.
func (c *Client) Cancel(ctx context.Context, runID string) (RunStatus, error) {
	var status RunStatus
	if err := c.do(ctx, http.MethodPost, runPath(runID)+"/cancel", nil, &status); err != nil {
		return RunStatus{}, err
	}
	if err := requireStatus(status); err != nil {
		return RunStatus{}, err
	}
	return status, nil
}

// runPath encodes a run identity as one URL path segment.
func runPath(runID string) string {
	return "/v1/runs/" + url.PathEscape(runID)
}

// requireStatus rejects a response that decoded but omitted the identity fields
// every status response must carry.
func requireStatus(status RunStatus) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "run_id", value: status.RunID},
		{name: "workflow_id", value: status.WorkflowID},
		{name: "state", value: status.State},
	} {
		if strings.TrimSpace(field.value) == "" {
			return &ResponseError{Field: field.name}
		}
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, requestBody, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("apiclient: encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("apiclient: build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("apiclient: %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return apiError(response)
	}
	if responseBody == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(responseBody); err != nil {
		return &ResponseError{Field: "body", Err: err}
	}
	return nil
}

// apiError turns a non-2xx response into a typed API error, keeping the stable
// code when the server supplied one.
func apiError(response *http.Response) error {
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	message := http.StatusText(response.StatusCode)
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&body); err == nil && body.Error != "" {
		message = body.Error
	}
	return &APIError{StatusCode: response.StatusCode, Code: body.Code, Message: message}
}

// isLoopbackHost reports whether host is an explicit loopback name or address.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
