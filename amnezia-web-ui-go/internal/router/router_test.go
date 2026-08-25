package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
)

func TestRouterHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		AppVersion: "1.0.0",
		Host:       "0.0.0.0",
		Port:       5000,
	}

	r := NewRouter(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
	if resp.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", resp.Version)
	}
}

func TestRouterVersionEndpoint(t *testing.T) {
	cfg := &config.Config{
		AppVersion: "2.1.0",
	}

	r := NewRouter(cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["version"] != "2.1.0" {
		t.Errorf("expected version '2.1.0', got %q", resp["version"])
	}
}

func TestNewServer(t *testing.T) {
	cfg := &config.Config{
		Host: "127.0.0.1",
		Port: 9999,
	}
	r := NewRouter(cfg, nil)
	srv := NewServer(cfg, r)

	if srv.httpServer.Addr != "127.0.0.1:9999" {
		t.Errorf("expected Addr 127.0.0.1:9999, got %s", srv.httpServer.Addr)
	}
}
