package client

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

const (
	backoffBase = 500 * time.Millisecond
	backoffCap  = 8 * time.Second
)

// shouldRetry decides whether a failed attempt is sent again.
//
// 429 is always retried: the request was turned away before it was processed,
// so a replay cannot duplicate a side effect. A transport failure or a 5xx is
// different — the API may have done the work before failing to answer — so
// those are only retried for methods HTTP defines as idempotent. Creating a
// secret twice is worse than reporting one failed apply.
func shouldRetry(attempt, maxRetries int, method string, status int, transportFailure bool) bool {
	if attempt >= maxRetries {
		return false
	}
	if status == http.StatusTooManyRequests {
		return true
	}
	if !transportFailure && status < 500 {
		return false
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// backoff returns the delay before the next attempt. A server-supplied
// Retry-After wins outright; otherwise it is exponential with full jitter.
func backoff(attempt int, retryAfter *time.Duration) time.Duration {
	if retryAfter != nil {
		return min(*retryAfter, backoffCap)
	}
	ceiling := min(backoffBase<<attempt, backoffCap)
	return time.Duration(rand.Int64N(int64(ceiling) + 1))
}

// parseRetryAfter reads a Retry-After given in seconds. The HTTP-date form is
// ignored: it is rare, and jittered backoff is a fine fallback.
func parseRetryAfter(header http.Header) *time.Duration {
	raw := header.Get("Retry-After")
	if raw == "" {
		return nil
	}
	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil || seconds < 0 {
		return nil
	}
	d := time.Duration(seconds * float64(time.Second))
	return &d
}
