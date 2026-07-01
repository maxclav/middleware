package gzip_test

import (
	"bytes"
	stdgzip "compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxclav/middleware/gzip"
)

// largeJSON returns a compressible JSON payload comfortably above the default
// minimum size.
func largeJSON() []byte {
	return []byte(`{"items":[` + strings.Repeat(`{"name":"widget","value":42},`, 200) + `{"name":"end","value":0}]}`)
}

func jsonHandler(body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

// gzipRequest builds a GET request that advertises gzip support.
func gzipRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	return req
}

// mustGunzip decompresses r's contents, failing the test on error.
func mustGunzip(t *testing.T, r io.Reader) []byte {
	t.Helper()
	gr, err := stdgzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := gr.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return got
}

func TestCompressesLargeJSON(t *testing.T) {
	t.Parallel()

	body := largeJSON()
	mw, err := gzip.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(jsonHandler(body))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary = %q, want to contain Accept-Encoding", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length = %q, want empty for compressed response", got)
	}

	if got := mustGunzip(t, rec.Body); !bytes.Equal(got, body) {
		t.Fatalf("round-tripped body mismatch: got %d bytes, want %d", len(got), len(body))
	}
}

// TestMultipleWritesRoundTrip exercises the streaming path: several writes,
// the first of which crosses the minimum size so the decision is made and the
// remaining writes are forwarded through the gzip writer.
func TestMultipleWritesRoundTrip(t *testing.T) {
	t.Parallel()

	chunk := []byte(strings.Repeat("abcdefgh", 100)) // 800 bytes, over default minSize
	tail := []byte("-tail-chunk-")
	want := append(append([]byte(nil), chunk...), tail...)

	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(chunk)
		_, _ = w.Write(tail) // written after the compression decision
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := mustGunzip(t, rec.Body); !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: got %q, want %q", got, want)
	}
}

// TestMultipleWritesPassthrough exercises the writeDecided passthrough branch:
// a non-compressible type crosses the minimum size, then continues writing
// straight through the underlying writer.
func TestMultipleWritesPassthrough(t *testing.T) {
	t.Parallel()

	head := bytes.Repeat([]byte{0x00, 0x01, 0x02, 0x03}, 200) // 800 binary bytes
	tail := []byte{0xff, 0xfe, 0xfd}
	want := append(append([]byte(nil), head...), tail...)

	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(head)
		_, _ = w.Write(tail) // forwarded uncompressed after the decision
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for octet-stream", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatal("passthrough body altered across multiple writes")
	}
}

func TestNoGzipWhenNotAccepted(t *testing.T) {
	t.Parallel()

	body := largeJSON()
	mw, _ := gzip.New()
	h := mw(jsonHandler(body))

	rec := httptest.NewRecorder()
	// No Accept-Encoding header.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatal("body was altered when gzip not accepted")
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary = %q, want to contain Accept-Encoding even without gzip", got)
	}
}

func TestSmallBodyNotCompressed(t *testing.T) {
	t.Parallel()

	body := []byte(`{"ok":true}`) // well under the 512-byte default
	mw, _ := gzip.New()
	h := mw(jsonHandler(body))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for small body", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("small body altered: got %q", rec.Body.String())
	}
}

// TestEmptyBodyPassesThrough covers the Close path where the handler writes
// nothing at all: the passthrough branch must still emit the status.
func TestEmptyBodyPassesThrough(t *testing.T) {
	t.Parallel()

	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for empty body", got)
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
}

// TestNoWriteNoWriteHeader covers the Close path where the handler neither
// writes a body nor sets a status: Close must default the status to 200 and
// emit an empty, uncompressed response.
func TestNoWriteNoWriteHeader(t *testing.T) {
	t.Parallel()

	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// Intentionally writes nothing and never calls WriteHeader.
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
}

