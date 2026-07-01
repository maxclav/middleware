package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/maxclav/middleware/ratelimit"
	"golang.org/x/time/rate"
)

// okHandler writes 200 OK. It is the terminal handler wrapped by the middleware.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// serve runs one request against h using the given RemoteAddr and returns the
// recorder.
func serve(t *testing.T, h http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRefillOverTime(t *testing.T) {
	t.Parallel()

	// synctest makes the token refill deterministic: one token every 100ms with a
	// burst of 1, so the bucket is empty after the first request and a single
	// token returns exactly 100ms of synthetic time later.
	synctest.Test(t, func(t *testing.T) {
		mw, err := ratelimit.New(
			ratelimit.WithLimit(rate.Every(100*time.Millisecond)),
			ratelimit.WithBurst(1),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		h := mw(okHandler())

		if rec := serve(t, h, "1.2.3.4:1000"); rec.Code != http.StatusOK {
			t.Fatalf("first request: status = %d, want 200", rec.Code)
		}
		if rec := serve(t, h, "1.2.3.4:1000"); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("immediate second request: status = %d, want 429", rec.Code)
		}

		time.Sleep(100 * time.Millisecond) // one token refills
		synctest.Wait()

		if rec := serve(t, h, "1.2.3.4:1000"); rec.Code != http.StatusOK {
			t.Fatalf("after refill: status = %d, want 200", rec.Code)
		}
	})
}

func TestBurstThenRejected(t *testing.T) {
	t.Parallel()

	const burst = 3
	// A near-zero refill so the bucket does not replenish during the test.
	mw, err := ratelimit.New(
		ratelimit.WithLimit(rate.Limit(0.0001)),
		ratelimit.WithBurst(burst),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(okHandler())

	for i := range burst {
		if rec := serve(t, h, "1.2.3.4:1000"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i, rec.Code, http.StatusOK)
		}
	}

	rec := serve(t, h, "1.2.3.4:1000")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header not set on 429")
	}
}

func TestRetryAfterValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit rate.Limit
		want  string
	}{
		{
			// One token every 10s => Retry-After rounds up to 10.
			name:  "slow refill yields multiple seconds",
			limit: rate.Limit(0.1),
			want:  "10",
		},
		{
			// Fast refill (< 1s per token) still reports at least 1 second.
			name:  "fast refill floors at one second",
			limit: rate.Limit(100),
			want:  "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mw, err := ratelimit.New(
				ratelimit.WithLimit(tt.limit),
				ratelimit.WithBurst(1),
			)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			h := mw(okHandler())

			if rec := serve(t, h, "1.2.3.4:1000"); rec.Code != http.StatusOK {
				t.Fatalf("first request: status = %d, want 200", rec.Code)
			}
			rec := serve(t, h, "1.2.3.4:1000")
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("second request: status = %d, want 429", rec.Code)
			}
			if got := rec.Header().Get("Retry-After"); got != tt.want {
				t.Fatalf("Retry-After = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestZeroLimitRetryAfterFallback(t *testing.T) {
	t.Parallel()

	// A non-positive limit means the bucket never refills; Retry-After falls
	// back to the minimum of 1 second.
	mw, err := ratelimit.New(
		ratelimit.WithLimit(rate.Limit(0)),
		ratelimit.WithBurst(1),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(okHandler())

	if rec := serve(t, h, "1.2.3.4:1000"); rec.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200", rec.Code)
	}
	rec := serve(t, h, "1.2.3.4:1000")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}
}

