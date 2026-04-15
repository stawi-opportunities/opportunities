package httpx

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

type Client struct {
	httpClient *http.Client
	userAgent  string
}

func New(timeout time.Duration, userAgent string) *Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		userAgent:  userAgent,
	}
}

func (c *Client) Get(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("build request: %w", err)
		}
		if c.userAgent != "" {
			req.Header.Set("User-Agent", c.userAgent)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt == 5 {
				break
			}
			sleepWithJitter(attempt)
			continue
		}
		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt == 5 {
				break
			}
			sleepWithJitter(attempt)
			continue
		}
		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			lastErr = fmt.Errorf("status %d", res.StatusCode)
			if attempt == 5 {
				return body, res.StatusCode, fmt.Errorf("request failed: %w", lastErr)
			}
			sleepWithJitter(attempt)
			continue
		}
		return body, res.StatusCode, nil
	}
	return nil, 0, fmt.Errorf("request failed after retries: %w", lastErr)
}

func sleepWithJitter(attempt int) {
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	if base > 5*time.Minute {
		base = 5 * time.Minute
	}
	jitter := time.Duration(rand.Int63n(int64(base / 2)))
	time.Sleep(base/2 + jitter)
}
