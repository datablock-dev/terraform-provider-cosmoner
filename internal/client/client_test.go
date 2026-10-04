package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a test server that answers from a script, one response per
// request, and remembers what it was sent.
type recorder struct {
	mu        sync.Mutex
	responses []response
	requests  []*http.Request
	bodies    []string
}

type response struct {
	status int
	body   string
	header map[string]string
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()

	body, _ := io.ReadAll(req.Body)
	r.requests = append(r.requests, req)
	r.bodies = append(r.bodies, string(body))

	if len(r.responses) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	next := r.responses[0]
	r.responses = r.responses[1:]
	for k, v := range next.header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(next.status)
	_, _ = io.WriteString(w, next.body)
}

func newTestClient(t *testing.T, responses ...response) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{responses: responses}
	server := httptest.NewServer(rec)
	t.Cleanup(server.Close)

	c, err := New(Config{APIKey: "key_123", BaseURL: server.URL + "/", UserAgent: "cosmoner-terraform/test", MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c, rec
}

func TestNewValidatesConfig(t *testing.T) {
	cases := map[string]Config{
		"missing key":        {},
		"negative retries":   {APIKey: "k", MaxRetries: -1},
		"relative URL":       {APIKey: "k", BaseURL: "api.cosmoner.com"},
		"unsupported scheme": {APIKey: "k", BaseURL: "ftp://api.cosmoner.com"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestDoUnwrapsEnvelopeAndSendsHeaders(t *testing.T) {
	c, rec := newTestClient(t, response{status: 201, body: `{"success":true,"data":{"id":"var_1","name":"LOG_LEVEL","value":"debug"}}`})

	got, err := c.CreateVariable(context.Background(), "proj_1", CreateVariableInput{Name: "LOG_LEVEL", Value: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "var_1" || got.Value != "debug" {
		t.Fatalf("unexpected variable: %+v", got)
	}

	req := rec.requests[0]
	if req.Method != http.MethodPost || req.URL.Path != "/v1/projects/proj_1/variables" {
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
	}
	for header, want := range map[string]string{
		"Authorization": "Bearer key_123",
		"Accept":        "application/json",
		"Content-Type":  "application/json",
		"User-Agent":    "cosmoner-terraform/test",
	} {
		if got := req.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if req.Header.Get("Idempotency-Key") == "" {
		t.Error("a write should carry an idempotency key")
	}
	if rec.bodies[0] != `{"name":"LOG_LEVEL","value":"debug"}` {
		t.Errorf("unexpected body: %s", rec.bodies[0])
	}
}

func TestReadsCarryNoIdempotencyKey(t *testing.T) {
	c, rec := newTestClient(t, response{status: 200, body: `{"success":true,"data":[]}`})

	if _, err := c.ListSSHKeys(context.Background(), "proj_1"); err != nil {
		t.Fatal(err)
	}
	if rec.requests[0].Header.Get("Idempotency-Key") != "" {
		t.Error("a read should not carry an idempotency key")
	}
}

func TestPathSegmentsAreEscaped(t *testing.T) {
	c, rec := newTestClient(t, response{status: 200, body: `{"success":true,"data":{"id":"x"}}`})

	if _, err := c.GetSecret(context.Background(), "proj_1", "../../admin"); err != nil {
		t.Fatal(err)
	}
	if got := rec.requests[0].URL.EscapedPath(); got != "/v1/projects/proj_1/secrets/..%2F..%2Fadmin" {
		t.Fatalf("path was not escaped: %s", got)
	}
}

func TestNoContentHasNoBody(t *testing.T) {
	c, _ := newTestClient(t, response{status: 204})

	if err := c.DeleteSecret(context.Background(), "proj_1", "sec_1"); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyBodyOnOtherSuccessIsAnError(t *testing.T) {
	c, _ := newTestClient(t, response{status: 200, body: ""})

	_, err := c.GetSecret(context.Background(), "proj_1", "sec_1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "INVALID_RESPONSE" {
		t.Fatalf("expected INVALID_RESPONSE, got %v", err)
	}
}

func TestNullDataIsAnErrorWhenAResultIsExpected(t *testing.T) {
	c, _ := newTestClient(t, response{status: 200, body: `{"success":true,"data":null}`})

	_, err := c.GetVariable(context.Background(), "proj_1", "var_1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "INVALID_RESPONSE" {
		t.Fatalf("expected INVALID_RESPONSE, got %v", err)
	}
}

func TestNullDataIsFineWhenNoResultIsExpected(t *testing.T) {
	c, _ := newTestClient(t, response{status: 200, body: `{"success":true,"data":null}`})

	if err := c.DeleteWebhookEndpoint(context.Background(), "proj_1", "wh_1"); err != nil {
		t.Fatal(err)
	}
}

func TestErrorEnvelopeIsParsed(t *testing.T) {
	c, _ := newTestClient(t, response{
		status: 403,
		body:   `{"success":false,"error":{"code":"INSUFFICIENT_SCOPE","message":"Missing secrets:write","docsUrl":"https://cosmoner.com/docs/api-keys"}}`,
		header: map[string]string{"X-Request-Id": "req_9"},
	})

	_, err := c.CreateSecret(context.Background(), "proj_1", CreateSecretInput{Name: "A", Value: "b"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an APIError, got %v", err)
	}
	if apiErr.Status != 403 || apiErr.Code != "INSUFFICIENT_SCOPE" || apiErr.RequestID != "req_9" || apiErr.DocsURL == "" {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	for _, want := range []string{"Missing secrets:write", "HTTP 403", "https://cosmoner.com/docs/api-keys", "req_9"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message %q does not mention %q", err.Error(), want)
		}
	}
}

func TestNonJSONErrorFallsBackToStatus(t *testing.T) {
	c, _ := newTestClient(t, response{status: 502, body: "<html>Bad Gateway</html>"}, response{status: 502}, response{status: 502})

	_, err := c.GetSecret(context.Background(), "proj_1", "sec_1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 502 || apiErr.Message != "Bad Gateway" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsNotFound(t *testing.T) {
	c, _ := newTestClient(t, response{status: 404, body: `{"success":false,"error":{"code":"NOT_FOUND","message":"Secret not found"}}`})

	_, err := c.GetSecret(context.Background(), "proj_1", "sec_1")
	if !IsNotFound(err) {
		t.Fatalf("expected a not-found error, got %v", err)
	}
	if IsNotFound(errors.New("other")) {
		t.Fatal("a plain error is not a not-found")
	}
}

func TestRateLimitIsRetriedEvenForWrites(t *testing.T) {
	c, rec := newTestClient(t,
		response{status: 429, body: `{"success":false,"error":{"code":"RATE_LIMITED","message":"slow down"}}`},
		response{status: 201, body: `{"success":true,"data":{"id":"sec_1"}}`},
	)

	if _, err := c.CreateSecret(context.Background(), "proj_1", CreateSecretInput{Name: "A", Value: "b"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.requests) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(rec.requests))
	}
	if first, second := rec.requests[0].Header.Get("Idempotency-Key"), rec.requests[1].Header.Get("Idempotency-Key"); first != second {
		t.Fatalf("a retry must reuse the idempotency key: %q then %q", first, second)
	}
}

func TestServerErrorIsNotRetriedForNonIdempotentWrites(t *testing.T) {
	c, rec := newTestClient(t, response{status: 500}, response{status: 201, body: `{"success":true,"data":{}}`})

	if _, err := c.CreateSecret(context.Background(), "proj_1", CreateSecretInput{Name: "A", Value: "b"}); err == nil {
		t.Fatal("expected the 500 to be returned")
	}
	if len(rec.requests) != 1 {
		t.Fatalf("a POST must not be replayed after a 500, got %d attempts", len(rec.requests))
	}
}

func TestServerErrorIsRetriedForIdempotentMethods(t *testing.T) {
	for _, call := range []struct {
		name string
		do   func(*Client) error
	}{
		{"GET", func(c *Client) error { _, err := c.GetSecret(context.Background(), "p", "s"); return err }},
		{"DELETE", func(c *Client) error { return c.DeleteSecret(context.Background(), "p", "s") }},
	} {
		t.Run(call.name, func(t *testing.T) {
			c, rec := newTestClient(t, response{status: 503}, response{status: 503}, response{status: 200, body: `{"success":true,"data":{}}`})
			if err := call.do(c); err != nil {
				t.Fatal(err)
			}
			if len(rec.requests) != 3 {
				t.Fatalf("expected 3 attempts, got %d", len(rec.requests))
			}
		})
	}
}

func TestRetriesStopAtTheLimit(t *testing.T) {
	c, rec := newTestClient(t, response{status: 429}, response{status: 429}, response{status: 429}, response{status: 200, body: `{"success":true,"data":{}}`})

	if _, err := c.GetSecret(context.Background(), "p", "s"); err == nil {
		t.Fatal("expected the last 429 to be returned")
	}
	if len(rec.requests) != 3 {
		t.Fatalf("expected 1 attempt plus 2 retries, got %d", len(rec.requests))
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	c, rec := newTestClient(t, response{status: 400}, response{status: 200, body: `{"success":true,"data":{}}`})

	if _, err := c.GetSecret(context.Background(), "p", "s"); err == nil {
		t.Fatal("expected the 400 to be returned")
	}
	if len(rec.requests) != 1 {
		t.Fatalf("a 400 must not be retried, got %d attempts", len(rec.requests))
	}
}

func TestBackoff(t *testing.T) {
	retryAfter := 3 * time.Second
	if got := backoff(0, &retryAfter); got != retryAfter {
		t.Errorf("Retry-After should win, got %s", got)
	}
	long := time.Minute
	if got := backoff(0, &long); got != backoffCap {
		t.Errorf("Retry-After should be capped, got %s", got)
	}
	for attempt := range 10 {
		if got := backoff(attempt, nil); got < 0 || got > backoffCap {
			t.Errorf("attempt %d: backoff %s out of range", attempt, got)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	header := http.Header{}
	if parseRetryAfter(header) != nil {
		t.Error("absent header should give nil")
	}
	header.Set("Retry-After", "2")
	if got := parseRetryAfter(header); got == nil || *got != 2*time.Second {
		t.Errorf("got %v", got)
	}
	header.Set("Retry-After", "Wed, 21 Oct 2015 07:28:00 GMT")
	if parseRetryAfter(header) != nil {
		t.Error("the HTTP-date form is ignored")
	}
}

func TestTimeoutIsAConnectionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	c, err := New(Config{APIKey: "k", BaseURL: server.URL, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }

	_, err = c.GetSecret(context.Background(), "p", "s")
	var connErr *ConnectionError
	if !errors.As(err, &connErr) || !connErr.Timeout {
		t.Fatalf("expected a timeout, got %v", err)
	}
}

func TestWebhookGetUnwrapsEndpoint(t *testing.T) {
	c, _ := newTestClient(t, response{status: 200, body: `{"success":true,"data":{"endpoint":{"id":"wh_1","events":["app.deployed"]},"stats":{"succeeded":1}}}`})

	endpoint, err := c.GetWebhookEndpoint(context.Background(), "p", "wh_1")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.ID != "wh_1" || len(endpoint.Events) != 1 {
		t.Fatalf("unexpected endpoint: %+v", endpoint)
	}
}

func TestWebhookUpdateSendsNullDescription(t *testing.T) {
	c, rec := newTestClient(t, response{status: 200, body: `{"success":true,"data":{"id":"wh_1"}}`})

	_, err := c.UpdateWebhookEndpoint(context.Background(), "p", "wh_1", UpdateWebhookEndpointInput{Name: "n", URL: "https://x", Events: []string{"app.deployed"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.bodies[0], `"description":null`) {
		t.Fatalf("clearing a description needs an explicit null: %s", rec.bodies[0])
	}
}
