// Package csrf provides stateless double-submit-cookie CSRF protection.
//
// The middleware issues a random token in a cookie on safe requests and, on
// unsafe requests, requires the client to echo that token back in a header (or
// form field). Because both copies must be present and equal, a cross-site
// attacker who cannot read the victim's cookie cannot forge a matching token.
// No server-side state is kept, so the protection scales horizontally.
//
// Tokens are compared with [crypto/subtle.ConstantTimeCompare] to avoid leaking
// their contents through timing.
package csrf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/maxclav/middleware"
)

const (
	// DefaultCookieName is the default CSRF cookie name. It uses the "__Host-"
	// prefix, which browsers only accept on a Secure cookie scoped to Path "/"
	// with no Domain, so a cookie set by another subdomain cannot satisfy the
	// double-submit check. Serving over plain HTTP requires a name without the
	// prefix (see [WithCookieName]).
	DefaultCookieName = "__Host-csrf_token"
	// DefaultHeaderName is the default request header carrying the CSRF token.
	DefaultHeaderName = "X-CSRF-Token"
	// DefaultFieldName is the default form field carrying the CSRF token.
	DefaultFieldName = "csrf_token"

	// tokenBytes is the number of random bytes in a token before encoding.
	tokenBytes = 32
)

type ctxKey struct{}

type config struct {
	cookieName   string
	headerName   string
	fieldName    string
	path         string
	secure       bool
	sameSite     http.SameSite
	errorHandler middleware.ErrorHandler
}

// Option configures the csrf middleware.
type Option func(*config) error

// WithCookieName sets the name of the CSRF cookie. It must not be empty.
// Defaults to [DefaultCookieName].
//
// A name with the "__Host-" prefix requires WithSecureCookie(true) and
// WithPath("/"); a "__Secure-" prefix requires WithSecureCookie(true). Choose a
// name without a prefix (for example "csrf_token") to serve the cookie over
// plain HTTP during local development.
func WithCookieName(name string) Option {
	return func(c *config) error {
		if name == "" {
			return errors.New("csrf: cookie name must not be empty")
		}
		c.cookieName = name
		return nil
	}
}

// WithHeaderName sets the request header read for the token on unsafe requests.
// It must not be empty. Defaults to [DefaultHeaderName].
func WithHeaderName(name string) Option {
	return func(c *config) error {
		if name == "" {
			return errors.New("csrf: header name must not be empty")
		}
		c.headerName = name
		return nil
	}
}

// WithFieldName sets the form field read for the token on unsafe requests when
// the header is absent. It must not be empty. Defaults to [DefaultFieldName].
func WithFieldName(name string) Option {
	return func(c *config) error {
		if name == "" {
			return errors.New("csrf: field name must not be empty")
		}
		c.fieldName = name
		return nil
	}
}

// WithPath sets the Path attribute of the CSRF cookie. It must not be empty.
// Defaults to "/".
func WithPath(path string) Option {
	return func(c *config) error {
		if path == "" {
			return errors.New("csrf: cookie path must not be empty")
		}
		c.path = path
		return nil
	}
}

// WithSecureCookie controls whether the CSRF cookie carries the Secure
// attribute, restricting it to HTTPS. Defaults to true.
func WithSecureCookie(secure bool) Option {
	return func(c *config) error {
		c.secure = secure
		return nil
	}
}

// WithSameSite sets the SameSite attribute of the CSRF cookie. Defaults to
// [http.SameSiteLaxMode].
func WithSameSite(mode http.SameSite) Option {
	return func(c *config) error {
		c.sameSite = mode
		return nil
	}
}

// WithErrorHandler sets how a rejected unsafe request is rendered. It must not
// be nil. Defaults to [middleware.DefaultErrorHandler].
func WithErrorHandler(h middleware.ErrorHandler) Option {
	return func(c *config) error {
		if h == nil {
			return errors.New("csrf: error handler must not be nil")
		}
		c.errorHandler = h
		return nil
	}
}

// New returns middleware that enforces double-submit-cookie CSRF protection.
//
// Safe methods (GET, HEAD, OPTIONS, TRACE) are never rejected: the middleware
// ensures a token cookie exists, exposes the token in the request context (see
// [Token]) for embedding in templates, and calls the next handler.
//
// Unsafe methods (POST, PUT, PATCH, DELETE, and any other method) require the
// token from the configured header, or the configured form field when the
// header is absent, to be present and equal to the cookie token. On any
// mismatch or absence the request is rejected with 403 Forbidden through the
// configured [middleware.ErrorHandler] and the next handler is not called.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		cookieName:   DefaultCookieName,
		headerName:   DefaultHeaderName,
		fieldName:    DefaultFieldName,
		path:         "/",
		secure:       true,
		sameSite:     http.SameSiteLaxMode,
		errorHandler: middleware.DefaultErrorHandler,
	}
	var errs []error
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if err := validateCookiePrefix(cfg.cookieName, cfg.secure, cfg.path); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				token := cfg.ensureCookie(w, r)
				next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), token)))
				return
			}

			cookie, err := r.Cookie(cfg.cookieName)
			if err != nil || cookie.Value == "" {
				cfg.errorHandler(w, r, http.StatusForbidden, errMissingToken)
				return
			}
			sent := r.Header.Get(cfg.headerName)
			if sent == "" {
				sent = r.PostFormValue(cfg.fieldName)
			}
			if sent == "" || subtle.ConstantTimeCompare([]byte(sent), []byte(cookie.Value)) != 1 {
				cfg.errorHandler(w, r, http.StatusForbidden, errInvalidToken)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

var (
	errMissingToken = errors.New("csrf: token missing")
	errInvalidToken = errors.New("csrf: token invalid")
)

// ensureCookie returns the token from the request cookie, generating a fresh
// token and setting the cookie when one is absent or empty.
func (c *config) ensureCookie(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(c.cookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	token := generateToken()
	http.SetCookie(w, &http.Cookie{
		Name:     c.cookieName,
		Value:    token,
		Path:     c.path,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: c.sameSite,
	})
	return token
}

// NewContext returns a copy of ctx carrying the CSRF token.
func NewContext(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, ctxKey{}, token)
}

// Token returns the CSRF token stored in ctx, reporting whether one was
// present. It lets handlers and templates embed the token so clients can echo
// it back on unsafe requests.
func Token(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(ctxKey{}).(string)
	return token, ok
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// validateCookiePrefix enforces the RFC 6265bis cookie name prefix rules, which
// browsers require before granting the prefixes their security guarantees. The
// middleware never sets a Domain attribute, so only Secure and Path need
// checking here.
func validateCookiePrefix(name string, secure bool, path string) error {
	switch {
	case strings.HasPrefix(name, "__Host-"):
		if !secure || path != "/" {
			return errors.New(`csrf: a "__Host-" cookie name requires WithSecureCookie(true) and WithPath("/")`)
		}
	case strings.HasPrefix(name, "__Secure-"):
		if !secure {
			return errors.New(`csrf: a "__Secure-" cookie name requires WithSecureCookie(true)`)
		}
	}
	return nil
}

func generateToken() string {
	var b [tokenBytes]byte
	// crypto/rand.Read never returns an error on supported platforms.
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
