package secure_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maxclav/middleware/secure"
)

// newMiddleware builds a middleware with opts, failing the test on error.
func newMiddleware(t *testing.T, opts ...secure.Option) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := secure.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

// serve runs mw over a request built by mutate and returns the response headers.
func serve(t *testing.T, mw func(http.Handler) http.Handler, mutate func(*http.Request)) http.Header {
	t.Helper()
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	return rec.Header()
}

func TestDefaultHeaders(t *testing.T) {
	t.Parallel()

	got := serve(t, newMiddleware(t), nil)

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "", // off by default
		"Content-Security-Policy":   "", // opt-in
		"Permissions-Policy":        "", // opt-in
	}
	for key, exp := range want {
		if v := got.Get(key); v != exp {
			t.Errorf("%s = %q, want %q", key, v, exp)
		}
	}
}

func TestHSTS(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts   []secure.Option
		mutate func(*http.Request)
		want   string
	}{
		"omitted over plain HTTP": {
			opts:   []secure.Option{secure.WithHSTS(time.Hour, true, true)},
			mutate: nil,
			want:   "",
		},
		"emitted with flags over TLS": {
			opts:   []secure.Option{secure.WithHSTS(time.Hour, true, true)},
			mutate: func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
			want:   "max-age=3600; includeSubDomains; preload",
		},
		"bare max-age over TLS": {
			opts:   []secure.Option{secure.WithHSTS(time.Hour, false, false)},
			mutate: func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
			want:   "max-age=3600",
		},
		"includeSubDomains only over TLS": {
			opts:   []secure.Option{secure.WithHSTS(time.Hour, true, false)},
			mutate: func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
			want:   "max-age=3600; includeSubDomains",
		},
		"forwarded proto ignored when untrusted": {
			opts:   []secure.Option{secure.WithHSTS(time.Hour, false, false)},
			mutate: func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") },
			want:   "",
		},
		"forwarded proto honoured when trusted": {
			opts: []secure.Option{
				secure.WithHSTS(time.Hour, false, false),
				secure.WithTrustForwardedProto(true),
			},
			mutate: func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") },
			want:   "max-age=3600",
		},
		"trusted forwarded proto non-https stays off": {
			opts: []secure.Option{
				secure.WithHSTS(time.Hour, false, false),
				secure.WithTrustForwardedProto(true),
			},
			mutate: func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "http") },
			want:   "",
		},
		"zero max-age keeps HSTS disabled over TLS": {
			opts:   []secure.Option{secure.WithHSTS(0, true, true)},
			mutate: func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
			want:   "",
		},
		"one-second max-age over TLS": {
			opts:   []secure.Option{secure.WithHSTS(time.Second, false, false)},
			mutate: func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
			want:   "max-age=1",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := serve(t, newMiddleware(t, tc.opts...), tc.mutate)
			if v := got.Get("Strict-Transport-Security"); v != tc.want {
				t.Fatalf("Strict-Transport-Security = %q, want %q", v, tc.want)
			}
		})
	}
}

func TestFrameOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value   string
		want    string
		wantErr bool
	}{
		"default deny":   {value: "DENY", want: "DENY"},
		"sameorigin":     {value: "SAMEORIGIN", want: "SAMEORIGIN"},
		"empty disables": {value: "", want: ""},
		"invalid":        {value: "ALLOWALL", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mw, err := secure.New(secure.WithFrameOptions(tc.value))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error for invalid frame option")
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got := serve(t, mw, nil)
			if v := got.Get("X-Frame-Options"); v != tc.want {
				t.Fatalf("X-Frame-Options = %q, want %q", v, tc.want)
			}
		})
	}
}

func TestNegativeHSTSMaxAgeRejected(t *testing.T) {
	t.Parallel()

	if _, err := secure.New(secure.WithHSTS(-time.Second, false, false)); err == nil {
		t.Fatal("expected error for negative HSTS max-age")
	}
}

// TestSubSecondHSTSMaxAgeRejected guards against a positive max-age below one
// second: it would truncate to max-age=0 and silently disable HSTS.
func TestSubSecondHSTSMaxAgeRejected(t *testing.T) {
	t.Parallel()

	if _, err := secure.New(secure.WithHSTS(500*time.Millisecond, false, false)); err == nil {
		t.Fatal("expected error for sub-second HSTS max-age")
	}
}

func TestEmptyValuesOmitHeaders(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t,
		secure.WithFrameOptions(""),
		secure.WithContentTypeNosniff(false),
		secure.WithReferrerPolicy(""),
	)
	got := serve(t, mw, nil)

	for _, key := range []string{"X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy"} {
		if v := got.Get(key); v != "" {
			t.Fatalf("%s should be omitted, got %q", key, v)
		}
	}
}

func TestOptionalPoliciesEmitted(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t,
		secure.WithContentSecurityPolicy("default-src 'self'"),
		secure.WithPermissionsPolicy("geolocation=()"),
		secure.WithReferrerPolicy("strict-origin"),
		secure.WithContentTypeNosniff(true),
	)
	got := serve(t, mw, nil)

	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'",
		"Permissions-Policy":      "geolocation=()",
		"Referrer-Policy":         "strict-origin",
		"X-Content-Type-Options":  "nosniff",
	}
	for key, exp := range want {
		if v := got.Get(key); v != exp {
			t.Errorf("%s = %q, want %q", key, v, exp)
		}
	}
}

func TestOptionErrorsAreAggregated(t *testing.T) {
	t.Parallel()

	if _, err := secure.New(
		secure.WithHSTS(-time.Second, false, false),
		secure.WithFrameOptions("BOGUS"),
	); err == nil {
		t.Fatal("expected aggregated error for invalid options")
	}
}
