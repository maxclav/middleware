// Package cors provides middleware that adds Cross-Origin Resource Sharing
// (CORS) headers to HTTP responses and answers CORS preflight requests.
//
// The middleware inspects the request's Origin header, decides whether the
// origin is allowed, and for allowed cross-origin requests it sets the
// appropriate Access-Control-* response headers. Preflight requests (OPTIONS
// carrying an Access-Control-Request-Method header) are answered directly with
// 204 No Content and are never forwarded to the wrapped handler. Requests
// without an Origin header, and cross-origin requests from disallowed origins,
// pass through unchanged.
package cors

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maxclav/middleware"
)

const wildcard = "*"

type config struct {
	allowedOrigins   []string
	allowOriginFunc  func(origin string) bool
	allowedMethods   []string
	allowedHeaders   []string
	exposedHeaders   []string
	allowCredentials bool
	maxAge           time.Duration
}

// Option configures the cors middleware.
type Option func(*config) error

// WithAllowedOrigins sets the list of origins permitted to make cross-origin
// requests. Origins are matched case-insensitively. The special value "*"
// allows any origin. The list must not be empty. Defaults to ["*"].
//
// Using "*" together with [WithAllowCredentials](true) is rejected by [New],
// as the CORS specification forbids that combination.
func WithAllowedOrigins(origins ...string) Option {
	return func(c *config) error {
		if len(origins) == 0 {
			return errors.New("cors: allowed origins must not be empty")
		}
		c.allowedOrigins = slices.Clone(origins)
		return nil
	}
}

// WithAllowOriginFunc sets a predicate that decides, per request, whether an
// origin is allowed. When set, it takes precedence over the configured origin
// list and makes the wildcard/credentials restriction moot, since the function
// is responsible for returning the correct decision. It must not be nil.
// Defaults to nil.
func WithAllowOriginFunc(fn func(origin string) bool) Option {
	return func(c *config) error {
		if fn == nil {
			return errors.New("cors: allow-origin func must not be nil")
		}
		c.allowOriginFunc = fn
		return nil
	}
}

// WithAllowedMethods sets the methods advertised in the
// Access-Control-Allow-Methods header on preflight responses. The list must not
// be empty. Defaults to ["GET", "HEAD", "POST"].
func WithAllowedMethods(methods ...string) Option {
	return func(c *config) error {
		if len(methods) == 0 {
			return errors.New("cors: allowed methods must not be empty")
		}
		c.allowedMethods = slices.Clone(methods)
		return nil
	}
}

// WithAllowedHeaders sets the request headers advertised in the
// Access-Control-Allow-Headers header on preflight responses. When left empty,
// the middleware reflects the request's Access-Control-Request-Headers value
// instead. Defaults to empty (reflect).
func WithAllowedHeaders(headers ...string) Option {
	return func(c *config) error {
		c.allowedHeaders = slices.Clone(headers)
		return nil
	}
}

// WithExposedHeaders sets the response headers advertised in the
// Access-Control-Expose-Headers header on actual (non-preflight) responses,
// making them readable by the browser. Defaults to none.
func WithExposedHeaders(headers ...string) Option {
	return func(c *config) error {
		c.exposedHeaders = slices.Clone(headers)
		return nil
	}
}

// WithAllowCredentials controls whether the Access-Control-Allow-Credentials
// header is sent, permitting the browser to include cookies and HTTP
// authentication with cross-origin requests. It cannot be combined with a
// wildcard origin unless an origin func is set. Defaults to false.
func WithAllowCredentials(allow bool) Option {
	return func(c *config) error {
		c.allowCredentials = allow
		return nil
	}
}

// WithMaxAge sets how long a preflight response may be cached, emitted as the
// Access-Control-Max-Age header in whole seconds. A value of zero omits the
// header. It must not be negative. Defaults to zero.
func WithMaxAge(d time.Duration) Option {
	return func(c *config) error {
		if d < 0 {
			return errors.New("cors: max age must not be negative")
		}
		c.maxAge = d
		return nil
	}
}

// New returns middleware that applies CORS headers and answers preflight
// requests according to the configured options.
//
// After applying the options, New rejects the combination of a wildcard origin
// with credentials (unless an origin func is set), since the CORS specification
// does not allow Access-Control-Allow-Origin: * to be paired with
// Access-Control-Allow-Credentials: true.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		allowedOrigins:   []string{wildcard},
		allowedMethods:   []string{http.MethodGet, http.MethodHead, http.MethodPost},
		allowedHeaders:   []string{},
		allowCredentials: false,
	}
	var errs []error
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if cfg.allowCredentials && cfg.allowOriginFunc == nil && containsWildcard(cfg.allowedOrigins) {
		errs = append(errs, errors.New("cors: wildcard origin cannot be used with credentials"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	allowMethods := strings.Join(cfg.allowedMethods, ", ")
	allowHeaders := strings.Join(cfg.allowedHeaders, ", ")
	exposeHeaders := strings.Join(cfg.exposedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Non-CORS request: nothing to do.
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if isPreflight(r) {
				cfg.handlePreflight(w, r, origin, allowMethods, allowHeaders)
				return
			}
			cfg.handleActual(w, origin, exposeHeaders)
			next.ServeHTTP(w, r)
		})
	}, nil
}

func (c *config) handlePreflight(w http.ResponseWriter, r *http.Request, origin, allowMethods, allowHeaders string) {
	h := w.Header()
	// Preflight responses always vary on these headers so caches key on them.
	h.Add("Vary", "Origin")
	h.Add("Vary", "Access-Control-Request-Method")
	h.Add("Vary", "Access-Control-Request-Headers")

	allowed, value := c.resolveOrigin(origin)
	if !allowed {
		// Preflight is never forwarded; answer without CORS headers.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.Set("Access-Control-Allow-Origin", value)
	h.Set("Access-Control-Allow-Methods", allowMethods)

	if allowHeaders != "" {
		h.Set("Access-Control-Allow-Headers", allowHeaders)
	} else if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
		h.Set("Access-Control-Allow-Headers", reqHeaders)
	}

	if c.allowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	if c.maxAge > 0 {
		h.Set("Access-Control-Max-Age", strconv.Itoa(int(c.maxAge.Seconds())))
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *config) handleActual(w http.ResponseWriter, origin, exposeHeaders string) {
	h := w.Header()
	h.Add("Vary", "Origin")

	allowed, value := c.resolveOrigin(origin)
	if !allowed {
		return
	}

	h.Set("Access-Control-Allow-Origin", value)
	if c.allowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	if exposeHeaders != "" {
		h.Set("Access-Control-Expose-Headers", exposeHeaders)
	}
}

// resolveOrigin reports whether origin is allowed and returns the value to send
// in Access-Control-Allow-Origin. The specific origin is echoed rather than "*"
// whenever an origin func matches, credentials are enabled, or the origin was
// matched exactly, so that only a truly wildcard, non-credentialed policy emits
// "*".
func (c *config) resolveOrigin(origin string) (allowed bool, value string) {
	if c.allowOriginFunc != nil {
		if c.allowOriginFunc(origin) {
			return true, origin
		}
		return false, ""
	}
	if containsWildcard(c.allowedOrigins) {
		if c.allowCredentials {
			return true, origin
		}
		return true, wildcard
	}
	for _, o := range c.allowedOrigins {
		if strings.EqualFold(o, origin) {
			return true, origin
		}
	}
	return false, ""
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

func containsWildcard(origins []string) bool {
	return slices.Contains(origins, wildcard)
}
