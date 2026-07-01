package healthcheck_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxclav/middleware/healthcheck"
)

// passThrough is a next handler that always responds 418, so tests can tell
// when a request fell through the healthcheck middleware.
func passThrough() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

// newMiddleware builds the healthcheck middleware, failing the test if
// construction returns an error.
func newMiddleware(t *testing.T, opts ...healthcheck.Option) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := healthcheck.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

// serve runs the middleware-wrapped passThrough handler against a request and
// returns the recorder.
func serve(t *testing.T, method, path string, opts ...healthcheck.Option) *httptest.ResponseRecorder {
	t.Helper()
	h := newMiddleware(t, opts...)(passThrough())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, http.NoBody))
	return rec
}

func TestServesEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		opts       []healthcheck.Option
		wantStatus int
		wantBody   string
	}{
		{
			name:       "liveness returns ok",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
		{
			name:       "readiness with no checks returns ok",
			method:     http.MethodGet,
			path:       "/readyz",
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
		{
			name:       "HEAD on liveness is handled",
			method:     http.MethodHead,
			path:       "/healthz",
			wantStatus: http.StatusOK,
		},
		{
			name:       "unrelated path falls through",
			method:     http.MethodGet,
			path:       "/api/users",
			wantStatus: http.StatusTeapot,
		},
		{
			name:       "non-GET/HEAD method falls through",
			method:     http.MethodPost,
			path:       "/healthz",
			wantStatus: http.StatusTeapot,
		},
		{
			name:   "custom liveness path",
			method: http.MethodGet,
			path:   "/live",
			opts: []healthcheck.Option{
				healthcheck.WithLivenessPath("/live"),
				healthcheck.WithReadinessPath("/ready"),
			},
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
		{
			name:   "custom readiness path",
			method: http.MethodGet,
			path:   "/ready",
			opts: []healthcheck.Option{
				healthcheck.WithLivenessPath("/live"),
				healthcheck.WithReadinessPath("/ready"),
			},
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serve(t, tt.method, tt.path, tt.opts...)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" {
				if got := rec.Body.String(); got != tt.wantBody {
					t.Fatalf("body = %q, want %q", got, tt.wantBody)
				}
			}
		})
	}
}

func TestReadinessFailingCheck(t *testing.T) {
	t.Parallel()

	rec := serve(t, http.MethodGet, "/readyz",
		healthcheck.WithReadinessCheck("db", func(context.Context) error {
			return errors.New("connection refused")
		}),
		healthcheck.WithReadinessCheck("cache", func(context.Context) error {
			return nil
		}),
	)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "db") || !strings.Contains(body, "connection refused") {
		t.Fatalf("body %q should name the failing check and error", body)
	}
	if strings.Contains(body, "cache") {
		t.Fatalf("body %q should not list the passing check", body)
	}
}

func TestReadinessCheckReceivesRequestContext(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}
	var gotValue any
	mw := newMiddleware(t, healthcheck.WithReadinessCheck("ctx", func(ctx context.Context) error {
		gotValue = ctx.Value(ctxKey{})
		return nil
	}))
	h := mw(passThrough())

	req := httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, "present"))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotValue != "present" {
		t.Fatalf("check saw context value %v, want %q", gotValue, "present")
	}
}

func TestInvalidOptionsRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opt  healthcheck.Option
	}{
		{"empty liveness path", healthcheck.WithLivenessPath("")},
		{"liveness path without slash", healthcheck.WithLivenessPath("healthz")},
		{"empty readiness path", healthcheck.WithReadinessPath("")},
		{"readiness path without slash", healthcheck.WithReadinessPath("readyz")},
		{"empty check name", healthcheck.WithReadinessCheck("", func(context.Context) error { return nil })},
		{"nil check function", healthcheck.WithReadinessCheck("db", nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := healthcheck.New(tt.opt); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}
