// Package skipif provides a combinator that conditionally skips a wrapped
// middleware based on a request predicate.
package skipif

import (
	"errors"
	"net/http"
	"strings"

	"github.com/maxclav/middleware"
)

// Predicate reports whether the wrapped middleware should be skipped for the
// given request.
type Predicate func(*http.Request) bool

// New returns middleware that applies mw only when skip reports false. When skip
// reports true, the wrapped middleware is bypassed and the next handler is
// invoked directly. Both skip and mw are required.
func New(skip Predicate, mw middleware.Middleware) (middleware.Middleware, error) {
	var errs []error
	if skip == nil {
		errs = append(errs, errors.New("skipif: predicate must not be nil"))
	}
	if mw == nil {
		errs = append(errs, errors.New("skipif: middleware must not be nil"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip(r) {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}, nil
}

// PathHasPrefix returns a [Predicate] that reports true when the request path
// begins with any of the given prefixes.
func PathHasPrefix(prefixes ...string) Predicate {
	return func(r *http.Request) bool {
		for _, prefix := range prefixes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				return true
			}
		}
		return false
	}
}

// MethodIs returns a [Predicate] that reports true when the request method
// matches any of the given methods, compared case-insensitively.
func MethodIs(methods ...string) Predicate {
	return func(r *http.Request) bool {
		for _, method := range methods {
			if strings.EqualFold(r.Method, method) {
				return true
			}
		}
		return false
	}
}
