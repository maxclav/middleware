package skipif_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware"
	"github.com/maxclav/middleware/skipif"
)

// markingMiddleware records whether it ran by flipping ran to true, then calls
// the next handler.
func markingMiddleware(ran *bool) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*ran = true
			next.ServeHTTP(w, r)
		})
	}
}

func TestSkipControlsMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		skip        skipif.Predicate
		wantMWRan   bool
		wantNextRan bool
	}{
		{
			name:        "skip true bypasses middleware",
			skip:        func(*http.Request) bool { return true },
			wantMWRan:   false,
			wantNextRan: true,
		},
		{
			name:        "skip false runs middleware",
			skip:        func(*http.Request) bool { return false },
			wantMWRan:   true,
			wantNextRan: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var mwRan, nextRan bool
			mw, err := skipif.New(tt.skip, markingMiddleware(&mwRan))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				nextRan = true
			}))

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

			if mwRan != tt.wantMWRan {
				t.Fatalf("wrapped middleware ran = %v, want %v", mwRan, tt.wantMWRan)
			}
			if nextRan != tt.wantNextRan {
				t.Fatalf("next handler ran = %v, want %v", nextRan, tt.wantNextRan)
			}
		})
	}
}

func TestInvalidArgumentsRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		skip skipif.Predicate
		mw   middleware.Middleware
	}{
		{"nil predicate", nil, markingMiddleware(new(bool))},
		{"nil middleware", func(*http.Request) bool { return false }, nil},
		{"nil predicate and middleware", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := skipif.New(tt.skip, tt.mw); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestPathHasPrefix(t *testing.T) {
	t.Parallel()

	pred := skipif.PathHasPrefix("/health", "/metrics")

	tests := []struct {
		path string
		want bool
	}{
		{"/health", true},
		{"/health/live", true},
		{"/metrics", true},
		{"/api/users", false},
		{"/", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.path, http.NoBody)
			if got := pred(req); got != tt.want {
				t.Errorf("PathHasPrefix(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestMethodIs(t *testing.T) {
	t.Parallel()

	pred := skipif.MethodIs(http.MethodGet, "options")

	tests := []struct {
		method string
		want   bool
	}{
		{http.MethodGet, true},
		{"get", true},
		{http.MethodOptions, true},
		{http.MethodPost, false},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tt.method, "/", http.NoBody)
			if got := pred(req); got != tt.want {
				t.Errorf("MethodIs(%q) = %v, want %v", tt.method, got, tt.want)
			}
		})
	}
}
