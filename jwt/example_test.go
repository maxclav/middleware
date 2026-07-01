package jwt_test

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/maxclav/middleware/jwt"
)

// Example validates an HS256 bearer token and reads its claims from the request
// context. For asymmetric keys use [jwt.WithRSAPublicKey], [jwt.WithECDSAPublicKey]
// or [jwt.WithEdDSAPublicKey], which pin the accepted algorithms and so prevent
// algorithm-confusion attacks.
func Example() {
	secret := []byte("your-256-bit-secret")

	mw, err := jwt.New(jwt.WithHMACKey(secret))
	if err != nil {
		log.Fatal(err)
	}

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := jwt.FromContext(r.Context())
		mc, _ := claims.(gojwt.MapClaims)
		_, _ = fmt.Fprintf(w, "sub=%v", mc["sub"])
	}))

	// Mint a token, as an auth server would.
	signed, _ := gojwt.NewWithClaims(
		gojwt.SigningMethodHS256, gojwt.MapClaims{"sub": "alice"},
	).SignedString(secret)
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+signed)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	fmt.Println(rec.Code, rec.Body.String())
	// Output: 200 sub=alice
}
