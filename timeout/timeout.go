// Package timeout provides middleware that enforces a per-request time limit by
// wrapping the standard library's [http.TimeoutHandler].
//
// Note: because it builds on [http.TimeoutHandler], the middleware buffers the
// entire response in memory and does not support response streaming/flushing
// (the wrapped [http.ResponseWriter] does not implement [http.Flusher]) or
// connection hijacking (it does not implement [http.Hijacker]). It is therefore
// unsuitable in front of Server-Sent Events, chunked streaming, or WebSocket
// upgrade endpoints.
package timeout

import (
	"errors"
	"net/http"
	"time"

	"github.com/maxclav/middleware"
)

type config struct {
	timeout time.Duration
	message string
}

// Option configures the timeout middleware.
type Option func(*config) error

// WithTimeout sets the maximum duration a request may run before it is aborted
// with a 503 Service Unavailable response. It is required and must be greater
// than zero.
func WithTimeout(d time.Duration) Option {
	return func(c *config) error {
		if d <= 0 {
			return errors.New("timeout: timeout must be > 0")
		}
		c.timeout = d
		return nil
	}
}

// WithMessage sets the plain-text body written when a request times out.
// Defaults to "request timed out".
func WithMessage(msg string) Option {
	return func(c *config) error {
		c.message = msg
		return nil
	}
}

// New returns middleware that limits how long a request may run. It wraps
// [http.TimeoutHandler], which cancels the request context and writes a 503
// Service Unavailable response with the configured message when the deadline is
// exceeded. WithTimeout is required.
//
// Note: because it wraps [http.TimeoutHandler], the returned middleware buffers
// the entire response and does not support response streaming/flushing or
// connection hijacking, so do not place it in front of Server-Sent Events,
// chunked streaming, or WebSocket upgrade endpoints.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		message: "request timed out",
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
	if cfg.timeout <= 0 {
		return nil, errors.New("timeout: WithTimeout is required and must be > 0")
	}

	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, cfg.timeout, cfg.message)
	}, nil
}
