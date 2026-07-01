// Package ratelimit provides token-bucket rate-limiting middleware built on
// golang.org/x/time/rate. It can enforce a single global budget or an
// independent budget per key (for example per client IP). Requests that exceed
// the budget are rejected with 429 Too Many Requests and a Retry-After header.
package ratelimit

import (
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/maxclav/middleware"
	"golang.org/x/time/rate"
)

type config struct {
	limit        rate.Limit
	burst        int
	keyFunc      func(*http.Request) string
	maxKeys      int
	errorHandler middleware.ErrorHandler
}

// Option configures the ratelimit middleware.
type Option func(*config) error

// WithLimit sets the sustained refill rate in tokens per second. Defaults to 10.
func WithLimit(limit rate.Limit) Option {
	return func(c *config) error {
		c.limit = limit
		return nil
	}
}

// WithRPS sets the sustained refill rate from a requests-per-second value. It is
// a convenience wrapper over [WithLimit]. rps must be greater than zero.
func WithRPS(rps float64) Option {
	return func(c *config) error {
		if rps <= 0 {
			return errors.New("ratelimit: rps must be greater than zero")
		}
		c.limit = rate.Limit(rps)
		return nil
	}
}

// WithBurst sets the token-bucket depth, i.e. the maximum number of requests
// allowed in a burst. It must be greater than zero. Defaults to 20.
func WithBurst(burst int) Option {
	return func(c *config) error {
		if burst <= 0 {
			return errors.New("ratelimit: burst must be greater than zero")
		}
		c.burst = burst
		return nil
	}
}

// WithKeyFunc switches the middleware to per-key limiting: each distinct value
// returned by key gets its own token bucket. It must not be nil. Without it a
// single global bucket is shared by all requests.
func WithKeyFunc(key func(*http.Request) string) Option {
	return func(c *config) error {
		if key == nil {
			return errors.New("ratelimit: key function must not be nil")
		}
		c.keyFunc = key
		return nil
	}
}

// WithMaxKeys bounds the number of per-key buckets held in memory. When the
// limit would be exceeded, existing buckets are evicted to make room. It must be
// greater than zero. Defaults to 10000. It has no effect in global mode.
func WithMaxKeys(maxKeys int) Option {
	return func(c *config) error {
		if maxKeys <= 0 {
			return errors.New("ratelimit: maxKeys must be greater than zero")
		}
		c.maxKeys = maxKeys
		return nil
	}
}

// WithErrorHandler sets how the 429 response is rendered when a request is
// rejected. It must not be nil. Defaults to [middleware.DefaultErrorHandler].
func WithErrorHandler(h middleware.ErrorHandler) Option {
	return func(c *config) error {
		if h == nil {
			return errors.New("ratelimit: error handler must not be nil")
		}
		c.errorHandler = h
		return nil
	}
}

// New returns token-bucket rate-limiting middleware. By default a single global
// bucket is shared by all requests; supply [WithKeyFunc] for per-key limiting.
//
// When a request is not allowed the middleware sets a Retry-After header (a
// whole number of seconds, at least 1) and rejects it with 429 Too Many
// Requests via the configured [middleware.ErrorHandler]; the next handler is not
// called. Otherwise the next handler runs.
//
// In per-key mode the number of buckets is bounded by [WithMaxKeys], so memory
// use is bounded regardless of key cardinality. No background goroutines are
// started.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		limit:        rate.Limit(10),
		burst:        20,
		keyFunc:      nil,
		maxKeys:      10000,
		errorHandler: middleware.DefaultErrorHandler,
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

	retryAfter := retryAfterSeconds(cfg.limit)

	var (
		global *rate.Limiter
		// mu guards limiters, the per-key bucket map used in per-key mode. In
		// global mode limiters is nil and mu is never taken. global is set once
		// before any request is served and read-only thereafter, so it needs no
		// locking.
		mu       sync.Mutex
		limiters map[string]*rate.Limiter
	)
	if cfg.keyFunc == nil {
		global = rate.NewLimiter(cfg.limit, cfg.burst)
	} else {
		limiters = make(map[string]*rate.Limiter)
	}

	limiterFor := func(key string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		if l, ok := limiters[key]; ok {
			return l
		}
		// Bound memory: evict arbitrary entries until adding one stays within
		// maxKeys. Map iteration order is unspecified, which is fine here.
		for len(limiters) >= cfg.maxKeys {
			for k := range limiters {
				delete(limiters, k)
				break
			}
		}
		l := rate.NewLimiter(cfg.limit, cfg.burst)
		limiters[key] = l
		return l
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limiter := global
			if cfg.keyFunc != nil {
				limiter = limiterFor(cfg.keyFunc(r))
			}
			if !limiter.Allow() {
				w.Header().Set("Retry-After", retryAfter)
				cfg.errorHandler(w, r, http.StatusTooManyRequests, nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// retryAfterSeconds returns the Retry-After header value: a whole number of
// seconds, at least 1, approximating the time to refill one token.
func retryAfterSeconds(limit rate.Limit) string {
	secs := 1
	if limit > 0 {
		if n := int(math.Ceil(1 / float64(limit))); n > 1 {
			secs = n
		}
	}
	return strconv.Itoa(secs)
}

// ClientIP returns the host part of r.RemoteAddr, stripping the port when
// present and falling back to the raw RemoteAddr otherwise. It is a convenient
// key function for [WithKeyFunc].
//
// ClientIP does NOT consult X-Forwarded-For or any other client-supplied header,
// so it cannot be spoofed but also does not resolve the real client behind a
// trusted proxy. Handle proxy headers explicitly if your deployment requires it.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
