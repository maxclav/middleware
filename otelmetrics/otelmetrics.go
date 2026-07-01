// Package otelmetrics provides middleware that records OpenTelemetry HTTP
// server metrics: the duration of each request and the number of in-flight
// requests.
package otelmetrics

import (
	"errors"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/maxclav/middleware"
)

// scopeName identifies this instrumentation to the meter provider.
const scopeName = "github.com/maxclav/middleware/otelmetrics"

type config struct {
	meterProvider metric.MeterProvider
}

// Option configures the otelmetrics middleware.
type Option func(*config) error

// WithMeterProvider sets the [metric.MeterProvider] used to create the
// instruments. It must not be nil. Defaults to [otel.GetMeterProvider].
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) error {
		if mp == nil {
			return errors.New("otelmetrics: meter provider must not be nil")
		}
		c.meterProvider = mp
		return nil
	}
}

// New returns middleware that records HTTP server metrics for each request:
//
//   - http.server.request.duration, a float64 histogram of request durations in
//     seconds, attributed by request method and response status code.
//   - http.server.active_requests, an int64 up-down counter of in-flight
//     requests.
//
// The instruments are created once from the configured [metric.MeterProvider];
// if instrument creation fails, New returns the error.
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		meterProvider: otel.GetMeterProvider(),
	}
	var errs []error
	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	meter := cfg.meterProvider.Meter(scopeName)
	duration, err := meter.Float64Histogram(
		"http.server.request.duration",
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	active, err := meter.Int64UpDownCounter(
		"http.server.active_requests",
		metric.WithDescription("Number of in-flight HTTP server requests."),
	)
	if err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			start := time.Now()

			active.Add(ctx, 1)
			defer active.Add(ctx, -1)

			rw := middleware.WrapResponseWriter(w)
			next.ServeHTTP(rw, r)

			status := rw.Status()
			if status == 0 {
				status = http.StatusOK
			}
			duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.Int("http.response.status_code", status),
			))
		})
	}, nil
}