// TestFlushWithNoData covers the Flush path taken before anything is written:
// the status defaults to 200 and the empty buffer forces a decision. An empty
// buffer detects as text/plain (a configured type), so the stream compresses;
// the point here is that flushing early does not lose the later write.
func TestFlushWithNoData(t *testing.T) {
	t.Parallel()

	mw, _ := gzip.New()
	var flushErr error
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushErr = http.NewResponseController(w).Flush() // flush before any Write
		_, _ = w.Write([]byte("late"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if flushErr != nil {
		t.Fatalf("Flush: %v", flushErr)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (empty buffer detects text/plain)", got)
	}
	if got := mustGunzip(t, rec.Body); string(got) != "late" {
		t.Fatalf("body = %q, want %q", got, "late")
	}
}

func TestNonCompressibleContentType(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, 500) // PNG-ish bytes
	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for image/png", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatal("non-compressible body altered")
	}
}

func TestAlreadyEncodedNotDoubleCompressed(t *testing.T) {
	t.Parallel()

	body := largeJSON()
	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br") // handler already encoded
		_, _ = w.Write(body)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, want br (unchanged)", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatal("already-encoded body was re-compressed")
	}
}

func TestDetectsContentTypeWhenUnset(t *testing.T) {
	t.Parallel()

	body := []byte("<!DOCTYPE html><html><body>" + strings.Repeat("hello world ", 100) + "</body></html>")
	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// No explicit Content-Type: middleware must detect text/html.
		_, _ = w.Write(body)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip for detected html", got)
	}
	if got := mustGunzip(t, rec.Body); !bytes.Equal(got, body) {
		t.Fatal("detected-html body round-trip mismatch")
	}
}

// TestNoBodyStatusNotCompressed covers compressibleStatus: 204 and 304 carry no
// body and must never be gzip-encoded even when large writes are attempted.
func TestNoBodyStatusNotCompressed(t *testing.T) {
	t.Parallel()

	tests := map[string]int{
		"no content":   http.StatusNoContent,
		"not modified": http.StatusNotModified,
	}
	for name, status := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body := largeJSON()
			mw, _ := gzip.New()
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write(body)
			}))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, gzipRequest())

			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want empty for status %d", got, status)
			}
			if rec.Code != status {
				t.Fatalf("status = %d, want %d", rec.Code, status)
			}
		})
	}
}

// TestFlushCompressed verifies that flushing mid-stream commits the compression
// decision, that the underlying flush is reached via http.NewResponseController,
// and that the full body still round-trips.
func TestFlushCompressed(t *testing.T) {
	t.Parallel()

	first := []byte(strings.Repeat("streamed-line\n", 60)) // ~840 bytes, over minSize
	second := []byte("after-flush\n")
	want := append(append([]byte(nil), first...), second...)

	var flushed bool
	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(first)
		rc := http.NewResponseController(w)
		if err := rc.Flush(); err != nil {
			t.Errorf("Flush: %v", err)
		}
		flushed = true
		_, _ = w.Write(second)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if !flushed {
		t.Fatal("handler flush did not run")
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := mustGunzip(t, rec.Body); !bytes.Equal(got, want) {
		t.Fatalf("flushed body round-trip mismatch: got %q, want %q", got, want)
	}
}

// TestFlushBeforeMinSize flushes while still buffering below minSize, forcing an
// early compression decision (via DetectContentType) on a small compressible
// body, then continues writing.
func TestFlushBeforeMinSize(t *testing.T) {
	t.Parallel()

	first := []byte("<html><body>tiny") // small, but detectably text/html
	second := []byte("-rest</body></html>")
	want := append(append([]byte(nil), first...), second...)

	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(first)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush: %v", err)
		}
		_, _ = w.Write(second)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	// The forced decision detects text/html and compresses.
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip after early flush", got)
	}
	if got := mustGunzip(t, rec.Body); !bytes.Equal(got, want) {
		t.Fatalf("early-flush body round-trip mismatch: got %q, want %q", got, want)
	}
}

func TestValidLevelAccepted(t *testing.T) {
	t.Parallel()

	if _, err := gzip.New(gzip.WithLevel(stdgzip.BestCompression)); err != nil {
		t.Fatalf("BestCompression rejected: %v", err)
	}
}

