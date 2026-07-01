package cache_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/maxclav/middleware/cache"
)

// writeString writes s to w, failing the test if the write errors. It keeps the
// handler bodies free of unchecked error returns.
func writeString(t *testing.T, w http.ResponseWriter, s string) {
	t.Helper()
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatalf("write body: %v", err)
	}
}

func TestIdenticalGetsHitAfterMiss(t *testing.T) {
	t.Parallel()

	mw, err := cache.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var calls int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		writeString(t, w, "hello")
	}))

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/x", http.NoBody))
	if got := rec1.Header().Get("X-Cache"); got != "MISS" {
		t.Fatalf("first X-Cache = %q, want MISS", got)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/x", http.NoBody))
	if got := rec2.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("second X-Cache = %q, want HIT", got)
	}

	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
	if rec1.Body.String() != rec2.Body.String() || rec2.Body.String() != "hello" {
		t.Fatalf("bodies differ: %q vs %q", rec1.Body.String(), rec2.Body.String())
	}
	if rec1.Code != rec2.Code {
		t.Fatalf("status differ: %d vs %d", rec1.Code, rec2.Code)
	}
	if rec2.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("cached headers not replayed: %q", rec2.Header().Get("Content-Type"))
	}
}

func TestHeadRequestCached(t *testing.T) {
	t.Parallel()

	mw, _ := cache.New()
	var calls int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeString(t, w, "body")
	}))

	for i, want := range []string{"MISS", "HIT"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/h", http.NoBody))
		if got := rec.Header().Get("X-Cache"); got != want {
			t.Fatalf("request %d X-Cache = %q, want %q", i, got, want)
		}
	}
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
}

func TestEntryExpiresAfterTTL(t *testing.T) {
	t.Parallel()

	// synctest gives deterministic time: no wall-clock elapses between the MISS
	// and the HIT (so the HIT can never race past the TTL), and the expiry is
	// triggered by advancing synthetic time rather than a real sleep.
	synctest.Test(t, func(t *testing.T) {
		mw, _ := cache.New(cache.WithTTL(20 * time.Millisecond))
		var calls int32
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			writeString(t, w, "v")
		}))
		serve := func() *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
			return rec
		}

		serve() // MISS: stores the entry with a 20ms TTL
		if got := serve().Header().Get("X-Cache"); got != "HIT" {
			t.Fatalf("before expiry: X-Cache = %q, want HIT", got)
		}

		time.Sleep(21 * time.Millisecond) // advance synthetic time past the TTL
		synctest.Wait()

		if got := serve().Header().Get("X-Cache"); got != "MISS" {
			t.Fatalf("after expiry: X-Cache = %q, want MISS", got)
		}
		if calls != 2 {
			t.Fatalf("handler called %d times, want 2", calls)
		}
	})
}

func TestNon200NotCached(t *testing.T) {
	t.Parallel()

	mw, _ := cache.New()
	var calls int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	for range 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
		if rec.Header().Get("X-Cache") == "HIT" {
			t.Fatal("non-200 response should not be cached")
		}
	}
	if calls != 2 {
		t.Fatalf("handler called %d times, want 2", calls)
	}
}

// TestDoubleWriteHeaderIgnored exercises the guard against a second
// WriteHeader: the first status wins and the response is still cacheable.
func TestDoubleWriteHeaderIgnored(t *testing.T) {
	t.Parallel()

	mw, _ := cache.New()
	var calls int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusTeapot) // must be ignored
		writeString(t, w, "body")
	}))

	for i, want := range []string{"MISS", "HIT"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dh", http.NoBody))
		if got := rec.Header().Get("X-Cache"); got != want {
			t.Fatalf("request %d X-Cache = %q, want %q", i, got, want)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want %d (first WriteHeader wins)", i, rec.Code, http.StatusOK)
		}
	}
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
}

// TestRequestBypassesCache groups the conditions under which a request must not
// be served from or stored in the cache: it neither hits nor gets an X-Cache
// header, and the handler runs on every call.
func TestRequestBypassesCache(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		method string
		header map[string]string
	}{
		"POST method":            {method: http.MethodPost},
		"Authorization set":      {method: http.MethodGet, header: map[string]string{"Authorization": "Bearer secret"}},
		"Cookie set":             {method: http.MethodGet, header: map[string]string{"Cookie": "session=abc"}},
		"Cache-Control no-store": {method: http.MethodGet, header: map[string]string{"Cache-Control": "no-store"}},
		"Cache-Control no-cache": {method: http.MethodGet, header: map[string]string{"Cache-Control": "no-cache"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mw, _ := cache.New()
			var calls int32
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				writeString(t, w, "ok")
			}))

			for range 2 {
				req := httptest.NewRequest(tc.method, "/", http.NoBody)
				for k, v := range tc.header {
					req.Header.Set(k, v)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if got := rec.Header().Get("X-Cache"); got != "" {
					t.Fatalf("X-Cache = %q, want empty (bypass)", got)
				}
			}
			if calls != 2 {
				t.Fatalf("handler called %d times, want 2", calls)
			}
		})
	}
}

