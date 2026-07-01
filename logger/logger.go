// Package logger provides middleware that logs HTTP requests using log/slog.
package logger

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maxclav/middleware"
)

type config struct {
	logger  *slog.Logger
	attrs   func(*http.Request) []slog.Attr
	level   func(status int) slog.Level
	message string
}

// Option configures the logger middleware.
type Option func(*config) error

// WithLogger sets the destination logger. It must not be nil. Defaults to
// [slog.Default].
func WithLogger(l *slog.Logger) Option {
	return func(c *config) error {
		if l == nil {
			return errors.New("logger: logger must not be nil")
		}
		c.logger = l
		return nil
	}
}

// WithAttrs registers a hook that derives extra attributes from the request,
// such as a request ID pulled from the context, and appends them to every
// log record.
func WithAttrs(fn func(*http.Request) []slog.Attr) Option {
	return func(c *config) error {
		if fn == nil {
			return errors.New("logger: attrs hook must not be nil")
		}
		c.attrs = fn
		return nil
	}
}

// WithLevelFunc customises the log level selected from the response status
// code. It must not be nil. The default maps 5xx to Error, 4xx to Warn and
// everything else to Info.
func WithLevelFunc(fn func(status int) slog.Level) Option {
	return func(c *config) error {
		if fn == nil {
			return errors.New("logger: level function must not be nil")
		}
		c.level = fn
		return nil
	}
}

// WithMessage sets the log message. Defaults to "http request".
func WithMessage(msg string) Option {
	return func(c *config) error {
		c.message = msg
		return nil
	}
}

// New returns middleware that logs one record per request with the method,
// path, status code, response size, duration and remote address. The response
// status and size are captured via [middleware.WrapResponseWriter].
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		logger:  slog.Default(),
		level:   defaultLevel,
		message: "http request",
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
			start := time.Now()
			rw := middleware.WrapResponseWriter(w)

			next.ServeHTTP(rw, r)

			status := rw.Status()
			if status == 0 {
				status = http.StatusOK
			}
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", rw.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote", r.RemoteAddr),
			}
			if cfg.attrs != nil {
				attrs = append(attrs, cfg.attrs(r)...)
			}
			cfg.logger.LogAttrs(r.Context(), cfg.level(status), cfg.message, attrs...)
		})
	}, nil
}

func defaultLevel(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
