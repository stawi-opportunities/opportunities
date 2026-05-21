package httpmw

import (
	"encoding/json"
	"net/http"
)

// Problem is the RFC 9457 problem+json envelope.
type Problem struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// ProblemJSON writes an application/problem+json response.
func ProblemJSON(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type: "about:blank", Title: title, Status: status, Detail: detail,
	})
}

// ProblemJSONExtra writes a problem+json response with extra fields
// merged into the envelope (e.g., {"allowed": [...]} on 409).
func ProblemJSONExtra(w http.ResponseWriter, status int, title, detail string, extra map[string]any) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	body := map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	}
	for k, v := range extra {
		body[k] = v
	}
	_ = json.NewEncoder(w).Encode(body)
}
