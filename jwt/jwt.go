// Package jwt provides middleware that validates a JWT bearer token from an
// incoming request. On success the parsed claims are stored in the request
// context (see [FromContext]) and the next handler is invoked; otherwise the
// request is rejected with 401 Unauthorized through the configured error
// handler.
package jwt

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/maxclav/middleware"
)

type ctxKey struct{}

type config struct {
	keyfunc       jwt.Keyfunc
	parserOptions []jwt.ParserOption
	header        string
	scheme        string
	newClaims     func() jwt.Claims
	errorHandler  middleware.ErrorHandler
}

// Option configures the jwt middleware.
type Option func(*config) error

// WithKeyFunc sets the [jwt.Keyfunc] used to supply the verification key for a
// token. It must not be nil and is required unless [WithHMACKey],
// [WithRSAPublicKey] or [WithECDSAPublicKey] is used.
//
// When supplying an asymmetric verification key through a raw keyfunc, also call
// [WithValidMethods] to pin the accepted algorithms. Without that, a token
// forged with a symmetric algorithm can be verified against the public key, an
// algorithm-confusion attack. The typed helpers pin the algorithms for you.
func WithKeyFunc(keyfunc jwt.Keyfunc) Option {
	return func(c *config) error {
		if keyfunc == nil {
			return errors.New("jwt: keyfunc must not be nil")
		}
		c.keyfunc = keyfunc
		return nil
	}
}

// WithHMACKey is a convenience option for HMAC-signed tokens. It configures a
// keyfunc that returns key for every token and restricts the accepted signing
// methods to HS256, HS384 and HS512. The key must not be empty.
func WithHMACKey(key []byte) Option {
	return func(c *config) error {
		if len(key) == 0 {
			return errors.New("jwt: HMAC key must not be empty")
		}
		// Copy the key so a later mutation of the caller's slice cannot change
		// the verification secret.
		key = slices.Clone(key)
		c.keyfunc = func(*jwt.Token) (any, error) { return key, nil }
		c.parserOptions = append(c.parserOptions, jwt.WithValidMethods([]string{
			jwt.SigningMethodHS256.Alg(),
			jwt.SigningMethodHS384.Alg(),
			jwt.SigningMethodHS512.Alg(),
		}))
		return nil
	}
}

// WithRSAPublicKey is a convenience option for RSA-signed tokens. It configures
// a keyfunc returning key and restricts the accepted signing methods to the RSA
// family (RS256/384/512 and PS256/384/512). Pinning the algorithms prevents
// key-confusion attacks, where a token forged with a symmetric algorithm is
// verified against the public key. The key must not be nil.
func WithRSAPublicKey(key *rsa.PublicKey) Option {
	return func(c *config) error {
		if key == nil {
			return errors.New("jwt: RSA public key must not be nil")
		}
		c.keyfunc = func(*jwt.Token) (any, error) { return key, nil }
		c.parserOptions = append(c.parserOptions, jwt.WithValidMethods([]string{
			jwt.SigningMethodRS256.Alg(),
			jwt.SigningMethodRS384.Alg(),
			jwt.SigningMethodRS512.Alg(),
			jwt.SigningMethodPS256.Alg(),
			jwt.SigningMethodPS384.Alg(),
			jwt.SigningMethodPS512.Alg(),
		}))
		return nil
	}
}

// WithECDSAPublicKey is a convenience option for ECDSA-signed tokens. It
// configures a keyfunc returning key and restricts the accepted signing methods
// to the ECDSA family (ES256/384/512), preventing key-confusion attacks. The
// key must not be nil.
func WithECDSAPublicKey(key *ecdsa.PublicKey) Option {
	return func(c *config) error {
		if key == nil {
			return errors.New("jwt: ECDSA public key must not be nil")
		}
		c.keyfunc = func(*jwt.Token) (any, error) { return key, nil }
		c.parserOptions = append(c.parserOptions, jwt.WithValidMethods([]string{
			jwt.SigningMethodES256.Alg(),
			jwt.SigningMethodES384.Alg(),
			jwt.SigningMethodES512.Alg(),
		}))
		return nil
	}
}

// WithEdDSAPublicKey is a convenience option for EdDSA (Ed25519) signed tokens.
// It configures a keyfunc returning key and restricts the accepted signing
// method to EdDSA, preventing key-confusion attacks. The key must not be empty.
func WithEdDSAPublicKey(key ed25519.PublicKey) Option {
	return func(c *config) error {
		if len(key) == 0 {
			return errors.New("jwt: EdDSA public key must not be empty")
		}
		// Copy the key so a later mutation of the caller's slice cannot change
		// the verification key.
		key = slices.Clone(key)
		c.keyfunc = func(*jwt.Token) (any, error) { return key, nil }
		c.parserOptions = append(c.parserOptions, jwt.WithValidMethods([]string{
			jwt.SigningMethodEdDSA.Alg(),
		}))
		return nil
	}
}

