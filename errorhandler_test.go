package middleware_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxclav/middleware"
)

func TestDefaultErrorHandler(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

	middleware.DefaultErrorHandler(rr, req, http.StatusTeapot, errors.New("boom"))

	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusTeapot)
	}
	if got, want := strings.TrimSpace(rr.Body.String()), http.StatusText(http.StatusTeapot); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

// TestDefaultErrorHandlerToleratesNilError documents that a nil err is valid:
// middlewares may reject a request without an underlying error value.
func TestDefaultErrorHandlerToleratesNilError(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

	middleware.DefaultErrorHandler(rr, req, http.StatusForbidden, nil)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}
