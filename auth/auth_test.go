package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxclav/middleware"
	"github.com/maxclav/middleware/auth"
)

// newMiddleware builds an auth middleware, failing the test on error.
func newMiddleware(t *testing.T, verify auth.VerifyFunc, opts ...auth.Option) middleware.Middleware {
	t.Helper()
	mw, err := auth.New(verify, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

// serve runs h against req and returns the recorder.
func serve(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBasicAcceptsAndRejects(t *testing.T) {
	t.Parallel()

	verify := auth.Basic(func(user, pass string) (any, error) {
		if user == "alice" && pass == "secret" {
			return user, nil
		}
		return nil, errors.New("bad credentials")
	})
	mw := newMiddleware(t, verify)

	var identity any
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		identity, _ = auth.FromContext(r.Context())
	}))

	// Correct credentials pass and expose the identity.
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.SetBasicAuth("alice", "secret")
	rec := serve(t, h, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if identity != "alice" {
		t.Fatalf("identity = %v, want alice", identity)
	}

	// Wrong credentials are rejected with 401.
	req = httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.SetBasicAuth("alice", "wrong")
	rec = serve(t, h, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBasicMissingCredentials(t *testing.T) {
	t.Parallel()

	verify := auth.Basic(func(string, string) (any, error) {
		t.Error("check must not be called when credentials are absent")
		return nil, nil
	})
	mw := newMiddleware(t, verify)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := serve(t, h, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestBearerParsesToken(t *testing.T) {
	t.Parallel()

	var got string
	verify := auth.Bearer(func(token string) (any, error) {
		got = token
		return token, nil
	})
	mw := newMiddleware(t, verify)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "bearer abc123") // scheme is case-insensitive
	rec := serve(t, h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got != "abc123" {
		t.Fatalf("token = %q, want abc123", got)
	}
}

func TestBearerRejectsMalformedHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string // "" means no Authorization header
	}{
		{name: "missing header", header: ""},
		{name: "wrong scheme", header: "Basic abc123"},
		{name: "empty token", header: "Bearer "},
		{name: "no space separator", header: "Bearerabc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			verify := auth.Bearer(func(string) (any, error) {
				t.Error("check must not be called for a malformed header")
				return nil, nil
			})
			mw := newMiddleware(t, verify)

			reached := false
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				reached = true
			}))

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := serve(t, h, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if reached {
				t.Fatal("next handler was called on a rejected request")
			}
		})
	}
}

func TestAPIKeyReadsHeader(t *testing.T) {
	t.Parallel()

	var got string
	verify := auth.APIKey("X-API-Key", func(key string) (any, error) {
		got = key
		return key, nil
	})
	mw := newMiddleware(t, verify)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-API-Key", "k-42")
	rec := serve(t, h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got != "k-42" {
		t.Fatalf("key = %q, want k-42", got)
	}
}

func TestAPIKeyMissingHeader(t *testing.T) {
	t.Parallel()

	verify := auth.APIKey("X-API-Key", func(string) (any, error) {
		t.Error("check must not be called when the header is empty")
		return nil, nil
	})
	mw := newMiddleware(t, verify)

	reached := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	rec := serve(t, h, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if reached {
		t.Fatal("next handler was called with a missing API key")
	}
}

func TestMissingCredentialsReturn401WithChallenge(t *testing.T) {
	t.Parallel()

	verify := auth.Bearer(func(string) (any, error) { return nil, nil })
	mw := newMiddleware(t, verify, auth.WithRealm("api"), auth.WithChallengeScheme("Bearer"))

	nextCalled := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))

	rec := serve(t, h, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if nextCalled {
		t.Fatal("next handler was called on rejected request")
	}
	challenge := rec.Header().Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") || !strings.Contains(challenge, `realm="api"`) {
		t.Fatalf("WWW-Authenticate = %q, want Bearer realm=\"api\"", challenge)
	}
}

func TestDefaultRealmAndCustomScheme(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		opts          []auth.Option
		wantChallenge string
	}{
		{
			name:          "default realm and scheme",
			opts:          nil,
			wantChallenge: `Bearer realm="Restricted"`,
		},
		{
			name:          "custom realm and scheme",
			opts:          []auth.Option{auth.WithRealm("internal"), auth.WithChallengeScheme("Basic")},
			wantChallenge: `Basic realm="internal"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			verify := auth.Basic(func(string, string) (any, error) { return nil, nil })
			mw := newMiddleware(t, verify, tt.opts...)
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

			rec := serve(t, h, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
			if got := rec.Header().Get("WWW-Authenticate"); got != tt.wantChallenge {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, tt.wantChallenge)
			}
		})
	}
}

func TestCustomErrorHandlerReceivesStatusAndError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("denied")
	verify := func(*http.Request) (any, error) { return nil, wantErr }

	var gotStatus int
	var gotErr error
	handler := func(w http.ResponseWriter, _ *http.Request, status int, err error) {
		gotStatus = status
		gotErr = err
		w.WriteHeader(status)
	}

	mw := newMiddleware(t, verify, auth.WithErrorHandler(handler))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := serve(t, h, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if gotStatus != http.StatusUnauthorized {
		t.Fatalf("handler status = %d, want %d", gotStatus, http.StatusUnauthorized)
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("handler error = %v, want %v", gotErr, wantErr)
	}
}

func TestNewOptionErrors(t *testing.T) {
	t.Parallel()

	validVerify := auth.Bearer(func(string) (any, error) { return nil, nil })

	tests := []struct {
		name   string
		verify auth.VerifyFunc
		opts   []auth.Option
	}{
		{name: "nil verify", verify: nil},
		{name: "empty realm", verify: validVerify, opts: []auth.Option{auth.WithRealm("")}},
		{name: "empty challenge scheme", verify: validVerify, opts: []auth.Option{auth.WithChallengeScheme("")}},
		{name: "nil error handler", verify: validVerify, opts: []auth.Option{auth.WithErrorHandler(nil)}},
		{
			name:   "multiple invalid options aggregated",
			verify: validVerify,
			opts:   []auth.Option{auth.WithRealm(""), auth.WithChallengeScheme(""), auth.WithErrorHandler(nil)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := auth.New(tt.verify, tt.opts...); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestErrUnauthorizedReturnedByHelpers(t *testing.T) {
	t.Parallel()

	helpers := map[string]auth.VerifyFunc{
		"Basic":  auth.Basic(func(string, string) (any, error) { return nil, nil }),
		"Bearer": auth.Bearer(func(string) (any, error) { return nil, nil }),
		"APIKey": auth.APIKey("X-API-Key", func(string) (any, error) { return nil, nil }),
	}

	for name, verify := range helpers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := verify(httptest.NewRequest(http.MethodGet, "/", http.NoBody))
			if !errors.Is(err, auth.ErrUnauthorized) {
				t.Fatalf("err = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestFromContextAbsent(t *testing.T) {
	t.Parallel()

	if identity, ok := auth.FromContext(context.Background()); ok || identity != nil {
		t.Fatalf("FromContext on empty context = (%v, %v), want (nil, false)", identity, ok)
	}
}

func TestNewContextRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := auth.NewContext(context.Background(), "user-99")
	got, ok := auth.FromContext(ctx)
	if !ok {
		t.Fatal("identity not found after NewContext")
	}
	if got != "user-99" {
		t.Fatalf("identity = %v, want user-99", got)
	}
}
