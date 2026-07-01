package chaos_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/maxclav/middleware/chaos"
)

// constRand returns a randFloat func that always yields v, for deterministic
// tests.
func constRand(v float64) func() float64 {
	return func() float64 { return v }
}

// newRequest builds a GET request with an empty body.
func newRequest(t *testing.T) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, "/", http.NoBody)
}

// nextHandler returns a handler that records that it ran and writes 200 OK.
func nextHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestAbort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		opts       []chaos.Option
		wantCalled bool
		wantStatus int
	}{
		{
			name: "triggers and writes configured status",
			opts: []chaos.Option{
				chaos.WithProbability(1),
				chaos.WithRandFloat(constRand(0)),
				chaos.WithAbortStatus(http.StatusBadGateway),
			},
			wantCalled: false,
			wantStatus: http.StatusBadGateway,
		},
		{
			name: "default abort status is 500",
			opts: []chaos.Option{
				chaos.WithProbability(1),
				chaos.WithRandFloat(constRand(0)),
			},
			wantCalled: false,
			wantStatus: http.StatusInternalServerError,
		},
		{
			// probability 0 never triggers regardless of randFloat.
			name: "does not trigger at probability 0",
			opts: []chaos.Option{
				chaos.WithProbability(0),
				chaos.WithRandFloat(constRand(0)),
			},
			wantCalled: true,
			wantStatus: http.StatusOK,
		},
		{
			// randFloat >= probability does not trigger.
			name: "does not trigger when draw exceeds probability",
			opts: []chaos.Option{
				chaos.WithProbability(0.5),
				chaos.WithRandFloat(constRand(0.9)),
			},
			wantCalled: true,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mw, err := chaos.Abort(tt.opts...)
			if err != nil {
				t.Fatalf("Abort: %v", err)
			}
			called := false
			h := mw(nextHandler(&called))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, newRequest(t))

			if called != tt.wantCalled {
				t.Errorf("next called = %v, want %v", called, tt.wantCalled)
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestSleepTriggersAndCallsNext(t *testing.T) {
	t.Parallel()

	mw, err := chaos.Sleep(
		chaos.WithProbability(1),
		chaos.WithRandFloat(constRand(0.5)),
		chaos.WithDelayRange(time.Millisecond, 2*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	called := false
	h := mw(nextHandler(&called))

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t))

	if !called {
		t.Fatal("next handler not called after sleep")
	}
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Fatalf("elapsed %v, expected at least the min delay", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestSleepZeroDelayCallsNext(t *testing.T) {
	t.Parallel()

	// A degenerate [0, 0] range means the drawn delay is 0, exercising the
	// sleep fast path that skips the timer and checks the context directly.
	mw, err := chaos.Sleep(
		chaos.WithProbability(1),
		chaos.WithRandFloat(constRand(0)),
		chaos.WithDelayRange(0, 0),
	)
	if err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	called := false
	h := mw(nextHandler(&called))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t))

	if !called {
		t.Fatal("next handler not called after zero-length sleep")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestSleepZeroDelayHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	// With a zero delay and an already-canceled context, the fast path must
	// still observe the cancellation and skip the next handler.
	mw, err := chaos.Sleep(
		chaos.WithProbability(1),
		chaos.WithRandFloat(constRand(0)),
		chaos.WithDelayRange(0, 0),
	)
	if err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	called := false
	h := mw(nextHandler(&called))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t).WithContext(ctx))

	if called {
		t.Fatal("next handler called despite canceled context on zero-delay path")
	}
}

func TestSleepHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	mw, err := chaos.Sleep(
		chaos.WithProbability(1),
		chaos.WithRandFloat(constRand(0)),          // 0 < 1 => triggers
		chaos.WithDelayRange(time.Hour, time.Hour), // would block for an hour
	)
	if err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	called := false
	h := mw(nextHandler(&called))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	req := newRequest(t).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeHTTP did not return promptly on canceled context")
	}
	if called {
		t.Fatal("next handler called despite canceled context")
	}
}

func TestSleepDoesNotTrigger(t *testing.T) {
	t.Parallel()

	mw, err := chaos.Sleep(chaos.WithProbability(0), chaos.WithRandFloat(constRand(0)))
	if err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	called := false
	h := mw(nextHandler(&called))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t))

	if !called {
		t.Fatal("next handler not called when sleep should not trigger")
	}
}

