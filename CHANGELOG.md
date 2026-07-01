# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial release. A net/http middleware toolkit built on a `Middleware` type
  alias and an immutable `Chain`, with the following middlewares, each in its own
  subpackage: recovery, requestid, logger, timeout, cors, secure, headers, gzip,
  healthcheck, redirect, skipif, chaos, cache, csrf, pprof, auth, ratelimit, jwt,
  otelmetrics, oteltrace.
