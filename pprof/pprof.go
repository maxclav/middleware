// Package pprof provides middleware that exposes the net/http/pprof profiling
// endpoints under a configurable path prefix, without touching the global
// http.DefaultServeMux.
//
// Requests whose path falls under the prefix (default "/debug/pprof/") are
// served by the standard profiling handlers; all other requests pass through
// unchanged to the next handler.
//
// Security warning: the pprof endpoints expose sensitive runtime data: memory
// contents, goroutine stacks, command-line arguments and, via the CPU and trace
// profiles, the ability to consume significant resources. They MUST NOT be
// reachable by untrusted clients. Protect this middleware behind authentication
// and authorization, or bind it to an internal-only listener.
package pprof

import (
	"errors"
	"net/http"
	"net/http/pprof"
	"strings"

	"github.com/maxclav/middleware"
)

// DefaultPrefix is the default path prefix under which the pprof endpoints are
// served.
const DefaultPrefix = "/debug/pprof/"

type config struct {
	prefix string
}

// Option configures the pprof middleware.
type Option func(*config) error

// WithPrefix sets the path prefix under which the pprof endpoints are served.
// It must be non-empty and both start and end with "/". Defaults to
// [DefaultPrefix].
func WithPrefix(prefix string) Option {
	return func(c *config) error {
		if prefix == "" {
			return errors.New("pprof: prefix must not be empty")
		}
		if !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") {
			return errors.New("pprof: prefix must start and end with \"/\"")
		}
		c.prefix = prefix
		return nil
	}
}

// New returns middleware that serves the net/http/pprof endpoints under the
// configured prefix and passes every other request through to the next handler.
//
// The profiling handlers are registered on a private [http.ServeMux], so the
// middleware never relies on net/http/pprof's registration against
// http.DefaultServeMux.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{prefix: DefaultPrefix}
	var errs []error
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc(cfg.prefix, pprof.Index)
	mux.HandleFunc(cfg.prefix+"cmdline", pprof.Cmdline)
	mux.HandleFunc(cfg.prefix+"profile", pprof.Profile)
	mux.HandleFunc(cfg.prefix+"symbol", pprof.Symbol)
	mux.HandleFunc(cfg.prefix+"trace", pprof.Trace)
	for _, name := range []string{"goroutine", "heap", "allocs", "block", "threadcreate", "mutex"} {
		mux.Handle(cfg.prefix+name, pprof.Handler(name))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, cfg.prefix) {
				mux.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
