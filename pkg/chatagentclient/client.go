// Package chatagentclient is a Connect-JSON client for the platform chat-agent
// service (service-profile/apps/chatagent). Matching is the first consumer.
//
// Wire protocol: Connect unary over HTTP with application/json bodies.
// Base URL is typically https://api.stawi.org/chat-agent (prefix stripped at edge).
package chatagentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to ChatAgentService.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	// TokenSource optionally injects Authorization Bearer for S2S.
	TokenSource func(ctx context.Context) (string, error)
}

// New returns a client. httpClient may be nil.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: httpClient,
	}
}

// FieldDef matches chatagent.v1.FieldDef JSON (snake_case Connect).
type FieldDef struct {
	Name          string   `json:"name"`
	Type          string   `json:"type,omitempty"` // FIELD_TYPE_STRING etc.
	Required      bool     `json:"required,omitempty"`
	Priority      int      `json:"priority,omitempty"`
	Description   string   `json:"description,omitempty"`
	EnumValues    []string `json:"enum_values,omitempty"`
	MinLength     int      `json:"min_length,omitempty"`
	Ask           string   `json:"ask,omitempty"`
	Why           string   `json:"why,omitempty"`
	EvidenceHints []string `json:"evidence_hints,omitempty"`
}

// ContextDefinition is the product-only configuration unit.
type ContextDefinition struct {
	ContextKey   string     `json:"context_key"`
	Purpose      string     `json:"purpose"`
	SystemPrompt string     `json:"system_prompt,omitempty"`
	Fields       []FieldDef `json:"fields"`
	ExtractRules string     `json:"extract_rules,omitempty"`
	ReplyPolicy  *struct {
		MaxSentences      int32  `json:"max_sentences,omitempty"`
		AskOneMissingOnly bool   `json:"ask_one_missing_only,omitempty"`
		CompleteMessage   string `json:"complete_message,omitempty"`
	} `json:"reply_policy,omitempty"`
}

// DocumentEvidence is prior material (CV text, notes).
type DocumentEvidence struct {
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"`
	Text string `json:"text,omitempty"`
}

// ChatMessage is one transcript turn.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// FieldStatus is per-field readiness.
type FieldStatus struct {
	OK     bool   `json:"ok"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// ChatSession is session state returned by the service.
type ChatSession struct {
	ID             string                 `json:"id"`
	SubjectID      string                 `json:"subject_id"`
	ContextKey     string                 `json:"context_key"`
	ContextVersion int                    `json:"context_version"`
	Fields         map[string]any         `json:"fields"`
	Messages       []ChatMessage          `json:"messages"`
	Ready          bool                   `json:"ready"`
	Status         string                 `json:"status"`
	Missing        []string               `json:"missing"`
	FieldStatus    map[string]FieldStatus `json:"field_status"`
	Runtime        map[string]any         `json:"runtime"`
}

// UpsertContext registers a versioned context definition.
func (c *Client) UpsertContext(ctx context.Context, def ContextDefinition) (version int, err error) {
	var out struct {
		Version int `json:"version"`
	}
	err = c.call(ctx, "UpsertContext", map[string]any{"definition": def}, &out)
	return out.Version, err
}

// GetContext loads a context (latest when version==0).
func (c *Client) GetContext(ctx context.Context, key string, version int) (*ContextDefinition, int, error) {
	var out struct {
		Definition ContextDefinition `json:"definition"`
		Version    int               `json:"version"`
	}
	err := c.call(ctx, "GetContext", map[string]any{
		"context_key": key,
		"version":     version,
	}, &out)
	if err != nil {
		return nil, 0, err
	}
	return &out.Definition, out.Version, nil
}

// CreateSessionRequest opens an intake session.
type CreateSessionRequest struct {
	SubjectID        string             `json:"subject_id"`
	ContextKey       string             `json:"context_key,omitempty"`
	ContextVersion   int                `json:"context_version,omitempty"`
	InlineConfig     *ContextDefinition `json:"inline_config,omitempty"`
	SeedFields       map[string]any     `json:"seed_fields,omitempty"`
	Documents        []DocumentEvidence `json:"documents,omitempty"`
	SeedMessages     []ChatMessage      `json:"seed_messages,omitempty"`
	Runtime          map[string]any     `json:"runtime,omitempty"`
	EvaluateEvidence bool               `json:"evaluate_evidence,omitempty"`
}

// CreateSession starts a session.
func (c *Client) CreateSession(ctx context.Context, req CreateSessionRequest) (*ChatSession, error) {
	var out struct {
		Session ChatSession `json:"session"`
	}
	if err := c.call(ctx, "CreateSession", req, &out); err != nil {
		return nil, err
	}
	return &out.Session, nil
}

// GetSession loads session state.
func (c *Client) GetSession(ctx context.Context, sessionID string) (*ChatSession, error) {
	var out struct {
		Session ChatSession `json:"session"`
	}
	if err := c.call(ctx, "GetSession", map[string]any{"session_id": sessionID}, &out); err != nil {
		return nil, err
	}
	return &out.Session, nil
}

// TurnRequest is one conversational step.
type TurnRequest struct {
	SessionID  string             `json:"session_id"`
	Message    string             `json:"message,omitempty"`
	Structured map[string]any     `json:"structured,omitempty"`
	Documents  []DocumentEvidence `json:"documents,omitempty"`
}

// TurnResponse is the turn outcome.
type TurnResponse struct {
	Session *ChatSession `json:"session"`
	Reply   string       `json:"reply"`
	Source  string       `json:"source"`
}

// Turn runs one collection step.
func (c *Client) Turn(ctx context.Context, req TurnRequest) (*TurnResponse, error) {
	var out TurnResponse
	if err := c.call(ctx, "Turn", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) call(ctx context.Context, method string, body any, out any) error {
	if c == nil || c.BaseURL == "" {
		return fmt.Errorf("chatagentclient: not configured")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("chatagentclient: marshal: %w", err)
	}
	url := c.BaseURL + "/chatagent.v1.ChatAgentService/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("chatagentclient: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	if c.TokenSource != nil {
		tok, terr := c.TokenSource(ctx)
		if terr != nil {
			return fmt.Errorf("chatagentclient: token: %w", terr)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("chatagentclient: do: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("chatagentclient: %s status %d: %s", method, resp.StatusCode, string(b))
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("chatagentclient: decode %s: %w", method, err)
	}
	return nil
}
