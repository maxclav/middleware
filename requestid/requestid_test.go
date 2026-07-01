package requestid_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxclav/middleware/requestid"
)

func TestGeneratesIDWhenAbsent(t *testing.T) {
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
	if _, err := requestid.New(requestid.WithHeader(""), requestid.WithGenerator(nil)); err == nil {
		t.Fatal("expected aggregated error for invalid options")
	}
}
