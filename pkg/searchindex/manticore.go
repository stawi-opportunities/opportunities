// Package searchindex is a narrow HTTP client for Manticore Search.
// Manticore exposes a JSON API on port 9308 — we use /sql for DDL
// and introspection, /replace + /update for row upserts, and /search
// for queries. Using the HTTP API (rather than the MySQL wire
// protocol) keeps the dependency footprint to stdlib net/http.
package searchindex

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
)

// Config describes how to reach Manticore.
type Config struct {
	// URL is the root of Manticore's HTTP endpoint,
	// e.g. "http://manticore:9308". Trailing slashes are trimmed.
	URL string

	// Timeout bounds every request. Default 10s.
	Timeout time.Duration
}

// Client is the high-level Manticore handle. Safe for concurrent use
// (delegates to http.Client which is goroutine-safe).
type Client struct {
	http *http.Client
	base string
}

// Open constructs a Client. No network I/O — call Ping to verify
// reachability.
func Open(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("searchindex: empty URL")
	}
	t := cfg.Timeout
	if t == 0 {
		t = 10 * time.Second
	}
	return &Client{
		http: &http.Client{Timeout: t},
		base: strings.TrimRight(cfg.URL, "/"),
	}, nil
}

// Close is a no-op for the HTTP client but kept for symmetry with
// other resource-owning packages.
func (c *Client) Close() error { return nil }

// Ping checks that Manticore is reachable. Uses the /sql endpoint
// with a cheap SHOW STATUS query because there's no universally-
// available dedicated ping endpoint across Manticore versions.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.SQL(ctx, "SHOW STATUS")
	return err
}

// SQL runs an arbitrary SQL statement via /sql?mode=raw. Used for
// DDL (CREATE TABLE, DROP TABLE) and introspection (SHOW TABLES).
// Returns the raw response body — callers parse as needed.
func (c *Client) SQL(ctx context.Context, stmt string) ([]byte, error) {
	body := strings.NewReader("query=" + url.QueryEscape(stmt))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/sql?mode=raw", body)
	if err != nil {
		return nil, fmt.Errorf("searchindex: sql req: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searchindex: sql send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("searchindex: sql status %d: %s", resp.StatusCode, string(out))
	}
	// Manticore sometimes returns 200 with a non-empty JSON error field like
	// [{"error":"table idx_jobs_rt already exists"}] — treat that as an
	// error so callers can detect it. The status-query response includes
	// "error":"" (empty string), so we must skip blank error values.
	if isManticoreError(out) {
		return out, fmt.Errorf("searchindex: sql error: %s", strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Replace inserts or replaces a document by id. Uses /replace JSON.
// Manticore treats replace as upsert; providing a stable id makes
// the operation idempotent.
func (c *Client) Replace(ctx context.Context, index string, id uint64, doc map[string]any) error {
	return c.postJSON(ctx, "/replace", map[string]any{
		"index": index,
		"id":    id,
		"doc":   doc,
	})
}

// Update patches named fields on an existing document. Uses /update.
// A missing id is a no-op (Manticore returns {updated: 0}).
func (c *Client) Update(ctx context.Context, index string, id uint64, doc map[string]any) error {
	return c.postJSON(ctx, "/update", map[string]any{
		"index": index,
		"id":    id,
		"doc":   doc,
	})
}

// Search issues a JSON query against /search. Returns the raw
// response body — callers parse hits as needed (shape documented
// at https://manual.manticoresearch.com/Searching/Intro).
func (c *Client) Search(ctx context.Context, query map[string]any) ([]byte, error) {
	raw, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("searchindex: search marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/search", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("searchindex: search req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searchindex: search send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("searchindex: search status %d: %s", resp.StatusCode, string(out))
	}
	return out, nil
}

func (c *Client) postJSON(ctx context.Context, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("searchindex: marshal %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("searchindex: req %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("searchindex: send %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("searchindex: %s status %d: %s", path, resp.StatusCode, string(msg))
	}
	return nil
}

// isManticoreError returns true when the response body contains a
// Manticore JSON error with a non-empty error field. Manticore always
// includes an "error" key in /sql?mode=raw responses, but it is an
// empty string on success; only a non-empty value signals failure.
func isManticoreError(body []byte) bool {
	// Fast path: no "error" key at all.
	if !bytes.Contains(body, []byte(`"error"`)) {
		return false
	}
	// Parse as array-of-objects (mode=raw wraps results in an array).
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		// Not the expected shape — fall back to object.
		var obj map[string]json.RawMessage
		if err2 := json.Unmarshal(body, &obj); err2 != nil {
			// Unparseable — be conservative and treat as error.
			return true
		}
		rows = []map[string]json.RawMessage{obj}
	}
	for _, row := range rows {
		raw, ok := row["error"]
		if !ok {
			continue
		}
		var msg string
		if err := json.Unmarshal(raw, &msg); err != nil {
			return true // non-string error value
		}
		if msg != "" {
			return true
		}
	}
	return false
}
