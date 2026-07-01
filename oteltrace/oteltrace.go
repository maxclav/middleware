// Package oteltrace provides middleware that records an OpenTelemetry server
// span for each HTTP request. Incoming trace context is extracted from the
// request headers so the span joins any distributed trace, and response
// attributes (including the status code) are recorded once the handler returns.
package oteltrace

import (
	"errors"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/maxclav/middleware"
)

// scopeName identifies this instrumentation to the tracer provider.
const scopeName = "github.com/maxclav/middleware/oteltrace"

type config struct {
	tracerProvider trace.TracerProvider
	propagators    propagation.TextMapPropagator
	spanNameFunc   func(*http.Request) string
}

// Option configures the oteltrace middleware.
type Option func(*config) error

// WithTracerProvider sets the [trace.TracerProvider] used to obtain the tracer.
// It must not be nil. Defaults to [otel.GetTracerProvider].
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) error {
		if tp == nil {
			return errors.New("oteltrace: tracer provider must not be nil")
		}
		c.tracerProvider = tp
		return nil
	}
}

// WithPropagators sets the [propagation.TextMapPropagator] used to extract
// incoming trace context. It must not be nil. Defaults to
// [otel.GetTextMapPropagator].
func WithPropagators(p propagation.TextMapPropagator) Option {
	return func(c *config) error {
		if p == nil {
			return errors.New("oteltrace: propagators must not be nil")
		}
		c.propagators = p
		return nil
	}
}

// WithSpanNameFunc sets the function that derives a span name from a request.
// It must not be nil. Defaults to the request method followed by the URL path.
func WithSpanNameFunc(f func(*http.Request) string) Option {
	return func(c *config) error {
		if f == nil {
			return errors.New("oteltrace: span name func must not be nil")
		}
		c.spanNameFunc = f
		return nil
	}
}

// New returns middleware that records a server span for each request. The span
// starts before the handler runs and ends after it returns; its name comes from
// the configured span-name function and it carries request and response
// attributes. A response status of 500 or above marks the span's status as
// [codes.Error].
func New(opts ...Option) (middleware.Middleware, error) {
	cfg := config{
		tracerProvider: otel.GetTracerProvider(),
		propagators:    otel.GetTextMapPropagator(),
		spanNameFunc:   func(r *http.Request) string { return r.Method + " " + r.URL.Path },
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

	tracer := cfg.tracerProvider.Tracer(scopeName)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := cfg.propagators.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, cfg.spanNameFunc(r),
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.request.method", r.Method),
					attribute.String("url.path", r.URL.Path),
					attribute.String("network.protocol.version", protocolVersion(r)),
				),
			)
			defer span.End()

			rw := middleware.WrapResponseWriter(w)
			next.ServeHTTP(rw, r.WithContext(ctx))

			status := rw.Status()
			if status == 0 {
				status = http.StatusOK
			}
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, "")
			}
		})
	}, nil
}

// protocolVersion reports the HTTP protocol version as "1.1", "2", etc.,
// derived from the request's ProtoMajor and ProtoMinor.
func protocolVersion(r *http.Request) string {
	switch {
	case r.ProtoMajor == 1 && r.ProtoMinor == 1:
		return "1.1"
	case r.ProtoMajor == 1 && r.ProtoMinor == 0:
		return "1.0"
	case r.ProtoMajor == 2:
		return "2"
	case r.ProtoMajor == 3:
		return "3"
	default:
		return r.Proto
	}
}
