package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

func TestOpenAPISpecCoversAllRoutes(t *testing.T) {
	expectedRoutes := []string{
		"GET /workflows",
		"GET /workflows/{id}/graph",
		"GET /workflows/{id}",
		"GET /runs",
		"POST /runs",
		"POST /runs/{id}/cancel",
		"POST /runs/{id}/resume",
		"POST /runs/{id}/retrigger",
		"GET /runs/{id}/events/stream",
		"GET /approvals",
		"POST /approvals/{id}/resolve",
		"GET /runs/{id}/inspect/{aspect}",
		"GET /runs/{id}/nodes/{nodeId}/inspect/{aspect}",
		"GET /runs/{id}/interviews",
		"GET /runs/{id}/interviews/{nodeId}",
		"GET /runs/{id}/compound-graph",
		"GET /runs/{id}/metrics",
		"GET /runs/{id}/execution-group/metrics",
		"GET /runs/{id}/meta",
		"GET /runs/{id}/graph",
		"GET /runs/{id}/document",
		"GET /runs/{id}/document/row/{nodeId}",
		"GET /runs/{id}/session/{sid}",
		"GET /runs/{id}/events",
		"GET /runs/{id}",
		"POST /interrogations",
		"GET /interrogations",
		"POST /interrogations/{id}/ask",
		"GET /health",
	}

	spec := BuildSpec()

	var specRoutes []string
	for path, pathItem := range spec.Paths.Map() {
		if pathItem.Get != nil {
			specRoutes = append(specRoutes, fmt.Sprintf("GET %s", path))
		}
		if pathItem.Post != nil {
			specRoutes = append(specRoutes, fmt.Sprintf("POST %s", path))
		}
		if pathItem.Put != nil {
			specRoutes = append(specRoutes, fmt.Sprintf("PUT %s", path))
		}
		if pathItem.Delete != nil {
			specRoutes = append(specRoutes, fmt.Sprintf("DELETE %s", path))
		}
	}

	sort.Strings(expectedRoutes)
	sort.Strings(specRoutes)

	expectedSet := make(map[string]bool)
	for _, r := range expectedRoutes {
		expectedSet[r] = true
	}
	specSet := make(map[string]bool)
	for _, r := range specRoutes {
		specSet[r] = true
	}

	for _, r := range expectedRoutes {
		if !specSet[r] {
			t.Errorf("route %q is expected but missing from OpenAPI spec", r)
		}
	}
	for _, r := range specRoutes {
		if !expectedSet[r] {
			t.Errorf("route %q is in OpenAPI spec but not in expected list", r)
		}
	}
}

func TestOpenAPISpecIsValid(t *testing.T) {
	spec := BuildSpec()
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("OpenAPI spec validation failed: %v", err)
	}
}

func TestBuildSpecJSON(t *testing.T) {
	data := BuildSpecJSON()
	if len(data) == 0 {
		t.Fatal("BuildSpecJSON returned empty bytes")
	}
	if data[0] != '{' {
		t.Fatalf("expected JSON object, got %q", string(data[:20]))
	}
}

func TestOpenAPIHandler(t *testing.T) {
	specJSON := BuildSpecJSON()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(specJSON)
	})

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected non-empty body")
	}
}
