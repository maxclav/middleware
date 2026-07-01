// Package gzip provides middleware that compresses HTTP responses with gzip
// when the client accepts it and the response is worth compressing.
//
// The middleware only compresses a response when the request's Accept-Encoding
// header offers gzip, the response's Content-Type is in the configured set, the
// status code is compressible, and the body reaches a minimum size. The
// compress/no-compress decision is deferred until the handler starts writing,
// so small or non-qualifying responses are streamed through untouched. A
// Vary: Accept-Encoding header is always added, since the response depends on
// that request header.
package gzip

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/maxclav/middleware"
)

// defaultContentTypes is the set of response content types compressed by
// default. It covers common text and structured payloads that benefit from
// gzip while excluding already-compressed binary formats.
var defaultContentTypes = []string{
	"text/html",
	"text/plain",
	"text/css",
	"text/javascript",
	"application/javascript",
	"application/json",
	"application/xml",
	"image/svg+xml",
}

type config struct {
	level        int
	minSize      int
	contentTypes []string
}

// Option configures the gzip middleware.
type Option func(*config) error

// WithLevel sets the gzip compression level. Valid values are
// [gzip.DefaultCompression], [gzip.NoCompression], [gzip.BestSpeed] through
// [gzip.BestCompression], or [gzip.HuffmanOnly]. It is validated by attempting
// to construct a writer at that level. Defaults to [gzip.DefaultCompression].
func WithLevel(level int) Option {
	return func(c *config) error {
		if _, err := gzip.NewWriterLevel(io.Discard, level); err != nil {
			return fmt.Errorf("gzip: invalid level: %w", err)
		}
		c.level = level
		return nil
	}
}

// WithMinSize sets the minimum response body size, in bytes, before compression
// is applied. Responses smaller than this are streamed uncompressed. It must
// not be negative. Defaults to 512.
func WithMinSize(minSize int) Option {
	return func(c *config) error {
		if minSize < 0 {
			return errors.New("gzip: min size must not be negative")
		}
		c.minSize = minSize
		return nil
	}
}

// WithContentTypes sets the response content types eligible for compression,
// matched on the base type before any ";" parameters. The list must not be
// empty. Defaults to a sane set of text and structured formats.
func WithContentTypes(contentTypes ...string) Option {
	return func(c *config) error {
		if len(contentTypes) == 0 {
			return errors.New("gzip: content types must not be empty")
		}
		c.contentTypes = slices.Clone(contentTypes)
		return nil
	}
}

// New returns middleware that compresses qualifying responses with gzip.
//
// If the request does not accept gzip, the handler runs with the original
// writer. Otherwise the writer is wrapped so the compression decision can be
// deferred until the body is written: the response is compressed only when its
// status is compressible, its content type is configured, no Content-Encoding
// is already set, and the body reaches the configured minimum size.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		level:        gzip.DefaultCompression,
		minSize:      512,
		contentTypes: defaultContentTypes,
	}
	var errs []error
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	// A per-middleware pool avoids reallocating gzip writers per request.
	pool := &sync.Pool{
		New: func() any {
			w, _ := gzip.NewWriterLevel(io.Discard, cfg.level)
			return w
		},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The response depends on Accept-Encoding either way, so caches
			// must key on it.
			w.Header().Add("Vary", "Accept-Encoding")

			if !acceptsGzip(r) {
				next.ServeHTTP(w, r)
				return
			}

			gw := &gzipResponseWriter{
				ResponseWriter: w,
				cfg:            &cfg,
				pool:           pool,
			}
			defer gw.Close()
			next.ServeHTTP(gw, r)
		})
	}, nil
}

// acceptsGzip reports whether the request's Accept-Encoding header offers gzip,
// using a simple case-insensitive substring check.
func acceptsGzip(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip")
}

// Interface assertions: the wrapper satisfies http.ResponseWriter (embedded)
// and http.Flusher, and exposes Unwrap so http.NewResponseController can reach
// optional interfaces on the underlying writer.
var (
	_ http.ResponseWriter = (*gzipResponseWriter)(nil)
	_ http.Flusher        = (*gzipResponseWriter)(nil)
)

