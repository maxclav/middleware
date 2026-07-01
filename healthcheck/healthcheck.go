// Package healthcheck provides middleware that serves liveness and readiness
// endpoints for HTTP services.
//
// The liveness endpoint always reports healthy, signalling that the process is
// running. The readiness endpoint runs the configured checks and reports
// healthy only when all of them pass, signalling that the service is ready to
// receive traffic. Requests to any other path fall through to the next handler.
//
// A failing readiness response lists each failed check's name and error text,
// which can reveal internal detail. Expose these endpoints only to trusted
// networks, or keep check error messages free of sensitive information.
package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/maxclav/middleware"
)

type namedCheck struct {
	name string
	fn   func(context.Context) error
}

type config struct {
	livenessPath  string
	readinessPath string
	checks        []namedCheck
}

// Option configures the healthcheck middleware.
type Option func(*config) error

// WithLivenessPath sets the path served by the liveness endpoint. It must not
// be empty and must begin with "/". Defaults to "/healthz".
func WithLivenessPath(path string) Option {
	return func(c *config) error {
		if err := validatePath("liveness", path); err != nil {
			return err
		}
		c.livenessPath = path
		return nil
	}
}

// WithReadinessPath sets the path served by the readiness endpoint. It must not
// be empty and must begin with "/". Defaults to "/readyz".
func WithReadinessPath(path string) Option {
	return func(c *config) error {
		if err := validatePath("readiness", path); err != nil {
			return err
		}
		c.readinessPath = path
		return nil
	}
}

// WithReadinessCheck registers a readiness check. name must not be empty and fn
// must not be nil. The option may be supplied multiple times to register
// several checks, all of which must pass for the service to be ready.
func WithReadinessCheck(name string, fn func(ctx context.Context) error) Option {
	return func(c *config) error {
		if name == "" {
			return errors.New("healthcheck: readiness check name must not be empty")
		}
		if fn == nil {
			return errors.New("healthcheck: readiness check function must not be nil")
		}
		c.checks = append(c.checks, namedCheck{name: name, fn: fn})
		return nil
	}
}

// New returns middleware that serves the liveness and readiness endpoints. A
// GET or HEAD request to the liveness path returns 200 with body "ok". A GET or
// HEAD request to the readiness path runs every registered check and returns
// 200 "ok" when all pass, or 503 with a plain-text list of failures otherwise.
// All other requests are passed to the next handler.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		livenessPath:  "/healthz",
		readinessPath: "/readyz",
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
			if isHealthMethod(r.Method) {
				switch r.URL.Path {
				case cfg.livenessPath:
					writeOK(w)
					return
				case cfg.readinessPath:
					cfg.serveReadiness(w, r)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func (c *config) serveReadiness(w http.ResponseWriter, r *http.Request) {
	var failures []string
	for _, check := range c.checks {
		if err := check.fn(r.Context()); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", check.name, err))
		}
	}
	if len(failures) == 0 {
		writeOK(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, strings.Join(failures, "\n")+"\n")
}

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func isHealthMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func validatePath(kind, path string) error {
	if path == "" {
		return fmt.Errorf("healthcheck: %s path must not be empty", kind)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("healthcheck: %s path %q must start with %q", kind, path, "/")
	}
	return nil
}
