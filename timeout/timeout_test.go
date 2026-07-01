package timeout_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maxclav/middleware/timeout"
)

// newMiddleware builds the timeout middleware, failing the test if construction
// returns an error.
func newMiddleware(t *testing.T, opts ...timeout.Option) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := timeout.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

func TestSlowHandlerTimesOut(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, timeout.WithTimeout(10*time.Millisecond))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Body.String(); got != "request timed out" {
		t.Fatalf("body = %q, want %q", got, "request timed out")
	}
}

func TestCustomMessage(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t,
		timeout.WithTimeout(10*time.Millisecond),
		timeout.WithMessage("too slow"),
	)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if got := rec.Body.String(); got != "too slow" {
		t.Fatalf("body = %q, want %q", got, "too slow")
	}
}

func TestFastHandlerPassesThrough(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, timeout.WithTimeout(time.Second))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "ok")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusTeapot || rec.Body.String() != "ok" {
		t.Fatalf("passthrough broken: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestInvalidTimeoutRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []timeout.Option
	}{
		{"missing timeout", nil},
		{"zero timeout", []timeout.Option{timeout.WithTimeout(0)}},
		{"negative timeout", []timeout.Option{timeout.WithTimeout(-time.Second)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := timeout.New(tt.opts...); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}
