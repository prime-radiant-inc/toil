package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogRequestsMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := LogRequests(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("not valid JSON: %v\nraw: %s", err, buf.String())
	}
	if entry["msg"] != "toil.http.request" {
		t.Fatalf("expected msg=toil.http.request, got %v", entry["msg"])
	}
	if entry["method"] != "GET" {
		t.Fatalf("expected method=GET, got %v", entry["method"])
	}
	if entry["path"] != "/workflows" {
		t.Fatalf("expected path=/workflows, got %v", entry["path"])
	}
	status, ok := entry["status"].(float64)
	if !ok || int(status) != 200 {
		t.Fatalf("expected status=200, got %v", entry["status"])
	}
}

func TestLogRequestsSkipsHealthCheck(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := LogRequests(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if buf.Len() != 0 {
		t.Fatalf("expected no log output for /health, got: %s", buf.String())
	}
}

func TestLogRequestsPreservesFlusher(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	var flusherOK bool
	handler := LogRequests(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, flusherOK = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/runs/test/events/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !flusherOK {
		t.Fatal("expected wrapped ResponseWriter to implement http.Flusher")
	}
}
