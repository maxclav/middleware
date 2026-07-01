// Package cache provides an in-memory response cache with a fixed time-to-live.
//
// The middleware stores the status, headers and body of cacheable responses and
// replays them for matching subsequent requests, adding an X-Cache header that
// reports HIT or MISS. Only safe, public, successful responses are cached: the
// request method must be one of the configured methods (GET and HEAD by
// default), the response status must be 200, the body must not exceed the
// configured limit, and the request must not carry an Authorization header or a
// Cache-Control: no-store directive.
//
// The store is guarded by a [sync.RWMutex] and evicts entries lazily; there are
// no background goroutines.
package cache

import (
	"bytes"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maxclav/middleware"
)

const (
	// DefaultTTL is the default lifetime of a cached response.
	DefaultTTL = 60 * time.Second
	// DefaultMaxBodyBytes is the default maximum cacheable body size.
	DefaultMaxBodyBytes = 1 << 20
	// DefaultMaxEntries is the default maximum number of stored entries.
	DefaultMaxEntries = 1024
)

type config struct {
	ttl          time.Duration
	maxBodyBytes int
	maxEntries   int
	methods      map[string]struct{}
	keyFunc      func(*http.Request) string
}

// Option configures the cache middleware.
type Option func(*config) error

// WithTTL sets how long a cached response stays fresh. It must be positive.
// Defaults to [DefaultTTL].
func WithTTL(d time.Duration) Option {
	return func(c *config) error {
		if d <= 0 {
			return errors.New("cache: TTL must be positive")
		}
		c.ttl = d
		return nil
	}
}

// WithMaxBodyBytes sets the largest response body that will be cached. Larger
// responses are streamed through uncached. It must be positive. Defaults to
// [DefaultMaxBodyBytes].
func WithMaxBodyBytes(n int) Option {
	return func(c *config) error {
		if n <= 0 {
			return errors.New("cache: max body bytes must be positive")
		}
		c.maxBodyBytes = n
		return nil
	}
}

// WithMaxEntries sets the maximum number of entries retained in the store. When
// the store grows beyond this size, expired entries are evicted first and then,
// if still over, arbitrary entries are dropped until under the cap. It must be
// positive. Defaults to [DefaultMaxEntries].
func WithMaxEntries(n int) Option {
	return func(c *config) error {
		if n <= 0 {
			return errors.New("cache: max entries must be positive")
		}
		c.maxEntries = n
		return nil
	}
}

// WithMethods sets the request methods eligible for caching. At least one
// method must be supplied. Defaults to GET and HEAD.
func WithMethods(methods ...string) Option {
	return func(c *config) error {
		if len(methods) == 0 {
			return errors.New("cache: at least one method is required")
		}
		set := make(map[string]struct{}, len(methods))
		for _, m := range methods {
			if m == "" {
				return errors.New("cache: method must not be empty")
			}
			set[strings.ToUpper(m)] = struct{}{}
		}
		c.methods = set
		return nil
	}
}

// WithKeyFunc sets the function that derives the cache key from a request. It
// must not be nil. Defaults to the request method and RequestURI.
func WithKeyFunc(fn func(*http.Request) string) Option {
	return func(c *config) error {
		if fn == nil {
			return errors.New("cache: key function must not be nil")
		}
		c.keyFunc = fn
		return nil
	}
}

// entry is a stored response together with its expiry time.
type entry struct {
	status    int
	header    http.Header
	body      []byte
	expiresAt time.Time
}

func (e entry) expired(now time.Time) bool { return now.After(e.expiresAt) }

// New returns middleware that caches cacheable responses in memory for the
// configured TTL. On a cache hit the stored response is replayed with
// X-Cache: HIT and the next handler is not called; on a miss the response is
// captured, forwarded with X-Cache: MISS, and stored when it is cacheable.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		ttl:          DefaultTTL,
		maxBodyBytes: DefaultMaxBodyBytes,
		maxEntries:   DefaultMaxEntries,
		methods:      map[string]struct{}{http.MethodGet: {}, http.MethodHead: {}},
		keyFunc:      defaultKey,
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

	s := &store{
		entries:    make(map[string]entry),
		maxEntries: cfg.maxEntries,
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.cacheableRequest(r) {
				next.ServeHTTP(w, r)
				return
			}

			key := cfg.keyFunc(r)
			if e, ok := s.get(key); ok {
				replay(w, e)
				return
			}

			cw := &captureWriter{
				ResponseWriter: w,
				limit:          cfg.maxBodyBytes,
			}
			cw.Header().Set("X-Cache", "MISS")
			next.ServeHTTP(cw, r)

			if cfg.cacheableResponse(cw) {
				s.set(key, entry{
					status:    cw.status,
					header:    cloneHeader(cw.Header()),
					body:      cw.body.Bytes(),
					expiresAt: time.Now().Add(cfg.ttl),
				})
			}
		})
	}, nil
}

// cacheableRequest reports whether a request may be served from or stored in
// the cache.
func (c *config) cacheableRequest(r *http.Request) bool {
	if _, ok := c.methods[r.Method]; !ok {
		return false
	}
	if r.Header.Get("Authorization") != "" {
		return false
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Cache-Control")), "no-store") {
		return false
	}
	return true
}

// cacheableResponse reports whether a captured response may be stored.
func (c *config) cacheableResponse(cw *captureWriter) bool {
	return cw.status == http.StatusOK && !cw.tooLarge && cw.body.Len() <= c.maxBodyBytes
}

// replay writes a stored entry to w, marking it as a cache hit.
func replay(w http.ResponseWriter, e entry) {
	h := w.Header()
	for k, vs := range e.header {
		h[k] = slices.Clone(vs)
	}
	h.Set("X-Cache", "HIT")
	w.WriteHeader(e.status)
	_, _ = w.Write(e.body)
}

func defaultKey(r *http.Request) string {
	return r.Method + " " + r.URL.RequestURI()
}

func cloneHeader(h http.Header) http.Header {
	clone := make(http.Header, len(h))
	for k, vs := range h {
		clone[k] = slices.Clone(vs)
	}
	return clone
}

// store is a concurrency-safe map of cache entries with lazy eviction.
type store struct {
	mu         sync.RWMutex
	entries    map[string]entry
	maxEntries int
}

// get returns a fresh entry for key, reporting whether one was found. Expired
// entries are treated as absent.
func (s *store) get(key string) (entry, bool) {
	s.mu.RLock()
	e, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok || e.expired(time.Now()) {
		return entry{}, false
	}
	return e, true
}

// set stores e under key and evicts entries when the store exceeds its cap.
func (s *store) set(key string, e entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = e
	if len(s.entries) <= s.maxEntries {
		return
	}
	now := time.Now()
	for k, ent := range s.entries {
		if ent.expired(now) {
			delete(s.entries, k)
		}
	}
	for k := range s.entries {
		if len(s.entries) <= s.maxEntries {
			break
		}
		delete(s.entries, k)
	}
}

// captureWriter satisfies http.ResponseWriter (via the embedded writer).
var _ http.ResponseWriter = (*captureWriter)(nil)

// captureWriter buffers the status and body written by the next handler so the
// response can be inspected and stored after it completes.
type captureWriter struct {
	http.ResponseWriter
	status      int
	body        bytes.Buffer
	limit       int
	tooLarge    bool
	wroteHeader bool
}

func (w *captureWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.tooLarge {
		if w.body.Len()+len(b) > w.limit {
			w.tooLarge = true
			w.body.Reset()
		} else {
			w.body.Write(b)
		}
	}
	return w.ResponseWriter.Write(b)
}
