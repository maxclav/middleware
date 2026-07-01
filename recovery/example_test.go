package recovery_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/maxclav/middleware/recovery"
)

// Example turns a panicking handler into a 500 response instead of crashing the
// server. discardLogger keeps the example output clean; production code uses the
// default slog logger.
func Example() {
	mw, _ := recovery.New(recovery.WithLogger(discardLogger()))
	handler := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	fmt.Println(rec.Code)
	// Output: 500
}
