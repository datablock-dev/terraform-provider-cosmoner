// Package client is the provider's HTTP client for the Cosmoner API.
//
// It is hand-written for the same reason the other SDKs in this repository
// are, and keeps their transport behaviour: bearer authentication, an
// idempotency key on every write, retries with jittered backoff for failures
// that are safe to replay, and the `{ success, data }` envelope unwrapped
// before a caller sees it. Only the endpoints the provider needs are wrapped.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultBaseURL    = "https://api.cosmoner.com"
	DefaultTimeout    = 30 * time.Second
	DefaultMaxRetries = 2
)

// Config holds what New needs. Zero values pick the defaults above.
type Config struct {
	APIKey     string
	BaseURL    string
	UserAgent  string
	Timeout    time.Duration
	MaxRetries int
	// HTTPClient is used instead of a fresh one when set. Its own Timeout is
	// left alone; Timeout above is applied per request through the context.
	HTTPClient *http.Client
}

// Client issues authenticated requests against the API.
type Client struct {
	apiKey     string
	baseURL    string
	userAgent  string
	timeout    time.Duration
	maxRetries int
	http       *http.Client
	// sleep is swapped out by tests so a retry does not cost wall-clock time.
	sleep func(context.Context, time.Duration) error
}

// New validates cfg and returns a client.
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("an API key is required")
	}
	if cfg.MaxRetries < 0 {
		return nil, errors.New("max retries cannot be negative")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("API URL %q is not an absolute http(s) URL", baseURL)
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		userAgent:  cfg.UserAgent,
		timeout:    timeout,
		maxRetries: cfg.MaxRetries,
		http:       httpClient,
		sleep:      sleepContext,
	}, nil
}

// envelope is the success shape every route answers with.
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// Do sends one request and decodes the envelope's data into out, which may be
// nil when the caller does not need the response.
//
// path is joined onto the base URL as is, so callers escape any segment that
// came from user input — see Path.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
	}

	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	// Generated once per logical request and reused across retries, so a
	// replayed write can be collapsed server-side once the API honours it.
	idempotencyKey := ""
	if isWrite(method) {
		idempotencyKey = newIdempotencyKey()
	}

	for attempt := 0; ; attempt++ {
		status, header, raw, err := c.send(ctx, method, target, payload, idempotencyKey)

		if err == nil && status < 300 {
			return decode(status, header, raw, out)
		}

		var failure error
		var retryAfter *time.Duration
		if err != nil {
			failure = err
		} else {
			failure = errorFromResponse(status, header, raw)
			retryAfter = parseRetryAfter(header)
		}

		// A cancelled or expired parent context is the caller giving up, not a
		// transient failure, so it is never worth another attempt.
		if ctx.Err() != nil {
			return failure
		}

		if !shouldRetry(attempt, c.maxRetries, method, status, err != nil) {
			return failure
		}
		if sleepErr := c.sleep(ctx, backoff(attempt, retryAfter)); sleepErr != nil {
			return failure
		}
	}
}

// send performs a single attempt and reads the whole body. status is 0 when
// the request never produced a response.
func (c *Client) send(ctx context.Context, method, target string, payload []byte, idempotencyKey string) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return 0, nil, nil, &ConnectionError{Message: fmt.Sprintf("request timed out after %s", c.timeout), Timeout: true}
		}
		return 0, nil, nil, &ConnectionError{Message: err.Error()}
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, nil, &ConnectionError{Message: fmt.Sprintf("reading response: %s", err)}
	}
	return res.StatusCode, res.Header, raw, nil
}

// decode unwraps a successful response.
//
// A 204 has no body by design. An empty or non-JSON body on any other
// success status is an error, because it means a truncated response — the
// same distinction every transport in this repository keeps.
func decode(status int, header http.Header, raw []byte, out any) error {
	if status == http.StatusNoContent {
		return nil
	}

	var env envelope
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &env) != nil {
		return &APIError{
			Status:    status,
			Code:      "INVALID_RESPONSE",
			Message:   "API returned a non-JSON response",
			RequestID: header.Get("X-Request-Id"),
		}
	}

	if out == nil {
		return nil
	}
	// A caller that passes out expects something back. Decoding a missing or
	// null `data` would hand it a zero value that looks like a real resource
	// with every field empty.
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return &APIError{
			Status:    status,
			Code:      "INVALID_RESPONSE",
			Message:   "API response had no data",
			RequestID: header.Get("X-Request-Id"),
		}
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return &APIError{
			Status:    status,
			Code:      "INVALID_RESPONSE",
			Message:   fmt.Sprintf("API response did not match the expected shape: %s", err),
			RequestID: header.Get("X-Request-Id"),
		}
	}
	return nil
}

// Path joins segments into a route, escaping each one, so an id read from
// configuration can never step outside the route it was meant for.
func Path(segments ...string) string {
	var b strings.Builder
	for _, segment := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(segment))
	}
	return b.String()
}

func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func newIdempotencyKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
