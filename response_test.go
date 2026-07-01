package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware"
)

func TestWrapResponseWriterRecordsStatusAndBytes(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := middleware.WrapResponseWriter(rec)

	rw.WriteHeader(http.StatusTeapot)
	n, err := rw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := rw.Status(); got != http.StatusTeapot {
		t.Errorf("Status() = %d, want %d", got, http.StatusTeapot)
	}
	if rw.BytesWritten() != n || n != 5 {
		t.Errorf("BytesWritten() = %d, want 5", rw.BytesWritten())
	}
	if !rw.Written() {
		t.Error("Written() = false, want true")
	}
}

func TestWriteDefaultsStatusToOK(t *testing.T) {
	rw := middleware.WrapResponseWriter(httptest.NewRecorder())
	if _, err := rw.Write([]byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := rw.Status(); got != http.StatusOK {
		t.Errorf("Status() = %d, want %d", got, http.StatusOK)
	}
}

func TestWriteHeaderIsIdempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := middleware.WrapResponseWriter(rec)
	rw.WriteHeader(http.StatusCreated)
	rw.WriteHeader(http.StatusBadGateway) // must be ignored

	if got := rw.Status(); got != http.StatusCreated {
		t.Errorf("Status() = %d, want %d", got, http.StatusCreated)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("recorder code = %d, want %d", rec.Code, http.StatusCreated)
	}
}

func TestWrapIsIdempotent(t *testing.T) {
	rw := middleware.WrapResponseWriter(httptest.NewRecorder())
	if again := middleware.WrapResponseWriter(rw); again != rw {
		t.Error("wrapping an already-wrapped ResponseWriter allocated a new one")
	}
}

func TestUnwrapReturnsUnderlyingWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := middleware.WrapResponseWriter(rec)
	if rw.Unwrap() != http.ResponseWriter(rec) {
		t.Error("Unwrap() did not return the original writer")
	}
	// The response controller must be able to reach the underlying writer.
	if err := http.NewResponseController(rw).Flush(); err != nil {
		t.Errorf("Flush via ResponseController: %v", err)
	}
}