// WithValidMethods restricts the signing methods accepted during parsing to the
// given algorithm names (for example "RS256"). It appends a
// [jwt.WithValidMethods] parser option.
func WithValidMethods(methods ...string) Option {
	return func(c *config) error {
		c.parserOptions = append(c.parserOptions, jwt.WithValidMethods(slices.Clone(methods)))
		return nil
	}
}

// WithIssuer requires the token's "iss" claim to equal iss. It appends a
// [jwt.WithIssuer] parser option.
func WithIssuer(iss string) Option {
	return func(c *config) error {
		c.parserOptions = append(c.parserOptions, jwt.WithIssuer(iss))
		return nil
	}
}

// WithAudience requires the token's "aud" claim to include aud. It appends a
// [jwt.WithAudience] parser option.
func WithAudience(aud string) Option {
	return func(c *config) error {
		c.parserOptions = append(c.parserOptions, jwt.WithAudience(aud))
		return nil
	}
}

// WithLeeway allows for a clock-skew tolerance when validating time-based
// claims. It appends a [jwt.WithLeeway] parser option.
func WithLeeway(leeway time.Duration) Option {
	return func(c *config) error {
		c.parserOptions = append(c.parserOptions, jwt.WithLeeway(leeway))
		return nil
	}
}

// WithExpirationRequired rejects tokens that omit the "exp" (expiration) claim.
// Without it a token that carries no exp never expires. It appends a
// [jwt.WithExpirationRequired] parser option.
func WithExpirationRequired() Option {
	return func(c *config) error {
		c.parserOptions = append(c.parserOptions, jwt.WithExpirationRequired())
		return nil
	}
}

// WithClaims sets the factory used to allocate a fresh [jwt.Claims] value for
// each request, allowing a custom claims type to be populated during parsing.
// It must not be nil. Defaults to a factory returning an empty
// [jwt.MapClaims].
func WithClaims(newClaims func() jwt.Claims) Option {
	return func(c *config) error {
		if newClaims == nil {
			return errors.New("jwt: claims factory must not be nil")
		}
		c.newClaims = newClaims
		return nil
	}
}

// WithHeader sets the request header the token is read from. It must not be
// empty. Defaults to "Authorization".
func WithHeader(name string) Option {
	return func(c *config) error {
		if name == "" {
			return errors.New("jwt: header name must not be empty")
		}
		c.header = name
		return nil
	}
}

// WithScheme sets the authorization scheme prefix stripped from the header
// value before parsing (case-insensitive). It must not be empty. Defaults to
// "Bearer".
func WithScheme(scheme string) Option {
	return func(c *config) error {
		if scheme == "" {
			return errors.New("jwt: scheme must not be empty")
		}
		c.scheme = scheme
		return nil
	}
}

// WithErrorHandler sets how a rejected request is rendered. It must not be nil.
// Defaults to [middleware.DefaultErrorHandler].
func WithErrorHandler(h middleware.ErrorHandler) Option {
	return func(c *config) error {
		if h == nil {
			return errors.New("jwt: error handler must not be nil")
		}
		c.errorHandler = h
		return nil
	}
}

// New returns middleware that validates a JWT bearer token on every request. A
// verification key must be configured with [WithKeyFunc] or [WithHMACKey];
// otherwise New returns an error.
//
// The token is read from the configured header, its scheme prefix is stripped
// (case-insensitively), and it is parsed with [jwt.ParseWithClaims]. A missing
// header, a parse error, or an invalid token results in a 401 Unauthorized
// rendered by the configured [middleware.ErrorHandler]. On success the parsed
// claims are stored in the request context (see [FromContext]).
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		header:       "Authorization",
		scheme:       "Bearer",
		newClaims:    func() jwt.Claims { return jwt.MapClaims{} },
		errorHandler: middleware.DefaultErrorHandler,
	}
	var errs []error
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if cfg.keyfunc == nil {
		return nil, errors.New("jwt: a key must be configured with WithKeyFunc or WithHMACKey")
	}

	prefix := cfg.scheme + " "
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get(cfg.header)
			if len(raw) < len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
				cfg.errorHandler(w, r, http.StatusUnauthorized,
					errors.New("jwt: missing or malformed authorization header"))
				return
			}
			tokenString := raw[len(prefix):]

			token, err := jwt.ParseWithClaims(tokenString, cfg.newClaims(), cfg.keyfunc, cfg.parserOptions...)
			if err != nil {
				cfg.errorHandler(w, r, http.StatusUnauthorized, err)
				return
			}
			if !token.Valid {
				cfg.errorHandler(w, r, http.StatusUnauthorized, errors.New("jwt: invalid token"))
				return
			}

			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), token.Claims)))
		})
	}, nil
}

// NewContext returns a copy of ctx carrying the validated claims.
func NewContext(ctx context.Context, claims jwt.Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, claims)
}

// FromContext returns the validated claims stored in ctx, reporting whether
// they were present.
func FromContext(ctx context.Context) (jwt.Claims, bool) {
	claims, ok := ctx.Value(ctxKey{}).(jwt.Claims)
	return claims, ok
}
