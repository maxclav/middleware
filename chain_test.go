package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/maxclav/middleware"
)

// tag records "<id>-in" before calling next and "<id>-out" after, so tests can
// assert both the ordering and the nesting of a Chain.
func tag(id string, log *[]string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*log = append(*log, id+"-in")
			next.ServeHTTP(w, r)
			*log = append(*log, id+"-out")
		})
	}
}

func serve(h http.Handler) {
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))
}

func TestChainExecutesInRegistrationOrder(t *testing.T) {
	t.Parallel()

	var log []string
	h := middleware.New(tag("a", &log), tag("b", &log), tag("c", &log)).
		ThenFunc(func(http.ResponseWriter, *http.Request) {
			log = append(log, "handler")
		})
	serve(h)

	want := []string{"a-in", "b-in", "c-in", "handler", "c-out", "b-out", "a-out"}
	if !slices.Equal(log, want) {
		t.Fatalf("order = %v, want %v", log, want)
	}
}

func TestAppendDoesNotMutateReceiver(t *testing.T) {
	t.Parallel()

	var log []string
	base := middleware.New(tag("a", &log))
	extended := base.Append(tag("b", &log))

	serve(base.ThenFunc(func(http.ResponseWriter, *http.Request) {}))
	if !slices.Equal(log, []string{"a-in", "a-out"}) {
		t.Fatalf("base chain changed by Append: %v", log)
	}

	log = nil
	serve(extended.ThenFunc(func(http.ResponseWriter, *http.Request) {}))
	if !slices.Equal(log, []string{"a-in", "b-in", "b-out", "a-out"}) {
		t.Fatalf("extended chain = %v", log)
	}
}

func TestExtendConcatenatesChains(t *testing.T) {
	t.Parallel()

	var log []string
	first := middleware.New(tag("a", &log), tag("b", &log))
	second := middleware.New(tag("c", &log))

	serve(first.Extend(second).ThenFunc(func(http.ResponseWriter, *http.Request) {}))
	want := []string{"a-in", "b-in", "c-in", "c-out", "b-out", "a-out"}
	if !slices.Equal(log, want) {
		t.Fatalf("extend order = %v, want %v", log, want)
	}
}

func TestZeroChainIsUsable(t *testing.T) {
	t.Parallel()

	called := false
	middleware.New().
		ThenFunc(func(http.ResponseWriter, *http.Request) { called = true }).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if !called {
		t.Fatal("handler not called through empty chain")
	}
}

func TestThenNilUsesDefaultServeMux(t *testing.T) {
	t.Parallel()

	// Then(nil) must not panic and must fall back to http.DefaultServeMux.
	if got := middleware.New().Then(nil); got != http.Handler(http.DefaultServeMux) {
		t.Fatalf("Then(nil) = %v, want http.DefaultServeMux", got)
	}
}

func TestThenFuncNilUsesDefaultServeMux(t *testing.T) {
	t.Parallel()

	// ThenFunc(nil) must behave like Then(nil).
	if got := middleware.New().ThenFunc(nil); got != http.Handler(http.DefaultServeMux) {
		t.Fatalf("ThenFunc(nil) = %v, want http.DefaultServeMux", got)
	}
}

func TestChainIsReusable(t *testing.T) {
	t.Parallel()

	// Then must not mutate the receiver, so the same Chain can wrap many handlers.
	var log []string
	c := middleware.New(tag("a", &log))

	for _, name := range []string{"h1", "h2"} {
		log = nil
		serve(c.ThenFunc(func(http.ResponseWriter, *http.Request) { log = append(log, name) }))
		if !slices.Equal(log, []string{"a-in", name, "a-out"}) {
			t.Fatalf("%s: log = %v", name, log)
		}
	}
}
