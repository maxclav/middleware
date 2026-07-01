package middleware

import "net/http"

// Middleware wraps an [http.Handler], returning a new handler that layers
// additional behaviour around the original.
//
// It is a type alias for the canonical net/http middleware signature, so any
// existing func(http.Handler) http.Handler value (from the standard library,
// chi, gorilla, alice or your own code) satisfies it without conversion.
type Middleware = func(http.Handler) http.Handler

// Chain is an immutable, ordered collection of [Middleware]. The zero value is
// an empty, ready-to-use Chain. Every method returns a new Chain and never
// mutates the receiver, so a Chain is safe to share and to reuse as a template.
//
// Middleware executes in the order it was added: the first Middleware is the
// outermost wrapper: it runs first on the way in and last on the way out.
type Chain struct {
	middlewares []Middleware
}

// New creates a [Chain] from the given middlewares, applied in order.
func New(middlewares ...Middleware) Chain {
	return Chain{middlewares: clone(middlewares)}
}

// Append returns a new [Chain] with the given middlewares added after the
// existing ones. The receiver is left unchanged.
func (c Chain) Append(middlewares ...Middleware) Chain {
	merged := make([]Middleware, 0, len(c.middlewares)+len(middlewares))
	merged = append(merged, c.middlewares...)
	merged = append(merged, middlewares...)
	return Chain{middlewares: merged}
}

// Extend returns a new [Chain] with the middlewares of other appended to the
// receiver's. The receiver is left unchanged.
func (c Chain) Extend(other Chain) Chain {
	return c.Append(other.middlewares...)
}

// Then wraps h with the Chain's middlewares and returns the resulting handler.
// If h is nil, [http.DefaultServeMux] is used, mirroring net/http conventions.
//
// Then is safe to call multiple times: it never mutates the receiver.
func (c Chain) Then(h http.Handler) http.Handler {
	if h == nil {
		h = http.DefaultServeMux
	}
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		h = c.middlewares[i](h)
	}
	return h
}

// ThenFunc is like [Chain.Then] but accepts an [http.HandlerFunc].
func (c Chain) ThenFunc(fn http.HandlerFunc) http.Handler {
	if fn == nil {
		return c.Then(nil)
	}
	return c.Then(fn)
}

func clone(m []Middleware) []Middleware {
	if len(m) == 0 {
		return nil
	}
	out := make([]Middleware, len(m))
	copy(out, m)
	return out
}
