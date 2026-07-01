// Package requestid provides middleware that assigns a unique identifier to
// each request, stores it in the request context, and echoes it in a response
// header for end-to-end correlation.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/maxclav/middleware"
)

// HeaderName is the default HTTP header used to read and write the request ID.
const HeaderName = "X-Request-ID"

type ctxKey struct{}

type config struct {
	header    string
	generator func() string
	trust     bool
}

// Option configures the requestid middleware.
type Option func(*config) error

// WithHeader sets the header used both to read an inherited ID and to write the
// resolved one. It must not be empty. Defaults to [HeaderName].
func WithHeader(name string) Option {
	return func(c *config) error {
		if name == "" {
			return errors.New("requestid: header name must not be empty")
		}
		c.header = name
		return nil
	}
}

// WithGenerator sets the function that produces a new ID when none is inherited
// from the request. It must not be nil. Defaults to a 128-bit random hex ID.
func WithGenerator(gen func() string) Option {
	return func(c *config) error {
		if gen == nil {
			return errors.New("requestid: generator must not be nil")
		}
		c.generator = gen
		return nil
	}
}

// WithTrustIncoming controls whether an ID supplied by the client in the
// configured header is accepted. When false, a fresh ID is always generated,
// which is the safer choice for internet-facing edges. Defaults to true.
func WithTrustIncoming(trust bool) Option {
	return func(c *config) error {
		c.trust = trust
		return nil
	}
}

// New returns middleware that ensures every request carries an ID. If the
// request already has one in the configured header and incoming IDs are
// trusted, it is reused; otherwise a new ID is generated. The resolved ID is
// stored in the request context (see [FromContext]) and written to the response
// header.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		header:    HeaderName,
		generator: randomID,
		trust:     true,
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
			var id string
			if cfg.trust {
				id = r.Header.Get(cfg.header)
			}
			if id == "" {
				id = cfg.generator()
			}
			w.Header().Set(cfg.header, id)
			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), id)))
		})
	}, nil
}

// NewContext returns a copy of ctx carrying the request ID.
func NewContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request ID stored in ctx, reporting whether one was
// present.
func FromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKey{}).(string)
	return id, ok
}

func randomID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error on supported platforms.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
