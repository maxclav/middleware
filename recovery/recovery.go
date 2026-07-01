// Package recovery provides middleware that recovers from panics in HTTP
// handlers, logs them with log/slog, and returns a 500 response.
package recovery

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/maxclav/middleware"
)

type config struct {
	logger       *slog.Logger
	errorHandler middleware.ErrorHandler
	stackTrace   bool
}

// Option configures the recovery middleware.
type Option func(*config) error

// WithLogger sets the logger used to report panics. It must not be nil.
// Defaults to [slog.Default].
func WithLogger(l *slog.Logger) Option {
	return func(c *config) error {
		if l == nil {
			return errors.New("recovery: logger must not be nil")
		}
		c.logger = l
		return nil
	}
}

// WithErrorHandler sets how the 500 response is rendered when a panic is
// recovered before the handler has written anything. It must not be nil.
// Defaults to [middleware.DefaultErrorHandler].
func WithErrorHandler(h middleware.ErrorHandler) Option {
	return func(c *config) error {
		if h == nil {
			return errors.New("recovery: error handler must not be nil")
		}
		c.errorHandler = h
		return nil
	}
}

// WithStackTrace controls whether the recovered panic's stack trace is included
// in the log record. Defaults to true.
func WithStackTrace(enabled bool) Option {
	return func(c *config) error {
		c.stackTrace = enabled
		return nil
	}
}

// New returns middleware that recovers from panics in downstream handlers. The
// panic is logged and, if the handler has not already written a response, the
// configured [middleware.ErrorHandler] renders a 500 Internal Server Error.
//
// [http.ErrAbortHandler] is re-panicked unchanged, honouring the net/http
// convention that it signals an intentional abort rather than a real panic.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		logger:       slog.Default(),
		errorHandler: middleware.DefaultErrorHandler,
		stackTrace:   true,
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
			rw := middleware.WrapResponseWriter(w)
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				err, ok := rec.(error)
				if !ok {
					err = fmt.Errorf("%v", rec)
				}

				attrs := []slog.Attr{
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", rec),
				}
				if cfg.stackTrace {
					attrs = append(attrs, slog.String("stack", string(debug.Stack())))
				}
				cfg.logger.LogAttrs(r.Context(), slog.LevelError, "recovered from panic", attrs...)

				if !rw.Written() {
					cfg.errorHandler(rw, r, http.StatusInternalServerError, err)
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}, nil
}
