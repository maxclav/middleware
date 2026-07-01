package cors_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxclav/middleware/cors"
)

// newMiddleware builds a middleware with opts, failing the test on error.
func newMiddleware(t *testing.T, opts ...cors.Option) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := cors.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

// nextRecorder returns a handler that records whether it was invoked.
func nextRecorder(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func preflightRequest(origin, method string) *http.Request {
	r := httptest.NewRequest(http.MethodOptions, "/", http.NoBody)
	r.Header.Set("Origin", origin)
	r.Header.Set("Access-Control-Request-Method", method)
	return r
}

func actualRequest(origin string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestPreflightFromAllowedOrigin(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t,
		cors.WithAllowedOrigins("https://good.example"),
		cors.WithAllowedMethods(http.MethodGet, http.MethodPost),
	)

	var called bool
	h := mw(nextRecorder(&called))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, preflightRequest("https://good.example", http.MethodPost))

	if called {
		t.Fatal("next handler must not be invoked for preflight")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://good.example" {
		t.Fatalf("Allow-Origin = %q, want https://good.example", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Fatalf("Allow-Methods = %q, want \"GET, POST\"", got)
	}
	if got := rec.Header().Values("Vary"); len(got) == 0 {
		t.Fatal("Vary should be set on a preflight response")
	}
}

func TestPreflightAllowHeaders(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts       []cors.Option
		reqHeaders string
		want       string
	}{
		"reflects request headers when unconfigured": {
			opts:       nil,
			reqHeaders: "X-Custom, Content-Type",
			want:       "X-Custom, Content-Type",
		},
		"uses configured headers over request headers": {
			opts:       []cors.Option{cors.WithAllowedHeaders("X-Api-Key")},
			reqHeaders: "X-Custom",
			want:       "X-Api-Key",
		},
		"omits header when neither configured nor requested": {
			opts:       nil,
			reqHeaders: "",
			want:       "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := append([]cors.Option{cors.WithAllowedOrigins("https://good.example")}, tc.opts...)
			mw := newMiddleware(t, opts...)
			h := mw(nextRecorder(new(bool)))

			req := preflightRequest("https://good.example", http.MethodPost)
			if tc.reqHeaders != "" {
				req.Header.Set("Access-Control-Request-Headers", tc.reqHeaders)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if got := rec.Header().Get("Access-Control-Allow-Headers"); got != tc.want {
				t.Fatalf("Allow-Headers = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPreflightWithCredentials covers the credentials branch of the preflight
// handler, which is only reachable with a specific (non-wildcard) origin.
func TestPreflightWithCredentials(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t,
		cors.WithAllowedOrigins("https://good.example"),
		cors.WithAllowCredentials(true),
		cors.WithMaxAge(120*time.Second),
	)
	h := mw(nextRecorder(new(bool)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, preflightRequest("https://good.example", http.MethodPost))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://good.example" {
		t.Fatalf("Allow-Origin = %q, want specific origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Allow-Credentials = %q, want true", got)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "120" {
		t.Fatalf("Max-Age = %q, want 120", got)
	}
}

func TestActualRequestFromAllowedOrigin(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, cors.WithAllowedOrigins("https://good.example"))

	var called bool
	h := mw(nextRecorder(&called))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, actualRequest("https://good.example"))

	if !called {
		t.Fatal("next handler must be invoked for an actual request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://good.example" {
		t.Fatalf("Allow-Origin = %q, want https://good.example", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("Vary = %q, want it to contain Origin", got)
	}
}

func TestActualRequestExposesHeaders(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t,
		cors.WithAllowedOrigins("https://good.example"),
		cors.WithExposedHeaders("X-Total-Count", "Link"),
	)
	h := mw(nextRecorder(new(bool)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, actualRequest("https://good.example"))

	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "X-Total-Count, Link" {
		t.Fatalf("Expose-Headers = %q, want \"X-Total-Count, Link\"", got)
	}
}

func TestDisallowedOrigin(t *testing.T) {
	t.Parallel()

	t.Run("actual request passes through without CORS headers", func(t *testing.T) {
		t.Parallel()

		mw := newMiddleware(t, cors.WithAllowedOrigins("https://good.example"))
		var called bool
		h := mw(nextRecorder(&called))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, actualRequest("https://evil.example"))

		if !called {
			t.Fatal("next handler must still be invoked for a disallowed actual request")
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Allow-Origin = %q, want no CORS header", got)
		}
	})

	t.Run("preflight answered with 204 and no CORS headers", func(t *testing.T) {
		t.Parallel()

		mw := newMiddleware(t, cors.WithAllowedOrigins("https://good.example"))
		var called bool
		h := mw(nextRecorder(&called))

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, preflightRequest("https://evil.example", http.MethodPost))

		if called {
			t.Fatal("next handler must not be invoked for preflight")
		}
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Allow-Origin = %q, want no CORS header", got)
		}
	})
}

func TestRequestWithoutOriginPassesThrough(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, cors.WithAllowedOrigins("https://good.example"))

	var called bool
	h := mw(nextRecorder(&called))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, actualRequest(""))

	if !called {
		t.Fatal("next handler must be invoked for a non-CORS request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want no CORS header", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Fatalf("Vary = %q, want none for a non-CORS request", got)
	}
}

// TestOptionsWithoutRequestMethodIsNotPreflight confirms an OPTIONS request
// lacking Access-Control-Request-Method is treated as an actual request.
func TestOptionsWithoutRequestMethodIsNotPreflight(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, cors.WithAllowedOrigins("https://good.example"))
	var called bool
	h := mw(nextRecorder(&called))

	req := httptest.NewRequest(http.MethodOptions, "/", http.NoBody)
	req.Header.Set("Origin", "https://good.example")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("plain OPTIONS should be forwarded as an actual request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://good.example" {
		t.Fatalf("Allow-Origin = %q, want https://good.example", got)
	}
}

func TestWildcardWithCredentialsRejected(t *testing.T) {
	t.Parallel()

	tests := map[string][]cors.Option{
		"implicit wildcard": {cors.WithAllowCredentials(true)},
		"explicit wildcard": {
			cors.WithAllowedOrigins("*"),
			cors.WithAllowCredentials(true),
		},
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := cors.New(opts...); err == nil {
				t.Fatal("expected error: wildcard origin with credentials")
			}
		})
	}
}

func TestOriginFuncAllowsCredentials(t *testing.T) {
	t.Parallel()

	// An origin func sidesteps the wildcard/credentials restriction.
	mw := newMiddleware(t,
		cors.WithAllowOriginFunc(func(origin string) bool {
			return strings.HasSuffix(origin, ".trusted.example")
		}),
		cors.WithAllowCredentials(true),
	)
	h := mw(nextRecorder(new(bool)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, actualRequest("https://app.trusted.example"))
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.trusted.example" {
		t.Fatalf("Allow-Origin = %q, want echoed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Allow-Credentials = %q, want true", got)
	}

	// A non-matching origin is denied.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, actualRequest("https://evil.example"))
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want denied", got)
	}
}