// gzipResponseWriter buffers the start of the response so it can decide whether
// to compress once it knows the status, content type, and enough of the body to
// compare against the minimum size. Once the decision is made it either flushes
// the buffer through a gzip.Writer or writes it straight through.
type gzipResponseWriter struct {
	http.ResponseWriter
	cfg  *config
	pool *sync.Pool

	status  int
	buf     bytes.Buffer
	gz      *gzip.Writer // non-nil once compressing
	decided bool         // whether compress/passthrough has been chosen
}

// WriteHeader records the status but defers writing it, since the compression
// decision (and thus the Content-Encoding header) is not yet known.
func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

// Write buffers bytes until the minimum size is reached, then commits to a
// compression decision and streams the remaining writes accordingly.
func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	if w.decided {
		return w.writeDecided(b)
	}

	w.buf.Write(b)
	if w.buf.Len() < w.cfg.minSize {
		// Not enough data yet to decide; keep buffering.
		return len(b), nil
	}

	if err := w.decide(); err != nil {
		return 0, err
	}
	return len(b), nil
}

// writeDecided forwards bytes to the chosen sink after the decision is made.
func (w *gzipResponseWriter) writeDecided(b []byte) (int, error) {
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// decide commits to compressing or not, writes the appropriate headers and
// status, and flushes the buffered bytes.
func (w *gzipResponseWriter) decide() error {
	w.decided = true

	if w.shouldCompress() {
		h := w.Header()
		h.Set("Content-Encoding", "gzip")
		// The compressed length is unknown, so any handler-set value is wrong.
		h.Del("Content-Length")
		w.ResponseWriter.WriteHeader(w.status)

		gz, ok := w.pool.Get().(*gzip.Writer)
		if !ok {
			// Unreachable: the pool's New only ever returns *gzip.Writer.
			gz = gzip.NewWriter(w.ResponseWriter)
		}
		gz.Reset(w.ResponseWriter)
		w.gz = gz
		_, err := io.Copy(gz, &w.buf)
		return err
	}

	w.ResponseWriter.WriteHeader(w.status)
	_, err := io.Copy(w.ResponseWriter, &w.buf)
	return err
}

// shouldCompress reports whether the buffered response qualifies for gzip based
// on status, existing Content-Encoding, and content type.
func (w *gzipResponseWriter) shouldCompress() bool {
	if !compressibleStatus(w.status) {
		return false
	}
	if w.Header().Get("Content-Encoding") != "" {
		return false
	}
	return w.contentTypeAllowed()
}

// contentTypeAllowed reports whether the response content type, taken from the
// header or detected from the buffered bytes, is in the configured set.
func (w *gzipResponseWriter) contentTypeAllowed() bool {
	ct := w.Header().Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(w.buf.Bytes())
	}
	base := strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])
	for _, allowed := range w.cfg.contentTypes {
		if strings.EqualFold(base, allowed) {
			return true
		}
	}
	return false
}

// Close flushes any buffered or in-flight data. If the handler finished before
// the minimum size was reached, the small buffer is written through
// uncompressed. A gzip writer is closed and returned to the pool.
func (w *gzipResponseWriter) Close() {
	if !w.decided {
		// Handler produced less than minSize (or nothing): pass through.
		w.decided = true
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.ResponseWriter.WriteHeader(w.status)
		_, _ = io.Copy(w.ResponseWriter, &w.buf)
		return
	}
	if w.gz != nil {
		_ = w.gz.Close()
		w.pool.Put(w.gz)
		w.gz = nil
	}
}

// Flush implements [http.Flusher]. It commits the compression decision if it is
// still pending, flushes the gzip writer when compressing, then flushes the
// underlying writer via [http.NewResponseController].
func (w *gzipResponseWriter) Flush() {
	if !w.decided {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		if err := w.decide(); err != nil {
			return
		}
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap returns the underlying [http.ResponseWriter], letting
// [http.NewResponseController] reach optional interfaces on it.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// compressibleStatus reports whether a status code may carry a compressed body.
// Informational (1xx), No Content (204) and Not Modified (304) responses have
// no body to compress.
func compressibleStatus(status int) bool {
	if status < 200 {
		return false
	}
	return status != http.StatusNoContent && status != http.StatusNotModified
}
