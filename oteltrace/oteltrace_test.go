package oteltrace_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/maxclav/middleware/oteltrace"
)

// newRecorder builds a SpanRecorder-backed TracerProvider and registers its
// shutdown so recorded spans can be inspected deterministically.
func newRecorder(t *testing.T) (*tracetest.SpanRecorder, *sdktrace.TracerProvider) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return sr, tp
}

// attr looks up a span attribute by key.
func attr(span sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// only returns the single recorded span, failing if the count differs.
func only(t *testing.T, sr *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	return spans[0]
}

func TestRecordsSpanWithName(t *testing.T) {
	t.Parallel()
	sr, tp := newRecorder(t)
	mw, err := oteltrace.New(oteltrace.WithTracerProvider(tp))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/things", http.NoBody))

	span := only(t, sr)
	if got := span.Name(); got != "GET /things" {
		t.Fatalf("name = %q, want %q", got, "GET /things")
	}
	if span.SpanKind() != trace.SpanKindServer {
		t.Fatalf("kind = %v, want server", span.SpanKind())
	}
	v, ok := attr(span, "http.response.status_code")
	if !ok {
		t.Fatal("missing http.response.status_code attribute")
	}
	if v.AsInt64() != http.StatusCreated {
		t.Fatalf("status attr = %d, want %d", v.AsInt64(), http.StatusCreated)
	}
	if v, ok := attr(span, "http.request.method"); !ok || v.AsString() != http.MethodGet {
		t.Fatalf("method attr = %v (ok=%v), want GET", v.AsString(), ok)
	}
	if v, ok := attr(span, "url.path"); !ok || v.AsString() != "/things" {
		t.Fatalf("url.path attr = %v (ok=%v), want /things", v.AsString(), ok)
	}
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		write     int // status the handler writes; 0 means write nothing
		wantAttr  int64
		wantError bool
	}{
		"server error sets error status": {write: http.StatusInternalServerError, wantAttr: http.StatusInternalServerError, wantError: true},
		"ok has no error status":         {write: http.StatusOK, wantAttr: http.StatusOK, wantError: false},
		"client error is not error":      {write: http.StatusNotFound, wantAttr: http.StatusNotFound, wantError: false},
		"default status is 200":          {write: 0, wantAttr: http.StatusOK, wantError: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sr, tp := newRecorder(t)
			mw, _ := oteltrace.New(oteltrace.WithTracerProvider(tp))
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.write != 0 {
					w.WriteHeader(tc.write)
				}
			}))

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

			span := only(t, sr)
			if v, ok := attr(span, "http.response.status_code"); !ok || v.AsInt64() != tc.wantAttr {
				t.Fatalf("status attr = %d (ok=%v), want %d", v.AsInt64(), ok, tc.wantAttr)
			}
			gotError := span.Status().Code == codes.Error
			if gotError != tc.wantError {
				t.Fatalf("error status = %v, want %v", gotError, tc.wantError)
			}
		})
	}
}

func TestContextPropagation(t *testing.T) {
	t.Parallel()
	sr, tp := newRecorder(t)
	prop := propagation.TraceContext{}
	mw, _ := oteltrace.New(
		oteltrace.WithTracerProvider(tp),
		oteltrace.WithPropagators(prop),
	)

	// Build a parent span context and inject it into the request headers.
	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), parent)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	prop.Inject(ctx, propagation.HeaderCarrier(req.Header))

	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	span := only(t, sr)
	if span.SpanContext().TraceID() != traceID {
		t.Fatalf("trace id = %s, want %s", span.SpanContext().TraceID(), traceID)
	}
	if span.Parent().SpanID() != spanID {
		t.Fatalf("parent span id = %s, want %s", span.Parent().SpanID(), spanID)
	}
}

func TestCustomSpanNameFunc(t *testing.T) {
	t.Parallel()
	sr, tp := newRecorder(t)
	mw, _ := oteltrace.New(
		oteltrace.WithTracerProvider(tp),
		oteltrace.WithSpanNameFunc(func(*http.Request) string { return "fixed-name" }),
	)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", http.NoBody))

	if got := only(t, sr).Name(); got != "fixed-name" {
		t.Fatalf("name = %q, want fixed-name", got)
	}
}

func TestProtocolVersionAttribute(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		major, minor int
		proto        string
		want         string
	}{
		"http 1.1":                    {major: 1, minor: 1, proto: "HTTP/1.1", want: "1.1"},
		"http 1.0":                    {major: 1, minor: 0, proto: "HTTP/1.0", want: "1.0"},
		"http 2":                      {major: 2, minor: 0, proto: "HTTP/2.0", want: "2"},
		"http 3":                      {major: 3, minor: 0, proto: "HTTP/3.0", want: "3"},
		"unknown falls back to proto": {major: 9, minor: 4, proto: "HTTP/9.4", want: "HTTP/9.4"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sr, tp := newRecorder(t)
			mw, _ := oteltrace.New(oteltrace.WithTracerProvider(tp))
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.ProtoMajor, req.ProtoMinor, req.Proto = tc.major, tc.minor, tc.proto
			h.ServeHTTP(httptest.NewRecorder(), req)

			v, ok := attr(only(t, sr), "network.protocol.version")
			if !ok || v.AsString() != tc.want {
				t.Fatalf("protocol version = %q (ok=%v), want %q", v.AsString(), ok, tc.want)
			}
		})
	}
}

func TestInvalidOptionsRejected(t *testing.T) {
	t.Parallel()
	tests := map[string]oteltrace.Option{
		"nil tracer provider": oteltrace.WithTracerProvider(nil),
		"nil propagators":     oteltrace.WithPropagators(nil),
		"nil span name func":  oteltrace.WithSpanNameFunc(nil),
	}
	for name, opt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := oteltrace.New(opt); err == nil {
				t.Fatal("expected error for invalid option")
			}
		})
	}

	t.Run("aggregated", func(t *testing.T) {
		t.Parallel()
		_, err := oteltrace.New(
			oteltrace.WithTracerProvider(nil),
			oteltrace.WithPropagators(nil),
			oteltrace.WithSpanNameFunc(nil),
		)
		if err == nil {
			t.Fatal("expected aggregated error for invalid options")
		}
	})
}
