# Contributing

Thanks for considering a contribution.

## Prerequisites

- Go 1.25 or newer.
- golangci-lint (the config in `.golangci.yml` is the gate).

## Develop

```sh
go test -race ./...                 # tests
go vet ./... && staticcheck ./...   # static analysis
golangci-lint run ./...             # lint gate, must be clean
go test -bench=. -benchmem ./...    # hot-path benchmarks
```

`CLAUDE.md` documents the architecture and the conventions every middleware
follows: the `New(...Option) (Middleware, error)` constructor shape, option
validation, error handling, and context accessors.

## Pull requests

- Keep `go test -race ./...` and the lint gate green.
- Match the existing conventions; `recovery` is the canonical template.
- Cover new behavior with tests.
- The public API is treated as stable, so avoid changing exported signatures.
