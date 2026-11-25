package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"readlogs/internal/config"
)

func TestHealthIsPublicWhenAppPasswordIsEnabled(t *testing.T) {
	app := &App{authPass: "secret"}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	res := httptest.NewRecorder()

	app.authGate(http.HandlerFunc(handleHealth)).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Body.String(); got != "ok\n" {
		t.Fatalf("health body = %q, want %q", got, "ok\n")
	}
}

func TestShutdownIsLocalOnly(t *testing.T) {
	app := &App{
		cfg:        &config.Config{Host: "127.0.0.1"},
		shutdownCh: make(chan struct{}),
	}
	res := httptest.NewRecorder()
	app.handleShutdown(res, httptest.NewRequest(http.MethodPost, "/api/shutdown", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("shutdown status = %d, want %d", res.Code, http.StatusOK)
	}
	select {
	case <-app.shutdownCh:
	default:
		t.Fatal("shutdown request did not notify main")
	}
}

func TestShutdownRejectsPublicHost(t *testing.T) {
	app := &App{
		cfg:        &config.Config{Host: "0.0.0.0"},
		shutdownCh: make(chan struct{}),
	}
	res := httptest.NewRecorder()
	app.handleShutdown(res, httptest.NewRequest(http.MethodPost, "/api/shutdown", nil))
	if res.Code != http.StatusForbidden {
		t.Fatalf("public shutdown status = %d, want %d", res.Code, http.StatusForbidden)
	}
}
