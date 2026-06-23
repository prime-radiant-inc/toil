package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	handler := HealthHandler(func() (int, int) { return 3, 41 })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", body["status"])
	}
	active, ok := body["active_runs"].(float64)
	if !ok || int(active) != 3 {
		t.Fatalf("expected active_runs=3, got %v", body["active_runs"])
	}
	total, ok := body["total_runs"].(float64)
	if !ok || int(total) != 41 {
		t.Fatalf("expected total_runs=41, got %v", body["total_runs"])
	}
}
