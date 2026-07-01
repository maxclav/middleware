package jwt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/maxclav/middleware"
	"github.com/maxclav/middleware/jwt"
)

var hmacKey = []byte("test-secret-key")

// signHS256 mints an HS256-signed token for the given claims and key.
func signHS256(t *testing.T, claims gojwt.Claims, key []byte) string {
	t.Helper()
	token := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	s, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// newMiddleware builds a jwt middleware from opts, failing the test on error.
func newMiddleware(t *testing.T, opts ...jwt.Option) middleware.Middleware {
	t.Helper()
	mw, err := jwt.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mw
}

// bearerRequest builds a GET request carrying "Authorization: Bearer <token>".
func bearerRequest(t *testing.T, token string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// serve runs h against req and returns the recorder.
func serve(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// okHandler is a next handler that records whether it was reached.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		*reached = true
	})
}

func TestValidTokenPassesAndClaimsInContext(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, jwt.WithHMACKey(hmacKey))

	var gotSub string
	var found bool
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		claims, ok := jwt.FromContext(r.Context())
		found = ok
		if mc, isMap := claims.(gojwt.MapClaims); isMap {
			gotSub, _ = mc["sub"].(string)
		}
	}))

	token := signHS256(t, gojwt.MapClaims{"sub": "alice"}, hmacKey)
	rec := serve(t, h, bearerRequest(t, token))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !found {
		t.Fatal("claims not found in context")
	}
	if gotSub != "alice" {
		t.Fatalf("sub = %q, want alice", gotSub)
	}
}

func TestRejectedRequests(t *testing.T) {
	t.Parallel()

	// noneToken is an "alg: none" token, which WithHMACKey must reject as a
	// disallowed signing method.
	noneToken := func(t *testing.T) string {
		t.Helper()
		tok := gojwt.NewWithClaims(gojwt.SigningMethodNone, gojwt.MapClaims{"sub": "eve"})
		signed, err := tok.SignedString(gojwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatalf("sign none: %v", err)
		}
		return signed
	}

	tests := []struct {
		name string
		opts []jwt.Option
		// req builds the request; when nil, a bearer request carrying token is used.
		req   func(t *testing.T) *http.Request
		token func(t *testing.T) string
	}{
		{
			name: "expired token",
			token: func(t *testing.T) string {
				return signHS256(t, gojwt.RegisteredClaims{ExpiresAt: gojwt.NewNumericDate(time.Now().Add(-time.Hour))}, hmacKey)
			},
		},
		{
			name: "wrong signing key",
			token: func(t *testing.T) string {
				return signHS256(t, gojwt.MapClaims{"sub": "bob"}, []byte("a-different-key"))
			},
		},
		{
			name:  "disallowed algorithm",
			token: noneToken,
		},
		{
			name:  "malformed token string",
			token: func(*testing.T) string { return "not-a-jwt" },
		},
		{
			name: "missing header",
			req:  func(t *testing.T) *http.Request { return httptest.NewRequest(http.MethodGet, "/", http.NoBody) },
		},
		{
			name: "wrong scheme",
			req: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
				req.Header.Set("Authorization", "Token abc.def.ghi")
				return req
			},
		},
		{
			name: "header shorter than scheme prefix",
			req: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
				req.Header.Set("Authorization", "B")
				return req
			},
		},
		{
			name:  "wrong issuer",
			opts:  []jwt.Option{jwt.WithIssuer("trusted")},
			token: func(t *testing.T) string { return signHS256(t, gojwt.RegisteredClaims{Issuer: "attacker"}, hmacKey) },
		},
		{
			name: "wrong audience",
			opts: []jwt.Option{jwt.WithAudience("api")},
			token: func(t *testing.T) string {
				return signHS256(t, gojwt.RegisteredClaims{Audience: gojwt.ClaimStrings{"other"}}, hmacKey)
			},
		},
		{
			name: "issued in the future beyond leeway",
			opts: []jwt.Option{jwt.WithLeeway(time.Second)},
			token: func(t *testing.T) string {
				return signHS256(t, gojwt.RegisteredClaims{NotBefore: gojwt.NewNumericDate(time.Now().Add(time.Hour))}, hmacKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]jwt.Option{jwt.WithHMACKey(hmacKey)}, tt.opts...)
			mw := newMiddleware(t, opts...)

			reached := false
			h := mw(okHandler(&reached))

			req := tt.req
			if req == nil {
				token := tt.token(t)
				req = func(t *testing.T) *http.Request { return bearerRequest(t, token) }
			}
			rec := serve(t, h, req(t))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if reached {
				t.Fatal("next handler was called on a rejected request")
			}
		})
	}
}

