package metrics

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestMetricsIncTotalRequests(t *testing.T) {
	const method = "/test.Method"
	before := testutil.ToFloat64(TotalRequests.WithLabelValues(method))
	IncTotalRequests(method)
	after := testutil.ToFloat64(TotalRequests.WithLabelValues(method))
	if after != before+1 {
		t.Errorf("expected counter to increase by 1, got %.0f -> %.0f", before, after)
	}
}

func TestMetricsIncTotalErrors(t *testing.T) {
	const (
		method = "/test.Method"
		code   = "NotFound"
	)
	before := testutil.ToFloat64(TotalErrors.WithLabelValues(method, code))
	IncTotalErrors(method, code)
	after := testutil.ToFloat64(TotalErrors.WithLabelValues(method, code))
	if after != before+1 {
		t.Errorf("expected error counter to increase by 1, got %.0f -> %.0f", before, after)
	}
}

func TestMetricsObserveRequestDuration(t *testing.T) {
	const method = "/test.Method"
	obs, err := RequestDuration.GetMetricWithLabelValues(method)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	h, ok := obs.(prometheus.Histogram)
	if !ok {
		t.Fatalf("metric is not a Histogram")
	}

	var before dto.Metric
	if err := h.Write(&before); err != nil {
		t.Fatalf("Write before: %v", err)
	}
	beforeCount := before.GetHistogram().GetSampleCount()

	ObserveRequestDuration(method, 0.5)

	var after dto.Metric
	if err := h.Write(&after); err != nil {
		t.Fatalf("Write after: %v", err)
	}
	afterCount := after.GetHistogram().GetSampleCount()

	if afterCount != beforeCount+1 {
		t.Errorf("expected histogram count to increase by 1, got %d -> %d", beforeCount, afterCount)
	}
}

func TestMetricsActiveStreams(t *testing.T) {
	before := testutil.ToFloat64(ActiveStreams)

	IncActiveStreams()
	if got := testutil.ToFloat64(ActiveStreams); got != before+1 {
		t.Errorf("after Inc: got %.0f, want %.0f", got, before+1)
	}

	DecActiveStreams()
	if got := testutil.ToFloat64(ActiveStreams); got != before {
		t.Errorf("after Dec: got %.0f, want %.0f", got, before)
	}

	// Dec below zero must NOT panic; the gauge must clamp at the previous
	// value rather than drifting negative (which would mislead operators
	// reading the Prometheus dashboard).
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DecActiveStreams below zero panicked: %v", r)
		}
	}()
	DecActiveStreams()
	if got := testutil.ToFloat64(ActiveStreams); got != before {
		t.Errorf("Dec below zero should clamp at %.0f, got %.0f", before, got)
	}
}

func TestMetricsConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			IncTotalRequests("/conc.Method")
			IncTotalErrors("/conc.Method", "OK")
			ObserveRequestDuration("/conc.Method", 1.0)
			IncActiveStreams()
			DecActiveStreams()
		}()
	}
	wg.Wait()
}

func TestObserveRequestDurationExemplar(t *testing.T) {
	// Both paths (with and without a trace id) must record a sample without panicking.
	const method = "/exemplar.Method"
	ObserveRequestDurationExemplar(method, 0.5, "0123456789abcdef0123456789abcdef")
	ObserveRequestDurationExemplar(method, 0.25, "")
	// Histogram observation count must have advanced by 2.
	m := &dto.Metric{}
	if err := RequestDuration.WithLabelValues(method).(prometheus.Histogram).Write(m); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := m.GetHistogram().GetSampleCount(); got != 2 {
		t.Fatalf("expected 2 samples, got %d", got)
	}
}
