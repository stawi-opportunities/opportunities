package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"stawi.jobs/apps/crawler/service"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeQualityResetter struct {
	n   int64
	err error
}

func (f *fakeQualityResetter) ResetQualityWindowAll(_ context.Context) (int64, error) {
	return f.n, f.err
}

type fakeHealthDecayer struct {
	n   int64
	err error
}

func (f *fakeHealthDecayer) DecayHealth(_ context.Context, _ float64) (int64, error) {
	return f.n, f.err
}

// ── QualityResetHandler ───────────────────────────────────────────────────────

func TestQualityResetHandler_Happy(t *testing.T) {
	h := service.QualityResetHandler(&fakeQualityResetter{n: 42})
	req := httptest.NewRequest(http.MethodPost, "/admin/sources/quality-reset", nil)
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("expected ok=true, got %v", body["ok"])
	}
	// JSON numbers unmarshal as float64
	if updated, _ := body["updated"].(float64); updated != 42 {
		t.Errorf("expected updated=42, got %v", body["updated"])
	}
}

func TestQualityResetHandler_Error(t *testing.T) {
	h := service.QualityResetHandler(&fakeQualityResetter{err: errors.New("db down")})
	req := httptest.NewRequest(http.MethodPost, "/admin/sources/quality-reset", nil)
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// ── HealthDecayHandler ────────────────────────────────────────────────────────

func TestHealthDecayHandler_Happy(t *testing.T) {
	h := service.HealthDecayHandler(&fakeHealthDecayer{n: 7})
	req := httptest.NewRequest(http.MethodPost, "/admin/sources/health-decay", nil)
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("expected ok=true, got %v", body["ok"])
	}
	if updated, _ := body["updated"].(float64); updated != 7 {
		t.Errorf("expected updated=7, got %v", body["updated"])
	}
}

func TestHealthDecayHandler_Error(t *testing.T) {
	h := service.HealthDecayHandler(&fakeHealthDecayer{err: errors.New("connection reset")})
	req := httptest.NewRequest(http.MethodPost, "/admin/sources/health-decay", nil)
	rec := httptest.NewRecorder()

	h(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