func TestCaseInsensitiveScheme(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, jwt.WithHMACKey(hmacKey))
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := jwt.FromContext(r.Context()); !ok {
			t.Error("claims missing in context")
		}
	}))

	token := signHS256(t, gojwt.MapClaims{"sub": "alice"}, hmacKey)
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "bearer "+token) // lowercase scheme
	rec := serve(t, h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWithKeyFuncValidatesToken(t *testing.T) {
	t.Parallel()

	// WithKeyFunc supplies the verification key; WithValidMethods restricts the
	// accepted algorithm to HS256.
	keyfunc := func(*gojwt.Token) (any, error) { return hmacKey, nil }
	mw := newMiddleware(t, jwt.WithKeyFunc(keyfunc), jwt.WithValidMethods(gojwt.SigningMethodHS256.Alg()))

	reached := false
	h := mw(okHandler(&reached))

	token := signHS256(t, gojwt.MapClaims{"sub": "carol"}, hmacKey)
	rec := serve(t, h, bearerRequest(t, token))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !reached {
		t.Fatal("next handler was not called for a valid token")
	}
}

func TestKeyFuncErrorRejected(t *testing.T) {
	t.Parallel()

	keyfunc := func(*gojwt.Token) (any, error) { return nil, errors.New("no key for token") }
	mw := newMiddleware(t, jwt.WithKeyFunc(keyfunc))

	reached := false
	h := mw(okHandler(&reached))

	token := signHS256(t, gojwt.MapClaims{"sub": "dave"}, hmacKey)
	rec := serve(t, h, bearerRequest(t, token))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if reached {
		t.Fatal("next handler was called after keyfunc error")
	}
}

func TestValidMethodsRejectsOtherAlg(t *testing.T) {
	t.Parallel()

	// Only HS384 is permitted, so an HS256-signed token must be rejected.
	keyfunc := func(*gojwt.Token) (any, error) { return hmacKey, nil }
	mw := newMiddleware(t, jwt.WithKeyFunc(keyfunc), jwt.WithValidMethods(gojwt.SigningMethodHS384.Alg()))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	token := signHS256(t, gojwt.MapClaims{"sub": "erin"}, hmacKey)
	rec := serve(t, h, bearerRequest(t, token))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestLeewayAllowsSkew(t *testing.T) {
	t.Parallel()

	// A token expiring 30s ago is accepted when a 1m leeway is configured.
	mw := newMiddleware(t, jwt.WithHMACKey(hmacKey), jwt.WithLeeway(time.Minute))

	reached := false
	h := mw(okHandler(&reached))

	claims := gojwt.RegisteredClaims{ExpiresAt: gojwt.NewNumericDate(time.Now().Add(-30 * time.Second))}
	token := signHS256(t, claims, hmacKey)
	rec := serve(t, h, bearerRequest(t, token))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !reached {
		t.Fatal("next handler was not called within leeway")
	}
}

func TestCustomClaimsFactory(t *testing.T) {
	t.Parallel()

	type customClaims struct {
		Role string `json:"role"`
		gojwt.RegisteredClaims
	}

	mw := newMiddleware(t,
		jwt.WithHMACKey(hmacKey),
		jwt.WithClaims(func() gojwt.Claims { return &customClaims{} }),
	)

	var gotRole string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		claims, _ := jwt.FromContext(r.Context())
		if cc, ok := claims.(*customClaims); ok {
			gotRole = cc.Role
		}
	}))

	token := signHS256(t, &customClaims{Role: "admin"}, hmacKey)
	rec := serve(t, h, bearerRequest(t, token))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if gotRole != "admin" {
		t.Fatalf("role = %q, want admin", gotRole)
	}
}