func TestPerKeyIsolation(t *testing.T) {
	t.Parallel()

	mw, err := ratelimit.New(
		ratelimit.WithLimit(rate.Limit(0.0001)),
		ratelimit.WithBurst(1),
		ratelimit.WithKeyFunc(ratelimit.ClientIP),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(okHandler())

	// Exhaust key A (same IP, different ports collapse to one key).
	if rec := serve(t, h, "10.0.0.1:5000"); rec.Code != http.StatusOK {
		t.Fatalf("A first: status = %d, want 200", rec.Code)
	}
	if rec := serve(t, h, "10.0.0.1:5001"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("A second: status = %d, want 429", rec.Code)
	}

	// Key B still has its own budget.
	if rec := serve(t, h, "10.0.0.2:5000"); rec.Code != http.StatusOK {
		t.Fatalf("B first: status = %d, want 200", rec.Code)
	}
}

func TestGlobalSharesBudget(t *testing.T) {
	t.Parallel()

	mw, err := ratelimit.New(
		ratelimit.WithLimit(rate.Limit(0.0001)),
		ratelimit.WithBurst(1),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(okHandler())

	// Different clients, but a single shared global bucket.
	if rec := serve(t, h, "10.0.0.1:5000"); rec.Code != http.StatusOK {
		t.Fatalf("first: status = %d, want 200", rec.Code)
	}
	if rec := serve(t, h, "10.0.0.2:5000"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second: status = %d, want 429", rec.Code)
	}
}

func TestEvictionUnderMaxKeys(t *testing.T) {
	t.Parallel()

	// With maxKeys=1 and burst=1, every new key evicts the previous bucket, so
	// even a returning key gets a fresh full bucket. This exercises the
	// eviction loop while keeping the assertion deterministic.
	mw, err := ratelimit.New(
		ratelimit.WithLimit(rate.Limit(0.0001)),
		ratelimit.WithBurst(1),
		ratelimit.WithKeyFunc(ratelimit.ClientIP),
		ratelimit.WithMaxKeys(1),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(okHandler())

	// Key A consumes its only token.
	if rec := serve(t, h, "10.0.0.1:5000"); rec.Code != http.StatusOK {
		t.Fatalf("A first: status = %d, want 200", rec.Code)
	}
	// Key B evicts A and gets its own token.
	if rec := serve(t, h, "10.0.0.2:5000"); rec.Code != http.StatusOK {
		t.Fatalf("B first: status = %d, want 200", rec.Code)
	}
	// A was evicted, so it comes back with a fresh full bucket rather than 429.
	if rec := serve(t, h, "10.0.0.1:5000"); rec.Code != http.StatusOK {
		t.Fatalf("A after eviction: status = %d, want 200", rec.Code)
	}
}

func TestCustomErrorHandler(t *testing.T) {
	t.Parallel()

	const body = "slow down"
	var gotStatus int
	handler := func(w http.ResponseWriter, _ *http.Request, status int, _ error) {
		gotStatus = status
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}

	mw, err := ratelimit.New(
		ratelimit.WithLimit(rate.Limit(0.0001)),
		ratelimit.WithBurst(1),
		ratelimit.WithErrorHandler(handler),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(okHandler())

	if rec := serve(t, h, "1.2.3.4:1000"); rec.Code != http.StatusOK {
		t.Fatalf("first: status = %d, want 200", rec.Code)
	}
	rec := serve(t, h, "1.2.3.4:1000")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if gotStatus != http.StatusTooManyRequests {
		t.Fatalf("handler status = %d, want 429", gotStatus)
	}
	if rec.Body.String() != body {
		t.Fatalf("body = %q, want %q", rec.Body.String(), body)
	}
	// The Retry-After header is set before the handler runs.
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header not set with custom error handler")
	}
}

func TestClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{
			name:       "strips port",
			remoteAddr: "203.0.113.7:44321",
			want:       "203.0.113.7",
		},
		{
			name:       "falls back to raw value without port",
			remoteAddr: "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "handles ipv6 with port",
			remoteAddr: "[2001:db8::1]:8080",
			want:       "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			if got := ratelimit.ClientIP(req); got != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInvalidOptionsAreRejected(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		opt     ratelimit.Option
		wantErr bool
	}{
		"burst zero":         {opt: ratelimit.WithBurst(0), wantErr: true},
		"burst negative":     {opt: ratelimit.WithBurst(-1), wantErr: true},
		"burst valid":        {opt: ratelimit.WithBurst(1), wantErr: false},
		"rps zero":           {opt: ratelimit.WithRPS(0), wantErr: true},
		"rps negative":       {opt: ratelimit.WithRPS(-5), wantErr: true},
		"rps valid":          {opt: ratelimit.WithRPS(5), wantErr: false},
		"maxKeys negative":   {opt: ratelimit.WithMaxKeys(-1), wantErr: true},
		"maxKeys zero":       {opt: ratelimit.WithMaxKeys(0), wantErr: true},
		"maxKeys valid":      {opt: ratelimit.WithMaxKeys(1), wantErr: false},
		"keyFunc nil":        {opt: ratelimit.WithKeyFunc(nil), wantErr: true},
		"errorHandler nil":   {opt: ratelimit.WithErrorHandler(nil), wantErr: true},
		"limit accepts zero": {opt: ratelimit.WithLimit(rate.Limit(0)), wantErr: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ratelimit.New(tt.opt)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestMultipleInvalidOptionsJoined(t *testing.T) {
	t.Parallel()

	// Several invalid options are joined; New still returns a single error.
	if _, err := ratelimit.New(ratelimit.WithBurst(0), ratelimit.WithRPS(-1)); err == nil {
		t.Fatal("expected joined error for multiple invalid options")
	}
}

func TestConcurrentRequests(t *testing.T) {
	t.Parallel()

	mw, err := ratelimit.New(
		ratelimit.WithRPS(1000),
		ratelimit.WithBurst(1000),
		ratelimit.WithKeyFunc(ratelimit.ClientIP),
		ratelimit.WithMaxKeys(8), // force eviction under load
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(okHandler())

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			// Many distinct keys to exercise the bounded map under contention.
			addr := "10.0." + strconv.Itoa(i%20) + ".1:9000"
			serve(t, h, addr)
		})
	}
	wg.Wait()
}
