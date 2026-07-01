# middleware

A small, idiomatic toolkit for composing `net/http` middleware in Go.

```go
import "github.com/maxclav/middleware"
```

`func(http.Handler) http.Handler` is the whole contract. This library gives you
an immutable way to compose those functions, a set of focused, well-tested
middlewares, and a consistent construction idiom — without dragging heavy
dependencies into your binary unless you actually use them.

- **HTTP only.** The middleware type is the standard `func(http.Handler) http.Handler`,
  so everything here interoperates with the standard library, chi, gorilla and
  alice with zero conversion. gRPC/ConnectRPC are deliberately out of scope —
  their interceptor models don't fit `http.Handler`, and a "unified" abstraction
  would leak.
- **One middleware per subpackage.** Import only what you use. Importing the
  root package (or a stdlib-only middleware) pulls in **nothing beyond the
  standard library**; the OTEL and JWT dependencies live behind their own
  subpackages.
- **Uniform construction.** Every middleware is `New(...Option) (Middleware, error)`.
  Options validate their inputs and can fail; `New` aggregates all option errors
  with `errors.Join`, so misconfiguration surfaces **once at startup**, not on a
  live request.
- **Battle-tested.** Every package ships table-driven tests and passes
  `go test -race`.

Requires **Go 1.25+**.

## Install

```sh
go get github.com/maxclav/middleware
```

## Quick start

```go
rec, e1 := recovery.New()
rid, e2 := requestid.New()
sec, e3 := secure.New()
cors, e4 := cors.New(cors.WithAllowedOrigins("https://app.example.com"))
tmo, e5 := timeout.New(timeout.WithTimeout(15 * time.Second))
if err := errors.Join(e1, e2, e3, e4, e5); err != nil {
	log.Fatalf("middleware config: %v", err)
}

// First added is the outermost wrapper: it runs first on the way in and
// last on the way out.
stack := middleware.New(rec, rid, sec, cors, tmo)

mux := http.NewServeMux()
mux.HandleFunc("/", index)

log.Fatal(http.ListenAndServe(":8080", stack.Then(mux)))
```

## Composition — `Chain`

`Chain` is immutable: every method returns a new `Chain` and never mutates the
receiver, so a chain is safe to share and reuse as a template.

```go
base := middleware.New(rec, rid, logMW)   // reusable prefix

api   := base.Append(cors, authMW)         // base + API concerns
admin := base.Extend(adminChain)           // base + another chain

mux.Handle("/api/",   api.Then(apiMux))
mux.Handle("/admin/", admin.Then(adminMux))
```

| Method | Purpose |
| --- | --- |
| `New(...Middleware) Chain` | Build a chain from middlewares, applied in order. |
| `Then(http.Handler) http.Handler` | Wrap a handler (nil → `http.DefaultServeMux`). |
| `ThenFunc(http.HandlerFunc) http.Handler` | Wrap a handler function. |
| `Append(...Middleware) Chain` | New chain with middlewares added after. |
| `Extend(Chain) Chain` | New chain with another chain's middlewares appended. |

## Middleware catalog

Everything marked **stdlib** adds no third-party dependency.

