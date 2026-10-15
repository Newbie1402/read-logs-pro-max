package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
