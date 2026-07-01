# middleware

[![Go Reference](https://pkg.go.dev/badge/github.com/maxclav/middleware.svg)](https://pkg.go.dev/github.com/maxclav/middleware)
[![Go Report Card](https://goreportcard.com/badge/github.com/maxclav/middleware)](https://goreportcard.com/report/github.com/maxclav/middleware)
[![Go Version](https://img.shields.io/github/go-mod/go-version/maxclav/middleware)](https://go.dev/)
[![License](https://img.shields.io/github/license/maxclav/middleware)](LICENSE)

Composable `net/http` middleware for Go. A small core (the `Middleware` type and
an immutable `Chain`) plus a set of focused middlewares, one per subpackage, so
importing the root package costs nothing beyond the standard library.

## Install

```sh
go get github.com/maxclav/middleware
```

Requires Go 1.25 or newer.

## Usage

```go
rec, _ := recovery.New()
rid, _ := requestid.New()
sec, _ := secure.New()

// The first middleware is the outermost wrapper.
stack := middleware.New(rec, rid, sec)

mux := http.NewServeMux()
mux.HandleFunc("/", index)

log.Fatal(http.ListenAndServe(":8080", stack.Then(mux)))
```

Every middleware is constructed with `New(...Option) (Middleware, error)`. Options
validate their input, so configuration errors are returned at startup instead of
failing a live request.

## Middlewares

`recovery`, `requestid`, `logger`, `timeout`, `cors`, `secure`, `headers`,
`gzip`, `healthcheck`, `redirect`, `skipif`, `chaos`, `cache`, `csrf`, `pprof`,
`auth`, `ratelimit`, `jwt`, `otelmetrics`, `oteltrace`.

The [middleware catalog](docs/middlewares.md) documents each one and its options.
The [design notes](docs/design.md) cover the architecture, conventions, and how to
write your own.

## Testing

```sh
go test -race ./...
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
