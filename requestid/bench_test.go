package requestid_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware/requestid"
)

type benchWriter struct{ h http.Header }

func (w *benchWriter) Header() http.Header {
	if w.h == nil {
		w.h = make(http.Header)
	}
	return w.h
}
func (w *benchWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *benchWriter) WriteHeader(int)             {}

func BenchmarkRequestID(b *testing.B) {
	mw, err := requestid.New()
	if err != nil {
		b.Fatal(err)
	}
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	w := &benchWriter{}
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(w, r)
	}
}
