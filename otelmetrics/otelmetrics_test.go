package otelmetrics_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/maxclav/middleware/otelmetrics"
)

// newReader builds a ManualReader-backed MeterProvider and registers its
// shutdown, so tests can collect metrics deterministically.
func newReader(t *testing.T) (*sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := mp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return reader, mp
}

// collect gathers the current metrics from the reader.
func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

// findMetric returns the aggregated data for the named instrument.
func findMetric(t *testing.T, rm metricdata.ResourceMetrics, name string) metricdata.Aggregation {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m.Data
			}
		}
	}
	t.Fatalf("metric %q not found", name)
	return nil
}

// serve builds the middleware, wraps handler, and serves a single GET request.
func serve(t *testing.T, mp metric.MeterProvider, handler http.HandlerFunc) {
	t.Helper()
	mw, err := otelmetrics.New(otelmetrics.WithMeterProvider(mp))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(handler)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))
}

func TestRecordsRequestDuration(t *testing.T) {
	t.Parallel()
	reader, mp := newReader(t)
	serve(t, mp, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	data := findMetric(t, collect(t, reader), "http.server.request.duration")
	hist, ok := data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("duration data type = %T, want Histogram[float64]", data)
	}
	if len(hist.DataPoints) != 1 {
		t.Fatalf("data points = %d, want 1", len(hist.DataPoints))
	}
	dp := hist.DataPoints[0]
	if dp.Count != 1 {
		t.Fatalf("count = %d, want 1", dp.Count)
	}

	status, ok := dp.Attributes.Value(attribute.Key("http.response.status_code"))
	if !ok {
		t.Fatal("missing http.response.status_code attribute")
	}
	if status.AsInt64() != http.StatusCreated {
		t.Fatalf("status attr = %d, want %d", status.AsInt64(), http.StatusCreated)
	}
	method, ok := dp.Attributes.Value(attribute.Key("http.request.method"))
	if !ok || method.AsString() != http.MethodGet {
		t.Fatalf("method attr = %q (ok=%v), want GET", method.AsString(), ok)
	}
}

func TestActiveRequestsReturnsToZero(t *testing.T) {
	t.Parallel()
	reader, mp := newReader(t)
	serve(t, mp, func(http.ResponseWriter, *http.Request) {})

	data := findMetric(t, collect(t, reader), "http.server.active_requests")
	sum, ok := data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("active_requests data type = %T, want Sum[int64]", data)
	}
	if len(sum.DataPoints) != 1 {
		t.Fatalf("data points = %d, want 1", len(sum.DataPoints))
	}
	// After the request completes the counter has been incremented and
	// decremented, netting zero.
	if got := sum.DataPoints[0].Value; got != 0 {
		t.Fatalf("active requests = %d, want 0 after completion", got)
	}
}

func TestDefaultStatusRecordedWhenNotSet(t *testing.T) {
	t.Parallel()
	reader, mp := newReader(t)
	// Handler writes nothing, so the status defaults to 200.
	serve(t, mp, func(http.ResponseWriter, *http.Request) {})

	hist, ok := findMetric(t, collect(t, reader), "http.server.request.duration").(metricdata.Histogram[float64])
	if !ok {
		t.Fatal("duration data is not a Histogram[float64]")
	}
	status, ok := hist.DataPoints[0].Attributes.Value(attribute.Key("http.response.status_code"))
	if !ok || status.AsInt64() != http.StatusOK {
		t.Fatalf("status attr = %d (ok=%v), want 200", status.AsInt64(), ok)
	}
}

func TestNilMeterProviderRejected(t *testing.T) {
	t.Parallel()
	if _, err := otelmetrics.New(otelmetrics.WithMeterProvider(nil)); err == nil {
		t.Fatal("expected error for nil meter provider")
	}
}

// errMeter is a metric.Meter that fails one instrument constructor and delegates
// the rest to the embedded noop meter, exercising the instrument-creation error
// paths in New.
type errMeter struct {
	noop.Meter
	failHistogram bool
	failCounter   bool
	err           error
}

func (m errMeter) Float64Histogram(name string, opts ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if m.failHistogram {
		return nil, m.err
	}
	return m.Meter.Float64Histogram(name, opts...)
}

func (m errMeter) Int64UpDownCounter(name string, opts ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	if m.failCounter {
		return nil, m.err
	}
	return m.Meter.Int64UpDownCounter(name, opts...)
}

// errMeterProvider hands out a single configured errMeter.
type errMeterProvider struct {
	noop.MeterProvider
	meter errMeter
}

func (p errMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return p.meter
}

func TestInstrumentCreationError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	tests := map[string]errMeter{
		"histogram fails": {failHistogram: true, err: sentinel},
		"counter fails":   {failCounter: true, err: sentinel},
	}
	for name, meter := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mp := errMeterProvider{meter: meter}
			_, err := otelmetrics.New(otelmetrics.WithMeterProvider(mp))
			if !errors.Is(err, sentinel) {
				t.Fatalf("err = %v, want %v", err, sentinel)
			}
		})
	}
}
