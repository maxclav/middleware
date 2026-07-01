package middleware_test

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/maxclav/middleware"
	"github.com/maxclav/middleware/recovery"
	"github.com/maxclav/middleware/requestid"
	"github.com/maxclav/middleware/secure"
	"github.com/maxclav/middleware/timeout"
)

// Example assembles a small production-style stack. Because every constructor
// returns (Middleware, error), configuration mistakes surface once at startup —
// aggregated with errors.Join — rather than at request time. The assembled
// Chain is immutable and reusable.
func Example() {
	rec, e1 := recovery.New()
	rid, e2 := requestid.New(
		// A fixed generator keeps this example's output deterministic; in
		// production omit it to get random 128-bit IDs.
		requestid.WithGenerator(func() string { return "req-1" }),
	)
	sec, e3 := secure.New()
	tmo, e4 := timeout.New(timeout.WithTimeout(5 * time.Second))
	if err := errors.Join(e1, e2, e3, e4); err != nil {
		log.Fatalf("middleware config: %v", err)
	}

	// First added is outermost: recovery wraps everything, then requestid, ...
	stack := middleware.New(rec, rid, sec, tmo)

	handler := stack.ThenFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := requestid.FromContext(r.Context())
		_, _ = fmt.Fprintf(w, "handled %s", id)
	})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	fmt.Println(rr.Body.String())
	fmt.Println(rr.Header().Get("X-Request-ID"))
	fmt.Println(rr.Header().Get("X-Content-Type-Options"))
	// Output:
	// handled req-1
	// req-1
	// nosniff
}
