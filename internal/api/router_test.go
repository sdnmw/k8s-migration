package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(NewRouter(Dependencies{Version: "test", Environment: "test", Logger: logger}))
	defer server.Close()

	response, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("expected security headers")
	}
}

func TestSystemInfo(t *testing.T) {
	server := httptest.NewServer(NewRouter(Dependencies{Version: "1.2.3", Environment: "test"}))
	defer server.Close()

	response, err := http.Get(server.URL + "/api/v1/system/info")
	if err != nil {
		t.Fatalf("GET /api/v1/system/info: %v", err)
	}
	defer response.Body.Close()

	var value systemInfo
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if value.Name != "SKS Migration Center" || value.Version != "1.2.3" {
		t.Fatalf("unexpected response: %+v", value)
	}
}

func TestReadyEndpointReportsDependencyFailure(t *testing.T) {
	server := httptest.NewServer(NewRouter(Dependencies{Readiness: func(context.Context) error {
		return errors.New("database unavailable")
	}}))
	defer server.Close()

	response, err := http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", response.StatusCode)
	}
	if response.Header.Get("Content-Type") != "application/problem+json; charset=utf-8" {
		t.Fatalf("unexpected content type: %s", response.Header.Get("Content-Type"))
	}
}

func TestPrometheusMetricsEndpoint(t *testing.T) {
	server := httptest.NewServer(NewRouter(Dependencies{}))
	defer server.Close()
	if response, err := http.Get(server.URL + "/healthz"); err != nil {
		t.Fatal(err)
	} else {
		_ = response.Body.Close()
	}
	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `route="GET /healthz"`) {
		t.Fatalf("health metric missing:\n%s", body)
	}
}
