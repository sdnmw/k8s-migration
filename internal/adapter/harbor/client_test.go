package harbor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsurePublicProjectCreatesApplicationProject(t *testing.T) {
	var path string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "harbor.example:5443", "sks-compose", "admin", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := client.EnsurePublicProject(context.Background(), "Online Mall")
	if err != nil {
		t.Fatal(err)
	}
	if repository != "harbor.example:5443/sks-compose-online-mall" || path != "/api/v2.0/projects" {
		t.Fatalf("unexpected repository/path %q %q", repository, path)
	}
}

func TestEnsurePublicProjectMakesExistingProjectPublic(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if r.Method != http.MethodPut || r.URL.Path != "/api/v2.0/projects/sks-compose-shop" {
			t.Fatalf("unexpected update %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "harbor.example", "sks-compose", "admin", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.EnsurePublicProject(context.Background(), "shop"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected create and update, got %d calls", calls)
	}
}

func TestEnsurePublicProjectFallsBackToLibrary(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "harbor.example", "sks-compose", "admin", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := client.EnsurePublicProject(context.Background(), "shop")
	if err == nil || repository != "harbor.example/library" {
		t.Fatalf("fallback = %q, %v", repository, err)
	}
}
