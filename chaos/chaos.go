// Package chaos provides fault-injection middleware for testing the resilience
// of HTTP clients and services.
//
// It exposes three constructors ([Abort], [Sleep] and [RandomResponse]) that
// share a single [Option] set, since they all decide whether to act using a
// configurable probability and source of randomness. Injecting a deterministic
// random function with [WithRandFloat] makes the behaviour reproducible in
// tests.
package chaos

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	"github.com/maxclav/middleware"
)

type config struct {
	probability float64
	randFloat   func() float64

	// Abort
	abortStatus int

	// Sleep
	minDelay time.Duration
	maxDelay time.Duration

	// RandomResponse
	statuses []int
}

// Option configures a chaos middleware. All three constructors share this
// option type.
type Option func(*config) error

// WithProbability sets the probability p, in [0, 1], that the fault is injected
// on any given request. A value of 0 never triggers; 1 always triggers.
// Defaults to 0.
func WithProbability(p float64) Option {
	return func(c *config) error {
		if p < 0 || p > 1 {
			return fmt.Errorf("chaos: probability must be in [0, 1], got %v", p)
		}
		c.probability = p
		return nil
	}
}

// WithRandFloat sets the function used to draw the [0, 1) value compared against
// the probability. It must not be nil. Tests can inject a deterministic
// function to force triggering. Defaults to [rand.Float64].
func WithRandFloat(fn func() float64) Option {
	return func(c *config) error {
		if fn == nil {
			return errors.New("chaos: rand func must not be nil")
		}
		c.randFloat = fn
		return nil
	}
}

// WithAbortStatus sets the status code written by [Abort] when it triggers. It
// must be a 4xx or 5xx code. Defaults to 500.
func WithAbortStatus(code int) Option {
	return func(c *config) error {
		if code < 400 || code > 599 {
			return fmt.Errorf("chaos: abort status must be in [400, 599], got %d", code)
		}
		c.abortStatus = code
		return nil
	}
}

// WithDelayRange sets the inclusive range from which [Sleep] draws its delay.
// The lower bound must not be negative and the upper bound must not be less than
// the lower bound. Defaults to [0, 1s].
func WithDelayRange(low, high time.Duration) Option {
	return func(c *config) error {
		if low < 0 {
			return errors.New("chaos: min delay must not be negative")
		}
		if high < low {
			return errors.New("chaos: max delay must not be less than min delay")
		}
		c.minDelay = low
		c.maxDelay = high
		return nil
	}
}

// WithStatuses sets the pool of status codes [RandomResponse] chooses from. The
// list must not be empty and every code must be a valid HTTP status in
// [100, 599]. Defaults to [500, 502, 503].
func WithStatuses(statuses ...int) Option {
	return func(c *config) error {
		if len(statuses) == 0 {
			return errors.New("chaos: statuses must not be empty")
		}
		for _, code := range statuses {
			if code < 100 || code > 599 {
				return fmt.Errorf("chaos: status must be in [100, 599], got %d", code)
			}
		}
		c.statuses = slices.Clone(statuses)
		return nil
	}
}

// newConfig builds the shared config from defaults and the given options,
// joining any validation errors.
func newConfig(opts ...Option) (config, error) {
	cfg := config{
		probability: 0,
		randFloat:   rand.Float64,
		abortStatus: http.StatusInternalServerError,
		minDelay:    0,
		maxDelay:    time.Second,
		statuses:    []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable},
	}
	var errs []error
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	return cfg, errors.Join(errs...)
}

// triggers reports whether the fault should be injected on this request.
func (c *config) triggers() bool {
	return c.randFloat() < c.probability
}

// Abort returns middleware that, with the configured probability, aborts the
// request before the next handler by writing the configured status code. When
// it does not trigger, the request proceeds to the next handler unchanged.
func Abort(opts ...Option) (middleware.Middleware, error) {
	cfg, err := newConfig(opts...)
	if err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.triggers() {
				http.Error(w, http.StatusText(cfg.abortStatus), cfg.abortStatus)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// Sleep returns middleware that, with the configured probability, delays the
// request by a random duration in the configured range before calling the next
// handler. The sleep respects the request context: if it is canceled while
// sleeping, the middleware returns without calling next. When it does not
// trigger, the request proceeds immediately.
func Sleep(opts ...Option) (middleware.Middleware, error) {
	cfg, err := newConfig(opts...)
	if err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.triggers() {
				if !sleep(r, cfg.randDelay()) {
					return // context canceled while sleeping
				}
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// randDelay picks a duration in [minDelay, maxDelay] using the injected random
// source.
func (c *config) randDelay() time.Duration {
	span := c.maxDelay - c.minDelay
	if span <= 0 {
		return c.minDelay
	}
	return c.minDelay + time.Duration(c.randFloat()*float64(span))
}

// sleep waits for d or until the request context is canceled. It reports true
// if the delay elapsed and false if the context was canceled first.
func sleep(r *http.Request, d time.Duration) bool {
	if d <= 0 {
		return r.Context().Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

// RandomResponse returns middleware that, with the configured probability,
// responds with a status code chosen at random from the configured set and does
// not call the next handler. When it does not trigger, the request proceeds to
// the next handler unchanged.
func RandomResponse(opts ...Option) (middleware.Middleware, error) {
	cfg, err := newConfig(opts...)
	if err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.triggers() {
				status := cfg.randStatus()
				http.Error(w, http.StatusText(status), status)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// randStatus picks one of the configured statuses using the injected random
// source. The index is clamped into [0, len-1] so that a randFloat result
// outside the documented [0, 1) range still selects a valid status rather than
// panicking.
func (c *config) randStatus() int {
	i := min(max(int(c.randFloat()*float64(len(c.statuses))), 0), len(c.statuses)-1)
	return c.statuses[i]
}