func TestCustomHeaderAndScheme(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t,
		jwt.WithHMACKey(hmacKey),
		jwt.WithHeader("X-Auth"),
		jwt.WithScheme("Token"),
	)

	reached := false
	h := mw(okHandler(&reached))

	token := signHS256(t, gojwt.MapClaims{"sub": "frank"}, hmacKey)
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth", "Token "+token)
	rec := serve(t, h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !reached {
		t.Fatal("next handler was not called for a custom header and scheme")
	}
}

func TestCustomErrorHandler(t *testing.T) {
	t.Parallel()

	var gotStatus int
	var gotErr error
	handler := func(w http.ResponseWriter, _ *http.Request, status int, err error) {
		gotStatus = status
		gotErr = err
		w.WriteHeader(status)
	}

	mw := newMiddleware(t, jwt.WithHMACKey(hmacKey), jwt.WithErrorHandler(handler))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := serve(t, h, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if gotStatus != http.StatusUnauthorized {
		t.Fatalf("handler status = %d, want %d", gotStatus, http.StatusUnauthorized)
	}
	if gotErr == nil {
		t.Fatal("error handler received a nil error")
	}
}

func TestNewOptionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []jwt.Option
	}{
		{
			name: "no key configured",
			opts: nil,
		},
		{
			name: "nil keyfunc",
			opts: []jwt.Option{jwt.WithKeyFunc(nil)},
		},
		{
			name: "empty HMAC key",
			opts: []jwt.Option{jwt.WithHMACKey(nil)},
		},
		{
			name: "empty header name",
			opts: []jwt.Option{jwt.WithHMACKey(hmacKey), jwt.WithHeader("")},
		},
		{
			name: "empty scheme",
			opts: []jwt.Option{jwt.WithHMACKey(hmacKey), jwt.WithScheme("")},
		},
		{
			name: "nil claims factory",
			opts: []jwt.Option{jwt.WithHMACKey(hmacKey), jwt.WithClaims(nil)},
		},
		{
			name: "nil error handler",
			opts: []jwt.Option{jwt.WithHMACKey(hmacKey), jwt.WithErrorHandler(nil)},
		},
		{
			name: "multiple invalid options aggregated",
			opts: []jwt.Option{jwt.WithKeyFunc(nil), jwt.WithHeader(""), jwt.WithScheme(""), jwt.WithClaims(nil), jwt.WithErrorHandler(nil)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := jwt.New(tt.opts...); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestFromContextAbsent(t *testing.T) {
	t.Parallel()

	if claims, ok := jwt.FromContext(context.Background()); ok || claims != nil {
		t.Fatalf("FromContext on empty context = (%v, %v), want (nil, false)", claims, ok)
	}
}

func TestNewContextRoundTrip(t *testing.T) {
	t.Parallel()

	want := gojwt.MapClaims{"sub": "grace"}
	ctx := jwt.NewContext(context.Background(), want)

	got, ok := jwt.FromContext(ctx)
	if !ok {
		t.Fatal("claims not found after NewContext")
	}
	if mc, isMap := got.(gojwt.MapClaims); !isMap || mc["sub"] != "grace" {
		t.Fatalf("claims = %v, want %v", got, want)
	}
}

func TestHMACKeyIsCopied(t *testing.T) {
	t.Parallel()

	// WithHMACKey must copy the key: mutating the caller's slice after
	// construction must not change the verification secret. The token is signed
	// with the original bytes before the mutation, so a middleware that aliased
	// the caller's slice would reject it with 401.
	key := []byte("original-secret!")
	mw := newMiddleware(t, jwt.WithHMACKey(key))
	token := signHS256(t, gojwt.MapClaims{"sub": "u1"}, key)
	for i := range key {
		key[i] = 'x'
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := serve(t, mw(next), bearerRequest(t, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; mutating the caller's key must not affect verification", rec.Code)
	}
}
