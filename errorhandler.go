package middleware

import "net/http"

// ErrorHandler renders an error response for a request that a middleware has
// decided to reject. status is the HTTP status code the middleware wants to
// return; err carries the underlying reason and may be nil.
//
// A middleware sets any status-specific headers (for example Retry-After or
// WWW-Authenticate) before invoking its ErrorHandler. Passing the same
// ErrorHandler to every middleware gives an application a single, consistent
// error-response format.
type ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)

// DefaultErrorHandler writes the status code together with its standard
// plain-text status message. It is the [ErrorHandler] used when a middleware is
// not given one.
func DefaultErrorHandler(w http.ResponseWriter, r *http.Request, status int, err error) {
	http.Error(w, http.StatusText(status), status)
}