// TestValidOptionsAccepted covers the success branch of each validating option.
func TestValidOptionsAccepted(t *testing.T) {
	t.Parallel()

	tests := map[string]gzip.Option{
		"level best speed":  gzip.WithLevel(stdgzip.BestSpeed),
		"level no compress": gzip.WithLevel(stdgzip.NoCompression),
		"zero min size":     gzip.WithMinSize(0),
		"positive min size": gzip.WithMinSize(2048),
		"single type":       gzip.WithContentTypes("application/json"),
		"multiple types":    gzip.WithContentTypes("text/plain", "text/html"),
	}
	for name, opt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := gzip.New(opt); err != nil {
				t.Errorf("%s: unexpected error: %v", name, err)
			}
		})
	}
}

// TestCustomContentTypesRespected checks that WithContentTypes replaces the
// default set: a type outside the custom list is not compressed, one inside is.
func TestCustomContentTypesRespected(t *testing.T) {
	t.Parallel()

	body := largeJSON()
	mw, _ := gzip.New(gzip.WithContentTypes("text/csv"))
	h := mw(jsonHandler(body)) // sets application/json, not in the custom set

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for type outside custom set", got)
	}

	csv := []byte(strings.Repeat("a,b,c\n", 200))
	h = mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write(csv)
	}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip for configured text/csv", got)
	}
}

// TestCustomMinSizeRespected verifies WithMinSize changes the threshold: a body
// between the default and the custom threshold stays uncompressed.
func TestCustomMinSizeRespected(t *testing.T) {
	t.Parallel()

	body := []byte(strings.Repeat("x", 700)) // above default 512, below custom 4096
	mw, _ := gzip.New(gzip.WithMinSize(4096))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty below custom min size", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatal("body altered below custom min size")
	}
}

func TestInvalidOptionsRejected(t *testing.T) {
	t.Parallel()

	tests := map[string]gzip.Option{
		"invalid level":       gzip.WithLevel(42),
		"negative min size":   gzip.WithMinSize(-1),
		"empty content types": gzip.WithContentTypes(),
	}
	for name, opt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := gzip.New(opt); err == nil {
				t.Errorf("%s: expected error, got nil", name)
			}
		})
	}
}

// deadlineProbe is a minimal http.ResponseWriter exposing SetWriteDeadline and
// recording whether its Unwrap was called. The gzip wrapper does not implement
// SetWriteDeadline, so http.NewResponseController must traverse the wrapper's
// Unwrap to reach this probe, which is what exercises the wrapper's Unwrap.
type deadlineProbe struct {
	http.ResponseWriter // the underlying recorder, as an interface
	deadlineSet         bool
}

// SetWriteDeadline records that the controller reached the underlying writer.
func (p *deadlineProbe) SetWriteDeadline(time.Time) error {
	p.deadlineSet = true
	return nil
}

// TestUnwrapReachesUnderlyingWriter forces http.NewResponseController to walk
// through the gzip wrapper's Unwrap: SetWriteDeadline is a control the wrapper
// does not implement, so the controller must Unwrap the wrapper to find the
// probe that does.
func TestUnwrapReachesUnderlyingWriter(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	probe := &deadlineProbe{ResponseWriter: rec}

	body := largeJSON()
	mw, _ := gzip.New()
	var deadlineErr error
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		// The wrapper has no SetWriteDeadline; the controller reaches the probe
		// only by calling the wrapper's Unwrap.
		deadlineErr = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	}))

	h.ServeHTTP(probe, gzipRequest())

	if deadlineErr != nil {
		t.Fatalf("SetWriteDeadline: %v", deadlineErr)
	}
	if !probe.deadlineSet {
		t.Fatal("underlying SetWriteDeadline not reached; Unwrap was not traversed")
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := mustGunzip(t, rec.Body); !bytes.Equal(got, body) {
		t.Fatal("body round-trip mismatch through Unwrap path")
	}
}

// TestInformationalStatusNotCompressed covers the status < 200 branch of
// compressibleStatus: a 1xx status recorded on the wrapper must not compress.
func TestInformationalStatusNotCompressed(t *testing.T) {
	t.Parallel()

	body := largeJSON()
	mw, _ := gzip.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Record a 1xx status directly on the wrapper; the real status/body
		// decision is deferred, so this drives compressibleStatus(status<200).
		w.WriteHeader(http.StatusEarlyHints)
		_, _ = w.Write(body)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gzipRequest())

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty for 1xx status", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatal("1xx-status body altered")
	}
}
