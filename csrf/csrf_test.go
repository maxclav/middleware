package csrf_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/maxclav/middleware/csrf"
)

// newMiddleware builds a middleware with opts, failing the test on error.
func newMiddleware(t *testing.T, opts ...csrf.Option) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := csrf.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

// cookieValue returns the value of the named cookie set on rec, or "".
func cookieValue(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func TestSafeMethodSetsCookieAndExposesToken(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t)

	var ctxToken string
	var ctxOK bool
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxToken, ctxOK = csrf.Token(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	cookie := cookieValue(t, rec, csrf.DefaultCookieName)
	if cookie == "" {
		t.Fatal("expected a csrf cookie to be set")
	}
	if !ctxOK || ctxToken == "" {
		t.Fatal("expected token in context")
	}
	if ctxToken != cookie {
		t.Fatalf("context token %q != cookie token %q", ctxToken, cookie)
	}
}

func TestSafeMethodNeverRejected(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t)
	for _, method := range []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodTrace,
	} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			called := false
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "/", http.NoBody))
			if !called {
				t.Fatalf("%s: next handler was not called", method)
			}
			if rec.Code == http.StatusForbidden {
				t.Fatalf("%s: safe method was rejected", method)
			}
		})
	}
}

func TestUnsafeMethodWithMatchingHeaderPasses(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t)
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	const token = "matching-token"
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: csrf.DefaultCookieName, Value: token})
	req.Header.Set(csrf.DefaultHeaderName, token)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("next handler was not called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUnsafeMethodWithMatchingFormFieldPasses(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t)
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	const token = "form-token"
	form := url.Values{csrf.DefaultFieldName: {token}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrf.DefaultCookieName, Value: token})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to run for matching form field")
	}
	if rec.Code == http.StatusForbidden {
		t.Fatal("request with matching form field was rejected")
	}
}

// TestUnsafeMethodRejected covers every rejection path on an unsafe request:
// the next handler must never run and the response must be 403 Forbidden.
func TestUnsafeMethodRejected(t *testing.T) {
	t.Parallel()

	const cookieToken = "cookie-token"
	tests := map[string]func(*http.Request){
		"missing header and field": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: csrf.DefaultCookieName, Value: cookieToken})
		},
		"mismatched header": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: csrf.DefaultCookieName, Value: cookieToken})
			r.Header.Set(csrf.DefaultHeaderName, "other-token")
		},
		"missing cookie": func(r *http.Request) {
			r.Header.Set(csrf.DefaultHeaderName, "some-token")
		},
		"empty cookie value": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: csrf.DefaultCookieName, Value: ""})
			r.Header.Set(csrf.DefaultHeaderName, "some-token")
		},
	}

	mw := newMiddleware(t)
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("next handler must not be called on rejection")
			}))

			req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
			setup(req)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}

// TestConstantTimeCompareRejectsSameLengthMismatch exercises the
// ConstantTimeCompare branch with equal-length but different tokens, which
// short-circuits earlier length checks and reaches the comparison itself.
func TestConstantTimeCompareRejectsSameLengthMismatch(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler must not run for a same-length token mismatch")
	}))

	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: csrf.DefaultCookieName, Value: "abcdef"})
	req.Header.Set(csrf.DefaultHeaderName, "uvwxyz") // same length, different bytes

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUnsafeMethodExtraMethodsAreUnsafe(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t)
	for _, method := range []string{
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		"PROPFIND", // arbitrary unknown method is treated as unsafe
	} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("next handler must not run without a valid token")
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, "/", http.NoBody))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s: status = %d, want %d", method, rec.Code, http.StatusForbidden)
			}
		})
	}
}

func TestCookieAttributes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opts       []csrf.Option
		wantSecure bool
		wantSame   http.SameSite
		wantPath   string
	}{
		"defaults": {
			opts:       nil,
			wantSecure: true,
			wantSame:   http.SameSiteLaxMode,
			wantPath:   "/",
		},
		"strict same-site": {
			opts:       []csrf.Option{csrf.WithSameSite(http.SameSiteStrictMode)},
			wantSecure: true,
			wantSame:   http.SameSiteStrictMode,
			wantPath:   "/",
		},
		"insecure cookie and custom path": {
			opts: []csrf.Option{
				// A prefix-free name is required once Secure or Path "/" is relaxed.
				csrf.WithCookieName("csrf_token"),
				csrf.WithSecureCookie(false),
				csrf.WithPath("/app"),
			},
			wantSecure: false,
			wantSame:   http.SameSiteLaxMode,
			wantPath:   "/app",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mw := newMiddleware(t, tc.opts...)
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

			cookies := rec.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("got %d cookies, want 1", len(cookies))
			}
			c := cookies[0]
			if !c.HttpOnly {
				t.Error("cookie should be HttpOnly")
			}
			if c.Secure != tc.wantSecure {
				t.Errorf("Secure = %v, want %v", c.Secure, tc.wantSecure)
			}
			if c.SameSite != tc.wantSame {
				t.Errorf("SameSite = %v, want %v", c.SameSite, tc.wantSame)
			}
			if c.Path != tc.wantPath {
				t.Errorf("Path = %q, want %q", c.Path, tc.wantPath)
			}
		})
	}
}

