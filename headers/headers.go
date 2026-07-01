// Package headers provides middleware for arbitrary manipulation of request
// and response headers.
//
// Response headers can be set (replacing), added (appending) or removed, and
// request headers can be set before the next handler runs. Removal is applied
// after the downstream handler by wrapping the response writer, so a header the
// handler sets is still stripped before it reaches the client.
package headers

import (
	"errors"
	"net/http"

	"github.com/maxclav/middleware"
)

type kv struct {
	key   string
	value string
}

type config struct {
	setResponse    []kv
	addResponse    []kv
	removeResponse []string
	setRequest     []kv
}

// Option configures the headers middleware.
type Option func(*config) error

// WithResponseHeader sets a response header to value, replacing any existing
// values for key. key must not be empty.
func WithResponseHeader(key, value string) Option {
	return func(c *config) error {
		if key == "" {
			return errors.New("headers: response header key must not be empty")
		}
		c.setResponse = append(c.setResponse, kv{key, value})
		return nil
	}
}

// WithAddedResponseHeader appends value to a response header, preserving any
// existing values for key. key must not be empty.
func WithAddedResponseHeader(key, value string) Option {
	return func(c *config) error {
		if key == "" {
			return errors.New("headers: response header key must not be empty")
		}
		c.addResponse = append(c.addResponse, kv{key, value})
		return nil
	}
}

// WithRemovedResponseHeader removes a header from the response, even if the
// downstream handler sets it. key must not be empty.
func WithRemovedResponseHeader(key string) Option {
	return func(c *config) error {
		if key == "" {
			return errors.New("headers: response header key must not be empty")
		}
		c.removeResponse = append(c.removeResponse, key)
		return nil
	}
}

// WithRequestHeader sets a header on the inbound request before the next
// handler runs, replacing any existing values for key. The caller's request is
// left unchanged; a clone carries the mutation. key must not be empty.
func WithRequestHeader(key, value string) Option {
	return func(c *config) error {
		if key == "" {
			return errors.New("headers: request header key must not be empty")
		}
		c.setRequest = append(c.setRequest, kv{key, value})
		return nil
	}
}

// New returns middleware that applies the configured header mutations. The
// set/added response headers and request header mutations take effect before
// the next handler runs; removed response headers are stripped when the handler
// writes its status, so they never reach the client.
func New(opts ...Option) (middleware.Middleware, error) {
	var cfg config
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
			h := w.Header()
			for _, e := range cfg.setResponse {
				h.Set(e.key, e.value)
			}
			for _, e := range cfg.addResponse {
				h.Add(e.key, e.value)
			}

			if len(cfg.setRequest) > 0 {
				r = r.Clone(r.Context())
				for _, e := range cfg.setRequest {
					r.Header.Set(e.key, e.value)
				}
			}

			if len(cfg.removeResponse) > 0 {
				w = &removingWriter{ResponseWriter: w, keys: cfg.removeResponse}
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// removingWriter deletes the configured header keys when the response header is
// about to be written, so they are stripped regardless of what the downstream
// handler set.
type removingWriter struct {
	http.ResponseWriter
	keys    []string
	deleted bool
}

var _ http.ResponseWriter = (*removingWriter)(nil)

func (w *removingWriter) removeKeys() {
	if w.deleted {
		return
	}
	w.deleted = true
	for _, key := range w.keys {
		w.Header().Del(key)
	}
}

func (w *removingWriter) WriteHeader(code int) {
	w.removeKeys()
	w.ResponseWriter.WriteHeader(code)
}

func (w *removingWriter) Write(b []byte) (int, error) {
	// A direct Write triggers an implicit WriteHeader(200) on the underlying
	// writer, so strip the keys here too before the header is flushed.
	w.removeKeys()
	return w.ResponseWriter.Write(b)
}

func (w *removingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
