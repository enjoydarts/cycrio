package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLivenessDoesNotDependOnDatabase(t *testing.T) {
	handler := NewHandler(func(context.Context) error { return errors.New("database unavailable") })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatalf("liveness = %d %q", response.Code, response.Body.String())
	}
}

func TestReadinessReflectsDependencyWithoutLeakingError(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"ready", nil, http.StatusOK, "ok\n"},
		{"unavailable", errors.New("postgres://private:secret@database"), http.StatusServiceUnavailable, "not ready\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(func(ctx context.Context) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("dependency check requires deadline")
				}
				return test.err
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf("readiness = %d %q", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatal("leaked dependency error")
			}
		})
	}
}

func TestUnknownRouteIsNotHealthy(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(func(context.Context) error { return nil }).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}
