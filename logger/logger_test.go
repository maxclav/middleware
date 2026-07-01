package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware/logger"
)

func TestLogsRequestFields(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil))

	mw, err := logger.New(logger.WithLogger(l))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/things", http.NoBody))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log is not valid JSON: %v (%q)", err, buf.String())
	}
	if rec["method"] != http.MethodPost {
		t.Errorf("method = %v, want POST", rec["method"])
	}
	if rec["path"] != "/things" {
		t.Errorf("path = %v, want /things", rec["path"])
	}
	if rec["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want 201", rec["status"])
	}
	if rec["bytes"] != float64(5) {
		t.Errorf("bytes = %v, want 5", rec["bytes"])
	}
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
}

func TestLevelReflectsStatus(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil))
	mw, _ := logger.New(logger.WithLogger(l))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	var rec map[string]any
	_ = json.Unmarshal(buf.Bytes(), &rec)
	if rec["level"] != "ERROR" {
		t.Fatalf("level = %v, want ERROR for 502", rec["level"])
	}
}

func TestAttrsHookIsApplied(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil))
	mw, _ := logger.New(
		logger.WithLogger(l),
		logger.WithAttrs(func(*http.Request) []slog.Attr {
			return []slog.Attr{slog.String("tenant", "acme")}
		}),
	)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	var rec map[string]any
	_ = json.Unmarshal(buf.Bytes(), &rec)
	if rec["tenant"] != "acme" {
		t.Fatalf("custom attr missing: %q", buf.String())
	}
}

func TestOptionValidationErrors(t *testing.T) {
	cases := map[string]logger.Option{
		"nil logger":     logger.WithLogger(nil),
		"nil attrs hook": logger.WithAttrs(nil),
		"nil level func": logger.WithLevelFunc(nil),
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := logger.New(opt); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestClientErrorLogsAtWarn(t *testing.T) {
	var buf bytes.Buffer
	mw, _ := logger.New(logger.WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log is not valid JSON: %v", err)
	}
	if rec["level"] != "WARN" {
		t.Fatalf("level = %v, want WARN for 404", rec["level"])
	}
}

func TestCustomLevelFuncAndMessage(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	mw, _ := logger.New(
		logger.WithLogger(slog.New(handler)),
		logger.WithMessage("access"),
		logger.WithLevelFunc(func(int) slog.Level { return slog.LevelDebug }),
	)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log is not valid JSON: %v", err)
	}
	if rec["level"] != "DEBUG" {
		t.Errorf("level = %v, want DEBUG", rec["level"])
	}
	if rec["msg"] != "access" {
		t.Errorf("msg = %v, want access", rec["msg"])
	}
}

func TestDefaultMessage(t *testing.T) {
	var buf bytes.Buffer
	mw, _ := logger.New(logger.WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	var rec map[string]any
	_ = json.Unmarshal(buf.Bytes(), &rec)
	if rec["msg"] != "http request" {
		t.Fatalf("default msg = %v, want %q", rec["msg"], "http request")
	}
}
