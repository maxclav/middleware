package logger_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware/logger"
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

func benchmarkLogger(b *testing.B, level slog.Level) {
	b.Helper()
	mw, err := logger.New(logger.WithLogger(
		slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: level})),
	))
	if err != nil {
		b.Fatal(err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	w := &benchWriter{}
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)

	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkLoggerRecorded measures a request whose INFO record is emitted.
func BenchmarkLoggerRecorded(b *testing.B) { benchmarkLogger(b, slog.LevelInfo) }

// BenchmarkLoggerDropped measures a successful request whose INFO record is
// discarded because the handler is set to WARN. The middleware should not build
// the attribute set for a record that will be dropped.
func BenchmarkLoggerDropped(b *testing.B) { benchmarkLogger(b, slog.LevelWarn) }
