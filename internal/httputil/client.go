// Package httputil provides a shared HTTP client with retry and timeout support.
package httputil

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/coolcake/cvkeharness/internal/log"
)

// DefaultTimeout is the default timeout for HTTP requests.
const DefaultTimeout = 120 * time.Second

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryConfig returns sensible retry defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    30 * time.Second,
	}
}

// Client wraps http.Client with retry logic.
type Client struct {
	inner *http.Client
	retry RetryConfig
}

// NewClient creates a new HTTP client with the given timeout and retry config.
func NewClient(timeout time.Duration, retry RetryConfig) *Client {
	return &Client{
		inner: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		retry: retry,
	}
}

// NewDefaultClient creates a client with default settings.
func NewDefaultClient() *Client {
	return NewClient(DefaultTimeout, DefaultRetryConfig())
}

// Do executes an HTTP request with retry on transient failures.
// Retries on 429 (rate limit) and 5xx server errors.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var lastErr error
	var resp *http.Response

	for attempt := 0; attempt < c.retry.MaxAttempts; attempt++ {
		if attempt > 0 {
			delay := c.backoffDelay(attempt)
			logger := log.FromContext(req.Context())
			logger.Warn("retrying request",
				"attempt", attempt+1,
				"delay", delay,
				"url", req.URL.String(),
			)
			time.Sleep(delay)
		}

		var err error
		resp, err = c.inner.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed (attempt %d/%d): %w", attempt+1, c.retry.MaxAttempts, err)
			continue
		}

		// Don't retry on success or client errors (except 429)
		if resp.StatusCode < 429 || (resp.StatusCode > 429 && resp.StatusCode < 500) {
			return resp, nil
		}

		// Retryable: 429 or 5xx
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d (attempt %d/%d)", resp.StatusCode, attempt+1, c.retry.MaxAttempts)
			resp.Body.Close()
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("all %d attempts exhausted: %w", c.retry.MaxAttempts, lastErr)
}

// backoffDelay calculates exponential backoff with a cap.
func (c *Client) backoffDelay(attempt int) time.Duration {
	delay := time.Duration(float64(c.retry.BaseDelay) * math.Pow(2, float64(attempt-1)))
	if delay > c.retry.MaxDelay {
		delay = c.retry.MaxDelay
	}
	return delay
}
