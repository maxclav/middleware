package headers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware/headers"
)

// newMiddleware builds the headers middleware, failing the test if construction
// returns an error.
func newMiddleware(t *testing.T, opts ...headers.Option) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := headers.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

// serve runs h against a GET request to "/" and returns the recorder.
func serve(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	return rec
}

func TestSetAndAddedResponseHeaders(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t,
		headers.WithResponseHeader("X-App", "one"),
		headers.WithAddedResponseHeader("X-Multi", "a"),
		headers.WithAddedResponseHeader("X-Multi", "b"),
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := serve(h)

	if got := rec.Header().Get("X-App"); got != "one" {
		t.Fatalf("X-App = %q, want one", got)
	}
	multi := rec.Header().Values("X-Multi")
	if len(multi) != 2 || multi[0] != "a" || multi[1] != "b" {
		t.Fatalf("X-Multi = %v, want [a b]", multi)
	}
}

func TestRemovedResponseHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		write func(w http.ResponseWriter)
	}{
		{
			name: "explicit WriteHeader",
			write: func(w http.ResponseWriter) {
				w.Header().Set("X-Powered-By", "leaky")
				w.WriteHeader(http.StatusOK)
			},
		},
		{
			name: "implicit WriteHeader via Write",
			write: func(w http.ResponseWriter) {
				w.Header().Set("X-Powered-By", "leaky")
				_, _ = w.Write([]byte("body"))
			},
		},
		{
			name: "WriteHeader then Write",
			write: func(w http.ResponseWriter) {
				w.Header().Set("X-Powered-By", "leaky")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("body"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mw := newMiddleware(t, headers.WithRemovedResponseHeader("X-Powered-By"))
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tt.write(w)
			}))

			rec := serve(h)

			if got := rec.Header().Get("X-Powered-By"); got != "" {
				t.Fatalf("X-Powered-By should be removed, got %q", got)
			}
		})
	}
}

func TestRemovingWriterUnwrap(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	var unwrapped http.ResponseWriter
	mw := newMiddleware(t, headers.WithRemovedResponseHeader("X-Powered-By"))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			t.Fatalf("response writer %T does not implement Unwrap", w)
		}
		unwrapped = u.Unwrap()
		w.WriteHeader(http.StatusOK)
	}))

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if unwrapped != rec {
		t.Fatalf("Unwrap returned %p, want the underlying recorder %p", unwrapped, rec)
	}
}

func TestRequestHeaderVisibleToHandler(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, headers.WithRequestHeader("X-Tenant", "acme"))

	var seen string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Tenant")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen != "acme" {
		t.Fatalf("handler saw X-Tenant = %q, want acme", seen)
	}
	if got := req.Header.Get("X-Tenant"); got != "" {
		t.Fatalf("caller's request was mutated: X-Tenant = %q", got)
	}
}

func TestEmptyKeyRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opt  headers.Option
	}{
		{"response header", headers.WithResponseHeader("", "v")},
		{"added response header", headers.WithAddedResponseHeader("", "v")},
		{"removed response header", headers.WithRemovedResponseHeader("")},
		{"request header", headers.WithRequestHeader("", "v")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := headers.New(tt.opt); err == nil {
				t.Fatalf("expected error for empty key")
			}
		})
	}
}
