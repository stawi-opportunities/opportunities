// Package httpx provides a resilient HTTP client with automatic retries,
// exponential backoff, and jitter for use by connector implementations.
package httpx

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

const (
	maxAttempts    = 5
	baseBackoff    = 200 * time.Millisecond
	backoffFactor  = 2.0
	maxBackoff     = 30 * time.Second
	jitterFraction = 0.3 // ±30 % of computed backoff
)

// Client is a resilient HTTP client that retries on transient failures.
type Client struct {
	http      *http.Client
	userAgent string
}

// NewClient creates a Client with the given request timeout and User-Agent.
func NewClient(timeout time.Duration, userAgent string) *Client {
	return &Client{
		http:      &http.Client{Timeout: timeout},
		userAgent: userAgent,
	}
}

// Get performs a GET request to url, attaching any extra headers supplied.
// It retries up to 5 times on HTTP 429 or 5xx responses, using exponential
// backoff with jitter between attempts. The raw response body, HTTP status
// code, and any error are returned.
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	var (
		lastStatus int
		lastErr    error
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			wait := computeBackoff(attempt)
			select {
			case <-ctx.Done():
				return nil, lastStatus, ctx.Err()
			case <-time.After(wait):
			}
		}

		body, status, err := c.doGet(ctx, url, headers)
		if err != nil {
			// Network-level error — retry.
			lastErr = err
			lastStatus = 0
			continue
		}

		if !shouldRetry(status) {
			return body, status, nil
		}

		// Retryable HTTP status — drain body and retry.
		lastStatus = status
		lastErr = fmt.Errorf("retryable HTTP status %d", status)
	}

	return nil, lastStatus, fmt.Errorf("all %d attempts failed for %s: %w", maxAttempts, url, lastErr)
}

// doGet performs a single HTTP GET without any retry logic.
func (c *Client) doGet(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB max
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}

	return body, resp.StatusCode, nil
}

// Verify checks whether url is reachable without downloading the body. It
// tries HEAD first and falls back to a GET with a Range: bytes=0-0 header for
// hosts that reject HEAD (Cloudflare's bot gateway is a common culprit). The
// returned status is the best observed status across both attempts; a zero
// status with a non-nil error means the request never reached a server.
//
// This is deliberately single-shot (no retries) and uses a short timeout —
// the scheduler calls it before every crawl dispatch, and we don't want a
// slow probe to hold up the queue.
func (c *Client) Verify(ctx context.Context, url string) (int, error) {
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, method := range []string{http.MethodHead, http.MethodGet} {
		req, err := http.NewRequestWithContext(vctx, method, url, nil)
		if err != nil {
			return 0, fmt.Errorf("build %s request: %w", method, err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		if method == http.MethodGet {
			req.Header.Set("Range", "bytes=0-0")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			// Network-level failure on HEAD — try GET before giving up.
			if method == http.MethodHead {
				continue
			}
			return 0, err
		}
		_ = resp.Body.Close()

		// 4xx/5xx on HEAD is common (method not allowed, anti-bot), so
		// only treat the HEAD response as definitive when it's a 2xx/3xx
		// or 401/403 (which mean the host is up). Otherwise, try GET.
		if method == http.MethodHead {
			switch {
			case resp.StatusCode >= 200 && resp.StatusCode < 400:
				return resp.StatusCode, nil
			case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
				return resp.StatusCode, nil
			default:
				continue
			}
		}
		return resp.StatusCode, nil
	}

	return 0, fmt.Errorf("verify %s: no successful probe", url)
}

// shouldRetry reports whether the HTTP status code warrants a retry.
func shouldRetry(status int) bool {
	return status == http.StatusTooManyRequests || (status >= 500 && status < 600)
}

// computeBackoff returns the sleep duration for the given (1-based) retry
// attempt using exponential backoff capped at maxBackoff, plus random jitter.
func computeBackoff(attempt int) time.Duration {
	backoff := float64(baseBackoff)
	for i := 1; i < attempt; i++ {
		backoff *= backoffFactor
		if time.Duration(backoff) > maxBackoff {
			backoff = float64(maxBackoff)
			break
		}
	}

	// Add ±jitterFraction of the backoff as jitter.
	jitter := backoff * jitterFraction * (rand.Float64()*2 - 1) //nolint:gosec
	result := time.Duration(backoff + jitter)
	if result < 0 {
		result = 0
	}
	return result
}