// TestOriginFuncDeniesPreflight covers the origin-func denial branch on the
// preflight path (allowed == false).
func TestOriginFuncDeniesPreflight(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, cors.WithAllowOriginFunc(func(string) bool { return false }))
	h := mw(nextRecorder(new(bool)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, preflightRequest("https://anything.example", http.MethodPost))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want denied", got)
	}
}

func TestResolveOriginEchoBehaviour(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts   []cors.Option
		origin string
		want   string
	}{
		"credentialed echoes specific origin": {
			opts: []cors.Option{
				cors.WithAllowedOrigins("https://good.example"),
				cors.WithAllowCredentials(true),
			},
			origin: "https://good.example",
			want:   "https://good.example",
		},
		"wildcard echoes star without credentials": {
			opts:   nil, // defaults: origins ["*"], no credentials
			origin: "https://anything.example",
			want:   "*",
		},
		"exact match echoes request origin case-insensitively": {
			opts:   []cors.Option{cors.WithAllowedOrigins("https://Good.Example")},
			origin: "https://good.example",
			want:   "https://good.example",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mw := newMiddleware(t, tc.opts...)
			h := mw(nextRecorder(new(bool)))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, actualRequest(tc.origin))

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
				t.Fatalf("Allow-Origin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMaxAge(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts []cors.Option
		want string
	}{
		"emitted in whole seconds": {
			opts: []cors.Option{cors.WithMaxAge(90 * time.Second)},
			want: "90",
		},
		"zero omits header": {
			opts: nil,
			want: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := append([]cors.Option{cors.WithAllowedOrigins("https://good.example")}, tc.opts...)
			mw := newMiddleware(t, opts...)
			h := mw(nextRecorder(new(bool)))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, preflightRequest("https://good.example", http.MethodPost))

			if got := rec.Header().Get("Access-Control-Max-Age"); got != tc.want {
				t.Fatalf("Max-Age = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInvalidOptionsAreRejected(t *testing.T) {
	t.Parallel()

	tests := map[string]cors.Option{
		"empty origins": cors.WithAllowedOrigins(),
		"empty methods": cors.WithAllowedMethods(),
		"nil func":      cors.WithAllowOriginFunc(nil),
		"negative age":  cors.WithMaxAge(-time.Second),
	}
	for name, opt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := cors.New(opt); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestInvalidOptionsAreAggregated(t *testing.T) {
	t.Parallel()

	if _, err := cors.New(
		cors.WithAllowedOrigins(),
		cors.WithAllowedMethods(),
		cors.WithAllowOriginFunc(nil),
		cors.WithMaxAge(-time.Second),
	); err == nil {
		t.Fatal("expected aggregated error for invalid options")
	}
}
