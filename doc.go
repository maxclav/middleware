// Package middleware provides a small, idiomatic toolkit for composing
// net/http middleware.
//
// The core of the package is the [Middleware] type — a type alias for
// func(http.Handler) http.Handler — and [Chain], an immutable, ordered
// collection of Middleware. Individual middlewares live in focused
// subpackages (recovery, requestid, cors, gzip, ...), each exposing a
// New(...Option) (Middleware, error) constructor. Subpackages that need a
// third-party dependency (jwt, otelmetrics, ...) keep it isolated, so
// importing this package pulls in nothing beyond the standard library.
//
// Example:
//
//	rec, _ := recovery.New()
//	rid, _ := requestid.New()
//	chain := middleware.New(rec, rid)
//	log.Fatal(http.ListenAndServe(":8080", chain.ThenFunc(index)))
package middleware
