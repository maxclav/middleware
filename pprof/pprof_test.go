package pprof_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxclav/middleware/pprof"
)

// next returns a sentinel handler and a pointer to the flag it sets when run,
// so tests can assert whether the request passed through the middleware.
func next(t *testing.T) (http.Handler, *bool) {
	t.Helper()
	called := false
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})
	return h, &called
}

func TestIndexListsProfiles(t *testing.T) {
	t.Parallel()
	mw, err := pprof.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inner, called := next(t)
	h := mw(inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "goroutine") || !strings.Contains(body, "heap") {
		t.Fatalf("index does not list profiles: %q", body)
	}
	if *called {
		t.Fatal("next handler should not run for pprof paths")
	}
}

func TestProfileEndpointsServed(t *testing.T) {
	t.Parallel()
	// Endpoints that respond quickly with 200 OK. The CPU "profile" and "trace"
	// endpoints are omitted because they block for a sampling window.
	paths := []string{
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
		"/debug/pprof/goroutine?debug=1",
		"/debug/pprof/heap",
		"/debug/pprof/allocs",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			mw, err := pprof.New()
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			inner, called := next(t)
			h := mw(inner)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if *called {
				t.Fatal("next handler should not run for pprof paths")
			}
		})
	}
}

func TestUnrelatedPathCallsNext(t *testing.T) {
	t.Parallel()
	mw, err := pprof.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inner, called := next(t)
	h := mw(inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", http.NoBody))

	if !*called {
		t.Fatal("next handler was not called for unrelated path")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
}

func TestCustomPrefix(t *testing.T) {
	t.Parallel()
	mw, err := pprof.New(pprof.WithPrefix("/_internal/prof/"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inner, called := next(t)
	h := mw(inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_internal/prof/", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("custom prefix status = %d, want %d", rec.Code, http.StatusOK)
	}
	if *called {
		t.Fatal("next handler should not run for custom-prefix pprof paths")
	}

	// Default prefix must no longer be intercepted.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", http.NoBody))
	if !*called {
		t.Fatal("default prefix should pass through when a custom prefix is set")
	}
}

func TestInvalidPrefixRejected(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"empty":         "",
		"no leading /":  "debug/pprof/",
		"no trailing /": "/debug/pprof",
	}
	for name, prefix := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := pprof.New(pprof.WithPrefix(prefix)); err == nil {
				t.Errorf("expected error for prefix %q, got nil", prefix)
			}
		})
	}
}

func TestValidCustomPrefixAccepted(t *testing.T) {
	t.Parallel()
	if _, err := pprof.New(pprof.WithPrefix("/_internal/prof/")); err != nil {
		t.Fatalf("New with valid prefix: %v", err)
	}
}
