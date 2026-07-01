package requestid_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxclav/middleware/requestid"
)

func TestGeneratesIDWhenAbsent(t *testing.T) {
	t.Parallel()

	mw, err := requestid.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var fromCtx string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		fromCtx, _ = requestid.FromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	header := rec.Header().Get(requestid.HeaderName)
	if header == "" {
		t.Fatal("response header not set")
	}
	if fromCtx != header {
		t.Fatalf("context id %q != header id %q", fromCtx, header)
	}
}

func TestReusesTrustedIncomingID(t *testing.T) {
	t.Parallel()

	mw, _ := requestid.New()
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set(requestid.HeaderName, "trace-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(requestid.HeaderName); got != "trace-123" {
		t.Fatalf("id = %q, want trace-123", got)
	}
}

func TestRegeneratesWhenIncomingNotTrusted(t *testing.T) {
	t.Parallel()

	mw, _ := requestid.New(requestid.WithTrustIncoming(false))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set(requestid.HeaderName, "spoofed")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(requestid.HeaderName); got == "spoofed" || got == "" {
		t.Fatalf("id = %q, want a freshly generated value", got)
	}
}

func TestCustomGeneratorAndHeader(t *testing.T) {
	t.Parallel()

	mw, _ := requestid.New(
		requestid.WithHeader("X-Trace"),
		requestid.WithGenerator(func() string { return "fixed" }),
	)
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if got := rec.Header().Get("X-Trace"); got != "fixed" {
		t.Fatalf("id = %q, want fixed", got)
	}
}

func TestInvalidOptionsAreRejected(t *testing.T) {
	t.Parallel()

	if _, err := requestid.New(requestid.WithHeader(""), requestid.WithGenerator(nil)); err == nil {
		t.Fatal("expected aggregated error for invalid options")
	}
}

func TestMalformedIncomingIDIsRegenerated(t *testing.T) {
	t.Parallel()

	mw, err := requestid.New() // trusts incoming IDs by default
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var fromCtx string
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		fromCtx, _ = requestid.FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set(requestid.HeaderName, "bad\r\nInjected: evil")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := rec.Header().Get(requestid.HeaderName)
	if strings.ContainsAny(got, "\r\n") || strings.ContainsAny(fromCtx, "\r\n") {
		t.Fatalf("control characters propagated: header=%q ctx=%q", got, fromCtx)
	}
	if got == "bad\r\nInjected: evil" {
		t.Fatal("malformed incoming ID was reused instead of regenerated")
	}
	if got == "" {
		t.Fatal("no ID generated")
	}
}