func TestRandomResponse(t *testing.T) {
	t.Parallel()

	// triggers() and randStatus() share one randFloat draw, so each case picks
	// a draw that both triggers (draw < prob) and lands on a known index
	// (index = int(draw*len(statuses))).
	statuses := []int{http.StatusBadGateway, http.StatusServiceUnavailable}

	tests := []struct {
		name        string
		prob        float64
		draw        float64
		wantCalled  bool
		wantStatus  int // 0 means "do not assert status"
		wantChecked bool
	}{
		{
			name:        "draw zero selects first status",
			prob:        1,
			draw:        0,
			wantCalled:  false,
			wantStatus:  http.StatusBadGateway,
			wantChecked: true,
		},
		{
			// draw 0.5 * len 2 = index 1, the last status.
			name:        "draw selects last status",
			prob:        1,
			draw:        0.5,
			wantCalled:  false,
			wantStatus:  http.StatusServiceUnavailable,
			wantChecked: true,
		},
		{
			// draw just under 1 * len 2 = index 1; the clamp is not needed but
			// this confirms the top of the range still maps inside the slice.
			name:        "draw near one selects last status",
			prob:        1,
			draw:        0.999,
			wantCalled:  false,
			wantStatus:  http.StatusServiceUnavailable,
			wantChecked: true,
		},
		{
			name:       "does not trigger at probability 0",
			prob:       0,
			draw:       0,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mw, err := chaos.RandomResponse(
				chaos.WithProbability(tt.prob),
				chaos.WithRandFloat(constRand(tt.draw)),
				chaos.WithStatuses(statuses...),
			)
			if err != nil {
				t.Fatalf("RandomResponse: %v", err)
			}
			called := false
			h := mw(nextHandler(&called))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, newRequest(t))

			if called != tt.wantCalled {
				t.Errorf("next called = %v, want %v", called, tt.wantCalled)
			}
			if tt.wantChecked && rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestRandomResponseDefaultStatuses(t *testing.T) {
	t.Parallel()

	// Without WithStatuses the default pool [500, 502, 503] is used.
	want := []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	}
	mw, err := chaos.RandomResponse(
		chaos.WithProbability(1),
		chaos.WithRandFloat(constRand(0)),
	)
	if err != nil {
		t.Fatalf("RandomResponse: %v", err)
	}
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t))

	if !slices.Contains(want, rec.Code) {
		t.Fatalf("status = %d, want one of %v", rec.Code, want)
	}
}

func TestOptionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		build   func() (any, error)
		wantErr bool
	}{
		{
			name:    "probability above one",
			build:   func() (any, error) { return chaos.Abort(chaos.WithProbability(1.5)) },
			wantErr: true,
		},
		{
			name:    "negative probability",
			build:   func() (any, error) { return chaos.Sleep(chaos.WithProbability(-0.1)) },
			wantErr: true,
		},
		{
			name:    "probability at boundary zero",
			build:   func() (any, error) { return chaos.Abort(chaos.WithProbability(0)) },
			wantErr: false,
		},
		{
			name:    "probability at boundary one",
			build:   func() (any, error) { return chaos.Abort(chaos.WithProbability(1)) },
			wantErr: false,
		},
		{
			name:    "nil rand func",
			build:   func() (any, error) { return chaos.Abort(chaos.WithRandFloat(nil)) },
			wantErr: true,
		},
		{
			name:    "abort status below 400",
			build:   func() (any, error) { return chaos.Abort(chaos.WithAbortStatus(http.StatusOK)) },
			wantErr: true,
		},
		{
			name:    "abort status above 599",
			build:   func() (any, error) { return chaos.Abort(chaos.WithAbortStatus(600)) },
			wantErr: true,
		},
		{
			name:    "abort status at 400 boundary",
			build:   func() (any, error) { return chaos.Abort(chaos.WithAbortStatus(http.StatusBadRequest)) },
			wantErr: false,
		},
		{
			name:    "delay range max below min",
			build:   func() (any, error) { return chaos.Sleep(chaos.WithDelayRange(2*time.Second, time.Second)) },
			wantErr: true,
		},
		{
			name:    "delay range negative min",
			build:   func() (any, error) { return chaos.Sleep(chaos.WithDelayRange(-time.Second, time.Second)) },
			wantErr: true,
		},
		{
			name:    "delay range equal bounds",
			build:   func() (any, error) { return chaos.Sleep(chaos.WithDelayRange(time.Second, time.Second)) },
			wantErr: false,
		},
		{
			name:    "empty statuses",
			build:   func() (any, error) { return chaos.RandomResponse(chaos.WithStatuses()) },
			wantErr: true,
		},
		{
			name:    "status below 100",
			build:   func() (any, error) { return chaos.RandomResponse(chaos.WithStatuses(99)) },
			wantErr: true,
		},
		{
			name:    "status above 599",
			build:   func() (any, error) { return chaos.RandomResponse(chaos.WithStatuses(600)) },
			wantErr: true,
		},
		{
			name:    "status boundaries valid",
			build:   func() (any, error) { return chaos.RandomResponse(chaos.WithStatuses(100, 599)) },
			wantErr: false,
		},
		{
			// Multiple invalid options join their errors; New still fails.
			name: "multiple invalid options joined",
			build: func() (any, error) {
				return chaos.Abort(chaos.WithProbability(2), chaos.WithAbortStatus(200))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := tt.build()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRandomResponseNegativeRandFloatDoesNotPanic(t *testing.T) {
	t.Parallel()

	// A misbehaving randFloat returning a value below the documented [0, 1)
	// range must not produce a negative slice index. randStatus clamps to a
	// valid index instead of panicking.
	statuses := []int{http.StatusBadGateway, http.StatusServiceUnavailable}
	mw, err := chaos.RandomResponse(
		chaos.WithProbability(1),
		chaos.WithRandFloat(constRand(-0.5)),
		chaos.WithStatuses(statuses...),
	)
	if err != nil {
		t.Fatalf("RandomResponse: %v", err)
	}
	called := false
	h := mw(nextHandler(&called))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t)) // must not panic

	if called {
		t.Fatal("next handler called despite a triggered random response")
	}
	if !slices.Contains(statuses, rec.Code) {
		t.Fatalf("status = %d, want one of %v", rec.Code, statuses)
	}
}
