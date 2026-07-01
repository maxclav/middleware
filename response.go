package middleware

import "net/http"

// ResponseWriter is an [http.ResponseWriter] that records the response status
// code and the number of bytes written to the body. It is the shared primitive
// used by observability middlewares (logger, metrics) and by any middleware
// that needs to inspect the response after the handler runs.
//
// The underlying writer is reachable with Unwrap, so optional interfaces such
// as [http.Flusher] and [http.Hijacker] remain available through
// [http.NewResponseController].
type ResponseWriter interface {
	http.ResponseWriter

	// Status returns the response status code, or 0 if the header has not been
	// written yet.
	Status() int

	// BytesWritten returns the total number of body bytes written.
	BytesWritten() int

	// Written reports whether the response header has been written.
	Written() bool

	// Unwrap returns the underlying [http.ResponseWriter].
	Unwrap() http.ResponseWriter
}

// WrapResponseWriter adapts w into a [ResponseWriter]. If w already implements
// ResponseWriter it is returned unchanged, so wrapping is idempotent and cheap
// to apply in nested middleware.
func WrapResponseWriter(w http.ResponseWriter) ResponseWriter {
	if rw, ok := w.(ResponseWriter); ok {
		return rw
	}
	return &responseWriter{ResponseWriter: w}
}

var _ ResponseWriter = (*responseWriter)(nil)

type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *responseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *responseWriter) Status() int { return w.status }

func (w *responseWriter) BytesWritten() int { return w.bytes }

func (w *responseWriter) Written() bool { return w.wroteHeader }

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
