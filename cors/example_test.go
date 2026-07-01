package cors_test

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/maxclav/middleware/cors"
)

// Example answers a CORS preflight for an explicitly allowed origin. Set
// explicit origins rather than relying on the permissive wildcard default.
func Example() {
	mw, err := cors.New(
		cors.WithAllowedOrigins("https://app.example.com"),
		cors.WithAllowedMethods(http.MethodGet, http.MethodPost),
	)
	if err != nil {
		log.Fatal(err)
	}
	handler := mw(http.NotFoundHandler())

	req := httptest.NewRequest(http.MethodOptions, "/", http.NoBody)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	fmt.Println(rec.Header().Get("Access-Control-Allow-Origin"))
	// Output:
	// 204
	// https://app.example.com
}