func TestCustomNamesAreHonoured(t *testing.T) {
	t.Parallel()

	const (
		cookieName = "my_csrf"
		headerName = "X-My-Token"
		fieldName  = "my_field"
		token      = "the-token"
	)

	mw := newMiddleware(t,
		csrf.WithCookieName(cookieName),
		csrf.WithHeaderName(headerName),
		csrf.WithFieldName(fieldName),
	)

	// Safe request sets the cookie under the custom name.
	setCookie := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec := httptest.NewRecorder()
	setCookie.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if got := cookieValue(t, rec, cookieName); got == "" {
		t.Fatalf("cookie %q was not set", cookieName)
	}

	// Unsafe request passes when the custom header matches the custom cookie.
	called := false
	pass := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	req.Header.Set(headerName, token)
	rec = httptest.NewRecorder()
	pass.ServeHTTP(rec, req)
	if !called {
		t.Fatal("request with matching custom header was rejected")
	}

	// Unsafe request passes when the custom form field matches the cookie.
	called = false
	form := url.Values{fieldName: {token}}
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	rec = httptest.NewRecorder()
	pass.ServeHTTP(rec, req)
	if !called {
		t.Fatal("request with matching custom form field was rejected")
	}
}

func TestExistingCookieIsReused(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t)
	var ctxToken string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxToken, _ = csrf.Token(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: csrf.DefaultCookieName, Value: "existing"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if ctxToken != "existing" {
		t.Fatalf("token = %q, want existing", ctxToken)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("no new cookie should be set when one already exists")
	}
}

// TestEmptyExistingCookieIsRegenerated confirms a present-but-empty cookie is
// treated as absent and a fresh token is minted on a safe request.
func TestEmptyExistingCookieIsRegenerated(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t)
	var ctxToken string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxToken, _ = csrf.Token(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.AddCookie(&http.Cookie{Name: csrf.DefaultCookieName, Value: ""})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	fresh := cookieValue(t, rec, csrf.DefaultCookieName)
	if fresh == "" {
		t.Fatal("expected a fresh cookie to be set for an empty existing cookie")
	}
	if ctxToken != fresh {
		t.Fatalf("context token %q != fresh cookie %q", ctxToken, fresh)
	}
}

func TestCustomErrorHandlerInvoked(t *testing.T) {
	t.Parallel()

	var gotErr error
	invoked := false
	mw := newMiddleware(t, csrf.WithErrorHandler(
		func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			invoked = true
			gotErr = err
			w.WriteHeader(status)
		},
	))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !invoked {
		t.Fatal("custom error handler was not invoked")
	}
	if gotErr == nil {
		t.Fatal("error handler received a nil error")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestToken(t *testing.T) {
	t.Parallel()

	if _, ok := csrf.Token(context.Background()); ok {
		t.Fatal("expected no token in a bare context")
	}

	ctx := csrf.NewContext(context.Background(), "abc")
	got, ok := csrf.Token(ctx)
	if !ok || got != "abc" {
		t.Fatalf("Token = (%q, %v), want (\"abc\", true)", got, ok)
	}
}

func TestValidOptionsSucceed(t *testing.T) {
	t.Parallel()

	// The success (non-error) branch of every validating option.
	newMiddleware(t,
		csrf.WithCookieName("c"),
		csrf.WithHeaderName("h"),
		csrf.WithFieldName("f"),
		csrf.WithPath("/p"),
		csrf.WithSecureCookie(false),
		csrf.WithSameSite(http.SameSiteNoneMode),
		csrf.WithErrorHandler(func(http.ResponseWriter, *http.Request, int, error) {}),
	)
}

func TestInvalidOptionsRejected(t *testing.T) {
	t.Parallel()

	tests := map[string]csrf.Option{
		"empty cookie name": csrf.WithCookieName(""),
		"empty header name": csrf.WithHeaderName(""),
		"empty field name":  csrf.WithFieldName(""),
		"empty path":        csrf.WithPath(""),
		"nil error handler": csrf.WithErrorHandler(nil),
	}
	for name, opt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := csrf.New(opt); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// TestOptionErrorsAreAggregated confirms multiple invalid options are joined
// into a single error rather than short-circuiting on the first.
func TestOptionErrorsAreAggregated(t *testing.T) {
	t.Parallel()

	_, err := csrf.New(csrf.WithCookieName(""), csrf.WithHeaderName(""))
	if err == nil {
		t.Fatal("expected an aggregated error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cookie name") || !strings.Contains(msg, "header name") {
		t.Fatalf("error %q should mention both failures", msg)
	}
	// The joined error must still be a non-nil error value.
	if errors.Is(err, nil) {
		t.Fatal("joined error unexpectedly matched nil")
	}
}

func TestHostPrefixRequiresSecureAndRootPath(t *testing.T) {
	t.Parallel()

	// The default cookie name uses the "__Host-" prefix, which browsers accept
	// only with a Secure cookie at Path "/". Relaxing either without renaming the
	// cookie is a configuration error rather than a silent security downgrade.
	if _, err := csrf.New(csrf.WithSecureCookie(false)); err == nil {
		t.Error("__Host- default with an insecure cookie should error")
	}
	if _, err := csrf.New(csrf.WithPath("/app")); err == nil {
		t.Error("__Host- default with a non-root path should error")
	}
	if _, err := csrf.New(csrf.WithCookieName("csrf_token"), csrf.WithSecureCookie(false)); err != nil {
		t.Errorf("a prefix-free name with an insecure cookie should succeed: %v", err)
	}
	if _, err := csrf.New(csrf.WithCookieName("__Secure-csrf"), csrf.WithPath("/app")); err != nil {
		t.Errorf("__Secure- allows a custom path: %v", err)
	}
	if _, err := csrf.New(csrf.WithCookieName("__Secure-csrf"), csrf.WithSecureCookie(false)); err == nil {
		t.Error("__Secure- with an insecure cookie should error")
	}
}
