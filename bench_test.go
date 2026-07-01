package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware"
)

// nopWriter is a minimal http.ResponseWriter for benchmarks: it allocates its
// header map once and discards writes, so the measurement reflects the
// middleware overhead rather than the cost of a response recorder.
type nopWriter struct{ h http.Header }

func (w *nopWriter) Header() http.Header {
	if w.h == nil {
		w.h = make(http.Header)
	}
	return w.h
}
func (w *nopWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nopWriter) WriteHeader(int)             {}

// benchWrap is a middleware that adds one real handler-call layer.
func benchWrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func BenchmarkChainServe(b *testing.B) {
	// A three-layer chain, built without a duplicated-argument literal.
	c := middleware.New()
	for range 3 {
		c = c.Append(benchWrap)
	}
	h := c.ThenFunc(func(http.ResponseWriter, *http.Request) {})
	w := &nopWriter{}
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkWrapResponseWriter(b *testing.B) {
	w := &nopWriter{}

	b.ReportAllocs()
	for b.Loop() {
		rw := middleware.WrapResponseWriter(w)
		rw.WriteHeader(http.StatusOK)
		_ = rw.Status()
	}
}