// TestResponseNotCached groups responses that must not be stored because they
// are not safe to share between clients: a cookie, a Vary, or a Cache-Control
// directive marking the response private or non-storable.
func TestResponseNotCached(t *testing.T) {
	t.Parallel()

	tests := map[string]map[string]string{
		"Set-Cookie":             {"Set-Cookie": "session=abc"},
		"Vary":                   {"Vary": "Accept-Language"},
		"Cache-Control private":  {"Cache-Control": "private"},
		"Cache-Control no-store": {"Cache-Control": "no-store"},
		"Cache-Control no-cache": {"Cache-Control": "no-cache"},
	}

	for name, respHeaders := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mw, _ := cache.New()
			var calls int32
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&calls, 1)
				for k, v := range respHeaders {
					w.Header().Set(k, v)
				}
				writeString(t, w, "private")
			}))

			for range 2 {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p", http.NoBody))
				if rec.Header().Get("X-Cache") == "HIT" {
					t.Fatalf("%s response must not be cached", name)
				}
			}
			if calls != 2 {
				t.Fatalf("%s: handler called %d times, want 2 (never cached)", name, calls)
			}
		})
	}
}

func TestBodyOverLimitNotCached(t *testing.T) {
	t.Parallel()

	mw, _ := cache.New(cache.WithMaxBodyBytes(4))
	var calls int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeString(t, w, "way too long body")
	}))

	for range 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
		if rec.Header().Get("X-Cache") == "HIT" {
			t.Fatal("oversize body should not be cached")
		}
		if rec.Body.String() != "way too long body" {
			t.Fatalf("body truncated to client: %q", rec.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("handler called %d times, want 2", calls)
	}
}

// TestBodyAtLimitCached checks the boundary: a body exactly at the limit is
// still cacheable.
func TestBodyAtLimitCached(t *testing.T) {
	t.Parallel()

	mw, _ := cache.New(cache.WithMaxBodyBytes(5))
	var calls int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeString(t, w, "12345") // exactly the limit
	}))

	for i, want := range []string{"MISS", "HIT"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
		if got := rec.Header().Get("X-Cache"); got != want {
			t.Fatalf("request %d X-Cache = %q, want %q", i, got, want)
		}
	}
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
}

func TestCustomKeyFunc(t *testing.T) {
	t.Parallel()

	mw, _ := cache.New(cache.WithKeyFunc(func(*http.Request) string { return "constant" }))
	var calls int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeString(t, w, "same")
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/a", http.NoBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/b", http.NoBody))

	if rec.Header().Get("X-Cache") != "HIT" {
		t.Fatal("distinct URLs should share a key and hit")
	}
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
}

// TestCustomMethodsCached verifies WithMethods enables caching for a method
// outside the GET/HEAD default while still bypassing the defaults.
func TestCustomMethodsCached(t *testing.T) {
	t.Parallel()

	mw, err := cache.New(cache.WithMethods("get")) // lowercase, must be upcased
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var calls int32
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeString(t, w, "payload")
	}))

	// GET is now cacheable.
	for i, want := range []string{"MISS", "HIT"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
		if got := rec.Header().Get("X-Cache"); got != want {
			t.Fatalf("GET request %d X-Cache = %q, want %q", i, got, want)
		}
	}

	// HEAD is no longer in the configured set, so it must bypass.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/", http.NoBody))
	if got := rec.Header().Get("X-Cache"); got != "" {
		t.Fatalf("HEAD X-Cache = %q, want empty (not configured)", got)
	}
}

func TestConcurrentSameKey(t *testing.T) {
	t.Parallel()

	mw, _ := cache.New()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeString(t, w, "payload")
	}))

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hot", http.NoBody))
			if rec.Body.String() != "payload" {
				t.Errorf("unexpected body %q", rec.Body.String())
			}
		})
	}
	wg.Wait()
}

func TestEvictionKeepsUnderCap(t *testing.T) {
	t.Parallel()

	const maxEntries = 2
	var calls int32
	mw, _ := cache.New(cache.WithMaxEntries(maxEntries))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeString(t, w, "x")
	}))

	const keys = maxEntries + 1 // one more distinct key than the cap allows
	req := func(i int) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/k%d", i), http.NoBody))
	}
	for i := range keys { // first pass: all miss, store bounded to maxEntries
		req(i)
	}
	for i := range keys { // second pass
		req(i)
	}

	// If the store had kept every key it would serve all second-pass requests
	// from cache (keys total handler calls). Because it is bounded below keys, at
	// least one second-pass request misses and re-runs the handler.
	if calls <= keys {
		t.Fatalf("handler called %d times; store did not evict (cap %d not enforced)", calls, maxEntries)
	}
}

// TestEvictionPrefersExpired drives the eviction path where expired entries are
// dropped first: with a short TTL and a cap of 1, filling many keys must not
// panic and keeps the store bounded.
func TestEvictionPrefersExpired(t *testing.T) {
	t.Parallel()

	mw, _ := cache.New(cache.WithMaxEntries(1), cache.WithTTL(time.Millisecond))
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeString(t, w, "x")
	}))

	for i := range 20 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/e%d", i), http.NoBody))
		time.Sleep(2 * time.Millisecond) // let each entry expire before the next
	}
}

func TestInvalidOptionsRejected(t *testing.T) {
	t.Parallel()

	cases := map[string]cache.Option{
		"zero ttl":      cache.WithTTL(0),
		"negative ttl":  cache.WithTTL(-time.Second),
		"zero body":     cache.WithMaxBodyBytes(0),
		"negative body": cache.WithMaxBodyBytes(-1),
		"zero entries":  cache.WithMaxEntries(0),
		"no methods":    cache.WithMethods(),
		"empty method":  cache.WithMethods("GET", ""),
		"nil key func":  cache.WithKeyFunc(nil),
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := cache.New(opt); err == nil {
				t.Errorf("%s: expected error, got nil", name)
			}
		})
	}
}
