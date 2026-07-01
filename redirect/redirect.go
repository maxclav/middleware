// Package redirect provides middleware that canonicalizes the scheme and host
// of incoming requests, issuing an HTTP redirect when they differ from the
// desired canonical form.
package redirect

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/maxclav/middleware"
)

type config struct {
	scheme         string
	host           string
	code           int
	trustForwarded bool
}

// Option configures the redirect middleware.
type Option func(*config) error

// WithScheme forces the canonical scheme. It accepts only "http" or "https".
// When unset, the scheme is not canonicalized.
func WithScheme(s string) Option {
	return func(c *config) error {
		if s != "http" && s != "https" {
			return fmt.Errorf("redirect: scheme must be \"http\" or \"https\", got %q", s)
		}
		c.scheme = s
		return nil
	}
}

// WithHost forces the canonical host. It must not be empty. When unset, the
// host is not canonicalized.
func WithHost(h string) Option {
	return func(c *config) error {
		if h == "" {
			return errors.New("redirect: host must not be empty")
		}
		c.host = h
		return nil
	}
}

// WithCode sets the HTTP status code used for the redirect. It must be a 3xx
// code. Defaults to [http.StatusPermanentRedirect] (308).
func WithCode(code int) Option {
	return func(c *config) error {
		if code < 300 || code > 399 {
			return fmt.Errorf("redirect: code must be a 3xx status, got %d", code)
		}
		c.code = code
		return nil
	}
}

// WithTrustForwardedHeaders controls whether the incoming scheme is derived from
// the X-Forwarded-Proto header when present. Enable this only behind a trusted
// proxy that sets the header. Defaults to false.
func WithTrustForwardedHeaders(trust bool) Option {
	return func(c *config) error {
		c.trustForwarded = trust
		return nil
	}
}

// New returns middleware that redirects requests whose scheme or host differ
// from the configured canonical form, preserving the request path and query
// string. Requests that already match pass through unchanged. If neither a
// scheme nor a host is configured, no request is ever redirected.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		code: http.StatusPermanentRedirect,
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

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scheme := requestScheme(r, cfg.trustForwarded)
			host := r.Host

			target := scheme
			targetHost := host
			if cfg.scheme != "" {
				target = cfg.scheme
			}
			if cfg.host != "" {
				targetHost = cfg.host
			}

			if target == scheme && targetHost == host {
				next.ServeHTTP(w, r)
				return
			}

			url := target + "://" + targetHost + r.URL.RequestURI()
			http.Redirect(w, r, url, cfg.code)
		})
	}, nil
}

// requestScheme derives the request's scheme, preferring X-Forwarded-Proto when
// forwarded headers are trusted, then TLS state, defaulting to "http".
func requestScheme(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			return proto
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
