package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// APIError is a response from the API with a non-2xx status, or a 2xx whose
// body could not be read.
type APIError struct {
	Status  int
	Code    string
	Message string
	// Details is the envelope's `details` field verbatim, when present —
	// validation failures list the offending fields there.
	Details   json.RawMessage
	RequestID string
	// DocsURL links a page explaining the fix. The API sends one only for
	// errors the caller can act on, so it is often empty.
	DocsURL string
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (HTTP %d, %s)", e.Message, e.Status, e.Code)
	if len(e.Details) > 0 && string(e.Details) != "null" {
		fmt.Fprintf(&b, "\ndetails: %s", e.Details)
	}
	if e.DocsURL != "" {
		fmt.Fprintf(&b, "\nsee %s", e.DocsURL)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, "\nrequest id: %s", e.RequestID)
	}
	return b.String()
}

// ConnectionError is a request that never produced a response.
type ConnectionError struct {
	Message string
	Timeout bool
}

func (e *ConnectionError) Error() string {
	return "could not reach the Cosmoner API: " + e.Message
}

// IsNotFound reports whether err is the API saying the resource does not
// exist, which a read turns into "remove it from state" rather than a failure.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

type errorEnvelope struct {
	Error *struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
		DocsURL any             `json:"docsUrl"`
	} `json:"error"`
}

// errorFromResponse reads the API's error envelope. Proxies and load
// balancers answer with HTML or nothing at all, so every field is optional and
// the status alone is enough to produce a useful error.
func errorFromResponse(status int, header http.Header, raw []byte) *APIError {
	apiErr := &APIError{
		Status:    status,
		Code:      "UNKNOWN",
		Message:   http.StatusText(status),
		RequestID: header.Get("X-Request-Id"),
	}
	if apiErr.Message == "" {
		apiErr.Message = "Unknown error"
	}

	var env errorEnvelope
	if json.Unmarshal(raw, &env) != nil || env.Error == nil {
		return apiErr
	}
	if env.Error.Code != "" {
		apiErr.Code = env.Error.Code
	}
	if env.Error.Message != "" {
		apiErr.Message = env.Error.Message
	}
	apiErr.Details = env.Error.Details
	if docsURL, ok := env.Error.DocsURL.(string); ok {
		apiErr.DocsURL = docsURL
	}
	return apiErr
}
