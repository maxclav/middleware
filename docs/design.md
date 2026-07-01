# Design notes

## The core type

A middleware is the standard `func(http.Handler) http.Handler`. The package
exposes it as a type alias:

```go
type Middleware = func(http.Handler) http.Handler
```

Because it is an alias rather than a defined type, any existing
`func(http.Handler) http.Handler` value, from the standard library, chi, gorilla,
alice or your own code, satisfies it without a conversion.

`Chain` composes middlewares and is immutable. Every method returns a new `Chain`
and leaves the receiver unchanged, so a chain can be built once and reused as a
template.

```go
base := middleware.New(rec, rid, logMW)   // reusable prefix
api := base.Append(cors, authMW)          // base plus API concerns
admin := base.Extend(adminChain)          // base plus another chain
```

| Method | Behavior |
| --- | --- |
| `New(...Middleware) Chain` | Build a chain from middlewares applied in order. |
| `Then(http.Handler) http.Handler` | Wrap a handler; nil uses `http.DefaultServeMux`. |
| `ThenFunc(http.HandlerFunc) http.Handler` | Wrap a handler function. |
| `Append(...Middleware) Chain` | New chain with middlewares added after. |
| `Extend(Chain) Chain` | New chain with another chain appended. |

The first middleware added is the outermost wrapper. It runs first on the way in
and last on the way out.

## Dependency isolation

Each middleware is a separate subpackage. Middlewares that need a third-party
dependency keep it behind that subpackage boundary, so importing the root package
or any standard-library middleware pulls in nothing else. The dependencies on
OpenTelemetry, golang-jwt and `golang.org/x/time` are only compiled into a binary
that imports `otelmetrics`, `oteltrace`, `jwt` or `ratelimit`.

The subpackages sit at import paths that can be promoted to nested modules later
without changing those paths, should a dependency ever warrant its own release
cadence.

## Scope

The library covers server-side middleware for `http.Handler`. Client-side
resilience such as retry, backoff and circuit breaking is out of scope: those wrap
outbound calls (`http.RoundTripper`), not inbound requests, and belong in a
separate client-transport package. gRPC and ConnectRPC are also out of scope,
since their interceptor models do not map onto `http.Handler`.

## Writing your own

Any `func(http.Handler) http.Handler` drops into a `Chain`. To read the response
after the handler runs, wrap the writer with `middleware.WrapResponseWriter`. It
records the status code and byte count and keeps `http.Flusher` and
`http.Hijacker` reachable through `http.NewResponseController`:

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

## Quality gate

The module passes `go test -race`, `go vet`, `staticcheck` and the `golangci-lint`
configuration in `.golangci.yml`. Statement coverage is 99.6 percent; the
remaining lines are defensive branches that the public API cannot reach, and they
are documented as such in the tests.
