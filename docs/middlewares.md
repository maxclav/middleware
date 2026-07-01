# Middleware catalog

Each middleware lives in its own subpackage and is constructed with
`New(...Option) (middleware.Middleware, error)`. Packages marked **stdlib** add no
third-party dependency.

| Package | Purpose | Key options and helpers | Extra deps |
| --- | --- | --- | --- |
| `recovery` | Recover from handler panics and return 500. Re-panics `http.ErrAbortHandler`. | `WithLogger`, `WithErrorHandler`, `WithStackTrace` | stdlib |
| `requestid` | Assign or propagate an `X-Request-ID` and store it in the context. | `WithHeader`, `WithGenerator`, `WithTrustIncoming`, `FromContext` | stdlib |
| `logger` | One `slog` record per request with method, status, size and duration. | `WithLogger`, `WithAttrs`, `WithLevelFunc`, `WithMessage` | stdlib |
| `timeout` | Per-request deadline via `http.TimeoutHandler`, returning 503 on expiry. | `WithTimeout` (required), `WithMessage` | stdlib |
| `cors` | CORS headers and preflight handling, with credentials safety checks. | `WithAllowedOrigins`, `WithAllowOriginFunc`, `WithAllowedMethods`, `WithAllowedHeaders`, `WithExposedHeaders`, `WithAllowCredentials`, `WithMaxAge` | stdlib |
| `secure` | Baseline security response headers. | `WithHSTS`, `WithFrameOptions`, `WithContentTypeNosniff`, `WithReferrerPolicy`, `WithContentSecurityPolicy`, `WithPermissionsPolicy` | stdlib |
| `headers` | Set, add or remove response headers; set request headers. | `WithResponseHeader`, `WithAddedResponseHeader`, `WithRemovedResponseHeader`, `WithRequestHeader` | stdlib |
| `gzip` | Negotiated gzip compression with pooled writers and a size threshold. | `WithLevel`, `WithMinSize`, `WithContentTypes` | stdlib |
| `healthcheck` | Liveness and readiness endpoints with pluggable checks. | `WithLivenessPath`, `WithReadinessPath`, `WithReadinessCheck` | stdlib |
| `redirect` | Canonicalize scheme or host, for example forcing HTTPS. | `WithScheme`, `WithHost`, `WithCode`, `WithTrustForwardedHeaders` | stdlib |
| `skipif` | Bypass a wrapped middleware when a predicate matches. | `New(skip, mw)`, `PathHasPrefix`, `MethodIs` | stdlib |
| `chaos` | Fault injection: `Abort`, `Sleep`, `RandomResponse`. | `WithProbability`, `WithRandFloat`, `WithAbortStatus`, `WithDelayRange`, `WithStatuses` | stdlib |
| `cache` | In-memory response cache for GET and HEAD, with TTL and size limits. | `WithTTL`, `WithMaxBodyBytes`, `WithMaxEntries`, `WithMethods`, `WithKeyFunc` | stdlib |
| `csrf` | Stateless double-submit-cookie CSRF protection. | `WithCookieName`, `WithHeaderName`, `WithFieldName`, `WithSecureCookie`, `WithSameSite`, `Token` | stdlib |
| `pprof` | Serve the `net/http/pprof` endpoints under a prefix. | `WithPrefix` | stdlib |
| `auth` | Scheme-based authentication with a pluggable verifier. | `New(verify, ...)`, `Basic`, `Bearer`, `APIKey`, `FromContext` | stdlib |
| `ratelimit` | Token-bucket rate limiting, global or per key. | `WithLimit`, `WithRPS`, `WithBurst`, `WithKeyFunc`, `WithMaxKeys`, `ClientIP` | `golang.org/x/time` |
| `jwt` | Validate a JWT bearer token and place its claims in the context. | `WithHMACKey`, `WithRSAPublicKey`, `WithECDSAPublicKey`, `WithValidMethods`, `WithExpirationRequired`, `WithIssuer`, `WithAudience`, `FromContext` | `golang-jwt/jwt/v5` |
| `otelmetrics` | OpenTelemetry HTTP server metrics (request duration, active requests). | `WithMeterProvider` | `go.opentelemetry.io/otel` |
| `oteltrace` | OpenTelemetry server spans with context propagation. | `WithTracerProvider`, `WithPropagators`, `WithSpanNameFunc` | `go.opentelemetry.io/otel` |

## Configuration

Options are `func(*config) error` and validate their input. `New` applies them,
collects every failure with `errors.Join`, and returns the aggregate:

```go
mw, err := ratelimit.New(
	ratelimit.WithRPS(100),
	ratelimit.WithBurst(50),
	ratelimit.WithKeyFunc(ratelimit.ClientIP),
)
if err != nil {
	return err
}
```

## Error responses

Middlewares that reject a request accept `WithErrorHandler`:

```go
type ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)
```

The default writes the status text as `text/plain`. Passing the same handler to
several middlewares gives the stack a single error format, for example RFC 9457
problem details. Status-specific headers such as `Retry-After` and
`WWW-Authenticate` are set before the handler runs.

## Context values

Middlewares that store request-scoped data expose typed accessors:

```go
id, ok := requestid.FromContext(ctx)
identity, ok := auth.FromContext(ctx)
claims, ok := jwt.FromContext(ctx)
```
