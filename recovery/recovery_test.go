package recovery_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxclav/middleware/recovery"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRecoversPanicAndReturns500(t *testing.T) {
	mw, err := recovery.New(recovery.WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestErrAbortHandlerPropagates(t *testing.T) {
	mw, _ := recovery.New(recovery.WithLogger(discardLogger()))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))
}

func TestNoPanicPassesThrough(t *testing.T) {
	mw, _ := recovery.New(recovery.WithLogger(discardLogger()))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "ok")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusTeapot || rec.Body.String() != "ok" {
		t.Fatalf("passthrough broken: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestPanicIsLoggedWithStack(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	mw, _ := recovery.New(recovery.WithLogger(logger))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	out := buf.String()
	if !strings.Contains(out, "recovered from panic") || !strings.Contains(out, "kaboom") {
		t.Fatalf("log missing panic details: %q", out)
	}
	if !strings.Contains(out, "stack=") {
		t.Fatalf("log missing stack trace: %q", out)
	}
}

func TestNilLoggerIsRejected(t *testing.T) {
	if _, err := recovery.New(recovery.WithLogger(nil)); err == nil {
		t.Fatal("expected error for nil logger")
	}
}

func TestNilErrorHandlerIsRejected(t *testing.T) {
	if _, err := recovery.New(recovery.WithErrorHandler(nil)); err == nil {
		t.Fatal("expected error for nil error handler")
	}
}

func TestCustomErrorHandlerReceivesPanic(t *testing.T) {
	var gotStatus int
	var gotErr error
	eh := func(w http.ResponseWriter, _ *http.Request, status int, err error) {
		gotStatus, gotErr = status, err
		w.WriteHeader(status)
	}
	mw, err := recovery.New(recovery.WithLogger(discardLogger()), recovery.WithErrorHandler(eh))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if gotStatus != http.StatusInternalServerError {
		t.Errorf("handler status = %d, want 500", gotStatus)
	}
	if gotErr == nil {
		t.Error("error handler received a nil error")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("response code = %d, want 500", rec.Code)
	}
}

func TestErrorValuedPanicIsPreserved(t *testing.T) {
	sentinel := errors.New("sentinel failure")
	var got error
	eh := func(_ http.ResponseWriter, _ *http.Request, _ int, err error) { got = err }
	mw, _ := recovery.New(recovery.WithLogger(discardLogger()), recovery.WithErrorHandler(eh))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(sentinel) }))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if !errors.Is(got, sentinel) {
		t.Fatalf("error handler got %v, want the panicked error value", got)
	}
}

func TestResponseNotOverwrittenAfterWrite(t *testing.T) {
	mw, _ := recovery.New(recovery.WithLogger(discardLogger()))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("after write")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (a committed response must not be overwritten)", rec.Code)
	}
}

func TestStackTraceCanBeDisabled(t *testing.T) {
	var buf bytes.Buffer
	mw, _ := recovery.New(
		recovery.WithLogger(slog.New(slog.NewTextHandler(&buf, nil))),
		recovery.WithStackTrace(false),
	)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("no stack please") }))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if strings.Contains(buf.String(), "stack=") {
		t.Fatalf("stack trace should be omitted, got: %q", buf.String())
	}
}