| Package | What it does | Notable options / API | Extra deps |
| --- | --- | --- | --- |
| `recovery` | Recover from panics → 500; re-panics `http.ErrAbortHandler` | `WithLogger`, `WithErrorHandler`, `WithStackTrace` | stdlib |
| `requestid` | Assign/propagate an `X-Request-ID`, store it in context | `WithHeader`, `WithGenerator`, `WithTrustIncoming`; `FromContext` | stdlib |
| `logger` | One `slog` record per request (method, status, bytes, duration) | `WithLogger`, `WithAttrs`, `WithLevelFunc`, `WithMessage` | stdlib |
| `timeout` | Per-request deadline via `http.TimeoutHandler` → 503 | `WithTimeout` (required), `WithMessage` | stdlib |
| `cors` | CORS headers + preflight, credentials-safe | `WithAllowedOrigins`, `WithAllowOriginFunc`, `WithAllowedMethods/Headers`, `WithExposedHeaders`, `WithAllowCredentials`, `WithMaxAge` | stdlib |
| `secure` | Security headers (HSTS, frame, nosniff, referrer, CSP, permissions) | `WithHSTS`, `WithFrameOptions`, `WithContentTypeNosniff`, `WithReferrerPolicy`, `WithContentSecurityPolicy`, `WithPermissionsPolicy` | stdlib |
| `headers` | Set/add/remove response headers; set request headers | `WithResponseHeader`, `WithAddedResponseHeader`, `WithRemovedResponseHeader`, `WithRequestHeader` | stdlib |
| `gzip` | Negotiated gzip compression (pooled writers, min-size, type filter) | `WithLevel`, `WithMinSize`, `WithContentTypes` | stdlib |
| `healthcheck` | Liveness/readiness endpoints with pluggable checks | `WithLivenessPath`, `WithReadinessPath`, `WithReadinessCheck` | stdlib |
| `redirect` | Canonicalize scheme/host (e.g. force HTTPS) | `WithScheme`, `WithHost`, `WithCode`, `WithTrustForwardedHeaders` | stdlib |
| `skipif` | Conditionally bypass a wrapped middleware | `New(skip Predicate, mw Middleware)`; `PathHasPrefix`, `MethodIs` | stdlib |
| `chaos` | Fault injection: `Abort`, `Sleep`, `RandomResponse` | `WithProbability`, `WithRandFloat`, `WithAbortStatus`, `WithDelayRange`, `WithStatuses` | stdlib |
| `cache` | In-memory response cache (GET/HEAD, TTL, size/entry caps) | `WithTTL`, `WithMaxBodyBytes`, `WithMaxEntries`, `WithMethods`, `WithKeyFunc` | stdlib |
| `csrf` | Stateless double-submit-cookie CSRF protection | `WithCookieName`, `WithHeaderName`, `WithFieldName`, `WithSecureCookie`, `WithSameSite`; `Token` | stdlib |
| `pprof` | Serve `net/http/pprof` under a prefix (protect it!) | `WithPrefix` | stdlib |
| `auth` | Scheme-based auth with a pluggable verifier | `New(verify, ...)`; `Basic`, `Bearer`, `APIKey`; `FromContext` | stdlib |
| `ratelimit` | Token-bucket limiting, global or per-key | `WithLimit`/`WithRPS`, `WithBurst`, `WithKeyFunc`, `WithMaxKeys`; `ClientIP` | `golang.org/x/time` |
| `jwt` | Validate a JWT bearer token, claims into context | `WithHMACKey`/`WithKeyFunc`, `WithValidMethods`, `WithIssuer`, `WithAudience`, `WithLeeway`; `FromContext` | `golang-jwt/jwt/v5` |
| `otelmetrics` | OpenTelemetry HTTP server metrics (duration, active) | `WithMeterProvider` | `go.opentelemetry.io/otel` |
| `oteltrace` | OpenTelemetry server spans + context propagation | `WithTracerProvider`, `WithPropagators`, `WithSpanNameFunc` | `go.opentelemetry.io/otel` |

> Client-side resilience (retry, backoff, circuit breaker) is intentionally not
> here: those wrap outbound calls (`http.RoundTripper`), not inbound requests.
> They belong in a separate client-transport package.

## Conventions

**Construction.** Options are `func(*config) error` and may validate:

```go
mw, err := ratelimit.New(
	ratelimit.WithRPS(100),
	ratelimit.WithBurst(50),
	ratelimit.WithKeyFunc(ratelimit.ClientIP),
)
if err != nil {
	return err // e.g. a non-positive burst is reported here, at startup
}
```

**Error responses.** Every rejecting middleware accepts
`WithErrorHandler(middleware.ErrorHandler)`:

```go
type ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)
```

The default writes the status text as `text/plain`. Pass the *same* handler to
every middleware to give your whole stack one consistent error format (e.g. RFC
9457 problem+json). Middleware-specific headers (`Retry-After`,
`WWW-Authenticate`) are set before the handler runs.

**Request-scoped values.** Middlewares that stash data in the context expose
typed accessors:

```go
id, ok       := requestid.FromContext(ctx)
identity, ok := auth.FromContext(ctx)
claims, ok   := jwt.FromContext(ctx)
```

## Writing your own middleware

Any `func(http.Handler) http.Handler` drops straight into a `Chain`. To observe
the response, wrap the writer with the shared helper — it captures the status
and byte count and preserves `http.Flusher`/`http.Hijacker` via
`http.NewResponseController`:

```go
func Timing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := middleware.WrapResponseWriter(w)
		start := time.Now()
		next.ServeHTTP(rw, r)
		log.Printf("%d in %s", rw.Status(), time.Since(start))
	})
}
```

## Testing

```sh
go test -race ./...
```

Every package is covered by table-driven tests using only the standard
`testing` and `net/http/httptest` packages, and the whole module passes under
the race detector.
