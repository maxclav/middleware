// Package auth provides scheme-based authentication middleware with pluggable
// credential verification. A [VerifyFunc] extracts and validates credentials
// from a request, returning an opaque identity on success. On failure the
// request is rejected with 401 Unauthorized and a WWW-Authenticate challenge.
//
// The Basic, Bearer, and APIKey helpers build a [VerifyFunc] for the common
// authentication schemes; any other scheme can be supported by writing a
// [VerifyFunc] directly.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/maxclav/middleware"
)

// ErrUnauthorized is returned by the scheme helpers ([Basic], [Bearer],
// [APIKey]) when credentials are absent or malformed. A caller's check function
// may return this or any other error to signal rejection.
var ErrUnauthorized = errors.New("auth: unauthorized")

// VerifyFunc extracts and validates the credentials carried by r. On success it
// returns an opaque identity to associate with the request; on failure it
// returns a non-nil error and the request is rejected.
type VerifyFunc func(r *http.Request) (identity any, err error)

type ctxKey struct{}

type config struct {
	realm           string
	challengeScheme string
	errorHandler    middleware.ErrorHandler
}

// Option configures the auth middleware.
type Option func(*config) error

// WithRealm sets the realm reported in the WWW-Authenticate challenge sent with
// a 401 response. It must not be empty. Defaults to "Restricted".
func WithRealm(realm string) Option {
	return func(c *config) error {
		if realm == "" {
			return errors.New("auth: realm must not be empty")
		}
		c.realm = realm
		return nil
	}
}

// WithChallengeScheme sets the authentication scheme named in the
// WWW-Authenticate challenge sent with a 401 response. It must not be empty.
// Defaults to "Bearer".
func WithChallengeScheme(scheme string) Option {
	return func(c *config) error {
		if scheme == "" {
			return errors.New("auth: challenge scheme must not be empty")
		}
		c.challengeScheme = scheme
		return nil
	}
}

// WithErrorHandler sets how the 401 response is rendered when verification
// fails. It must not be nil. Defaults to [middleware.DefaultErrorHandler].
func WithErrorHandler(h middleware.ErrorHandler) Option {
	return func(c *config) error {
		if h == nil {
			return errors.New("auth: error handler must not be nil")
		}
		c.errorHandler = h
		return nil
	}
}

// New returns middleware that authenticates each request with verify. On
// success the returned identity is stored in the request context (see
// [FromContext]) and the next handler is called. On failure the request is
// rejected with 401 Unauthorized carrying a WWW-Authenticate challenge, rendered
// by the configured [middleware.ErrorHandler]; the next handler is not called.
//
// verify must not be nil and must return a non-nil identity on success; a nil
// identity is treated as an authentication failure.
func New(verify VerifyFunc, opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		realm:           "Restricted",
		challengeScheme: "Bearer",
		errorHandler:    middleware.DefaultErrorHandler,
	}
	var errs []error
	if verify == nil {
		errs = append(errs, errors.New("auth: verify must not be nil"))
	}
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	challenge := fmt.Sprintf("%s realm=%q", cfg.challengeScheme, cfg.realm)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, err := verify(r)
			if err == nil && identity == nil {
				// A successful verify must yield a non-nil identity; a nil one is
				// ambiguous with "not authenticated", so fail closed.
				err = ErrUnauthorized
			}
			if err != nil {
				w.Header().Set("WWW-Authenticate", challenge)
				cfg.errorHandler(w, r, http.StatusUnauthorized, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), identity)))
		})
	}, nil
}

// Basic returns a [VerifyFunc] that reads HTTP Basic credentials via
// [http.Request.BasicAuth] and validates them with check. When no Basic
// credentials are present it returns [ErrUnauthorized].
func Basic(check func(user, pass string) (any, error)) VerifyFunc {
	return func(r *http.Request) (any, error) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			return nil, ErrUnauthorized
		}
		return check(user, pass)
	}
}

// Bearer returns a [VerifyFunc] that parses an "Authorization: Bearer <token>"
// header (the scheme is matched case-insensitively) and validates the token with
// check. When the header is missing or not a Bearer credential it returns
// [ErrUnauthorized].
func Bearer(check func(token string) (any, error)) VerifyFunc {
	return func(r *http.Request) (any, error) {
		scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
			return nil, ErrUnauthorized
		}
		return check(token)
	}
}

// APIKey returns a [VerifyFunc] that reads the named request header and
// validates its value with check. When the header is empty it returns
// [ErrUnauthorized].
func APIKey(header string, check func(key string) (any, error)) VerifyFunc {
	return func(r *http.Request) (any, error) {
		key := r.Header.Get(header)
		if key == "" {
			return nil, ErrUnauthorized
		}
		return check(key)
	}
}

// NewContext returns a copy of ctx carrying the authenticated identity.
func NewContext(ctx context.Context, identity any) context.Context {
	return context.WithValue(ctx, ctxKey{}, identity)
}

// FromContext returns the authenticated identity stored in ctx, reporting
// whether one was present.
func FromContext(ctx context.Context) (any, bool) {
	identity := ctx.Value(ctxKey{})
	return identity, identity != nil
}
