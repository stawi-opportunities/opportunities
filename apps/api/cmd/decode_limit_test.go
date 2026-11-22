package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON_RejectsOversizedBody(t *testing.T) {
	big := `{"reason":"` + strings.Repeat("a", maxJSONBodyBytes+1) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(big))
	var dst struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(req, &dst); err == nil {
		t.Fatal("decodeJSON accepted a body larger than maxJSONBodyBytes")
	}
}

func TestDecodeJSON_AcceptsNormalBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"reason":"scam"}`))
	var dst struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(req, &dst); err != nil || dst.Reason != "scam" {
		t.Fatalf("decodeJSON = %v, reason %q", err, dst.Reason)
	}
}
