package handlers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// JSON round-trip: marshal → unmarshal → compare. If any `json:"..."` tag
// changes, this test catches it.
func TestCrawlRequestPayload_roundTrip(t *testing.T) {
	in := CrawlRequestPayload{SourceID: 42, Attempt: 3}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"source_id":42`) {
		t.Errorf("expected source_id tag in %s", raw)
	}
	if !strings.Contains(string(raw), `"attempt":3`) {
		t.Errorf("expected attempt tag in %s", raw)
	}

	var out CrawlRequestPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch: %+v vs %+v", in, out)
	}
}

// Attempt is omitempty — at zero it should not appear in the JSON output.
func TestCrawlRequestPayload_attemptOmitEmpty(t *testing.T) {
	raw, err := json.Marshal(CrawlRequestPayload{SourceID: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "attempt") {
		t.Errorf("Attempt=0 should be omitted, got %s", raw)
	}
}

func TestJobPublishedPayload_roundTrip(t *testing.T) {
	in := JobPublishedPayload{
		CanonicalJobID: 101,
		Slug:           "backend-eng",
		SourceLang:     "en",
		R2Version:      2,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"canonical_job_id":101`,
		`"slug":"backend-eng"`,
		`"source_lang":"en"`,
		`"r2_version":2`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("payload missing %q: %s", want, raw)
		}
	}

	var out JobPublishedPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch: %+v vs %+v", in, out)
	}
}

func TestJobReadyPayload_roundTrip(t *testing.T) {
	in := JobReadyPayload{CanonicalJobID: 7}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"canonical_job_id":7`) {
		t.Errorf("expected canonical_job_id tag: %s", raw)
	}

	var out JobReadyPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch: %+v vs %+v", in, out)
	}
}
