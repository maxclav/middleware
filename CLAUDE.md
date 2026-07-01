# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

Go 1.25 or newer. The `.golangci.yml` at the repo root is the lint gate (golangci-lint v2).

```sh
go build ./...
go test -race ./...                                  # full suite
go test ./cors/...                                   # one package
go test -run TestPreflightFromAllowedOrigin ./cors/... # one test
go test -coverprofile=/tmp/c.out ./... && go tool cover -func=/tmp/c.out | tail -1  # coverage
go test -run Example .                               # runnable end-to-end example (example_test.go)
go test -bench=. -benchmem -run='^$' ./...           # per-request hot-path benchmarks

gofmt -l .
go vet ./...
staticcheck ./...
golangci-lint run ./...                              # honors .golangci.yml
```

golangci-lint caps repeated findings by default; add `--max-same-issues=0 --max-issues-per-linter=0` when checking that a package is fully clean.

## Architecture

This is a single Go module of HTTP server middleware. It is an importable library with no binary or entrypoint; the only runnable artifact is the `Example` in `example_test.go`. The design intent lives in `docs/design.md`; the per-package reference is `docs/middlewares.md`.

The **root package** (`middleware`) is the shared contract and depends only on the standard library:

- `Middleware` is a type alias for `func(http.Handler) http.Handler`. It is an alias, not a defined type, so any stdlib/chi/gorilla/alice function is assignable without conversion. Do not change it to a defined type.
- `Chain` is immutable. Every method returns a new `Chain`. The first middleware added is the outermost wrapper.
- `ErrorHandler` and `DefaultErrorHandler` are the shared error-response type used by every rejecting middleware.
- `WrapResponseWriter` returns a `ResponseWriter` that records status and byte count and exposes `Unwrap()` for `http.NewResponseController`. Any middleware that inspects the response (logger, metrics, gzip, cache) goes through it.

Every other middleware is its **own subpackage**. This is a dependency-isolation boundary, not a stylistic one: the OpenTelemetry, golang-jwt and `golang.org/x/time` dependencies are confined to `otelmetrics`, `oteltrace`, `jwt` and `ratelimit`, so importing the root package or any stdlib middleware pulls in nothing else. When adding a middleware, keep any third-party dependency inside its own subpackage; do not introduce it into a package that other middlewares import.

## Conventions to follow when editing or adding a middleware

These are load-bearing for consistency across the library. Match the existing packages (`recovery` is the canonical template).

- Constructor is `New(...Option) (middleware.Middleware, error)`. A package that groups closely related middlewares uses named constructors sharing one `Option` set instead (see `chaos`: `Abort`, `Sleep`, `RandomResponse`).
- `type Option func(*config) error` over an unexported `config` struct. `WithX` builders validate their input and return an error prefixed with the package name (for example `errors.New("cors: ...")`). `New` seeds defaults, applies options while collecting errors, and returns `errors.Join(errs...)`.
- Rejecting middlewares take `WithErrorHandler(middleware.ErrorHandler)` defaulting to `middleware.DefaultErrorHandler`. Set status-specific headers (`Retry-After`, `WWW-Authenticate`) before calling the handler.
- Middlewares that log take `WithLogger(*slog.Logger)` defaulting to `slog.Default()`.
- Request-scoped values use an unexported `type ctxKey struct{}` with exported `FromContext` and `NewContext` accessors.
- Add `var _ Iface = (*T)(nil)` where a concrete type implements an interface (the response-writer wrappers).
- The public API is treated as frozen. Renaming exported identifiers or changing constructor and option signatures is a breaking change; parameter renames and internal changes are fine.

## Testing

Tests use the standard `testing` and `net/http/httptest` only, plus a package's own third-party dependency for fixtures (golang-jwt for minting tokens, the OTEL SDK for in-memory readers). They are table-driven with `t.Run` subtests, `t.Parallel()` where safe, `t.Helper()` on builders, and `http.NoBody` for empty request bodies. Coverage is expected to be near complete; a branch that the public API genuinely cannot reach is left uncovered and explained in a comment rather than exercised with an artificial test.

## Scope

Server-side `http.Handler` middleware only. gRPC and ConnectRPC are out of scope because their interceptor models do not map onto `http.Handler`. Client-side resilience (retry, backoff, circuit breaker) is out of scope because it wraps `http.RoundTripper`, not inbound requests.

## Documentation style

Keep the README short and put detail in `docs/`. Prose in comments and markdown must not contain em-dashes; reword with commas, colons, parentheses, or separate sentences.
